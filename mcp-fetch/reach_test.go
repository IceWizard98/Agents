package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- routing (pure) ---

func TestRouteHost(t *testing.T) {
	cases := map[string]backend{
		"github.com":        backendGitHub,
		"www.github.com":    backendGitHub,
		"youtube.com":       backendYouTube,
		"youtu.be":          backendYouTube,
		"m.youtube.com":     backendYouTube,
		"x.com":             backendTwitter,
		"twitter.com":       backendTwitter,
		"reddit.com":        backendReddit,
		"old.reddit.com":    backendReddit,
		"example.com":       backendReader,
		"news.ycombinator.": backendReader,
	}
	for host, want := range cases {
		if got := routeHost(host); got != want {
			t.Errorf("routeHost(%q) = %d, want %d", host, got, want)
		}
	}
}

func TestReach_EmptyURL_Errors(t *testing.T) {
	if _, err := Reach(context.Background(), Backends{}, ReachRequest{URL: "  "}); err == nil {
		t.Fatal("expected error for empty url")
	}
}

func TestReach_NonHTTPScheme_Errors(t *testing.T) {
	if _, err := Reach(context.Background(), Backends{}, ReachRequest{URL: "ftp://x"}); err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}

// --- reach dispatch with fakes ---

type fakeGitHub struct{ md, raw, issue string }

func (f fakeGitHub) RepoMarkdown(context.Context, string, string) (string, error) { return f.md, nil }
func (f fakeGitHub) RawFile(_ context.Context, _, _, _, path string) (string, error) {
	return "file:" + path, nil
}
func (f fakeGitHub) Issue(_ context.Context, _, _, num string) (string, error) {
	return "issue:" + num, nil
}

func TestReach_GitHubRepo_UsesRepoMarkdown(t *testing.T) {
	b := Backends{GitHub: fakeGitHub{md: "# repo readme"}}
	out, err := Reach(context.Background(), b, ReachRequest{URL: "https://github.com/owner/repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Backend != "github" || out.Content != "# repo readme" {
		t.Errorf("got %+v", out)
	}
}

func TestReach_GitHubBlob_UsesRawFile(t *testing.T) {
	b := Backends{GitHub: fakeGitHub{}}
	out, err := Reach(context.Background(), b, ReachRequest{URL: "https://github.com/o/r/blob/main/dir/f.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Content != "file:dir/f.go" {
		t.Errorf("content = %q", out.Content)
	}
}

func TestReach_GitHubIssue_UsesIssue(t *testing.T) {
	b := Backends{GitHub: fakeGitHub{}}
	out, err := Reach(context.Background(), b, ReachRequest{URL: "https://github.com/o/r/issues/42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Content != "issue:42" {
		t.Errorf("content = %q", out.Content)
	}
}

type fakeTranscriber struct{ id string }

func (f *fakeTranscriber) Transcript(_ context.Context, id string) (string, error) {
	f.id = id
	return "hello world transcript", nil
}

func TestReach_YouTube_ExtractsIDAndTranscribes(t *testing.T) {
	ft := &fakeTranscriber{}
	b := Backends{YouTube: ft}
	out, err := Reach(context.Background(), b, ReachRequest{URL: "https://youtu.be/abc123XYZ01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft.id != "abc123XYZ01" {
		t.Errorf("video id = %q", ft.id)
	}
	if out.Kind != "transcript" || out.Content != "hello world transcript" {
		t.Errorf("got %+v", out)
	}
}

func TestReach_Twitter_UsesSolverWithCookies(t *testing.T) {
	fs := &fakeSolver{res: okResult("<html>tweet</html>")}
	ck := []SolveCookie{{Name: "auth_token", Value: "t", Domain: ".x.com"}}
	b := Backends{Solver: fs, TwitterCookies: ck}
	out, err := Reach(context.Background(), b, ReachRequest{URL: "https://x.com/user/status/1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Backend != "flaresolverr" || out.Content != "<html>tweet</html>" {
		t.Errorf("got %+v", out)
	}
	if len(fs.got.Cookies) != 1 || fs.got.Cookies[0].Name != "auth_token" {
		t.Errorf("cookies not forwarded: %+v", fs.got.Cookies)
	}
}

func TestReach_Default_UsesReader(t *testing.T) {
	fr := &fakeReader{res: ReadResult{Markdown: "# page"}}
	b := Backends{Reader: fr}
	out, err := Reach(context.Background(), b, ReachRequest{URL: "https://example.com/x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Backend != "jina" || out.Content != "# page" {
		t.Errorf("got %+v", out)
	}
}

func TestReach_TruncatesContent(t *testing.T) {
	fr := &fakeReader{res: ReadResult{Markdown: strings.Repeat("è", 10)}}
	b := Backends{Reader: fr}
	out, err := Reach(context.Background(), b, ReachRequest{URL: "https://example.com", MaxChars: 4})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Truncated || len([]rune(out.Content)) != 4 {
		t.Errorf("truncation wrong: %+v (runes=%d)", out, len([]rune(out.Content)))
	}
}

func TestReach_UnsupportedGitHubURL_Errors(t *testing.T) {
	b := Backends{GitHub: fakeGitHub{}}
	if _, err := Reach(context.Background(), b, ReachRequest{URL: "https://github.com/owner"}); err == nil {
		t.Fatal("expected error for /owner only")
	}
}

// --- adapters ---

func TestGitHubClient_RepoMarkdown_ComposesMetaAndReadme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/readme"):
			w.Write([]byte("README BODY"))
		default:
			w.Write([]byte(`{"full_name":"o/r","description":"d","stargazers_count":5,"language":"Go","topics":["a"]}`))
		}
	}))
	defer srv.Close()

	gc := githubClient{apiBase: srv.URL, rawBase: srv.URL, client: srv.Client()}
	out, err := gc.RepoMarkdown(context.Background(), "o", "r")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"o/r", "⭐ 5", "Go", "README BODY"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestGitHubClient_RepoMarkdown_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	gc := githubClient{apiBase: srv.URL, rawBase: srv.URL, client: srv.Client()}
	_, err := gc.RepoMarkdown(context.Background(), "o", "r")
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("want rate limit error, got %v", err)
	}
}

func TestGitHubClient_SendsTokenWhenSet(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte("x"))
	}))
	defer srv.Close()
	gc := githubClient{apiBase: srv.URL, rawBase: srv.URL, token: "sek", client: srv.Client()}
	if _, err := gc.RawFile(context.Background(), "o", "r", "main", "f"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer sek" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestYouTubeTranscriber_ExtractsAndParses(t *testing.T) {
	// timedtext server returns json3 segments.
	tt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"events":[{"segs":[{"utf8":"hello "},{"utf8":"world"}]},{"segs":[{"utf8":"!"}]}]}`))
	}))
	defer tt.Close()

	// Watch HTML embeds the caption baseUrl (JSON-escaped &) pointing at tt.
	baseURL := tt.URL + "/api/timedtext?lang=en\\u0026v=x"
	html := `...garbage..."captionTracks":[{"baseUrl":"` + baseURL + `","name":{}}]...more...`
	fs := &fakeSolver{res: okResult(html)}

	yt := youtubeTranscriber{solver: fs, client: tt.Client(), watchBase: "https://www.youtube.com"}
	out, err := yt.Transcript(context.Background(), "vid")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world!" {
		t.Errorf("transcript = %q", out)
	}
}

func TestYouTubeTranscriber_NoCaptions_Errors(t *testing.T) {
	fs := &fakeSolver{res: okResult("<html>no captions here</html>")}
	yt := youtubeTranscriber{solver: fs, client: http.DefaultClient, watchBase: "https://www.youtube.com"}
	if _, err := yt.Transcript(context.Background(), "vid"); err == nil {
		t.Fatal("expected error when no captions")
	}
}

func TestParseCookieHeader(t *testing.T) {
	got := parseCookieHeader("a=1; b=2 ;; c=", ".reddit.com")
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(got), got)
	}
	if got[0].Name != "a" || got[0].Value != "1" || got[0].Domain != ".reddit.com" {
		t.Errorf("cookie[0] = %+v", got[0])
	}
	if got[2].Name != "c" || got[2].Value != "" {
		t.Errorf("cookie[2] = %+v", got[2])
	}
	if parseCookieHeader("  ", ".x.com") != nil {
		t.Error("empty header should yield nil")
	}
}
