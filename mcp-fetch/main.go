// mcp-fetch — MCP server (streamable HTTP) exposing FlareSolverr as a fetch tool.
// Tool: fetch_url — fetch a web page through FlareSolverr, passing Cloudflare /
// DDoS-Guard challenges that block plain chromium/curl. Default port :9002.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	solver := flareSolver{
		url: env("FLARESOLVERR_URL", "http://flaresolverr:8191/v1"),
		// No global client timeout: Solve derives a per-request deadline from the
		// request's maxTimeout (+margin), so it always exceeds FlareSolverr's own.
		client: &http.Client{},
	}

	reader := jinaReader{
		base: env("JINA_READER_URL", "https://r.jina.ai"),
		// Keyless by default (20 RPM). Set JINA_API_KEY for 500 RPM + 10M free tokens.
		key:    os.Getenv("JINA_API_KEY"),
		client: &http.Client{},
	}

	// reach routes a URL to a platform-aware backend. All pure Go (distroless
	// runtime). Twitter/Reddit only work with cookies supplied via env.
	backends := Backends{
		Solver: solver,
		Reader: reader,
		GitHub: githubClient{
			apiBase: env("GITHUB_API_URL", "https://api.github.com"),
			rawBase: env("GITHUB_RAW_URL", "https://raw.githubusercontent.com"),
			token:   os.Getenv("GITHUB_TOKEN"), // optional: raises rate limit, enables private
			client:  &http.Client{},
		},
		YouTube: youtubeTranscriber{
			solver:    solver, // watch page passes YouTube's consent/bot wall via FlareSolverr
			client:    &http.Client{},
			watchBase: env("YOUTUBE_WATCH_URL", "https://www.youtube.com"),
		},
		// TWITTER_COOKIE / REDDIT_COOKIE: raw "name=v; name2=v2" header strings.
		TwitterCookies: parseCookieHeader(os.Getenv("TWITTER_COOKIE"), ".x.com"),
		RedditCookies:  parseCookieHeader(os.Getenv("REDDIT_COOKIE"), ".reddit.com"),
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "mcp-fetch", Version: "0.1.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "fetch_url",
		Description: "Fetch a web page through FlareSolverr, bypassing Cloudflare/DDoS-Guard " +
			"challenges. Use ONLY for sites that block plain requests with a 403/503 challenge " +
			"page. Returns RAW HTML (truncated to max_chars) — for reading page content prefer " +
			"read_url. Returns solved HTML, final URL, HTTP status, and user-agent.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FetchRequest) (*mcp.CallToolResult, FetchResult, error) {
		out, err := Fetch(ctx, solver, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, FetchResult{}, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "read_url",
		Description: "Read a web page as clean markdown via Jina Reader. DEFAULT tool for reading " +
			"page content: it runs the page in a headless browser (executes JavaScript, renders " +
			"SPAs) so it works on client-side-rendered sites that fetch_url/curl return empty. " +
			"Returns LLM-ready markdown (truncated to max_chars). Note: the target URL is sent to " +
			"the Jina service — use only for public pages, not private/authenticated URLs.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReadRequest) (*mcp.CallToolResult, ReadResult, error) {
		out, err := Read(ctx, reader, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, ReadResult{}, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "reach",
		Description: "Fetch a URL through a platform-aware backend, routed by host. " +
			"github.com → repo overview+README, raw files, or issues/PRs via the GitHub API. " +
			"youtube.com/youtu.be → video transcript. x.com/reddit.com → solved HTML (needs " +
			"TWITTER_COOKIE/REDDIT_COOKIE env to be logged in; best-effort otherwise). " +
			"Any other host → clean markdown via Jina. Prefer this over fetch_url/read_url when " +
			"the URL is on one of these platforms.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReachRequest) (*mcp.CallToolResult, ReachResult, error) {
		out, err := Reach(ctx, backends, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, ReachResult{}, nil
		}
		return nil, out, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux := http.NewServeMux()
	mux.Handle("/sse", handler)
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	addr := ":" + env("PORT", "9002")
	slog.Info("mcp-fetch", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
