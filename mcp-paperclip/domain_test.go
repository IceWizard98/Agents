package main

import (
	"context"
	"errors"
	"testing"
)

// fakeClient is an in-memory double for AgentClient + IssueClient.
type fakeClient struct {
	agents      map[string]*Agent
	issues      map[string]*Issue
	listErr     error
	pauseErr    map[string]error
	resumeErr   map[string]error
	wakeErr     map[string]error
	updateErr   map[string]error
	getIssueErr map[string]error
	woken       []string
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		agents:      map[string]*Agent{},
		issues:      map[string]*Issue{},
		pauseErr:    map[string]error{},
		resumeErr:   map[string]error{},
		wakeErr:     map[string]error{},
		updateErr:   map[string]error{},
		getIssueErr: map[string]error{},
	}
}

func (f *fakeClient) ListAgents(ctx context.Context, companyID string) ([]Agent, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []Agent
	for _, a := range f.agents {
		out = append(out, *a)
	}
	return out, nil
}

func (f *fakeClient) PauseAgent(ctx context.Context, agentID string) error {
	if err := f.pauseErr[agentID]; err != nil {
		return err
	}
	f.agents[agentID].Status = "paused"
	return nil
}

func (f *fakeClient) ResumeAgent(ctx context.Context, agentID string) error {
	if err := f.resumeErr[agentID]; err != nil {
		return err
	}
	f.agents[agentID].Status = "idle"
	return nil
}

func (f *fakeClient) WakeAgent(ctx context.Context, agentID string) error {
	if err := f.wakeErr[agentID]; err != nil {
		return err
	}
	f.woken = append(f.woken, agentID)
	return nil
}

func (f *fakeClient) ListIssues(ctx context.Context, companyID, status string) ([]Issue, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []Issue
	for _, iss := range f.issues {
		if status == "" || iss.Status == status {
			out = append(out, *iss)
		}
	}
	return out, nil
}

func (f *fakeClient) GetIssue(ctx context.Context, issueID string) (Issue, error) {
	if err := f.getIssueErr[issueID]; err != nil {
		return Issue{}, err
	}
	iss, ok := f.issues[issueID]
	if !ok {
		return Issue{}, errors.New("not found")
	}
	return *iss, nil
}

func (f *fakeClient) UpdateIssueStatus(ctx context.Context, issueID, status, comment string) error {
	if err := f.updateErr[issueID]; err != nil {
		return err
	}
	f.issues[issueID].Status = status
	return nil
}

// ---- StopAllAgents ----

func TestStopAllAgents_PausesRunningAndIdle(t *testing.T) {
	f := newFakeClient()
	f.agents["a1"] = &Agent{ID: "a1", Name: "One", Status: "idle"}
	f.agents["a2"] = &Agent{ID: "a2", Name: "Two", Status: "running"}

	results, err := StopAllAgents(context.Background(), f, "co1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if f.agents["a1"].Status != "paused" || f.agents["a2"].Status != "paused" {
		t.Fatalf("agents not paused: %+v %+v", f.agents["a1"], f.agents["a2"])
	}
}

func TestStopAllAgents_SkipsAlreadyPausedAndTerminated(t *testing.T) {
	f := newFakeClient()
	f.agents["a1"] = &Agent{ID: "a1", Status: "paused"}
	f.agents["a2"] = &Agent{ID: "a2", Status: "terminated"}

	results, err := StopAllAgents(context.Background(), f, "co1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("want 0 results (nothing to pause), got %d: %+v", len(results), results)
	}
}

func TestStopAllAgents_OneFailureDoesNotStopTheRest(t *testing.T) {
	f := newFakeClient()
	f.agents["a1"] = &Agent{ID: "a1", Name: "One", Status: "idle"}
	f.agents["a2"] = &Agent{ID: "a2", Name: "Two", Status: "idle"}
	f.pauseErr["a1"] = errors.New("boom")

	results, err := StopAllAgents(context.Background(), f, "co1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if f.agents["a2"].Status != "paused" {
		t.Fatalf("a2 should still be paused despite a1 failing: %+v", f.agents["a2"])
	}
	var sawErr bool
	for _, r := range results {
		if r.AgentID == "a1" && r.Error != "" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("expected a1's error to be reported: %+v", results)
	}
}

func TestStopAllAgents_EmptyCompanyID(t *testing.T) {
	f := newFakeClient()
	if _, err := StopAllAgents(context.Background(), f, ""); err == nil {
		t.Fatal("want error for empty company id")
	}
}

func TestStopAllAgents_ListAgentsError(t *testing.T) {
	f := newFakeClient()
	f.listErr = errors.New("network down")
	if _, err := StopAllAgents(context.Background(), f, "co1"); err == nil {
		t.Fatal("want error propagated from ListAgents")
	}
}

func TestStopAllAgents_CancelledContextStopsEarly(t *testing.T) {
	f := newFakeClient()
	f.agents["a1"] = &Agent{ID: "a1", Status: "idle"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := StopAllAgents(ctx, f, "co1")
	if err == nil {
		t.Fatal("want error for cancelled context")
	}
	if len(results) != 0 {
		t.Fatalf("want no agents paused once cancelled, got %+v", results)
	}
	if f.agents["a1"].Status != "idle" {
		t.Fatalf("a1 must not have been paused: %+v", f.agents["a1"])
	}
}

// ---- RestartAllAgents ----

func TestRestartAllAgents_ResumesOnlyPaused(t *testing.T) {
	f := newFakeClient()
	f.agents["a1"] = &Agent{ID: "a1", Status: "paused"}
	f.agents["a2"] = &Agent{ID: "a2", Status: "idle"}
	f.agents["a3"] = &Agent{ID: "a3", Status: "terminated"}

	results, err := RestartAllAgents(context.Background(), f, "co1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].AgentID != "a1" {
		t.Fatalf("want only a1 resumed, got %+v", results)
	}
	if f.agents["a1"].Status != "idle" {
		t.Fatalf("a1 should be idle after resume: %+v", f.agents["a1"])
	}
}

func TestRestartAllAgents_EmptyCompanyID(t *testing.T) {
	f := newFakeClient()
	if _, err := RestartAllAgents(context.Background(), f, ""); err == nil {
		t.Fatal("want error for empty company id")
	}
}

// ---- RetryIssueByID ----

func TestRetryIssueByID_BlockedIssueReopensAndWakesAgent(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: "a1"}

	if err := RetryIssueByID(context.Background(), f, f, "i1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.issues["i1"].Status != "todo" {
		t.Fatalf("want status todo, got %q", f.issues["i1"].Status)
	}
	if len(f.woken) != 1 || f.woken[0] != "a1" {
		t.Fatalf("want a1 woken, got %v", f.woken)
	}
}

func TestRetryIssueByID_NotBlockedRejected(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "in_progress", AssigneeAgentID: "a1"}

	err := RetryIssueByID(context.Background(), f, f, "i1")
	if err == nil {
		t.Fatal("want error for non-blocked issue")
	}
	if f.issues["i1"].Status != "in_progress" {
		t.Fatalf("status must not change: %q", f.issues["i1"].Status)
	}
}

func TestRetryIssueByID_NoAssignee(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: ""}

	if err := RetryIssueByID(context.Background(), f, f, "i1"); err == nil {
		t.Fatal("want error for missing assignee")
	}
}

func TestRetryIssueByID_EmptyID(t *testing.T) {
	f := newFakeClient()
	if err := RetryIssueByID(context.Background(), f, f, ""); err == nil {
		t.Fatal("want error for empty issue id")
	}
}

func TestRetryIssueByID_GetIssueError(t *testing.T) {
	f := newFakeClient()
	f.getIssueErr["i1"] = errors.New("404")
	if err := RetryIssueByID(context.Background(), f, f, "i1"); err == nil {
		t.Fatal("want error propagated from GetIssue")
	}
}

func TestRetryIssueByID_UpdateFailsDoesNotWake(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: "a1"}
	f.updateErr["i1"] = errors.New("409 conflict")

	if err := RetryIssueByID(context.Background(), f, f, "i1"); err == nil {
		t.Fatal("want error propagated from UpdateIssueStatus")
	}
	if len(f.woken) != 0 {
		t.Fatalf("must not wake agent when reopen failed: %v", f.woken)
	}
}

func TestRetryIssueByID_WakeFails(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: "a1"}
	f.wakeErr["a1"] = errors.New("agent gone")

	if err := RetryIssueByID(context.Background(), f, f, "i1"); err == nil {
		t.Fatal("want error propagated from WakeAgent")
	}
	// Issue is already reopened even though the wake failed — next heartbeat
	// or manual wake will still pick it up.
	if f.issues["i1"].Status != "todo" {
		t.Fatalf("issue should still be reopened: %q", f.issues["i1"].Status)
	}
}

// ---- RetryAllErrorIssues ----

func TestRetryAllErrorIssues_OnlyBlockedRetried(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: "a1"}
	f.issues["i2"] = &Issue{ID: "i2", Identifier: "PAP-2", Status: "done", AssigneeAgentID: "a1"}

	results, err := RetryAllErrorIssues(context.Background(), f, f, "co1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].IssueID != "i1" {
		t.Fatalf("want only i1 retried, got %+v", results)
	}
}

func TestRetryAllErrorIssues_OneFailureDoesNotStopTheRest(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: ""} // no assignee -> fails
	f.issues["i2"] = &Issue{ID: "i2", Identifier: "PAP-2", Status: "blocked", AssigneeAgentID: "a1"}

	results, err := RetryAllErrorIssues(context.Background(), f, f, "co1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	if f.issues["i2"].Status != "todo" {
		t.Fatalf("i2 should still be retried despite i1 failing: %+v", f.issues["i2"])
	}
}

func TestRetryAllErrorIssues_EmptyCompanyID(t *testing.T) {
	f := newFakeClient()
	if _, err := RetryAllErrorIssues(context.Background(), f, f, ""); err == nil {
		t.Fatal("want error for empty company id")
	}
}

func TestRetryAllErrorIssues_ListIssuesError(t *testing.T) {
	f := newFakeClient()
	f.listErr = errors.New("network down")
	if _, err := RetryAllErrorIssues(context.Background(), f, f, "co1"); err == nil {
		t.Fatal("want error propagated from ListIssues")
	}
}

func TestRetryAllErrorIssues_CancelledContextStopsEarly(t *testing.T) {
	f := newFakeClient()
	f.issues["i1"] = &Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: "a1"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results, err := RetryAllErrorIssues(ctx, f, f, "co1")
	if err == nil {
		t.Fatal("want error for cancelled context")
	}
	if len(results) != 0 {
		t.Fatalf("want no issues retried once cancelled, got %+v", results)
	}
	if f.issues["i1"].Status != "blocked" {
		t.Fatalf("i1 must not have been touched: %+v", f.issues["i1"])
	}
}
