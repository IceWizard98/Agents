package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fakePoster is a test double for the Poster port.
type fakePoster struct {
	gotPath    string
	gotPayload map[string]any
	res        []byte
	err        error
}

func (f *fakePoster) Post(_ context.Context, path string, payload any) ([]byte, error) {
	f.gotPath = path
	// Round-trip through JSON so assertions see the wire shape (e.g. []string).
	b, _ := json.Marshal(payload)
	_ = json.Unmarshal(b, &f.gotPayload)
	return f.res, f.err
}

func TestAddMemory_HappyPath_PostsDocument(t *testing.T) {
	fp := &fakePoster{res: []byte(`{"id":"doc_1"}`)}
	out, err := AddMemory(context.Background(), fp, "hermes", AddRequest{Content: "  user likes Go  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.gotPath != "/v3/documents" {
		t.Errorf("path = %q", fp.gotPath)
	}
	if fp.gotPayload["content"] != "user likes Go" {
		t.Errorf("content not trimmed: %q", fp.gotPayload["content"])
	}
	if fp.gotPayload["dreaming"] != "instant" {
		t.Errorf("dreaming = %v", fp.gotPayload["dreaming"])
	}
	tags, _ := fp.gotPayload["containerTags"].([]any)
	if len(tags) != 1 || tags[0] != "hermes" {
		t.Errorf("containerTags = %v (want default hermes)", fp.gotPayload["containerTags"])
	}
	if out.Response != `{"id":"doc_1"}` {
		t.Errorf("response = %q", out.Response)
	}
}

func TestAddMemory_EmptyContent_Errors(t *testing.T) {
	if _, err := AddMemory(context.Background(), &fakePoster{}, "hermes", AddRequest{Content: "   "}); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestAddMemory_ExplicitTagAndCustomID_Forwarded(t *testing.T) {
	fp := &fakePoster{res: []byte(`{}`)}
	_, err := AddMemory(context.Background(), fp, "hermes",
		AddRequest{Content: "x", ContainerTag: "project-a", CustomID: "conv-42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tags, _ := fp.gotPayload["containerTags"].([]any)
	if len(tags) != 1 || tags[0] != "project-a" {
		t.Errorf("explicit tag not used: %v", fp.gotPayload["containerTags"])
	}
	if fp.gotPayload["customId"] != "conv-42" {
		t.Errorf("customId = %v", fp.gotPayload["customId"])
	}
}

func TestAddMemory_NoTag_OmitsContainerTags(t *testing.T) {
	fp := &fakePoster{res: []byte(`{}`)}
	if _, err := AddMemory(context.Background(), fp, "", AddRequest{Content: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := fp.gotPayload["containerTags"]; ok {
		t.Errorf("containerTags should be omitted when no tag: %v", fp.gotPayload["containerTags"])
	}
}

func TestAddMemory_PosterError_Propagated(t *testing.T) {
	sentinel := errors.New("connection refused")
	_, err := AddMemory(context.Background(), &fakePoster{err: sentinel}, "hermes", AddRequest{Content: "x"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error should wrap poster error, got %v", err)
	}
}

func TestSearchMemory_HappyPath_PostsSearch(t *testing.T) {
	fp := &fakePoster{res: []byte(`{"results":[]}`)}
	out, err := SearchMemory(context.Background(), fp, "hermes", SearchRequest{Query: "  what does the user like  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.gotPath != "/v4/search" {
		t.Errorf("path = %q", fp.gotPath)
	}
	if fp.gotPayload["q"] != "what does the user like" {
		t.Errorf("q not trimmed: %q", fp.gotPayload["q"])
	}
	if fp.gotPayload["searchMode"] != "hybrid" {
		t.Errorf("searchMode = %v", fp.gotPayload["searchMode"])
	}
	// JSON numbers decode to float64.
	if fp.gotPayload["limit"] != float64(defaultSearchLimit) {
		t.Errorf("default limit = %v", fp.gotPayload["limit"])
	}
	// /v4/search uses containerTag (singular string), not the plural array.
	if fp.gotPayload["containerTag"] != "hermes" {
		t.Errorf("containerTag = %v (want singular string hermes)", fp.gotPayload["containerTag"])
	}
	if _, isArray := fp.gotPayload["containerTags"]; isArray {
		t.Errorf("v4/search must not send plural containerTags: %v", fp.gotPayload["containerTags"])
	}
	if out.Response != `{"results":[]}` {
		t.Errorf("response = %q", out.Response)
	}
}

func TestSearchMemory_EmptyQuery_Errors(t *testing.T) {
	if _, err := SearchMemory(context.Background(), &fakePoster{}, "hermes", SearchRequest{Query: " "}); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearchMemory_CustomLimit_Forwarded(t *testing.T) {
	fp := &fakePoster{res: []byte(`{}`)}
	if _, err := SearchMemory(context.Background(), fp, "hermes", SearchRequest{Query: "x", Limit: 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.gotPayload["limit"] != float64(3) {
		t.Errorf("custom limit not forwarded: %v", fp.gotPayload["limit"])
	}
}

// --- real adapter against a stub supermemory HTTP server ---

func TestSuperMemory_Post_SendsBodyPathContentType_Decodes(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, client: srv.Client()} // no apiKey
	data, err := sm.Post(context.Background(), "/v3/documents", map[string]any{"content": "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v3/documents" || gotCT != "application/json" || gotBody["content"] != "hi" {
		t.Errorf("path=%q ct=%q body=%v", gotPath, gotCT, gotBody)
	}
	if gotAuth != "" {
		t.Errorf("no Authorization header expected when apiKey empty, got %q", gotAuth)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("data = %q", string(data))
	}
}

func TestSuperMemory_Post_WithAPIKey_SetsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, keyEnv: "secret", client: srv.Client()}
	if _, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestSuperMemory_Post_RefreshesKeyOn401(t *testing.T) {
	f := t.TempDir() + "/api-key"
	if err := os.WriteFile(f, []byte("goodkey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer goodkey" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"Unauthorized"}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	// keyCache starts stale (as if the server regenerated its key); the file has
	// the current one. First attempt 401s, refresh reads the file, retry succeeds.
	sm := superMemory{baseURL: srv.URL, keyFile: f, keyCache: "stalekey", client: srv.Client()}
	data, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("data = %q", string(data))
	}
	if calls != 2 {
		t.Errorf("want 2 calls (401 then retry), got %d", calls)
	}
}

func TestSuperMemory_Post_401WithoutKeyFile_NoRetry(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "nope")
	}))
	defer srv.Close()

	// No keyFile and no keyEnv → a 401 must NOT loop; it returns the error once.
	sm := superMemory{baseURL: srv.URL, client: srv.Client()}
	if _, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"}); err == nil {
		t.Fatal("expected error on 401")
	}
	if calls != 1 {
		t.Errorf("want 1 call (no refresh path), got %d", calls)
	}
}

func TestSuperMemory_Post_CapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(bytes.Repeat([]byte("a"), maxResponseBytes+1024))
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, client: srv.Client()}
	data, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != maxResponseBytes {
		t.Errorf("response not capped: got %d bytes, want %d", len(data), maxResponseBytes)
	}
}

func TestSuperMemory_Post_HTTPError_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, client: srv.Client()}
	_, err := sm.Post(context.Background(), "/v3/documents", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected http 500 error, got %v", err)
	}
}
