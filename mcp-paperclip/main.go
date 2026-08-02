// mcp-paperclip — MCP server (streamable HTTP) for Paperclip agent-fleet
// control, on :9500. Paperclip's own MCP package (@paperclipai/mcp-server)
// only wraps issue CRUD — it has no agent lifecycle or retry tools, so this
// fills that gap by calling the Paperclip REST API directly.
//
// Tools: list_agents, stop_all_agents, restart_all_agents, pause_agent,
// resume_agent, list_error_issues, retry_issue, retry_all_error_issues.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	baseURL := os.Getenv("PAPERCLIP_API_URL")
	apiKey := os.Getenv("PAPERCLIP_ADMIN_API_KEY")
	defaultCompanyID := os.Getenv("PAPERCLIP_COMPANY_ID")
	if baseURL == "" || apiKey == "" {
		slog.Error("PAPERCLIP_API_URL / PAPERCLIP_ADMIN_API_KEY missing")
		os.Exit(1)
	}
	client := newHTTPClient(baseURL, apiKey)

	srv := mcp.NewServer(&mcp.Implementation{Name: "paperclip-control", Version: "0.1.0"}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_agents",
		Description: "List every agent in the company (id, name, status: idle/running/paused/terminated/pending_approval).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CompanyRequest) (*mcp.CallToolResult, struct {
		Agents []Agent `json:"agents"`
	}, error) {
		agents, err := client.ListAgents(ctx, companyID(in.CompanyID, defaultCompanyID))
		if err != nil {
			return errResult(err), struct {
				Agents []Agent `json:"agents"`
			}{}, nil
		}
		return nil, struct {
			Agents []Agent `json:"agents"`
		}{Agents: agents}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stop_all_agents",
		Description: "Pause every agent in the company that isn't already paused or terminated (halts them, cancels their active runs). Best-effort: reports per-agent errors instead of stopping at the first failure.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CompanyRequest) (*mcp.CallToolResult, struct {
		Results []AgentActionResult `json:"results"`
	}, error) {
		results, err := StopAllAgents(ctx, client, companyID(in.CompanyID, defaultCompanyID))
		if err != nil {
			return errResult(err), struct {
				Results []AgentActionResult `json:"results"`
			}{}, nil
		}
		return nil, struct {
			Results []AgentActionResult `json:"results"`
		}{Results: results}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "restart_all_agents",
		Description: "Resume every paused agent in the company. Best-effort: reports per-agent errors instead of stopping at the first failure.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CompanyRequest) (*mcp.CallToolResult, struct {
		Results []AgentActionResult `json:"results"`
	}, error) {
		results, err := RestartAllAgents(ctx, client, companyID(in.CompanyID, defaultCompanyID))
		if err != nil {
			return errResult(err), struct {
				Results []AgentActionResult `json:"results"`
			}{}, nil
		}
		return nil, struct {
			Results []AgentActionResult `json:"results"`
		}{Results: results}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "pause_agent",
		Description: "Pause one agent by id (halts it, cancels its active run).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AgentRequest) (*mcp.CallToolResult, struct{ Paused bool }, error) {
		if err := client.PauseAgent(ctx, in.AgentID); err != nil {
			return errResult(err), struct{ Paused bool }{false}, nil
		}
		return nil, struct{ Paused bool }{true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "resume_agent",
		Description: "Resume one paused agent by id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in AgentRequest) (*mcp.CallToolResult, struct{ Resumed bool }, error) {
		if err := client.ResumeAgent(ctx, in.AgentID); err != nil {
			return errResult(err), struct{ Resumed bool }{false}, nil
		}
		return nil, struct{ Resumed bool }{true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_error_issues",
		Description: "List every issue currently blocked (an agent's run on it failed and it can't progress on its own — Paperclip has no dedicated 'error' status, 'blocked' is it).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CompanyRequest) (*mcp.CallToolResult, struct {
		Issues []Issue `json:"issues"`
	}, error) {
		issues, err := client.ListIssues(ctx, companyID(in.CompanyID, defaultCompanyID), blockedStatus)
		if err != nil {
			return errResult(err), struct {
				Issues []Issue `json:"issues"`
			}{}, nil
		}
		return nil, struct {
			Issues []Issue `json:"issues"`
		}{Issues: issues}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "retry_issue",
		Description: "Retry one blocked issue by id: reopens it (status -> todo) and wakes its assignee agent on demand. Fails if the issue isn't blocked or has no assignee.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in IssueRequest) (*mcp.CallToolResult, struct{ Retried bool }, error) {
		if err := RetryIssueByID(ctx, client, client, in.IssueID); err != nil {
			return errResult(err), struct{ Retried bool }{false}, nil
		}
		return nil, struct{ Retried bool }{true}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "retry_all_error_issues",
		Description: "Retry every blocked issue in the company (reopen + wake assignee agent for each). Best-effort: reports per-issue errors instead of stopping at the first failure.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in CompanyRequest) (*mcp.CallToolResult, struct {
		Results []IssueRetryResult `json:"results"`
	}, error) {
		results, err := RetryAllErrorIssues(ctx, client, client, companyID(in.CompanyID, defaultCompanyID))
		if err != nil {
			return errResult(err), struct {
				Results []IssueRetryResult `json:"results"`
			}{}, nil
		}
		return nil, struct {
			Results []IssueRetryResult `json:"results"`
		}{Results: results}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	addr := ":" + env("PORT", "9500")
	slog.Info("paperclip-control mcp", "addr", addr, "paperclip_api", baseURL)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
	}
}

// CompanyRequest is the payload for company-wide tools. CompanyID is
// optional — falls back to PAPERCLIP_COMPANY_ID (single-company deployment).
type CompanyRequest struct {
	CompanyID string `json:"company_id,omitempty" jsonschema:"Paperclip company id. Optional if PAPERCLIP_COMPANY_ID is set on the server."`
}

// AgentRequest is the payload for single-agent tools.
type AgentRequest struct {
	AgentID string `json:"agent_id" jsonschema:"Paperclip agent id (from list_agents)."`
}

// IssueRequest is the payload for single-issue tools.
type IssueRequest struct {
	IssueID string `json:"issue_id" jsonschema:"Paperclip issue id or identifier (e.g. PAP-42, from list_error_issues)."`
}

func companyID(fromRequest, fromEnv string) string {
	if fromRequest != "" {
		return fromRequest
	}
	return fromEnv
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
