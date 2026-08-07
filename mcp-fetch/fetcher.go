package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultTimeoutMs = 60000
	// defaultMaxChars caps returned HTML so a huge page doesn't blow the LLM turn.
	// ponytail: fixed cap; raise if truncation loses content the agent needs.
	defaultMaxChars = 20000
)

// SolveRequest is the FlareSolverr POST /v1 payload.
type SolveRequest struct {
	Cmd        string        `json:"cmd"`
	URL        string        `json:"url"`
	MaxTimeout int           `json:"maxTimeout"`
	Cookies    []SolveCookie `json:"cookies,omitempty"`
}

// SolveCookie is a cookie passed to FlareSolverr so cookie-only sites
// (Twitter/X, Reddit) can be fetched with a logged-in session.
type SolveCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain,omitempty"`
}

// SolveResult is the FlareSolverr response. status == "ok" means the challenge
// was passed; otherwise message carries the failure reason.
type SolveResult struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Solution struct {
		URL       string `json:"url"`
		Status    int    `json:"status"`
		Response  string `json:"response"`
		UserAgent string `json:"userAgent"`
	} `json:"solution"`
}

// Solver is the hexagonal port. Real adapter talks to FlareSolverr over HTTP;
// tests use a fake.
type Solver interface {
	Solve(context.Context, SolveRequest) (SolveResult, error)
}

// flareSolver is the real adapter: POSTs to a FlareSolverr /v1 endpoint.
type flareSolver struct {
	url    string
	client *http.Client
}

func (f flareSolver) Solve(ctx context.Context, req SolveRequest) (SolveResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return SolveResult{}, fmt.Errorf("marshal flaresolverr request: %w", err)
	}
	// Deadline derives from the caller's ctx (so client cancellation propagates)
	// but must exceed FlareSolverr's own maxTimeout (challenge budget) so a genuinely
	// slow solve isn't cut off here first. Margin covers browser spin-up + transfer.
	ctx, cancel := context.WithTimeout(ctx,
		time.Duration(req.MaxTimeout)*time.Millisecond+30*time.Second)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, f.url, bytes.NewReader(body))
	if err != nil {
		return SolveResult{}, fmt.Errorf("build flaresolverr request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(httpReq)
	if err != nil {
		return SolveResult{}, fmt.Errorf("flaresolverr request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return SolveResult{}, fmt.Errorf("read flaresolverr response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SolveResult{}, fmt.Errorf("flaresolverr http %d: %s", resp.StatusCode, string(data))
	}
	var out SolveResult
	if err := json.Unmarshal(data, &out); err != nil {
		return SolveResult{}, fmt.Errorf("decode flaresolverr response: %w", err)
	}
	return out, nil
}

// FetchRequest is the fetch_url tool payload.
type FetchRequest struct {
	URL          string `json:"url" jsonschema:"URL to fetch (http/https)"`
	MaxTimeoutMs int    `json:"max_timeout_ms,omitempty" jsonschema:"browser challenge timeout in ms (default 60000)"`
	MaxChars     int    `json:"max_chars,omitempty" jsonschema:"cap on returned HTML length (default 20000)"`
}

// FetchResult is the fetch_url tool output.
type FetchResult struct {
	HTML       string `json:"html"`
	StatusCode int    `json:"status_code"`
	FinalURL   string `json:"final_url"`
	UserAgent  string `json:"user_agent"`
	Truncated  bool   `json:"truncated"`
}

// Fetch validates the request, drives the solver, and shapes the result.
func Fetch(ctx context.Context, s Solver, in FetchRequest) (FetchResult, error) {
	url := strings.TrimSpace(in.URL)
	if url == "" {
		return FetchResult{}, fmt.Errorf("empty url")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return FetchResult{}, fmt.Errorf("url must start with http:// or https://: %q", url)
	}
	timeout := in.MaxTimeoutMs
	if timeout <= 0 {
		timeout = defaultTimeoutMs
	}
	maxChars := in.MaxChars
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}

	res, err := s.Solve(ctx, SolveRequest{Cmd: "request.get", URL: url, MaxTimeout: timeout})
	if err != nil {
		return FetchResult{}, fmt.Errorf("solve %s: %w", url, err)
	}
	if res.Status != "ok" {
		msg := res.Message
		if msg == "" {
			msg = fmt.Sprintf("status %q (page http %d)", res.Status, res.Solution.Status)
		}
		return FetchResult{}, fmt.Errorf("flaresolverr: %s", msg)
	}

	// Truncate on rune boundaries so a multibyte page isn't cut mid-character.
	html := res.Solution.Response
	truncated := false
	if utf8.RuneCountInString(html) > maxChars {
		html = string([]rune(html)[:maxChars])
		truncated = true
	}
	return FetchResult{
		HTML:       html,
		StatusCode: res.Solution.Status,
		FinalURL:   res.Solution.URL,
		UserAgent:  res.Solution.UserAgent,
		Truncated:  truncated,
	}, nil
}
