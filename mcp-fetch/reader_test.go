package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeReader is a test double for the Reader port.
type fakeReader struct {
	got ReadRequest
	res ReadResult
	err error
}

func (f *fakeReader) Read(_ context.Context, in ReadRequest) (ReadResult, error) {
	f.got = in
	return f.res, f.err
}

func TestRead_HappyPath_ReturnsMarkdown(t *testing.T) {
	fr := &fakeReader{res: ReadResult{Markdown: "# Title\n\nbody"}}
	out, err := Read(context.Background(), fr, ReadRequest{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Markdown != "# Title\n\nbody" {
		t.Errorf("Markdown = %q", out.Markdown)
	}
	if out.Truncated {
		t.Errorf("should not be truncated")
	}
	// The domain layer forwards the request unchanged to the port.
	if fr.got.URL != "https://example.com" {
		t.Errorf("forwarded URL = %q", fr.got.URL)
	}
}

func TestRead_EmptyURL_Errors(t *testing.T) {
	fr := &fakeReader{}
	if _, err := Read(context.Background(), fr, ReadRequest{URL: "  "}); err == nil {
		t.Fatal("expected error for empty url")
	}
}

func TestRead_NonHTTPScheme_Errors(t *testing.T) {
	fr := &fakeReader{}
	if _, err := Read(context.Background(), fr, ReadRequest{URL: "ftp://example.com"}); err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}

func TestRead_TruncatesOnRuneBoundary(t *testing.T) {
	// 10 multibyte runes; cap at 4 must yield 4 runes, not 4 bytes (which would
	// slice mid-character and corrupt the output).
	md := strings.Repeat("è", 10)
	fr := &fakeReader{res: ReadResult{Markdown: md}}
	out, err := Read(context.Background(), fr, ReadRequest{URL: "https://example.com", MaxChars: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Truncated {
		t.Error("expected Truncated = true")
	}
	if utf8.RuneCountInString(out.Markdown) != 4 {
		t.Errorf("rune count = %d, want 4", utf8.RuneCountInString(out.Markdown))
	}
	if !utf8.ValidString(out.Markdown) {
		t.Error("truncated output is not valid UTF-8")
	}
}

// --- adapter (jinaReader) tests against a stub Jina server ---

func TestJinaReader_AppendsRawTargetURL(t *testing.T) {
	var gotPath, gotNoCache string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.String()
		gotNoCache = r.Header.Get("X-No-Cache")
		w.Write([]byte("# ok"))
	}))
	defer srv.Close()

	jr := jinaReader{base: srv.URL, client: srv.Client()}
	target := "https://x.com/p?q=1&a=2"
	out, err := jr.Read(context.Background(), ReadRequest{URL: target})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Jina wants the target appended raw (query string intact), not url-encoded.
	if !strings.HasSuffix(gotPath, target) {
		t.Errorf("request path = %q, want suffix %q", gotPath, target)
	}
	// No-cache is what forces a fresh render instead of a stale early snapshot.
	if gotNoCache != "true" {
		t.Errorf("X-No-Cache = %q, want %q", gotNoCache, "true")
	}
	if out.Markdown != "# ok" {
		t.Errorf("Markdown = %q", out.Markdown)
	}
}

func TestJinaReader_XTimeoutFlooredToOne(t *testing.T) {
	var gotTimeout string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTimeout = r.Header.Get("X-Timeout")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// Sub-second timeout must not send X-Timeout: 0 (Jina would skip the render wait).
	jr := jinaReader{base: srv.URL, client: srv.Client()}
	if _, err := jr.Read(context.Background(), ReadRequest{URL: "https://x.com", MaxTimeoutMs: 500}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTimeout != "1" {
		t.Errorf("X-Timeout = %q, want %q", gotTimeout, "1")
	}
}

func TestJinaReader_SendsBearerOnlyWhenKeySet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	// No key -> no Authorization header.
	jrNoKey := jinaReader{base: srv.URL, client: srv.Client()}
	if _, err := jrNoKey.Read(context.Background(), ReadRequest{URL: "https://x.com"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "" {
		t.Errorf("Authorization sent without key: %q", gotAuth)
	}

	// Key set -> Bearer header present.
	jrKey := jinaReader{base: srv.URL, key: "secret", client: srv.Client()}
	if _, err := jrKey.Read(context.Background(), ReadRequest{URL: "https://x.com"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer secret")
	}
}

func TestJinaReader_RespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never respond; rely on the caller's ctx to abort
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	jr := jinaReader{base: srv.URL, client: srv.Client()}
	_, err := jr.Read(ctx, ReadRequest{URL: "https://x.com"})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestJinaReader_Non2xx_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	jr := jinaReader{base: srv.URL, client: srv.Client()}
	if _, err := jr.Read(context.Background(), ReadRequest{URL: "https://x.com"}); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestJinaReader_429_ClearRateLimitMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	jr := jinaReader{base: srv.URL, client: srv.Client()}
	_, err := jr.Read(context.Background(), ReadRequest{URL: "https://x.com"})
	if err == nil {
		t.Fatal("expected error on 429")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		t.Errorf("429 error should mention rate limit, got: %v", err)
	}
}
