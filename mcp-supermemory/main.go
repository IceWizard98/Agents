// mcp-supermemory — MCP server (streamable HTTP) exposing a self-hosted
// supermemory server (REST on :6767) as memory tools for hermes.
// Tools: add_memory (POST /v3/documents), search_memory (POST /v4/search).
// Default port :9003.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	// Pointer: the adapter caches/refreshes the api-key across calls. The key is
	// loaded lazily on first request (not here), so a slow supermemory boot never
	// blocks this server from binding its port.
	store := &superMemory{
		baseURL: env("SUPERMEMORY_URL", "http://supermemory:6767"),
		keyEnv:  os.Getenv("SUPERMEMORY_API_KEY"),      // explicit override; wins
		keyFile: os.Getenv("SUPERMEMORY_API_KEY_FILE"), // auto key from the volume
		// Generous: the write path runs synchronous LLM fact-extraction upstream.
		timeout: time.Duration(envInt("SUPERMEMORY_TIMEOUT_SECONDS", 120)) * time.Second,
		client:  &http.Client{},
	}
	// Default scope so single-user hermes memories group together even when a
	// tool call omits container_tag.
	defaultTag := env("SUPERMEMORY_CONTAINER_TAG", "hermes")

	srv := mcp.NewServer(&mcp.Implementation{Name: "mcp-supermemory", Version: "0.1.0"}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name: "add_memory",
		Description: "Save a durable memory (fact, preference, note) to supermemory so it can be " +
			"recalled in future conversations. Use a stable custom_id to update instead of duplicate.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AddRequest) (*mcp.CallToolResult, AddResult, error) {
		out, err := AddMemory(ctx, store, defaultTag, in)
		if err != nil {
			return errResult(err), AddResult{}, nil
		}
		return nil, out, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "search_memory",
		Description: "Recall memories from supermemory by natural-language query. Returns matching " +
			"memories/documents. Use before answering when past user context may be relevant.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchRequest) (*mcp.CallToolResult, SearchResult, error) {
		out, err := SearchMemory(ctx, store, defaultTag, in)
		if err != nil {
			return errResult(err), SearchResult{}, nil
		}
		return nil, out, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux := http.NewServeMux()
	mux.Handle("/sse", handler)
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	addr := ":" + env("PORT", "9003")
	slog.Info("mcp-supermemory", "addr", addr, "backend", store.baseURL)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
