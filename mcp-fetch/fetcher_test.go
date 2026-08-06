package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeSolver is a test double for the Solver port.
type fakeSolver struct {
	got SolveRequest
	res SolveResult
	err error
}

func (f *fakeSolver) Solve(req SolveRequest) (SolveResult, error) {
	f.got = req
	return f.res, f.err
}

func okResult(html string) SolveResult {
	r := SolveResult{Status: "ok", Message: "Challenge solved!"}
	r.Solution.URL = "https://example.com/final"
	r.Solution.Status = 200
	r.Solution.Response = html
	r.Solution.UserAgent = "Mozilla/5.0"
	return r
}

func TestFetch_HappyPath_ReturnsSolvedHTML(t *testing.T) {
	fs := &fakeSolver{res: okResult("<html>hi</html>")}
	out, err := Fetch(fs, FetchRequest{URL: "https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.HTML != "<html>hi</html>" {
		t.Errorf("HTML = %q", out.HTML)
	}
	if out.StatusCode != 200 || out.FinalURL != "https://example.com/final" || out.UserAgent != "Mozilla/5.0" {
		t.Errorf("unexpected result: %+v", out)
	}
	if out.Truncated {
		t.Errorf("should not be truncated")
	}
	// Defaults propagated to the solver request.
	if fs.got.Cmd != "request.get" || fs.got.URL != "https://example.com" || fs.got.MaxTimeout != defaultTimeoutMs {
		t.Errorf("solver request = %+v", fs.got)
	}
}

func TestFetch_EmptyURL_Errors(t *testing.T) {
	if _, err := Fetch(&fakeSolver{res: okResult("x")}, FetchRequest{URL: "  "}); err == nil {
		t.Fatal("expected error for empty url")
	}
}

func TestFetch_NonHTTPScheme_Errors(t *testing.T) {
	if _, err := Fetch(&fakeSolver{res: okResult("x")}, FetchRequest{URL: "ftp://host/f"}); err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}

func TestFetch_SolverTransportError_Propagated(t *testing.T) {
	sentinel := errors.New("connection refused")
	_, err := Fetch(&fakeSolver{err: sentinel}, FetchRequest{URL: "https://example.com"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error should wrap solver error, got %v", err)
	}
}

func TestFetch_FlareSolverrStatusError_Errors(t *testing.T) {
	res := SolveResult{Status: "error", Message: "Cloudflare challenge not solved"}
	_, err := Fetch(&fakeSolver{res: res}, FetchRequest{URL: "https://example.com"})
	if err == nil || !strings.Contains(err.Error(), "not solved") {
		t.Fatalf("expected flaresolverr status error, got %v", err)
	}
}

func TestFetch_TruncatesToMaxChars(t *testing.T) {
	fs := &fakeSolver{res: okResult(strings.Repeat("a", 500))}
	out, err := Fetch(fs, FetchRequest{URL: "https://example.com", MaxChars: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.HTML) != 100 || !out.Truncated {
		t.Errorf("expected 100 chars truncated, got len=%d truncated=%v", len(out.HTML), out.Truncated)
	}
}

func TestFetch_ForwardsCustomTimeout(t *testing.T) {
	fs := &fakeSolver{res: okResult("x")}
	if _, err := Fetch(fs, FetchRequest{URL: "https://example.com", MaxTimeoutMs: 5000}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fs.got.MaxTimeout != 5000 {
		t.Errorf("MaxTimeout not forwarded: got %d", fs.got.MaxTimeout)
	}
}

func TestFetch_ExactBoundary_NotTruncated(t *testing.T) {
	fs := &fakeSolver{res: okResult(strings.Repeat("a", 100))}
	out, err := Fetch(fs, FetchRequest{URL: "https://example.com", MaxChars: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Truncated || len(out.HTML) != 100 {
		t.Errorf("len==maxChars must not truncate: len=%d truncated=%v", len(out.HTML), out.Truncated)
	}
}

func TestFetch_TruncatesOnRuneBoundary(t *testing.T) {
	fs := &fakeSolver{res: okResult(strings.Repeat("é", 200))} // 2 bytes/rune
	out, err := Fetch(fs, FetchRequest{URL: "https://example.com", MaxChars: 100})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Truncated {
		t.Fatal("expected truncation")
	}
	if utf8.RuneCountInString(out.HTML) != 100 {
		t.Errorf("expected 100 runes, got %d", utf8.RuneCountInString(out.HTML))
	}
	if !utf8.ValidString(out.HTML) {
		t.Error("truncated HTML is not valid UTF-8 (cut mid-rune)")
	}
}

// flareSolver adapter against a stub FlareSolverr HTTP server.
func TestFlareSolver_Solve_PostsCorrectBodyAndDecodes(t *testing.T) {
	var gotBody SolveRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok","message":"ok","solution":{"url":"https://x/","status":200,"response":"<b>ok</b>","userAgent":"UA"}}`)
	}))
	defer srv.Close()

	fs := flareSolver{url: srv.URL, client: srv.Client()}
	res, err := fs.Solve(SolveRequest{Cmd: "request.get", URL: "https://x", MaxTimeout: 1000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody.Cmd != "request.get" || gotBody.URL != "https://x" || gotBody.MaxTimeout != 1000 {
		t.Errorf("posted body = %+v", gotBody)
	}
	if res.Status != "ok" || res.Solution.Response != "<b>ok</b>" {
		t.Errorf("decoded = %+v", res)
	}
}

func TestFlareSolver_Solve_HTTPErrorStatus_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream down")
	}))
	defer srv.Close()

	fs := flareSolver{url: srv.URL, client: srv.Client()}
	if _, err := fs.Solve(SolveRequest{Cmd: "request.get", URL: "https://x"}); err == nil {
		t.Fatal("expected error on non-2xx flaresolverr response")
	}
}
