package commands_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// fakeOptimal is Optimal's own API: sessions, a stream and a message endpoint. It records
// what the CLI sent and with which credential.
type fakeOptimal struct {
	mu        sync.Mutex
	calls     []string
	auth      []string
	messages  []string
	events    string
	sessions  string
	approvals string
	runs      string
	run       string
	// decideStatus is the status POST /approvals/:id/approve|reject answers (default 200).
	decideStatus int
}

func newFakeOptimal(t *testing.T, events string) *fakeOptimal {
	t.Helper()
	f := &fakeOptimal{events: events}
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, r.Method+" "+r.URL.Path)
		f.auth = append(f.auth, r.Header.Get("Authorization")+"|"+r.URL.Query().Get("token"))
		f.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sessions":
			fmt.Fprint(w, `{"id":"s1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/s1/stream":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, f.events)
		case r.Method == http.MethodPost && r.URL.Path == "/sessions/s1/message":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.messages = append(f.messages, fmt.Sprint(body["message"]))
			f.mu.Unlock()
			fmt.Fprint(w, `{"status":"accepted","turn_id":"t1"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/sessions":
			f.mu.Lock()
			body := f.sessions
			f.mu.Unlock()
			fmt.Fprint(w, body)
		case r.Method == http.MethodGet && r.URL.Path == "/approvals" && f.approvals != "":
			fmt.Fprint(w, f.approvals)
		case r.Method == http.MethodPost && r.URL.Path == "/sessions/s1/cancel":
			fmt.Fprint(w, `{"status":"cancelled"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/sessions/s1":
			fmt.Fprint(w, `{"id":"s1","running":false}`)
		case r.Method == http.MethodGet && r.URL.Path == "/runs":
			f.mu.Lock()
			body := f.runs
			f.mu.Unlock()
			fmt.Fprint(w, body)
		case r.Method == http.MethodGet && r.URL.Path == "/runs/run_1":
			f.mu.Lock()
			body := f.run
			f.mu.Unlock()
			fmt.Fprint(w, body)
		case r.Method == http.MethodPost && r.URL.Path == "/runs/run_1/cancel":
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"status":"cancel_requested","run":{"id":"run_1","chat_id":"s1","state":"running","cancel_requested":true}}`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/approvals/perm_1/"):
			if f.decideStatus == http.StatusForbidden {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":"approval_scope_required","details":"This API key cannot approve or reject changes."}`)
				return
			}
			fmt.Fprint(w, `{"id":"perm_1","status":"approved"}`)
		default:
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
		}
	})
	t.Setenv("MIOSA_OPTIMAL_URL", srv.URL)
	return f
}

const orgTurn = "event: tool_call\ndata: {\"tool\":\"list_sandboxes\",\"phase\":\"start\"}\n\n" +
	"event: agent_response\ndata: {\"response\":\"default has 2 sandboxes, staging has 1.\"}\n\n" +
	"event: response_complete\ndata: {}\n\n"

func TestOptimalChatOrgWideStreamsJSONWithScope(t *testing.T) {
	api := fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"opt-org-token"}`})
	opt := newFakeOptimal(t, orgTurn)

	out, err := run(t, "optimal", "chat", "how many sandboxes are in each workspace?", "--org-wide", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(api.find("POST", "/optimal/org-token")) != 1 {
		t.Fatalf("compute calls = %+v", api.calls)
	}
	var scopes []string
	var answer string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var ev map[string]any
		if json.Unmarshal([]byte(line), &ev) != nil {
			t.Fatalf("not one JSON event per line: %q", line)
		}
		if ev["chat_id"] != "s1" {
			t.Fatalf("event without its chat: %q", line)
		}
		if ev["type"] == "message" {
			scopes = append(scopes, fmt.Sprint(ev["scope"]))
			answer = fmt.Sprint(ev["content"])
		}
	}
	if len(scopes) != 1 || scopes[0] != "org-wide" || !strings.Contains(answer, "staging has 1") {
		t.Fatalf("scopes=%v answer=%q out=%s", scopes, answer, out)
	}
	if len(opt.messages) != 1 || opt.messages[0] != "how many sandboxes are in each workspace?" {
		t.Fatalf("messages = %v", opt.messages)
	}
	for _, a := range opt.auth {
		if !strings.Contains(a, "opt-org-token") {
			t.Fatalf("Optimal call without the minted credential: %v", opt.auth)
		}
	}
}

func TestOptimalChatOrgWideWriteStopsAtTheWorkspaceQuestion(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"opt-org-token"}`})
	newFakeOptimal(t, "event: needs_workspace\ndata: {\"request_id\":\"q1\",\"tool\":\"create_sandbox\",\"candidates\":[{\"id\":\"w1\",\"name\":\"default\"},{\"id\":\"w2\",\"name\":\"staging\"}]}\n\n"+
		"event: agent_response\ndata: {\"response\":\"should not be read\"}\n\n")

	out, err := run(t, "optimal", "chat", "create a sandbox named wo15-sb", "--org-wide")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "which workspace") || !strings.Contains(out, "default, staging") || !strings.Contains(out, "Nothing was changed") {
		t.Fatalf("out = %s", out)
	}
	if strings.Contains(out, "should not be read") {
		t.Fatalf("kept reading past the question: %s", out)
	}
}

func TestOptimalChatInAWorkspaceMintsForThatWorkspace(t *testing.T) {
	ws := "597374a5-8df9-4566-8103-9d2956496004"
	api := fakeRoutes(t, map[string]any{"POST /optimal/workspace-token": `{"token":"opt-ws-token"}`})
	newFakeOptimal(t, orgTurn)

	if _, err := run(t, "optimal", "chat", "hi", "--workspace", ws); err != nil {
		t.Fatal(err)
	}
	c := api.find("POST", "/optimal/workspace-token")
	if len(c) != 1 || c[0].Body["workspace_id"] != ws {
		t.Fatalf("calls = %+v", api.calls)
	}
}

func TestOptimalChatNeedsExactlyOneScope(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	if _, err := run(t, "optimal", "chat", "hi"); err == nil || !strings.Contains(err.Error(), "--org-wide") {
		t.Fatalf("no scope: err = %v", err)
	}
	if _, err := run(t, "optimal", "chat", "hi", "--org-wide", "--workspace", "x"); err == nil {
		t.Fatal("both scopes accepted")
	}
}

func TestOptimalChatReportsAnAgentError(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"t"}`})
	newFakeOptimal(t, "event: agent_error\ndata: {\"message\":\"model unavailable\"}\n\n")
	if _, err := run(t, "optimal", "chat", "hi", "--org-wide"); err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestOptimalRunsReadsRunsAndCancelStopsOne(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"t"}`})
	opt := newFakeOptimal(t, "")
	opt.runs = `{"runs":[` +
		`{"id":"run_1","chat_id":"s1","scope":"org-wide","workspace_id":"w1","state":"running","awaiting_approval":0,"started_at":"2026-10-19T10:00:00Z"},` +
		`{"id":"run_2","chat_id":"s3","scope":"org-wide","workspace_id":"w2","state":"running","awaiting_approval":1,"started_at":"2026-10-19T09:00:00Z"}]}`

	out, err := run(t, "optimal", "runs", "--org-wide", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Data  []struct{ ID, State string } `json:"data"`
		Scope string                       `json:"scope"`
	}
	if json.Unmarshal([]byte(out), &page) != nil || len(page.Data) != 2 || page.Data[0].ID != "run_1" || page.Scope != "org-wide" {
		t.Fatalf("runs = %s", out)
	}

	// The cancel answers while the turn still runs; the CLI reads the run until it ends.
	opt.mu.Lock()
	opt.run = `{"id":"run_1","chat_id":"s1","state":"canceled","cancel_requested":true}`
	opt.mu.Unlock()
	out, err = run(t, "optimal", "cancel", "run_1", "--org-wide", "--json")
	if err != nil || !strings.Contains(out, `"state":"canceled"`) {
		t.Fatalf("%v %s", err, out)
	}

	// A chat id stops that chat's running run.
	out, err = run(t, "optimal", "cancel", "s1", "--org-wide", "--json")
	if err != nil || !strings.Contains(out, `"id":"run_1"`) {
		t.Fatalf("%v %s", err, out)
	}
	cancels := 0
	for _, c := range opt.calls {
		if c == "POST /runs/run_1/cancel" {
			cancels++
		}
	}
	if cancels != 2 {
		t.Fatalf("calls = %v", opt.calls)
	}
}

func TestOptimalRunShowsOneRun(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"t"}`})
	opt := newFakeOptimal(t, "")
	opt.run = `{"id":"run_1","chat_id":"s1","scope":"org-wide","workspace_id":"w1","state":"failed","error":"The turn ended with an error.","credits":4,"started_at":"2026-10-19T10:00:00Z"}`

	out, err := run(t, "optimal", "run", "run_1", "--org-wide")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Run run_1: failed", "s1 (org-wide)", "The turn ended with an error.", "credits    4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}

func TestOptimalApprovalsAndApprove(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"t"}`})
	opt := newFakeOptimal(t, "")
	opt.runs = `{"runs":[{"id":"run_2","chat_id":"s1","state":"running","awaiting_approval":1}]}`
	opt.approvals = `{"approvals":[{"id":"perm_1","title":"Create 1 sandbox","session_id":"s1","workspace_id":"w2","workspace_name":"staging","status":"pending"}]}`

	out, err := run(t, "optimal", "approvals", "--org-wide")
	if err != nil || !strings.Contains(out, "perm_1") || !strings.Contains(out, "staging") {
		t.Fatalf("%v %s", err, out)
	}

	out, err = run(t, "optimal", "approve", "perm_1", "--org-wide", "--chat", "s1")
	if err != nil || !strings.Contains(out, "Approval perm_1: approved") {
		t.Fatalf("%v %s", err, out)
	}

	if _, err := run(t, "optimal", "approve", "perm_1", "--org-wide"); err == nil {
		t.Fatal("an org-wide approve without --chat must ask for the chat")
	}

	opt.decideStatus = http.StatusForbidden
	_, err = run(t, "optimal", "reject", "perm_1", "--org-wide", "--chat", "s1")
	if err == nil || !strings.Contains(err.Error(), "osa:approve") {
		t.Fatalf("err = %v", err)
	}
}

// An org-wide chat bound to a workspace mid-turn: each event says so, and --json reports what the event says.
func TestOptimalChatJSONTakesTheScopeFromEachEvent(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"t"}`})
	newFakeOptimal(t, "event: tool_call\ndata: {\"tool\":\"list_sandboxes\",\"chat\":{\"scope\":\"org-wide\",\"workspace_id\":null}}\n\n"+
		"event: agent_response\ndata: {\"response\":\"Created in staging.\",\"chat\":{\"scope\":\"org-wide\",\"workspace_id\":\"w2\"}}\n\n"+
		"event: response_complete\ndata: {}\n\n")

	out, err := run(t, "optimal", "chat", "create a sandbox in staging", "--org-wide", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var sawBound bool
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var ev struct {
			Type        string `json:"type"`
			Scope       string `json:"scope"`
			WorkspaceID string `json:"workspace_id"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Type == "message" {
			sawBound = ev.Scope == "org-wide" && ev.WorkspaceID == "w2"
		}
	}
	if !sawBound {
		t.Fatalf("the reply should carry the bound workspace: %s", out)
	}
}

func TestOptimalChatSendsTheKeyInTheHeaderNotTheURL(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"opt-org-token"}`})
	opt := newFakeOptimal(t, orgTurn)
	if _, err := run(t, "optimal", "chat", "hello", "--org-wide"); err != nil {
		t.Fatal(err)
	}
	for i, c := range opt.calls {
		if c == "GET /sessions/s1/stream" && opt.auth[i] != "Bearer opt-org-token|" {
			t.Fatalf("stream auth = %q", opt.auth[i])
		}
	}
}

func TestOptimalChatOnAChatWaitingForApprovalSaysSoInsteadOfQueueing(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /optimal/org-token": `{"token":"t"}`})
	opt := newFakeOptimal(t, orgTurn)
	opt.approvals = `{"approvals":[{"id":"perm_1","title":"Create 1 sandbox","description":"Create a small sandbox","session_id":"s1","workspace_id":"w2","workspace_name":"staging","status":"pending"}]}`

	out, err := run(t, "optimal", "chat", "status?", "--org-wide", "--chat", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Waiting for your approval in workspace staging") || !strings.Contains(out, "perm_1") {
		t.Fatalf("out = %s", out)
	}
	if len(opt.messages) != 0 {
		t.Fatalf("sent a message that would only queue: %v", opt.messages)
	}
}
