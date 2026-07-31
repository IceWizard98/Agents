// mcp-icloud — MCP server (streamable HTTP) for sending iCloud mail on :9001.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	user := os.Getenv("ICLOUD_SMTP_USER")
	pass := os.Getenv("ICLOUD_SMTP_APP_PASSWORD")
	if user == "" || pass == "" {
		slog.Error("ICLOUD_SMTP_USER / ICLOUD_SMTP_APP_PASSWORD missing")
		os.Exit(1)
	}
	mailer := smtpMailer{
		host: env("ICLOUD_SMTP_HOST", "smtp.mail.me.com"),
		port: env("ICLOUD_SMTP_PORT", "587"),
		user: user, pass: pass,
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "icloud-smtp", Version: "0.1.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "send_email",
		Description: "Send an email from the configured iCloud account.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EmailRequest) (*mcp.CallToolResult, struct{ Sent bool }, error) {
		if err := SendEmail(mailer, user, in); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, struct{ Sent bool }{false}, nil
		}
		return nil, struct{ Sent bool }{true}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux := http.NewServeMux()
	mux.Handle("/sse", handler)
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	addr := ":" + env("PORT", "9001")
	slog.Info("icloud-smtp mcp", "addr", addr)
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
