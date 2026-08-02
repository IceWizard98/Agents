// mcp-paperclip domain: agent lifecycle control + issue-error retry against
// the Paperclip API. Zero HTTP/JSON here — that's the adapter's job.
package main

import (
	"context"
	"fmt"
	"strings"
)

// blockedStatus is Paperclip's issue status when an agent's run on it failed
// and it can't progress on its own — there is no dedicated "error"/"failed"
// status in the API (verified: backlog, todo, in_progress, in_review, done,
// blocked, cancelled). "Retry the issues in error" targets this status.
const blockedStatus = "blocked"

// Agent mirrors the fields of a Paperclip agent we need.
type Agent struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // idle, running, paused, terminated, pending_approval, ...
}

// Company mirrors the fields of a Paperclip company we need. The board API
// key can see every company it has access to — callers must pick the right
// id explicitly (id, not issuePrefix — verified live: passing the issue
// prefix like "COD" 403s with "User does not have access to this company").
type Company struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"` // active, archived, ...
}

// Issue mirrors the fields of a Paperclip issue we need.
type Issue struct {
	ID              string `json:"id"`
	Identifier      string `json:"identifier"`
	Title           string `json:"title"`
	Status          string `json:"status"`
	AssigneeAgentID string `json:"assigneeAgentId"`
}

// AgentClient controls agent lifecycle against the Paperclip API. ctx carries
// the MCP tool call's cancellation/deadline through to the outbound HTTP
// call — a caller that gives up on stop_all_agents/retry_all_error_issues
// must actually stop the in-flight work, not leave it running server-side
// where a caller retry could double it up (e.g. two wakeups for one issue).
type AgentClient interface {
	ListAgents(ctx context.Context, companyID string) ([]Agent, error)
	PauseAgent(ctx context.Context, agentID string) error
	ResumeAgent(ctx context.Context, agentID string) error
	WakeAgent(ctx context.Context, agentID string) error
}

// IssueClient reads/updates issues against the Paperclip API.
type IssueClient interface {
	ListIssues(ctx context.Context, companyID, status string) ([]Issue, error)
	GetIssue(ctx context.Context, issueID string) (Issue, error)
	UpdateIssueStatus(ctx context.Context, issueID, status, comment string) error
}

// AgentActionResult reports the outcome of a lifecycle action for one agent.
type AgentActionResult struct {
	AgentID string `json:"agent_id"`
	Name    string `json:"name"`
	Error   string `json:"error,omitempty"`
}

// StopAllAgents pauses every agent in the company not already
// paused/terminated. Best-effort: one agent's failure doesn't stop the rest.
// Stops early if ctx is cancelled between agents.
func StopAllAgents(ctx context.Context, c AgentClient, companyID string) ([]AgentActionResult, error) {
	if strings.TrimSpace(companyID) == "" {
		return nil, fmt.Errorf("company id is required")
	}
	agents, err := c.ListAgents(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	results := make([]AgentActionResult, 0, len(agents))
	for _, a := range agents {
		if ctx.Err() != nil {
			return results, fmt.Errorf("cancelled: %w", ctx.Err())
		}
		if a.Status == "paused" || a.Status == "terminated" {
			continue
		}
		r := AgentActionResult{AgentID: a.ID, Name: a.Name}
		if err := c.PauseAgent(ctx, a.ID); err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	return results, nil
}

// RestartAllAgents resumes every paused agent in the company. Stops early if
// ctx is cancelled between agents.
func RestartAllAgents(ctx context.Context, c AgentClient, companyID string) ([]AgentActionResult, error) {
	if strings.TrimSpace(companyID) == "" {
		return nil, fmt.Errorf("company id is required")
	}
	agents, err := c.ListAgents(ctx, companyID)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	results := make([]AgentActionResult, 0, len(agents))
	for _, a := range agents {
		if ctx.Err() != nil {
			return results, fmt.Errorf("cancelled: %w", ctx.Err())
		}
		if a.Status != "paused" {
			continue
		}
		r := AgentActionResult{AgentID: a.ID, Name: a.Name}
		if err := c.ResumeAgent(ctx, a.ID); err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	return results, nil
}

// IssueRetryResult reports the outcome of retrying one issue.
type IssueRetryResult struct {
	IssueID    string `json:"issue_id"`
	Identifier string `json:"identifier"`
	Error      string `json:"error,omitempty"`
}

// retryIssue re-opens a blocked issue and wakes its assignee agent so the
// next heartbeat picks it up. Paperclip has no native retry endpoint (a
// failed run just leaves the issue "blocked", confirmed against the API
// docs) — this reproduces the manual dashboard workaround: move the issue
// back to todo, then nudge its assignee agent on demand.
func retryIssue(ctx context.Context, ic IssueClient, ac AgentClient, issue Issue) error {
	if issue.Status != blockedStatus {
		return fmt.Errorf("issue %s is %q, not %q — nothing to retry", issue.Identifier, issue.Status, blockedStatus)
	}
	if strings.TrimSpace(issue.AssigneeAgentID) == "" {
		return fmt.Errorf("issue %s has no assignee agent — retry it manually in the dashboard", issue.Identifier)
	}
	if err := ic.UpdateIssueStatus(ctx, issue.ID, "todo", "Retried automatically via mcp-paperclip-control after error."); err != nil {
		return fmt.Errorf("reopen issue: %w", err)
	}
	if err := ac.WakeAgent(ctx, issue.AssigneeAgentID); err != nil {
		return fmt.Errorf("wake assignee agent: %w", err)
	}
	return nil
}

// RetryIssueByID looks up one issue by id and retries it if it's blocked.
func RetryIssueByID(ctx context.Context, ic IssueClient, ac AgentClient, issueID string) error {
	if strings.TrimSpace(issueID) == "" {
		return fmt.Errorf("issue id is required")
	}
	issue, err := ic.GetIssue(ctx, issueID)
	if err != nil {
		return fmt.Errorf("get issue: %w", err)
	}
	return retryIssue(ctx, ic, ac, issue)
}

// RetryAllErrorIssues retries every blocked issue in the company.
// Best-effort: one issue's failure doesn't stop the rest. Stops early if ctx
// is cancelled between issues.
func RetryAllErrorIssues(ctx context.Context, ic IssueClient, ac AgentClient, companyID string) ([]IssueRetryResult, error) {
	if strings.TrimSpace(companyID) == "" {
		return nil, fmt.Errorf("company id is required")
	}
	issues, err := ic.ListIssues(ctx, companyID, blockedStatus)
	if err != nil {
		return nil, fmt.Errorf("list blocked issues: %w", err)
	}
	results := make([]IssueRetryResult, 0, len(issues))
	for _, iss := range issues {
		if ctx.Err() != nil {
			return results, fmt.Errorf("cancelled: %w", ctx.Err())
		}
		r := IssueRetryResult{IssueID: iss.ID, Identifier: iss.Identifier}
		if err := retryIssue(ctx, ic, ac, iss); err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	return results, nil
}
