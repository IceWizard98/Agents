package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// defaultSearchLimit caps recall so a broad query doesn't flood the LLM turn.
const defaultSearchLimit = 10

// maxResponseBytes caps how much of a supermemory response we read back into the
// model turn. The backend is trusted, but a huge recall payload would bloat the
// turn. ponytail: fixed cap; raise if a legit response gets clipped.
const maxResponseBytes = 512 * 1024

// Poster is the hexagonal port: POST a JSON payload to a supermemory REST path
// and return the raw response body. The real adapter talks HTTP; tests fake it.
type Poster interface {
	Post(ctx context.Context, path string, payload any) ([]byte, error)
}

// Deleter is the hexagonal port for removing a document from supermemory.
// Delete sends an HTTP DELETE to the given path (no JSON body).
type Deleter interface {
	Delete(ctx context.Context, path string) ([]byte, error)
}

// superMemory is the real adapter against a self-hosted supermemory server
// (REST on :6767). Auth is optional — the local server runs unauthenticated for
// localhost, but cross-container calls need its api-key. keyEnv (an explicit
// SUPERMEMORY_API_KEY) wins and is never refreshed; otherwise the key is loaded
// lazily from keyFile (the <data-dir>/api-key the server writes) and re-read on a
// 401 (the server regenerates it on a data-volume reset). timeout bounds a single
// request; the write path (LLM fact-extraction) can be slow, so keep it generous.
type superMemory struct {
	baseURL string
	keyEnv  string // explicit SUPERMEMORY_API_KEY override; wins, never refreshed
	keyFile string // path to the server's auto-generated key; may be ""
	timeout time.Duration
	client  *http.Client

	mu       sync.Mutex
	keyCache string // last key read from keyFile
}

func (s *superMemory) Post(ctx context.Context, path string, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal supermemory request: %w", err)
	}
	return s.send(ctx, http.MethodPost, path, body)
}

// Delete sends an HTTP DELETE to path (no body) for removing a document.
func (s *superMemory) Delete(ctx context.Context, path string) ([]byte, error) {
	return s.send(ctx, http.MethodDelete, path, nil)
}

// send issues one request with the given HTTP method and body, retrying once
// on 401 when the cached key may be stale (server regenerated it after a
// data-volume reset) — refresh from the key file and try once more.
func (s *superMemory) send(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		data, status, err := s.do(ctx, method, path, body)
		if err != nil {
			return nil, err
		}
		if status == http.StatusUnauthorized && attempt == 0 && s.keyEnv == "" && s.keyFile != "" {
			s.refreshKey()
			continue
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("supermemory http %d: %s", status, string(data))
		}
		return data, nil
	}
}

// do sends one request and returns (body, status, transport-error).
func (s *superMemory) do(ctx context.Context, method, path string, body []byte) ([]byte, int, error) {
	// Bound the request so a stalled backend can't hang the tool call forever,
	// while still composing with the inbound MCP ctx (cancel wins whichever first).
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build supermemory request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if k := s.authKey(); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("supermemory request %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("read supermemory response: %w", err)
	}
	return data, resp.StatusCode, nil
}

// authKey returns the bearer token, loading it lazily from keyFile on first use
// (brief retry to cover the boot race — the server may still be writing it). Does
// NOT block server startup: this runs on the first request, not in main().
func (s *superMemory) authKey() string {
	if s.keyEnv != "" {
		return s.keyEnv
	}
	if s.keyFile == "" {
		return ""
	}
	// Fast path: return the cached key under a brief lock.
	s.mu.Lock()
	cached := s.keyCache
	s.mu.Unlock()
	if cached != "" {
		return cached
	}
	// Slow path: read the file WITHOUT holding s.mu — readKeyFile retries and
	// may sleep up to ~9s on the boot race; holding the lock there would
	// serialize every concurrent request behind this one read.
	key := readKeyFile(s.keyFile, 10)
	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check: another goroutine may have populated the cache while we read.
	if s.keyCache != "" {
		return s.keyCache
	}
	if key != "" {
		s.keyCache = key
		slog.Info("supermemory api key loaded", "file", s.keyFile)
	} else {
		slog.Warn("supermemory api key file not readable; sending unauthenticated (localhost-only will 401)", "file", s.keyFile)
	}
	return s.keyCache
}

// refreshKey re-reads keyFile after a 401 (server regenerated the key on a
// data-volume reset). No-op when an explicit key is configured.
func (s *superMemory) refreshKey() {
	if s.keyEnv != "" || s.keyFile == "" {
		return
	}
	if k := readKeyFile(s.keyFile, 1); k != "" {
		s.mu.Lock()
		if k != s.keyCache {
			slog.Info("supermemory api key refreshed after 401", "file", s.keyFile)
		}
		s.keyCache = k
		s.mu.Unlock()
	}
}

// readKeyFile reads a trimmed non-empty key from path, retrying once per second
// up to attempts times (attempts=1 = single read).
func readKeyFile(path string, attempts int) string {
	for i := 0; i < attempts; i++ {
		if b, err := os.ReadFile(path); err == nil {
			if k := strings.TrimSpace(string(b)); k != "" {
				return k
			}
		}
		if i < attempts-1 {
			time.Sleep(time.Second)
		}
	}
	return ""
}

// AddRequest is the add_memory tool payload.
type AddRequest struct {
	Content      string `json:"content" jsonschema:"the text to remember (a fact, preference, or note)"`
	ContainerTag string `json:"container_tag,omitempty" jsonschema:"scope/namespace for this memory (defaults to the server's configured tag)"`
	CustomID     string `json:"custom_id,omitempty" jsonschema:"stable id (e.g. conversation id) so re-sends upsert instead of duplicating"`
}

// AddResult carries supermemory's raw JSON response (document id / status) back
// to the model, which can read it directly.
type AddResult struct {
	Response string `json:"response"`
}

// AddMemory validates input and ingests text via POST /v3/documents. dreaming
// "instant" makes the extracted memory searchable right away instead of async.
func AddMemory(ctx context.Context, p Poster, defaultTag string, in AddRequest) (AddResult, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return AddResult{}, fmt.Errorf("empty content")
	}
	payload := map[string]any{
		"content":  content,
		"dreaming": "instant",
	}
	if tag := firstNonEmpty(in.ContainerTag, defaultTag); tag != "" {
		payload["containerTags"] = []string{tag}
	}
	if id := strings.TrimSpace(in.CustomID); id != "" {
		payload["customId"] = id
	}
	data, err := p.Post(ctx, "/v3/documents", payload)
	if err != nil {
		return AddResult{}, fmt.Errorf("add memory: %w", err)
	}
	return AddResult{Response: string(data)}, nil
}

// SearchRequest is the search_memory tool payload.
type SearchRequest struct {
	Query        string `json:"query" jsonschema:"what to recall (natural language)"`
	ContainerTag string `json:"container_tag,omitempty" jsonschema:"limit recall to this scope (defaults to the server's configured tag)"`
	Limit        int    `json:"limit,omitempty" jsonschema:"max results (default 10)"`
}

// SearchResult carries supermemory's raw JSON results back to the model.
type SearchResult struct {
	Response string `json:"response"`
}

// SearchMemory validates input and recalls via POST /v4/search. searchMode
// "hybrid" blends extracted-memory and document recall.
func SearchMemory(ctx context.Context, p Poster, defaultTag string, in SearchRequest) (SearchResult, error) {
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return SearchResult{}, fmt.Errorf("empty query")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	payload := map[string]any{
		"q":          q,
		"limit":      limit,
		"searchMode": "hybrid",
	}
	// /v4/search scopes by containerTag (singular string) — NOT containerTags (the
	// plural array that /v3/documents ingest uses). Sending the array here silently
	// matches nothing (verified against the running server).
	if tag := firstNonEmpty(in.ContainerTag, defaultTag); tag != "" {
		payload["containerTag"] = tag
	}
	data, err := p.Post(ctx, "/v4/search", payload)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search memory: %w", err)
	}
	return SearchResult{Response: string(data)}, nil
}

func firstNonEmpty(a, b string) string {
	if s := strings.TrimSpace(a); s != "" {
		return s
	}
	return strings.TrimSpace(b)
}

// DeleteRequest is the delete_memory tool payload.
type DeleteRequest struct {
	// DocumentID is the document id (or customId) to remove. Required. It
	// becomes a URL path segment ("/v3/documents/{id}"), so DeleteMemory rejects
	// separators and escapes the rest before any HTTP call.
	DocumentID string `json:"document_id" jsonschema:"document id or customId to delete (from add_memory or search_memory)"`
}

// DeleteResult carries supermemory's raw delete response back to the model.
type DeleteResult struct {
	Response string `json:"response"`
}

// DeleteMemory validates input and deletes one document via
// DELETE /v3/documents/{id}. The endpoint accepts the document id OR a
// customId (see supermemory document-operations docs). Deletes are permanent —
// there is no undo.
//
// The id is used as a literal path segment, so anything that could change the
// route is rejected outright: "/" and "\" (segment separators), "%" (a
// pre-encoded "%2F" may be normalised back by the server's reverse proxy,
// silently deleting a different document) and control characters. Everything
// else is URL-escaped rather than rejected — add_memory accepts any custom_id
// (":", "@", spaces, non-ASCII), and banning those here would make such
// memories undeletable.
func DeleteMemory(ctx context.Context, d Deleter, in DeleteRequest) (DeleteResult, error) {
	id := strings.TrimSpace(in.DocumentID)
	if id == "" {
		return DeleteResult{}, fmt.Errorf("document_id is required")
	}
	// Reject the dot segments whose decoded forms collide with path traversal.
	// "." and ".." as a whole id would resolve to the collection / its parent
	// via the path normaliser, so treat them as invalid instead of a resource.
	if id == "." || id == ".." {
		return DeleteResult{}, fmt.Errorf("invalid document_id: %q is not a document identifier", id)
	}
	for _, r := range id {
		if r == '/' || r == '\\' || r == '%' || r < 0x20 || r == 0x7f {
			return DeleteResult{}, fmt.Errorf("invalid document_id: %q may not contain '/', '\\', '%%' or control characters", id)
		}
	}
	// Deletes are irreversible and the backend keeps no undo, so leave a trail.
	slog.Info("delete_memory", "document_id", id)
	data, err := d.Delete(ctx, "/v3/documents/"+url.PathEscape(id))
	if err != nil {
		return DeleteResult{}, fmt.Errorf("delete memory: %w", err)
	}
	return DeleteResult{Response: string(data)}, nil
}
