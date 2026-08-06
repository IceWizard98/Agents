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

	srv := mcp.NewServer(&mcp.Implementation{Name: "mcp-fetch", Version: "0.1.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name: "fetch_url",
		Description: "Fetch a web page through FlareSolverr, bypassing Cloudflare/DDoS-Guard " +
			"challenges. Use for sites that block plain chromium/curl (403/503 challenge pages). " +
			"Returns the solved HTML (truncated to max_chars), final URL, HTTP status, and user-agent.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FetchRequest) (*mcp.CallToolResult, FetchResult, error) {
		out, err := Fetch(solver, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, FetchResult{}, nil
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
