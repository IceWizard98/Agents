package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// defaultReadTimeoutMs covers Jina's render + solve latency (avg ~8s, more for
	// heavy SPAs). Generous so a slow client-side render isn't cut off.
	defaultReadTimeoutMs = 30000
)

// ReadRequest is the read_url tool payload.
type ReadRequest struct {
	URL          string `json:"url" jsonschema:"URL to read (http/https)"`
	MaxChars     int    `json:"max_chars,omitempty" jsonschema:"cap on returned markdown length (default 20000)"`
	MaxTimeoutMs int    `json:"max_timeout_ms,omitempty" jsonschema:"render timeout in ms (default 30000)"`
}

// ReadResult is the read_url tool output. No final_url field: Jina's markdown
// already carries the landing URL in its "URL Source:" preamble, and echoing the
// input URL back would lie whenever Jina followed a redirect.
type ReadResult struct {
	Markdown  string `json:"markdown"`
	Truncated bool   `json:"truncated"`
}

// Reader is the hexagonal port. Real adapter talks to Jina Reader over HTTP;
// tests use a fake.
type Reader interface {
	Read(context.Context, ReadRequest) (ReadResult, error)
}

// jinaReader is the real adapter: GETs https://r.jina.ai/<target-url>, which runs
// the page in a headless browser server-side (executing JS / rendering SPAs) and
// returns clean, LLM-ready markdown. key is optional (empty = keyless, 20 RPM).
type jinaReader struct {
	base   string
	key    string
	client *http.Client
}

func (j jinaReader) Read(ctx context.Context, in ReadRequest) (ReadResult, error) {
	timeoutMs := in.MaxTimeoutMs
	if timeoutMs <= 0 {
		timeoutMs = defaultReadTimeoutMs
	}
	// Deadline derives from the caller's ctx (so client cancellation propagates)
	// but must exceed Jina's own X-Timeout wait so a genuinely slow render isn't
	// cut off here first.
	ctx, cancel := context.WithTimeout(ctx,
		time.Duration(timeoutMs)*time.Millisecond+15*time.Second)
	defer cancel()

	// Jina wants the target appended raw (query string intact), NOT url-encoded:
	// https://r.jina.ai/https://x.com/p?q=1
	reqURL := strings.TrimRight(j.base, "/") + "/" + in.URL
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return ReadResult{}, fmt.Errorf("build jina request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")
	req.Header.Set("X-Return-Format", "markdown")
	// Without these, Jina may serve a stale/early cached snapshot taken before the
	// SPA's JS finished rendering — the exact failure we're fixing. No-cache forces
	// a fresh render; X-Timeout (seconds) tells Jina how long to wait for load.
	req.Header.Set("X-No-Cache", "true")
	// Floor at 1s: integer division would send X-Timeout: 0 for sub-second timeouts,
	// which Jina may read as "no wait" — reviving the stale-snapshot bug this fixes.
	timeoutSecs := timeoutMs / 1000
	if timeoutSecs < 1 {
		timeoutSecs = 1
	}
	req.Header.Set("X-Timeout", strconv.Itoa(timeoutSecs))
	if j.key != "" {
		req.Header.Set("Authorization", "Bearer "+j.key)
	}

	resp, err := j.client.Do(req)
	if err != nil {
		return ReadResult{}, fmt.Errorf("jina request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return ReadResult{}, fmt.Errorf("read jina response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return ReadResult{}, fmt.Errorf("jina rate limit (20 RPM keyless): retry shortly or set JINA_API_KEY")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ReadResult{}, fmt.Errorf("jina http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return ReadResult{Markdown: string(data)}, nil
}

// Read validates the request, drives the reader, and caps the output.
func Read(ctx context.Context, r Reader, in ReadRequest) (ReadResult, error) {
	url := strings.TrimSpace(in.URL)
	if url == "" {
		return ReadResult{}, fmt.Errorf("empty url")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return ReadResult{}, fmt.Errorf("url must start with http:// or https://: %q", url)
	}
	in.URL = url

	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}

	res, err := r.Read(ctx, in)
	if err != nil {
		return ReadResult{}, fmt.Errorf("read %s: %w", url, err)
	}

	// Truncate on rune boundaries so a multibyte page isn't cut mid-character.
	if utf8.RuneCountInString(res.Markdown) > maxChars {
		res.Markdown = string([]rune(res.Markdown)[:maxChars])
		res.Truncated = true
	}
	return res, nil
}
