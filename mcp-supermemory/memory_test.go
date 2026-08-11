package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// authKey uses a double-check: fast path under lock, slow file read OUTSIDE the
// lock, re-acquire to store. Under -race this catches any regression that writes
// keyCache without re-acquiring the lock. All callers must observe the loaded key.
func TestSuperMemory_AuthKey_ConcurrentLoadIsRaceFree(t *testing.T) {
	f := t.TempDir() + "/api-key"
	if err := os.WriteFile(f, []byte("thekey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sm := &superMemory{keyFile: f}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if k := sm.authKey(); k != "thekey" {
				t.Errorf("authKey = %q, want thekey", k)
			}
		}()
	}
	wg.Wait()
}

// fakePoster is a test double for the Poster port.
type fakePoster struct {
	gotPath    string
	gotPayload map[string]any
	res        []byte
	err        error
}

func (f *fakePoster) Post(_ context.Context, path string, payload any) ([]byte, error) {
	f.gotPath = path
	// Round-trip through JSON so assertions see the wire shape (e.g. []string).
	b, _ := json.Marshal(payload)
	_ = json.Unmarshal(b, &f.gotPayload)
	return f.res, f.err
}

func TestAddMemory_HappyPath_PostsDocument(t *testing.T) {
	fp := &fakePoster{res: []byte(`{"id":"doc_1"}`)}
	out, err := AddMemory(context.Background(), fp, "hermes", AddRequest{Content: "  user likes Go  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.gotPath != "/v3/documents" {
		t.Errorf("path = %q", fp.gotPath)
	}
	if fp.gotPayload["content"] != "user likes Go" {
		t.Errorf("content not trimmed: %q", fp.gotPayload["content"])
	}
	if fp.gotPayload["dreaming"] != "instant" {
		t.Errorf("dreaming = %v", fp.gotPayload["dreaming"])
	}
	tags, _ := fp.gotPayload["containerTags"].([]any)
	if len(tags) != 1 || tags[0] != "hermes" {
		t.Errorf("containerTags = %v (want default hermes)", fp.gotPayload["containerTags"])
	}
	if out.Response != `{"id":"doc_1"}` {
		t.Errorf("response = %q", out.Response)
	}
}

func TestAddMemory_EmptyContent_Errors(t *testing.T) {
	if _, err := AddMemory(context.Background(), &fakePoster{}, "hermes", AddRequest{Content: "   "}); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestAddMemory_ExplicitTagAndCustomID_Forwarded(t *testing.T) {
	fp := &fakePoster{res: []byte(`{}`)}
	_, err := AddMemory(context.Background(), fp, "hermes",
		AddRequest{Content: "x", ContainerTag: "project-a", CustomID: "conv-42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tags, _ := fp.gotPayload["containerTags"].([]any)
	if len(tags) != 1 || tags[0] != "project-a" {
		t.Errorf("explicit tag not used: %v", fp.gotPayload["containerTags"])
	}
	if fp.gotPayload["customId"] != "conv-42" {
		t.Errorf("customId = %v", fp.gotPayload["customId"])
	}
}

func TestAddMemory_NoTag_OmitsContainerTags(t *testing.T) {
	fp := &fakePoster{res: []byte(`{}`)}
	if _, err := AddMemory(context.Background(), fp, "", AddRequest{Content: "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := fp.gotPayload["containerTags"]; ok {
		t.Errorf("containerTags should be omitted when no tag: %v", fp.gotPayload["containerTags"])
	}
}

func TestAddMemory_PosterError_Propagated(t *testing.T) {
	sentinel := errors.New("connection refused")
	_, err := AddMemory(context.Background(), &fakePoster{err: sentinel}, "hermes", AddRequest{Content: "x"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error should wrap poster error, got %v", err)
	}
}

func TestSearchMemory_HappyPath_PostsSearch(t *testing.T) {
	fp := &fakePoster{res: []byte(`{"results":[]}`)}
	out, err := SearchMemory(context.Background(), fp, "hermes", SearchRequest{Query: "  what does the user like  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.gotPath != "/v4/search" {
		t.Errorf("path = %q", fp.gotPath)
	}
	if fp.gotPayload["q"] != "what does the user like" {
		t.Errorf("q not trimmed: %q", fp.gotPayload["q"])
	}
	if fp.gotPayload["searchMode"] != "hybrid" {
		t.Errorf("searchMode = %v", fp.gotPayload["searchMode"])
	}
	// JSON numbers decode to float64.
	if fp.gotPayload["limit"] != float64(defaultSearchLimit) {
		t.Errorf("default limit = %v", fp.gotPayload["limit"])
	}
	// /v4/search uses containerTag (singular string), not the plural array.
	if fp.gotPayload["containerTag"] != "hermes" {
		t.Errorf("containerTag = %v (want singular string hermes)", fp.gotPayload["containerTag"])
	}
	if _, isArray := fp.gotPayload["containerTags"]; isArray {
		t.Errorf("v4/search must not send plural containerTags: %v", fp.gotPayload["containerTags"])
	}
	if out.Response != `{"results":[]}` {
		t.Errorf("response = %q", out.Response)
	}
}

// ---- DeleteMemory ----

// fakeDeleter is a test double for the Deleter port.
type fakeDeleter struct {
	gotPath string
	res     []byte
	err     error
	called  bool
}

func (f *fakeDeleter) Delete(_ context.Context, path string) ([]byte, error) {
	f.called = true
	f.gotPath = path
	return f.res, f.err
}

func TestDeleteMemory_HappyPath_DeletesDocument(t *testing.T) {
	fd := &fakeDeleter{res: []byte(`{"deleted":true}`)}
	out, err := DeleteMemory(context.Background(), fd, DeleteRequest{DocumentID: "doc_123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fd.called {
		t.Fatal("deleter must be called")
	}
	if fd.gotPath != "/v3/documents/doc_123" {
		t.Errorf("path = %q, want /v3/documents/doc_123", fd.gotPath)
	}
	if out.Response != `{"deleted":true}` {
		t.Errorf("response = %q", out.Response)
	}
}

func TestDeleteMemory_TrimsDocumentID(t *testing.T) {
	fd := &fakeDeleter{res: []byte(`{}`)}
	if _, err := DeleteMemory(context.Background(), fd, DeleteRequest{DocumentID: "  doc_9  "}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fd.gotPath != "/v3/documents/doc_9" {
		t.Errorf("path = %q, want trimmed /v3/documents/doc_9", fd.gotPath)
	}
}

func TestDeleteMemory_EmptyID_Errors(t *testing.T) {
	fd := &fakeDeleter{res: []byte(`{}`)}
	if _, err := DeleteMemory(context.Background(), fd, DeleteRequest{DocumentID: "   "}); err == nil {
		t.Fatal("expected error for empty document_id")
	}
	if fd.called {
		t.Fatal("deleter must not be called for empty document_id")
	}
}

func TestDeleteMemory_RejectsPathTraversal(t *testing.T) {
	cases := []string{
		"../../etc/passwd",
		"a/b/c",
		"..%2F..%2Fetc",
		"doc\\id",
		"doc\u0000id",
		"/v3/documents/other",
		".",
		"..",
	}
	for _, id := range cases {
		fd := &fakeDeleter{res: []byte(`{}`)}
		if _, err := DeleteMemory(context.Background(), fd, DeleteRequest{DocumentID: id}); err == nil {
			t.Errorf("expected error for document_id %q", id)
		}
		if fd.called {
			t.Errorf("deleter must not be called for document_id %q", id)
		}
	}
}

// custom_id is free-form on the add_memory side, so delete must cope with the
// characters add accepts (":", "@", "#", spaces) instead of rejecting them —
// otherwise those memories would be undeletable. They are escaped, not banned.
func TestDeleteMemory_EscapesCustomIDCharacters(t *testing.T) {
	cases := map[string]string{
		"conv:42":        "/v3/documents/conv:42",
		"doc with space": "/v3/documents/doc%20with%20space",
		"user@host":      "/v3/documents/user@host",
		"thread#1":       "/v3/documents/thread%231",
		"città":          "/v3/documents/citt%C3%A0",
		"release+notes":  "/v3/documents/release%2Bnotes",
	}
	for id, wantPath := range cases {
		fd := &fakeDeleter{res: []byte(`{}`)}
		if _, err := DeleteMemory(context.Background(), fd, DeleteRequest{DocumentID: id}); err != nil {
			t.Errorf("document_id %q: unexpected error: %v", id, err)
			continue
		}
		if fd.gotPath != wantPath {
			t.Errorf("document_id %q: path = %q, want %q", id, fd.gotPath, wantPath)
		}
	}
}

func TestDeleteMemory_DeleterError_Propagated(t *testing.T) {
	sentinel := errors.New("connection refused")
	_, err := DeleteMemory(context.Background(), &fakeDeleter{err: sentinel}, DeleteRequest{DocumentID: "doc_1"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error should wrap deleter error, got %v", err)
	}
}

func TestSearchMemory_EmptyQuery_Errors(t *testing.T) {
	if _, err := SearchMemory(context.Background(), &fakePoster{}, "hermes", SearchRequest{Query: " "}); err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearchMemory_CustomLimit_Forwarded(t *testing.T) {
	fp := &fakePoster{res: []byte(`{}`)}
	if _, err := SearchMemory(context.Background(), fp, "hermes", SearchRequest{Query: "x", Limit: 3}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp.gotPayload["limit"] != float64(3) {
		t.Errorf("custom limit not forwarded: %v", fp.gotPayload["limit"])
	}
}

// --- real adapter against a stub supermemory HTTP server ---

func TestSuperMemory_Post_SendsBodyPathContentType_Decodes(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, client: srv.Client()} // no apiKey
	data, err := sm.Post(context.Background(), "/v3/documents", map[string]any{"content": "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/v3/documents" || gotCT != "application/json" || gotBody["content"] != "hi" {
		t.Errorf("path=%q ct=%q body=%v", gotPath, gotCT, gotBody)
	}
	if gotAuth != "" {
		t.Errorf("no Authorization header expected when apiKey empty, got %q", gotAuth)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("data = %q", string(data))
	}
}

func TestSuperMemory_Post_WithAPIKey_SetsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, keyEnv: "secret", client: srv.Client()}
	if _, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
}

func TestSuperMemory_Post_RefreshesKeyOn401(t *testing.T) {
	f := t.TempDir() + "/api-key"
	if err := os.WriteFile(f, []byte("goodkey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer goodkey" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"Unauthorized"}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	// keyCache starts stale (as if the server regenerated its key); the file has
	// the current one. First attempt 401s, refresh reads the file, retry succeeds.
	sm := superMemory{baseURL: srv.URL, keyFile: f, keyCache: "stalekey", client: srv.Client()}
	data, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"ok":true}` {
		t.Errorf("data = %q", string(data))
	}
	if calls != 2 {
		t.Errorf("want 2 calls (401 then retry), got %d", calls)
	}
}

func TestSuperMemory_Post_401WithoutKeyFile_NoRetry(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "nope")
	}))
	defer srv.Close()

	// No keyFile and no keyEnv → a 401 must NOT loop; it returns the error once.
	sm := superMemory{baseURL: srv.URL, client: srv.Client()}
	if _, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"}); err == nil {
		t.Fatal("expected error on 401")
	}
	if calls != 1 {
		t.Errorf("want 1 call (no refresh path), got %d", calls)
	}
}

func TestSuperMemory_Post_CapsResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(bytes.Repeat([]byte("a"), maxResponseBytes+1024))
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, client: srv.Client()}
	data, err := sm.Post(context.Background(), "/v4/search", map[string]any{"q": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != maxResponseBytes {
		t.Errorf("response not capped: got %d bytes, want %d", len(data), maxResponseBytes)
	}
}

func TestSuperMemory_Delete_SendsDeleteMethodAndAuth(t *testing.T) {
	var gotMethod, gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, `{"deleted":true}`)
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, keyEnv: "secret", client: srv.Client()}
	data, err := sm.Delete(context.Background(), "/v3/documents/doc_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/v3/documents/doc_1" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if string(data) != `{"deleted":true}` {
		t.Errorf("data = %q", string(data))
	}
}

func TestSuperMemory_Delete_RefreshesKeyOn401(t *testing.T) {
	f := t.TempDir() + "/api-key"
	if err := os.WriteFile(f, []byte("goodkey\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Authorization") != "Bearer goodkey" {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"error":"Unauthorized"}`)
			return
		}
		io.WriteString(w, `{"deleted":true}`)
	}))
	defer srv.Close()

	// keyCache starts stale; file has current key. First DELETE 401s, refresh
	// reads the file, retry succeeds.
	sm := superMemory{baseURL: srv.URL, keyFile: f, keyCache: "stalekey", client: srv.Client()}
	data, err := sm.Delete(context.Background(), "/v3/documents/doc_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"deleted":true}` {
		t.Errorf("data = %q", string(data))
	}
	if calls != 2 {
		t.Errorf("want 2 calls (401 then retry), got %d", calls)
	}
}

func TestSuperMemory_Post_HTTPError_Errors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer srv.Close()

	sm := superMemory{baseURL: srv.URL, client: srv.Client()}
	_, err := sm.Post(context.Background(), "/v3/documents", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected http 500 error, got %v", err)
	}
}
