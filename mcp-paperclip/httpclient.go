// httpAgentIssueClient is the adapter: implements AgentClient and IssueClient
// against the real Paperclip REST API (github.com/paperclipai/paperclip).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type httpAgentIssueClient struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

func newHTTPClient(baseURL, apiKey string) *httpAgentIssueClient {
	return &httpAgentIssueClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

// maxErrorBodyEcho caps how much of an upstream error body we quote back to
// the MCP caller — enough to identify the problem, not a full stack trace or
// internal error dump from paperclip.
const maxErrorBodyEcho = 300

// do performs one request against the Paperclip API. body/out are JSON;
// either may be nil (no request body / no response body to decode). ctx
// propagates the MCP tool call's cancellation/deadline to the outbound
// request, so a caller giving up actually stops the call instead of leaving
// it running server-side.
func (c *httpAgentIssueClient) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body for %s %s: %w", method, path, err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request %s %s: %w", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("call %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body for %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		echo := respBody
		if len(echo) > maxErrorBodyEcho {
			echo = echo[:maxErrorBodyEcho]
		}
		return fmt.Errorf("%s %s: unexpected status %d: %s", method, path, resp.StatusCode, string(echo))
	}
	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("decode response for %s %s: %w", method, path, err)
	}
	return nil
}

func (c *httpAgentIssueClient) ListAgents(ctx context.Context, companyID string) ([]Agent, error) {
	if strings.TrimSpace(companyID) == "" {
		return nil, fmt.Errorf("company id is required")
	}
	var agents []Agent
	if err := c.do(ctx, http.MethodGet, "/api/companies/"+url.PathEscape(companyID)+"/agents", nil, &agents); err != nil {
		return nil, err
	}
	return agents, nil
}

func (c *httpAgentIssueClient) PauseAgent(ctx context.Context, agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("agent id is required")
	}
	return c.do(ctx, http.MethodPost, "/api/agents/"+url.PathEscape(agentID)+"/pause", nil, nil)
}

func (c *httpAgentIssueClient) ResumeAgent(ctx context.Context, agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("agent id is required")
	}
	return c.do(ctx, http.MethodPost, "/api/agents/"+url.PathEscape(agentID)+"/resume", nil, nil)
}

// WakeAgent invokes the agent's heartbeat on demand — used after reopening a
// retried issue so it doesn't have to wait for the next scheduled heartbeat.
func (c *httpAgentIssueClient) WakeAgent(ctx context.Context, agentID string) error {
	if strings.TrimSpace(agentID) == "" {
		return fmt.Errorf("agent id is required")
	}
	return c.do(ctx, http.MethodPost, "/api/agents/"+url.PathEscape(agentID)+"/wakeup", map[string]string{"source": "on_demand"}, nil)
}

func (c *httpAgentIssueClient) ListIssues(ctx context.Context, companyID, status string) ([]Issue, error) {
	if strings.TrimSpace(companyID) == "" {
		return nil, fmt.Errorf("company id is required")
	}
	path := "/api/companies/" + url.PathEscape(companyID) + "/issues"
	if status != "" {
		path += "?status=" + url.QueryEscape(status)
	}
	var issues []Issue
	if err := c.do(ctx, http.MethodGet, path, nil, &issues); err != nil {
		return nil, err
	}
	return issues, nil
}

func (c *httpAgentIssueClient) GetIssue(ctx context.Context, issueID string) (Issue, error) {
	if strings.TrimSpace(issueID) == "" {
		return Issue{}, fmt.Errorf("issue id is required")
	}
	var issue Issue
	if err := c.do(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(issueID), nil, &issue); err != nil {
		return Issue{}, err
	}
	return issue, nil
}

func (c *httpAgentIssueClient) UpdateIssueStatus(ctx context.Context, issueID, status, comment string) error {
	if strings.TrimSpace(issueID) == "" {
		return fmt.Errorf("issue id is required")
	}
	body := map[string]string{"status": status, "comment": comment}
	return c.do(ctx, http.MethodPatch, "/api/issues/"+url.PathEscape(issueID), body, nil)
}
