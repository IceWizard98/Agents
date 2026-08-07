package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// reach is a router tool: one entry point that dispatches a URL to the backend
// best suited to its host. Generic web pages already have fetch_url / read_url;
// reach adds platform-aware handling for GitHub, YouTube, Twitter/X and Reddit.
//
// Every backend is pure Go (the runtime image is distroless/static — no shell,
// no python, so yt-dlp and the agent-reach CLI cannot run here). Twitter/Reddit
// have no zero-config path: they work only when cookies are supplied via env.

const defaultReachMaxChars = defaultMaxChars

// ReachRequest is the reach tool payload.
type ReachRequest struct {
	URL          string `json:"url" jsonschema:"URL to reach (http/https). Routed by host to a platform backend."`
	MaxChars     int    `json:"max_chars,omitempty" jsonschema:"cap on returned content length (default 20000)"`
	MaxTimeoutMs int    `json:"max_timeout_ms,omitempty" jsonschema:"backend timeout in ms"`
}

// ReachResult is the reach tool output.
type ReachResult struct {
	Content   string `json:"content"`
	Backend   string `json:"backend"`   // github | youtube | flaresolverr | jina
	Kind      string `json:"kind"`      // markdown | transcript | html
	Truncated bool   `json:"truncated"`
}

// GitHubClient is the port for the GitHub public API adapter.
type GitHubClient interface {
	RepoMarkdown(ctx context.Context, owner, repo string) (string, error)
	RawFile(ctx context.Context, owner, repo, ref, path string) (string, error)
	Issue(ctx context.Context, owner, repo, num string) (string, error)
}

// Transcriber is the port for the YouTube transcript adapter.
type Transcriber interface {
	Transcript(ctx context.Context, videoID string) (string, error)
}

// Backends bundles every port reach can dispatch to, plus the cookies that make
// the cookie-only platforms usable.
type Backends struct {
	Solver         Solver
	Reader         Reader
	GitHub         GitHubClient
	YouTube        Transcriber
	TwitterCookies []SolveCookie
	RedditCookies  []SolveCookie
}

// backend identifies which route a host resolves to.
type backend int

const (
	backendReader backend = iota // default: generic web via Jina
	backendGitHub
	backendYouTube
	backendTwitter
	backendReddit
)

// routeHost picks the backend for a host. Pure so it is trivially testable.
func routeHost(host string) backend {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	switch {
	case host == "github.com":
		return backendGitHub
	case host == "youtube.com" || host == "m.youtube.com" || host == "youtu.be":
		return backendYouTube
	case host == "x.com" || host == "twitter.com" || host == "mobile.twitter.com":
		return backendTwitter
	case host == "reddit.com" || host == "old.reddit.com" || host == "redd.it":
		return backendReddit
	default:
		return backendReader
	}
}

// Reach validates the request, routes by host, and caps the output.
func Reach(ctx context.Context, b Backends, in ReachRequest) (ReachResult, error) {
	raw := strings.TrimSpace(in.URL)
	if raw == "" {
		return ReachResult{}, fmt.Errorf("empty url")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return ReachResult{}, fmt.Errorf("url must start with http:// or https://: %q", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ReachResult{}, fmt.Errorf("parse url %q: %w", raw, err)
	}

	var (
		content, kind, name string
	)
	switch routeHost(u.Host) {
	case backendGitHub:
		content, err = reachGitHub(ctx, b.GitHub, u)
		kind, name = "markdown", "github"
	case backendYouTube:
		content, err = reachYouTube(ctx, b.YouTube, u)
		kind, name = "transcript", "youtube"
	case backendTwitter:
		content, err = reachSolver(ctx, b.Solver, raw, in.MaxTimeoutMs, b.TwitterCookies)
		kind, name = "html", "flaresolverr"
	case backendReddit:
		content, err = reachSolver(ctx, b.Solver, raw, in.MaxTimeoutMs, b.RedditCookies)
		kind, name = "html", "flaresolverr"
	default:
		content, err = reachReader(ctx, b.Reader, raw, in.MaxChars, in.MaxTimeoutMs)
		kind, name = "markdown", "jina"
	}
	if err != nil {
		return ReachResult{}, err
	}

	content, truncated := capRunes(content, in.MaxChars)
	return ReachResult{Content: content, Backend: name, Kind: kind, Truncated: truncated}, nil
}

// capRunes truncates on rune boundaries so a multibyte page isn't cut mid-character.
func capRunes(s string, max int) (string, bool) {
	if max <= 0 {
		max = defaultReachMaxChars
	}
	if utf8.RuneCountInString(s) > max {
		return string([]rune(s)[:max]), true
	}
	return s, false
}

// --- GitHub routing ---

func reachGitHub(ctx context.Context, gh GitHubClient, u *url.URL) (string, error) {
	if gh == nil {
		return "", fmt.Errorf("github backend unavailable")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// parts: owner/repo/[blob|issues|pull]/...
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("unsupported github url (need /owner/repo): %s", u.Path)
	}
	owner, repo := parts[0], parts[1]
	if len(parts) == 2 {
		return gh.RepoMarkdown(ctx, owner, repo)
	}
	switch parts[2] {
	case "blob":
		if len(parts) < 5 {
			return "", fmt.Errorf("unsupported github blob url: %s", u.Path)
		}
		ref, path := parts[3], strings.Join(parts[4:], "/")
		return gh.RawFile(ctx, owner, repo, ref, path)
	case "issues", "pull":
		if len(parts) < 4 {
			return "", fmt.Errorf("unsupported github issue url: %s", u.Path)
		}
		return gh.Issue(ctx, owner, repo, parts[3])
	default:
		// Unknown sub-path (tree, actions, ...): repo overview is the useful fallback.
		return gh.RepoMarkdown(ctx, owner, repo)
	}
}

// --- YouTube routing ---

func reachYouTube(ctx context.Context, yt Transcriber, u *url.URL) (string, error) {
	if yt == nil {
		return "", fmt.Errorf("youtube backend unavailable")
	}
	id := youtubeID(u)
	if id == "" {
		return "", fmt.Errorf("could not extract youtube video id from %s", u.String())
	}
	return yt.Transcript(ctx, id)
}

// youtubeID pulls the 11-char video id from the common URL shapes.
func youtubeID(u *url.URL) string {
	host := strings.ToLower(strings.TrimPrefix(u.Host, "www."))
	if host == "youtu.be" {
		return strings.Trim(u.Path, "/")
	}
	if v := u.Query().Get("v"); v != "" {
		return v
	}
	// /shorts/ID, /embed/ID, /v/ID
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 2 && (parts[0] == "shorts" || parts[0] == "embed" || parts[0] == "v") {
		return parts[1]
	}
	return ""
}

// --- solver / reader routing (existing infra) ---

func reachSolver(ctx context.Context, s Solver, url string, timeoutMs int, cookies []SolveCookie) (string, error) {
	if s == nil {
		return "", fmt.Errorf("flaresolverr backend unavailable")
	}
	if timeoutMs <= 0 {
		timeoutMs = defaultTimeoutMs
	}
	res, err := s.Solve(ctx, SolveRequest{Cmd: "request.get", URL: url, MaxTimeout: timeoutMs, Cookies: cookies})
	if err != nil {
		return "", fmt.Errorf("solve %s: %w", url, err)
	}
	if res.Status != "ok" {
		msg := res.Message
		if msg == "" {
			msg = fmt.Sprintf("status %q", res.Status)
		}
		return "", fmt.Errorf("flaresolverr: %s", msg)
	}
	return res.Solution.Response, nil
}

func reachReader(ctx context.Context, r Reader, url string, maxChars, timeoutMs int) (string, error) {
	if r == nil {
		return "", fmt.Errorf("jina backend unavailable")
	}
	res, err := r.Read(ctx, ReadRequest{URL: url, MaxChars: maxChars, MaxTimeoutMs: timeoutMs})
	if err != nil {
		return "", err
	}
	return res.Markdown, nil
}

// --- GitHub adapter: real GitHub public REST API over HTTP ---

type githubClient struct {
	apiBase string // https://api.github.com
	rawBase string // https://raw.githubusercontent.com
	token   string // optional; lifts rate limit and enables private repos
	client  *http.Client
}

func (g githubClient) get(ctx context.Context, url, accept string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build github request: %w", err)
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "mcp-fetch")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("github request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read github response: %w", err)
	}
	return data, resp.StatusCode, nil
}

func (g githubClient) RepoMarkdown(ctx context.Context, owner, repo string) (string, error) {
	data, code, err := g.get(ctx, g.apiBase+"/repos/"+owner+"/"+repo, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	if code == http.StatusForbidden || code == http.StatusTooManyRequests {
		return "", fmt.Errorf("github rate limit (set GITHUB_TOKEN to raise it)")
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("github http %d: %s", code, strings.TrimSpace(string(data)))
	}
	var r struct {
		FullName    string   `json:"full_name"`
		Description string   `json:"description"`
		Stars       int      `json:"stargazers_count"`
		Forks       int      `json:"forks_count"`
		Language    string   `json:"language"`
		Topics      []string `json:"topics"`
		HTMLURL     string   `json:"html_url"`
		License     struct {
			SPDX string `json:"spdx_id"`
		} `json:"license"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("decode github repo: %w", err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", r.FullName)
	if r.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", r.Description)
	}
	fmt.Fprintf(&b, "⭐ %d · 🍴 %d", r.Stars, r.Forks)
	if r.Language != "" {
		fmt.Fprintf(&b, " · %s", r.Language)
	}
	if r.License.SPDX != "" && r.License.SPDX != "NOASSERTION" {
		fmt.Fprintf(&b, " · %s", r.License.SPDX)
	}
	b.WriteString("\n")
	if len(r.Topics) > 0 {
		fmt.Fprintf(&b, "topics: %s\n", strings.Join(r.Topics, ", "))
	}
	b.WriteString("\n---\n\n")

	// README is best-effort: a repo without one is still a useful result.
	readme, code, err := g.get(ctx, g.apiBase+"/repos/"+owner+"/"+repo+"/readme", "application/vnd.github.raw+json")
	if err == nil && code >= 200 && code < 300 {
		b.Write(readme)
	} else {
		b.WriteString("(no README)\n")
	}
	return b.String(), nil
}

func (g githubClient) RawFile(ctx context.Context, owner, repo, ref, path string) (string, error) {
	data, code, err := g.get(ctx, g.rawBase+"/"+owner+"/"+repo+"/"+ref+"/"+path, "text/plain")
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("github raw http %d for %s", code, path)
	}
	return string(data), nil
}

func (g githubClient) Issue(ctx context.Context, owner, repo, num string) (string, error) {
	data, code, err := g.get(ctx, g.apiBase+"/repos/"+owner+"/"+repo+"/issues/"+num, "application/vnd.github+json")
	if err != nil {
		return "", err
	}
	if code < 200 || code >= 300 {
		return "", fmt.Errorf("github http %d: %s", code, strings.TrimSpace(string(data)))
	}
	var r struct {
		Title string `json:"title"`
		State string `json:"state"`
		Body  string `json:"body"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("decode github issue: %w", err)
	}
	return fmt.Sprintf("# %s (#%s, %s)\n\nby @%s\n\n%s", r.Title, num, r.State, r.User.Login, r.Body), nil
}

// --- YouTube adapter: fetch the watch page (via FlareSolverr, which passes the
// consent/bot walls) then pull the timedtext caption track. Pure Go, no yt-dlp. ---

type youtubeTranscriber struct {
	solver    Solver
	client    *http.Client
	watchBase string // https://www.youtube.com — overridable for tests
	timeoutMs int
}

// captionsBaseURL finds the first caption track's timedtext URL in the watch HTML.
// ponytail: brittle by nature — YouTube's player JSON shape can shift; when it
// does this regex is the thing to update, and the error names it clearly.
var captionsBaseURL = regexp.MustCompile(`"captionTracks":\[\{"baseUrl":"(.*?)"`)

func (y youtubeTranscriber) Transcript(ctx context.Context, videoID string) (string, error) {
	watchURL := strings.TrimRight(y.watchBase, "/") + "/watch?v=" + videoID
	timeout := y.timeoutMs
	if timeout <= 0 {
		timeout = defaultTimeoutMs
	}
	res, err := y.solver.Solve(ctx, SolveRequest{Cmd: "request.get", URL: watchURL, MaxTimeout: timeout})
	if err != nil {
		return "", fmt.Errorf("fetch youtube watch page: %w", err)
	}
	if res.Status != "ok" {
		return "", fmt.Errorf("youtube watch page: flaresolverr status %q", res.Status)
	}

	m := captionsBaseURL.FindStringSubmatch(res.Solution.Response)
	if m == nil {
		return "", fmt.Errorf("no captions found for video %s (may have none, or player shape changed)", videoID)
	}
	// The URL sits inside JSON, so & etc. are escaped — decode via JSON.
	var base string
	if err := json.Unmarshal([]byte(`"`+m[1]+`"`), &base); err != nil {
		return "", fmt.Errorf("decode caption url: %w", err)
	}

	// fmt=json3 gives clean segmented text instead of the timed XML.
	ttURL := base
	if !strings.Contains(ttURL, "fmt=") {
		ttURL += "&fmt=json3"
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond+10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ttURL, nil)
	if err != nil {
		return "", fmt.Errorf("build timedtext request: %w", err)
	}
	resp, err := y.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("timedtext request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read timedtext: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("timedtext http %d", resp.StatusCode)
	}
	return parseTimedTextJSON3(data)
}

// parseTimedTextJSON3 joins the segment texts of a timedtext json3 document.
func parseTimedTextJSON3(data []byte) (string, error) {
	var doc struct {
		Events []struct {
			Segs []struct {
				UTF8 string `json:"utf8"`
			} `json:"segs"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("decode timedtext json3: %w", err)
	}
	var b strings.Builder
	for _, e := range doc.Events {
		for _, s := range e.Segs {
			b.WriteString(s.UTF8)
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("empty transcript")
	}
	return out, nil
}

// --- cookie helpers ---

// parseCookieHeader turns a "name=v; name2=v2" header into solver cookies for a
// single domain. Empty input yields nil (best-effort, no cookies).
func parseCookieHeader(header, domain string) []SolveCookie {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}
	var out []SolveCookie
	for _, pair := range strings.Split(header, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		out = append(out, SolveCookie{Name: name, Value: strings.TrimSpace(value), Domain: domain})
	}
	return out
}
