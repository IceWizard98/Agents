package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClient_ListAgents_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/companies/co1/agents" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key123" {
			t.Fatalf("missing/wrong auth header: %q", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode([]Agent{{ID: "a1", Name: "One", Status: "idle"}})
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key123")
	agents, err := c.ListAgents(context.Background(), "co1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 || agents[0].ID != "a1" {
		t.Fatalf("unexpected agents: %+v", agents)
	}
}

func TestHTTPClient_HostOverride_SentOnRequest(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		json.NewEncoder(w).Encode([]Agent{})
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key").withHostOverride("paperclip.example.com")
	if _, err := c.ListAgents(context.Background(), "co1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHost != "paperclip.example.com" {
		t.Fatalf("Host header = %q, want paperclip.example.com", gotHost)
	}
}

func TestHTTPClient_NoHostOverride_UsesBaseURLHost(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		json.NewEncoder(w).Encode([]Agent{})
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if _, err := c.ListAgents(context.Background(), "co1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantHost := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")
	if gotHost != wantHost {
		t.Fatalf("Host header = %q, want %q (unchanged, no override set)", gotHost, wantHost)
	}
}

func TestHTTPClient_ListAgents_EmptyCompanyID(t *testing.T) {
	c := newHTTPClient("http://unused", "key")
	if _, err := c.ListAgents(context.Background(), ""); err == nil {
		t.Fatal("want error for empty company id")
	}
}

func TestHTTPClient_ListAgents_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"cross-company access forbidden"}`))
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if _, err := c.ListAgents(context.Background(), "co1"); err == nil {
		t.Fatal("want error for 403 response")
	} else if !strings.Contains(err.Error(), "403") {
		t.Fatalf("error should mention status code, got: %v", err)
	}
}

func TestHTTPClient_ListAgents_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if _, err := c.ListAgents(context.Background(), "co1"); err == nil {
		t.Fatal("want error for malformed JSON body")
	}
}

func TestHTTPClient_ListAgents_ConnectionRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // closed immediately: connections to it are refused

	c := newHTTPClient(srv.URL, "key")
	if _, err := c.ListAgents(context.Background(), "co1"); err == nil {
		t.Fatal("want error for connection refused")
	}
}

func TestHTTPClient_ListAgents_CancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]Agent{})
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := newHTTPClient(srv.URL, "key")
	if _, err := c.ListAgents(ctx, "co1"); err == nil {
		t.Fatal("want error for cancelled context, request must not go out")
	}
}

func TestHTTPClient_PauseAgent_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agents/a1/pause" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if err := c.PauseAgent(context.Background(), "a1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClient_PauseAgent_InvalidStateTransition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"invalid state transition"}`))
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if err := c.PauseAgent(context.Background(), "a1"); err == nil {
		t.Fatal("want error for 422 response")
	}
}

func TestHTTPClient_PauseAgent_EmptyID(t *testing.T) {
	c := newHTTPClient("http://unused", "key")
	if err := c.PauseAgent(context.Background(), ""); err == nil {
		t.Fatal("want error for empty agent id, must not call the API")
	}
}

func TestHTTPClient_ResumeAgent_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/agents/a1/resume" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if err := c.ResumeAgent(context.Background(), "a1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClient_ResumeAgent_EmptyID(t *testing.T) {
	c := newHTTPClient("http://unused", "key")
	if err := c.ResumeAgent(context.Background(), "  "); err == nil {
		t.Fatal("want error for whitespace-only agent id, must not call the API")
	}
}

func TestHTTPClient_WakeAgent_SendsSourceOnDemand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/a1/wakeup" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["source"] != "on_demand" {
			t.Fatalf("want source=on_demand, got %+v", body)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if err := c.WakeAgent(context.Background(), "a1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClient_WakeAgent_EmptyID(t *testing.T) {
	c := newHTTPClient("http://unused", "key")
	if err := c.WakeAgent(context.Background(), ""); err == nil {
		t.Fatal("want error for empty agent id, must not call the API")
	}
}

func TestHTTPClient_ListIssues_FiltersByStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/companies/co1/issues" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("status") != "blocked" {
			t.Fatalf("want status=blocked query param, got %q", r.URL.RawQuery)
		}
		json.NewEncoder(w).Encode([]Issue{{ID: "i1", Identifier: "PAP-1", Status: "blocked"}})
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	issues, err := c.ListIssues(context.Background(), "co1", "blocked")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(issues) != 1 || issues[0].ID != "i1" {
		t.Fatalf("unexpected issues: %+v", issues)
	}
}

func TestHTTPClient_GetIssue_Happy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/issues/i1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(Issue{ID: "i1", Identifier: "PAP-1", Status: "blocked", AssigneeAgentID: "a1"})
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	issue, err := c.GetIssue(context.Background(), "i1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if issue.AssigneeAgentID != "a1" {
		t.Fatalf("unexpected issue: %+v", issue)
	}
}

func TestHTTPClient_GetIssue_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if _, err := c.GetIssue(context.Background(), "missing"); err == nil {
		t.Fatal("want error for 404 response")
	}
}

func TestHTTPClient_GetIssue_EmptyID(t *testing.T) {
	c := newHTTPClient("http://unused", "key")
	if _, err := c.GetIssue(context.Background(), ""); err == nil {
		t.Fatal("want error for empty issue id, must not call the API")
	}
}

func TestHTTPClient_UpdateIssueStatus_SendsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/issues/i1" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["status"] != "todo" || body["comment"] == "" {
			t.Fatalf("unexpected body: %+v", body)
		}
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	if err := c.UpdateIssueStatus(context.Background(), "i1", "todo", "retried"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPClient_UpdateIssueStatus_EmptyID(t *testing.T) {
	c := newHTTPClient("http://unused", "key")
	if err := c.UpdateIssueStatus(context.Background(), "", "todo", "x"); err == nil {
		t.Fatal("want error for empty issue id, must not call the API")
	}
}

func TestHTTPClient_ErrorBodyEchoIsCapped(t *testing.T) {
	huge := strings.Repeat("X", 5000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(huge))
	}))
	defer srv.Close()

	c := newHTTPClient(srv.URL, "key")
	_, err := c.ListAgents(context.Background(), "co1")
	if err == nil {
		t.Fatal("want error for 500 response")
	}
	if len(err.Error()) > maxErrorBodyEcho+200 {
		t.Fatalf("error message not capped, got %d chars: %.50s...", len(err.Error()), err.Error())
	}
	if strings.Contains(err.Error(), huge) {
		t.Fatal("full upstream body leaked into error, want it truncated")
	}
}
