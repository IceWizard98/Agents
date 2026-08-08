// mcp-icloud — MCP server (streamable HTTP) for iCloud mail on :9001.
// Tools: send_email (SMTP); list_emails, read_email, search_emails,
// mark_email, move_email, list_mailboxes (IMAP).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	// Auth username = your primary iCloud address (e.g. name@icloud.com). iCloud
	// rejects custom-domain aliases as the SMTP/IMAP login user.
	user := os.Getenv("ICLOUD_SMTP_USER")
	pass := os.Getenv("ICLOUD_SMTP_APP_PASSWORD")
	if user == "" || pass == "" {
		slog.Error("ICLOUD_SMTP_USER / ICLOUD_SMTP_APP_PASSWORD missing")
		os.Exit(1)
	}
	// From address for outgoing mail. Defaults to the auth user; set ICLOUD_FROM
	// to a verified alias (e.g. a Custom Email Domain address) to send as that.
	from := env("ICLOUD_FROM", user)
	mailer := smtpMailer{
		host: env("ICLOUD_SMTP_HOST", "smtp.mail.me.com"),
		port: env("ICLOUD_SMTP_PORT", "587"),
		user: user, pass: pass,
	}
	reader := imapReader{
		addr: env("ICLOUD_IMAP_HOST", "imap.mail.me.com") + ":" + env("ICLOUD_IMAP_PORT", "993"),
		user: user, pass: pass,
	}

	srv := mcp.NewServer(&mcp.Implementation{Name: "icloud-mail", Version: "0.2.0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "send_email",
		Description: "Send an email from the configured iCloud account.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in EmailRequest) (*mcp.CallToolResult, struct{ Sent bool }, error) {
		if err := SendEmail(mailer, from, in); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, struct{ Sent bool }{false}, nil
		}
		return nil, struct{ Sent bool }{true}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_emails",
		Description: "List the most recent emails in a mailbox (default INBOX), unfiltered. Only accepts mailbox and limit — no filters. To find an email by sender, subject, date, or read status, call search_emails instead. Returns UID, from, subject, date — no body; use read_email with a UID to get the body.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListRequest) (*mcp.CallToolResult, struct {
		Emails []MailHeader `json:"emails"`
	}, error) {
		headers, err := ListEmails(reader, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, struct {
				Emails []MailHeader `json:"emails"`
			}{}, nil
		}
		return nil, struct {
			Emails []MailHeader `json:"emails"`
		}{Emails: headers}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_email",
		Description: "Read one full email (including body) by UID from a mailbox (default INBOX).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ReadRequest) (*mcp.CallToolResult, MailMessage, error) {
		msg, err := ReadEmail(reader, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, MailMessage{}, nil
		}
		return nil, msg, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_emails",
		Description: "Search emails in a mailbox (default INBOX). Filters: from (sender contains), to (recipient contains — use this on the Sent mailbox to find emails to a person), subject (contains), since/before (YYYY-MM-DD), unread_only. Requires at least one filter. Does NOT support body/content search — that parameter does not exist and will be rejected; use only the exact filter names listed above. Returns UID, from, to, subject, date — no body; use read_email for the body.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchRequest) (*mcp.CallToolResult, struct {
		Emails []MailHeader `json:"emails"`
	}, error) {
		headers, err := SearchEmails(reader, in)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, struct {
				Emails []MailHeader `json:"emails"`
			}{}, nil
		}
		return nil, struct {
			Emails []MailHeader `json:"emails"`
		}{Emails: headers}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "mark_email",
		Description: "Mark an email as read or unread by UID. mailbox is REQUIRED and must be the mailbox the UID came from (UIDs are per-mailbox) — pass the same mailbox you searched or listed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MarkRequest) (*mcp.CallToolResult, struct{ Marked bool }, error) {
		if err := MarkEmail(reader, in); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, struct{ Marked bool }{false}, nil
		}
		return nil, struct{ Marked bool }{true}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "move_email",
		Description: "Move an email to another mailbox by UID. mailbox (source) is REQUIRED and must be the mailbox the UID came from (UIDs are per-mailbox) — pass the same mailbox you searched or listed, or the wrong message gets moved. To delete an email, move it to the Trash/Deleted Messages folder — use list_mailboxes to find its exact name.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in MoveRequest) (*mcp.CallToolResult, struct{ Moved bool }, error) {
		if err := MoveEmail(reader, in); err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, struct{ Moved bool }{false}, nil
		}
		return nil, struct{ Moved bool }{true}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_mailboxes",
		Description: "List selectable mailbox/folder names (INBOX, Sent, Drafts, Trash, custom folders, ...). Use these names with the mailbox/to parameters of other tools.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, struct {
		Mailboxes []string `json:"mailboxes"`
	}, error) {
		boxes, err := reader.Mailboxes()
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, struct {
				Mailboxes []string `json:"mailboxes"`
			}{}, nil
		}
		return nil, struct {
			Mailboxes []string `json:"mailboxes"`
		}{Mailboxes: boxes}, nil
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
