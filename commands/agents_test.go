package commands_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const runID = "5d0f2a64-3b7a-4d6d-9d3a-1a2b3c4d5e6f"

// runAPI serves a run that is running for `polls` reads, then reaches final.
func runAPI(t *testing.T, final string, polls int, extra func(c call) (int, string, bool)) *fakeAPI {
	t.Helper()
	var reads int32
	return newFakeAPI(t, func(c call) (int, string) {
		if extra != nil {
			if st, body, ok := extra(c); ok {
				return st, body
			}
		}
		switch {
		case c.is("GET", "/sandboxes/by-name/my-box"):
			return 200, `{"id":"` + boxID + `"}`
		case c.is("POST", "/runs"):
			return 202, `{"data":{"id":"` + runID + `","status":"queued","runner":"claude-code","chat_id":null}}`
		case c.is("GET", "/runs/"+runID+"/events"):
			ev := `{"data":[{"id":"e1","type":"run.created","message":"Run created"}`
			if atomic.LoadInt32(&reads) >= 1 {
				ev += `,{"id":"e2","type":"run.running","message":"Run running"}`
			}
			return 200, ev + `]}`
		case c.is("GET", "/runs/"+runID):
			st := "running"
			if int(atomic.AddInt32(&reads, 1)) > polls {
				st = final
			}
			extraFields := ""
			if st == "failed" {
				extraFields = `,"error_code":"RUN_FAILED","error_message":"{\"type\":\"turn.failed\",\"error\":{\"message\":\"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header\"}}"`
			}
			return 200, `{"data":{"id":"` + runID + `","status":"` + st + `","runner":"claude-code","cost":{"total_credits":12}` + extraFields + `}}`
		case c.is("GET", "/runs/"+runID+"/outputs"):
			return 200, `{"data":{"result":{"type":"text","text":"All done: 3 files changed."}}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND"}}`
	})
}

func TestPromptRunsAndPrintsTheAnswer(t *testing.T) {
	f := runAPI(t, "succeeded", 1, nil)
	defer commands.SetRunPollForTest(time.Millisecond)()
	out, errOut, err := runSplit(t, "prompt", "--sandbox", "my-box", "--model", "opus", "add", "a", "health", "check")
	if err != nil {
		t.Fatalf("prompt: %v\n%s", err, out)
	}
	if out != "All done: 3 files changed.\n" {
		t.Fatalf("stdout must carry only the answer, got %q", out)
	}
	if !strings.Contains(errOut, "run.running") || !strings.Contains(errOut, "succeeded run") {
		t.Fatalf("progress belongs on stderr: %q", errOut)
	}
	body := f.find("POST", "/runs")[0].Body
	if body["instruction"] != "add a health check" || body["runner"] != "claude-code" || body["model"] != "opus" || body["sandbox_id"] != boxID || body["source"] != "cli" {
		t.Fatalf("body = %v", body)
	}
}

func TestPromptBodyOptions(t *testing.T) {
	f := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
		if c.is("GET", "/agents") {
			return 200, `{"data":[{"id":"` + boxID + `","name":"reviewer"}]}`, true
		}
		if c.is("GET", "/workspaces") {
			return 200, `{"data":[{"id":"w-1","slug":"acme","name":"Acme"}]}`, true
		}
		return 0, "", false
	})
	defer commands.SetRunPollForTest(time.Millisecond)()
	_, err := run(t, "prompt", "--agent", "reviewer", "--new-machine", "--workspace", "acme", "--size", "medium", "--template", "nextjs",
		"--machine", "sandbox", "--after-run", "pause", "--reuse", "chat", "--max-time", "600", "--notify-email", "--notify-webhook", "wh-1",
		"--idempotency-key", "k-1", "--harness", "codex", "--source", "ci", "review it")
	if err != nil {
		t.Fatal(err)
	}
	c := f.find("POST", "/runs")[0]
	b := c.Body
	m, _ := b["machine"].(map[string]any)
	n, _ := b["notify"].(map[string]any)
	if b["agent_definition_id"] != boxID || b["target"] != "new" || b["workspace_id"] != "w-1" || b["runner"] != "codex" || b["timeout_sec"] != float64(600) || b["source"] != "ci" {
		t.Fatalf("body = %v", b)
	}
	if m["size"] != "medium" || m["template"] != "nextjs" || m["target"] != "sandbox" || m["after_run"] != "pause" || m["reuse"] != "chat" {
		t.Fatalf("machine = %v", m)
	}
	if n["email"] != true || n["webhook_id"] != "wh-1" {
		t.Fatalf("notify = %v", n)
	}
	if b["sandbox_id"] != nil {
		t.Fatal("a new machine run names no sandbox")
	}
}

func TestPromptNeedsATargetAndAPrompt(t *testing.T) {
	f := runAPI(t, "succeeded", 0, nil)
	for _, args := range [][]string{
		{"prompt"},          // no text
		{"prompt", "do it"}, // no target
		{"prompt", "--new-machine", "-s", "x", "do it"},
		{"prompt", "-s", "x", "--computer", "y", "do it"},
		{"prompt", "-s", "x", "--chat", "a", "--new-chat", "do it"},
		{"prompt", "-s", "x", "--harness", "gemini", "do it"},
		{"prompt", "-s", "x", "--after-run", "explode", "do it"},
		{"prompt", "-s", "x", "--reuse", "forever", "do it"},
	} {
		_, err := run(t, args...)
		if err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("%v: err=%v exit=%d", args, err, commands.ExitCode(err))
		}
	}
	if len(f.find("POST", "/runs")) != 0 {
		t.Fatal("invalid prompts must not start runs")
	}
}

func TestPromptReadsStdinAndFile(t *testing.T) {
	f := runAPI(t, "succeeded", 0, nil)
	defer commands.SetRunPollForTest(time.Millisecond)()
	if _, err := runIn(t, "from a pipe\n", "prompt", "-s", "my-box", "-"); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "p.md")
	os.WriteFile(file, []byte("from a file\n"), 0o600)
	if _, err := run(t, "prompt", "-s", "my-box", "--file", file); err != nil {
		t.Fatal(err)
	}
	calls := f.find("POST", "/runs")
	if calls[0].Body["instruction"] != "from a pipe" || calls[1].Body["instruction"] != "from a file" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestPromptUsesTheCurrentSandbox(t *testing.T) {
	f := runAPI(t, "succeeded", 0, nil)
	defer commands.SetRunPollForTest(time.Millisecond)()
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \"my-box\"\n")
	if _, err := run(t, "prompt", "go"); err != nil {
		t.Fatal(err)
	}
	if f.find("POST", "/runs")[0].Body["sandbox_id"] != boxID {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestPromptFailureExitsNonZeroWithAReadableReasonAndTheBYOKHint(t *testing.T) {
	runAPI(t, "failed", 0, nil)
	defer commands.SetRunPollForTest(time.Millisecond)()
	out, _, err := runSplit(t, "prompt", "-s", "my-box", "go")
	if err == nil || commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("err = %v", err)
	}
	if out != "" {
		t.Fatalf("a failed run prints no answer on stdout: %q", out)
	}
	msg := err.Error()
	for _, want := range []string{"unexpected status 401 Unauthorized", "miosa connections add models", "does not supply one", "miosa run diagnostics " + runID} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, `{"type"`) {
		t.Errorf("raw harness JSON leaked into the message:\n%s", msg)
	}
}

func TestPromptNoFollowPrintsOnlyTheRunID(t *testing.T) {
	f := runAPI(t, "succeeded", 0, nil)
	out, _, err := runSplit(t, "prompt", "-s", "my-box", "--no-follow", "--new-chat", "go")
	if err != nil || strings.TrimSpace(out) != runID {
		t.Fatalf("%v %q", err, out)
	}
	chat, _ := f.find("POST", "/runs")[0].Body["chat_id"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(chat) {
		t.Fatalf("--new-chat must send a v4 uuid, got %q", chat)
	}
	if len(f.find("GET", "/runs/"+runID)) != 0 {
		t.Fatal("--no-follow must not poll")
	}
}

func TestPromptJSONStreamsEventsThenTheRun(t *testing.T) {
	runAPI(t, "succeeded", 1, nil)
	defer commands.SetRunPollForTest(time.Millisecond)()
	out, err := run(t, "prompt", "-s", "my-box", "go", "--json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(strings.NewReader(out))
	var kinds []string
	var last map[string]any
	for dec.More() {
		var v map[string]any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, out)
		}
		if ev, ok := v["event"].(string); ok {
			kinds = append(kinds, ev)
		}
		last = v
	}
	if len(kinds) < 1 || kinds[0] != "run.created" || last["run"] == nil || last["outputs"] == nil {
		t.Fatalf("kinds=%v last=%v", kinds, last)
	}
}

func TestPromptContinueAndChatResumeFromTheLastFinishedRun(t *testing.T) {
	prev := "11111111-1111-1111-1111-111111111111"
	f := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
		if c.is("GET", "/runs") {
			q, _ := url.ParseQuery(c.Query)
			if q.Get("chat_id") == "chat-1" || q.Get("target_id") == boxID {
				return 200, `{"data":[{"id":"22222222-2222-2222-2222-222222222222","status":"running"},{"id":"` + prev + `","status":"succeeded"}]}`, true
			}
			return 200, `{"data":[]}`, true
		}
		return 0, "", false
	})
	defer commands.SetRunPollForTest(time.Millisecond)()
	if _, err := run(t, "prompt", "-s", "my-box", "--chat", "chat-1", "more"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "prompt", "-s", "my-box", "--continue", "again"); err != nil {
		t.Fatal(err)
	}
	for i, c := range f.find("POST", "/runs") {
		if c.Body["continue_from_run_id"] != prev {
			t.Errorf("call %d must continue from the newest FINISHED run, body = %v", i, c.Body)
		}
	}
	if f.find("POST", "/runs")[0].Body["chat_id"] != "chat-1" {
		t.Fatal("chat id must be sent")
	}
	// Nothing to continue from.
	if _, err := run(t, "prompt", "-s", "my-box", "--chat", "none", "x"); err != nil {
		t.Fatalf("a new chat id starts fresh: %v", err)
	}
	f2 := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
		if c.is("GET", "/runs") {
			return 200, `{"data":[]}`, true
		}
		return 0, "", false
	})
	_, err := run(t, "prompt", "-s", "my-box", "--continue", "x")
	if err == nil || commands.ExitCode(err) != commands.ExitNotFound || len(f2.find("POST", "/runs")) != 0 {
		t.Fatalf("--continue with no history: %v", err)
	}
}

func TestPromptServerRefusalsKeepTheirCodes(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
		exit   int
	}{
		{403, `{"error":{"code":"AGENT_RUN_FORBIDDEN","message":"your role may not run agents"}}`, "AGENT_RUN_FORBIDDEN", commands.ExitAuth},
		{422, `{"error":{"code":"OWN_CREDENTIALS_REQUIRED","message":"connect your own key"}}`, "OWN_CREDENTIALS_REQUIRED", commands.ExitConflict},
		{409, `{"error":{"code":"RUN_SESSION_NOT_RESUMABLE","details":{"reason":"still_running"}}}`, "RUN_SESSION_NOT_RESUMABLE", commands.ExitConflict},
		{402, `{"error":{"code":"INSUFFICIENT_CREDITS"}}`, "billing", commands.ExitAuth + 0},
	}
	for _, tc := range cases {
		runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
			if c.is("POST", "/runs") {
				return tc.status, tc.body, true
			}
			return 0, "", false
		})
		_, err := run(t, "prompt", "-s", "my-box", "go")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d: err = %v", tc.status, err)
		}
	}
}

func TestSummarizeRunError(t *testing.T) {
	cases := map[string]string{
		`{"type":"turn.started"} {"type":"turn.failed","error":{"message":"rate limit exceeded"}}`:                           "rate limit exceeded",
		`{"type":"error","message":"Reconnecting... 2/5 (unexpected status 401 Unauthorized: bad key, url: wss://x)"} {"typ`: "unexpected status 401 Unauthorized: bad key",
		`Run exited with code 1: timeout`: "Run exited with code 1: timeout",
		``:                                "the run failed",
	}
	for in, want := range cases {
		if got := commands.SummarizeRunErrorForTest(in); got != want {
			t.Errorf("summarize(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("x", 500)
	if got := commands.SummarizeRunErrorForTest(long); len(got) > 223 {
		t.Errorf("not clipped: %d", len(got))
	}
}

func TestRunCommands(t *testing.T) {
	runJSON := `{"id":"` + runID + `","status":"failed","runner":"codex","model":"gpt-6","created_at":"2026-10-01T00:00:00Z","chat_id":"3f0c1111-0000-4000-8000-000000000000","instruction":"fix the tests","error_code":"RUN_FAILED"}`
	f := fakeRoutes(t, map[string]any{
		"GET /runs":                                 `{"data":[` + runJSON + `]}`,
		"GET /runs/" + runID:                        `{"data":` + runJSON + `}`,
		"POST /runs/" + runID + "/cancel":           `{"data":{"id":"` + runID + `","status":"canceled"}}`,
		"GET /runs/" + runID + "/events":            `{"data":[{"type":"run.failed","status":"failed","message":"Run failed","created_at":"2026-10-01T00:00:00Z"}]}`,
		"GET /runs/" + runID + "/outputs":           `{"data":{"result":{"text":"hello"}}}`,
		"GET /runs/" + runID + "/messages":          `{"data":[]}`,
		"GET /runs/" + runID + "/files":             `{"data":[{"id":"f1f1f1f1-0000-0000-0000-000000000000","path":"out.txt","kind":"file","size_bytes":2048}]}`,
		"GET /runs/" + runID + "/diagnostics":       `{"data":[{"code":"RUN_FAILED","exit_code":1,"retryable":true,"message":"boom"}]}`,
		"GET /runs/" + runID + "/command-output":    `{"data":{"stdout":"out line\n","stderr":"err line\n","exit_code":3}}`,
		"GET /runs/usage":                           `{"data":{"from":"2026-10-01T00:00:00Z","to":"2026-10-09T00:00:00Z","totals":{"runs":4,"input_tokens":1000,"output_tokens":200,"estimated_usd":0.12}}}`,
		"GET /runs/" + runID + "/files/f1/download": `FILEBYTES`,
	})
	out, err := run(t, "run", "list", "--status", "failed", "--harness", "codex", "--chat", "c1", "--agent-id", "a1", "--target", "t1", "--source", "api", "-n", "5")
	if err != nil || !strings.Contains(out, "5d0f2a64") || !strings.Contains(out, "fix the tests") || !strings.Contains(out, "3f0c1111") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	q, _ := url.ParseQuery(f.find("GET", "/runs")[0].Query)
	if q.Get("status") != "failed" || q.Get("harness") != "codex" || q.Get("chat_id") != "c1" || q.Get("agent_definition_id") != "a1" || q.Get("target_id") != "t1" || q.Get("limit") != "5" {
		t.Fatalf("query = %v", q)
	}
	if out, err := run(t, "run", "get", runID); err != nil || !strings.Contains(out, "gpt-6") || !strings.Contains(out, "RUN_FAILED") {
		t.Fatalf("get: %v\n%s", err, out)
	}
	if out, err := run(t, "run", "stop", runID); err != nil || !strings.Contains(out, "canceled") {
		t.Fatalf("stop: %v\n%s", err, out)
	}
	if out, err := run(t, "run", "events", runID); err != nil || !strings.Contains(out, "run.failed") {
		t.Fatalf("events: %v\n%s", err, out)
	}
	if out, err := run(t, "run", "files", runID); err != nil || !strings.Contains(out, "out.txt") || !strings.Contains(out, "2.0 KB") {
		t.Fatalf("files: %v\n%s", err, out)
	}
	if out, err := run(t, "run", "diagnostics", runID); err != nil || !strings.Contains(out, "boom") {
		t.Fatalf("diagnostics: %v\n%s", err, out)
	}
	for _, sub := range []string{"outputs", "messages"} {
		if _, err := run(t, "run", sub, runID); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
	}
	if out, err := run(t, "run", "usage", "--from", "2026-10-01", "--group-by", "runner"); err != nil || !strings.Contains(out, "Runs:") || !strings.Contains(out, "1000") {
		t.Fatalf("usage: %v\n%s", err, out)
	}
	out, err = run(t, "run", "logs", runID)
	if commands.ExitCode(err) != 3 || !strings.Contains(out, "out line") {
		t.Fatalf("logs must exit with the command's status: err=%v out=%q", err, out)
	}
	dest := filepath.Join(t.TempDir(), "o.txt")
	if _, err := run(t, "run", "download", runID, "f1", "-O", dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "FILEBYTES" {
		t.Fatalf("download = %q", b)
	}
}

func TestRunFollowDetachesCleanlyForFinishedRuns(t *testing.T) {
	runAPI(t, "succeeded", 0, nil)
	defer commands.SetRunPollForTest(time.Millisecond)()
	out, err := run(t, "run", "follow", runID)
	if err != nil || !strings.Contains(out, "All done") {
		t.Fatalf("%v %q", err, out)
	}
}

func TestChatListShowAndStart(t *testing.T) {
	chat := "3f0c1111-0000-4000-8000-000000000000"
	f := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
		if c.is("GET", "/runs") {
			q, _ := url.ParseQuery(c.Query)
			if q.Get("chat_id") != "" {
				return 200, `{"data":[
 {"id":"` + runID + `","status":"succeeded","created_at":"2026-10-02T00:00:00Z","instruction":"second turn"},
 {"id":"6e6e6e6e-0000-4000-8000-000000000000","status":"succeeded","created_at":"2026-10-01T00:00:00Z","instruction":"first turn"}]}`, true
			}
			return 200, `{"data":[
 {"id":"` + runID + `","chat_id":"` + chat + `","runner":"codex","status":"succeeded","created_at":"2026-10-02T00:00:00Z","instruction":"second turn"},
 {"id":"6e6e6e6e-0000-4000-8000-000000000000","chat_id":"` + chat + `","runner":"codex","status":"succeeded","created_at":"2026-10-01T00:00:00Z","instruction":"first turn"},
 {"id":"7f7f7f7f-0000-4000-8000-000000000000","chat_id":null,"runner":"codex","status":"failed","created_at":"2026-09-30T00:00:00Z","instruction":"one-off"}]}`, true
		}
		return 0, "", false
	})
	defer commands.SetRunPollForTest(time.Millisecond)()
	out, err := run(t, "chat", "list")
	if err != nil || !strings.Contains(out, chat) || !strings.Contains(out, "first turn") || strings.Contains(out, "one-off") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2") { // two runs in the chat
		t.Fatalf("run count missing:\n%s", out)
	}
	out, err = run(t, "chat", "show", chat)
	if err != nil || strings.Index(out, "first turn") > strings.Index(out, "second turn") || !strings.Contains(out, "All done") {
		t.Fatalf("show must print oldest first with answers: %v\n%s", err, out)
	}
	// Interactive: each line is one follow-up in the same chat; /exit ends.
	_, err = runIn(t, "one\n\ntwo\n/exit\nnever\n", "chat", "start", "-s", "my-box", "--chat", chat)
	if err != nil {
		t.Fatal(err)
	}
	posts := f.find("POST", "/runs")
	if len(posts) != 2 || posts[0].Body["instruction"] != "one" || posts[1].Body["instruction"] != "two" || posts[1].Body["chat_id"] != chat {
		t.Fatalf("posts = %+v", posts)
	}
	// Without an id a chat id is generated.
	f2 := runAPI(t, "succeeded", 0, nil)
	if _, err := runIn(t, "hello\n", "chat", "start", "-s", "my-box"); err != nil {
		t.Fatal(err)
	}
	if id, _ := f2.find("POST", "/runs")[0].Body["chat_id"].(string); len(id) != 36 {
		t.Fatalf("generated chat id = %q", id)
	}
}

func TestAgentCRUDAndVersions(t *testing.T) {
	agent := `{"id":"` + boxID + `","name":"reviewer","status":"active","description":"reviews","updated_at":"2026-10-01T00:00:00Z"}`
	f := fakeRoutes(t, map[string]any{
		"GET /agents":                          `{"data":[` + agent + `],"next_cursor":null}`,
		"POST /agents":                         resp{201, `{"data":{"definition":` + agent + `,"version":{"version":1}}}`},
		"GET /agents/" + boxID:                 `{"data":` + agent + `}`,
		"PATCH /agents/" + boxID:               `{"data":` + agent + `}`,
		"DELETE /agents/" + boxID:              `{"data":{"status":"archived"}}`,
		"POST /agents/" + boxID + "/restore":   `{"data":{"status":"active"}}`,
		"POST /agents/" + boxID + "/duplicate": resp{201, `{"data":` + agent + `}`},
		"GET /agents/" + boxID + "/versions":   `{"data":[{"version":1,"fingerprint":"abcdef1234","configuration":{"harness":"codex","model":"gpt-6"},"created_at":"2026-10-01T00:00:00Z"}]}`,
		"GET /agents/" + boxID + "/versions/1": `{"data":{"version":1,"configuration":{"harness":"codex","model":"gpt-6","execution":{"overdrive":false}}}}`,
		"GET /agents/" + boxID + "/versions/2": `{"data":{"version":2,"configuration":{"harness":"codex","model":"gpt-6.1","execution":{"overdrive":true},"instructions":"be brief"}}}`,
		"POST /agents/" + boxID + "/versions":  resp{201, `{"data":{"version":2}}`},
		"GET /agents/" + boxID + "/runs":       `{"data":[]}`,
		"GET /agents/harnesses":                `{"data":[{"harness":"codex","name":"Codex","default_model":"gpt-6.1-sol","models":["gpt-6.1-sol","gpt-6"]}]}`,
		"GET /agents/defaults":                 `{"data":{"harness":"codex"}}`,
		"PUT /agents/defaults":                 `{"data":{"harness":"osa"}}`,
	})
	if out, err := run(t, "agent", "list", "--status", "active"); err != nil || !strings.Contains(out, "reviewer") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	// An agent may be given by name: the id comes from the list.
	if out, err := run(t, "agent", "get", "reviewer"); err != nil || !strings.Contains(out, "reviews") {
		t.Fatalf("get by name: %v\n%s", err, out)
	}
	if _, err := run(t, "agent", "update", "reviewer", "--description", "new"); err != nil {
		t.Fatal(err)
	}
	if u := f.find("PATCH", "/agents/"+boxID); len(u) != 1 || u[0].Body["description"] != "new" {
		t.Fatalf("update = %+v", u)
	}
	if _, err := run(t, "agent", "rm", "reviewer"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("rm without --yes: %v", err)
	}
	for _, args := range [][]string{{"agent", "rm", "reviewer", "-y"}, {"agent", "restore", "reviewer"}, {"agent", "duplicate", "reviewer", "--name", "copy"},
		{"agent", "versions", "reviewer"}, {"agent", "version", "reviewer", "1"}, {"agent", "runs", "reviewer"}, {"agent", "defaults"}} {
		if _, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if out, err := run(t, "agent", "harnesses"); err != nil || !strings.Contains(out, "gpt-6.1-sol") {
		t.Fatalf("harnesses: %v\n%s", err, out)
	}
	if _, err := run(t, "agent", "harnesses", "default", "osa"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "agent", "harnesses", "set", "codex", "--model", "gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	d := f.find("PUT", "/agents/defaults")
	if len(d) != 2 || d[0].Body["harness"] != "osa" || d[1].Body["harness"] != "codex" || d[1].Body["model"] != "gpt-6.1-sol" {
		t.Fatalf("defaults = %+v", d)
	}
	// The old spelling still reaches connections.
	if out, err := run(t, "agent", "credentials", "list"); err != nil || !strings.Contains(out, "No connections") {
		t.Fatalf("legacy alias: %v\n%s", err, out)
	}
	out, err := run(t, "agent", "diff", "reviewer", "1", "2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"model", "gpt-6.1", "execution.overdrive", "instructions", "be brief"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "harness") {
		t.Errorf("unchanged settings must not be listed:\n%s", out)
	}
}

func TestAgentNewAndPublishBuildTheConfiguration(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /agents":                        resp{201, `{"data":{"definition":{"id":"` + boxID + `"},"version":{"version":1}}}`},
		"GET /agents":                         `{"data":[{"id":"` + boxID + `","name":"builder"}]}`,
		"POST /agents/" + boxID + "/versions": resp{201, `{"data":{"version":2}}`},
	})
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.yaml")
	os.WriteFile(cfg, []byte("harness: codex\nmodel: gpt-6\nexecution:\n  max_turns: 3\ncapabilities:\n  tools: [bash]\n"), 0o600)
	ins := filepath.Join(dir, "i.md")
	os.WriteFile(ins, []byte("Review code.\n"), 0o600)

	out, err := run(t, "agent", "new", "builder", "-f", cfg, "--model", "gpt-6.1", "--instructions-file", ins, "--overdrive", "--timeout-sec", "900", "--description", "d", "--workspace-id", "w1")
	if err != nil || !strings.Contains(out, "Created agent") {
		t.Fatalf("%v\n%s", err, out)
	}
	b := f.find("POST", "/agents")[0].Body
	conf := b["configuration"].(map[string]any)
	exec := conf["execution"].(map[string]any)
	if b["name"] != "builder" || b["description"] != "d" || b["workspace_id"] != "w1" ||
		conf["harness"] != "codex" || conf["model"] != "gpt-6.1" || conf["instructions"] != "Review code." ||
		exec["overdrive"] != true || exec["timeout_seconds"] != float64(900) || exec["max_turns"] != float64(3) {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "agent", "publish", "builder", "--harness", "osa", "--instructions", "go"); err != nil {
		t.Fatal(err)
	}
	pub := f.find("POST", "/agents/"+boxID+"/versions")[0].Body["configuration"].(map[string]any)
	if pub["harness"] != "osa" || pub["instructions"] != "go" {
		t.Fatalf("publish = %v", pub)
	}
	if _, err := run(t, "agent", "publish", "builder"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("empty publish: %v", err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte(": : :"), 0o600)
	if _, err := run(t, "agent", "new", "x", "-f", bad); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad file: %v", err)
	}
}

func TestAgentUnknownNameAndMissingScope(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /agents": resp{403, `{"error":{"code":"FORBIDDEN","message":"insufficient scope - this action requires the 'agents:read' scope"}}`},
	})
	_, err := run(t, "agent", "get", "reviewer")
	if err == nil || !strings.Contains(err.Error(), "agents:read") || commands.ExitCode(err) != commands.ExitAuth {
		t.Fatalf("scope error must name the scope: %v", err)
	}
	fakeRoutes(t, map[string]any{"GET /agents": `{"data":[]}`})
	_, err = run(t, "agent", "get", "ghost")
	if err == nil || !strings.Contains(err.Error(), `no agent named "ghost"`) || commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestAgentTriggersApprovalsAuthority(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents":                                          `{"data":[{"id":"` + boxID + `","name":"reviewer"}]}`,
		"GET /agents/" + boxID + "/triggers":                   `{"data":[{"id":"t1t1t1t1-0000-0000-0000-000000000000","kind":"schedule","status":"active","schedule":"0 9 * * *"}]}`,
		"POST /agents/" + boxID + "/triggers":                  resp{201, `{"data":{"id":"t2"}}`},
		"DELETE /agents/" + boxID + "/triggers/t1":             `{}`,
		"POST /agents/" + boxID + "/triggers/t1/fire":          `{"data":{"run_id":"r1"}}`,
		"POST /agents/" + boxID + "/triggers/t1/rotate-secret": `{"data":{"secret":"whsec_x"}}`,
		"GET /actions/approvals":                               `{"data":[{"id":"ap1ap1ap1","action":"deploy.publish","status":"pending"}]}`,
		"POST /actions/approvals/ap1/approve":                  `{}`,
		"POST /actions/approvals/ap1/deny":                     `{}`,
		"GET /actions/catalog":                                 `{"data":[{"name":"deploy.publish","scope":"workspace","risk":"high"}]}`,
		"GET /actions/grants":                                  `{"data":[]}`,
		"DELETE /actions/grants/g1":                            `{}`,
		"GET /actions/receipts":                                `{"data":[]}`,
	})
	if out, err := run(t, "agent", "triggers", "list", "reviewer"); err != nil || !strings.Contains(out, "0 9 * * *") {
		t.Fatalf("triggers list: %v\n%s", err, out)
	}
	if _, err := run(t, "agent", "triggers", "add", "reviewer", "--kind", "schedule", "--schedule", "0 9 * * *", "--input", "daily review"); err != nil {
		t.Fatal(err)
	}
	if a := f.find("POST", "/agents/"+boxID+"/triggers"); len(a) != 1 || a[0].Body["kind"] != "schedule" || a[0].Body["schedule"] != "0 9 * * *" || a[0].Body["input"] != "daily review" {
		t.Fatalf("add = %+v", a)
	}
	if _, err := run(t, "agent", "triggers", "add", "reviewer", "--kind", "cron"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad kind: %v", err)
	}
	for _, args := range [][]string{{"agent", "triggers", "fire", "reviewer", "t1"}, {"agent", "triggers", "rotate-secret", "reviewer", "t1"},
		{"agent", "triggers", "rm", "reviewer", "t1", "-y"}, {"agent", "approvals", "list"}, {"agent", "approvals", "approve", "ap1"}, {"agent", "approvals", "deny", "ap1"},
		{"agent", "authority", "catalog"}, {"agent", "authority", "grants"}, {"agent", "authority", "receipts"}, {"agent", "authority", "revoke", "g1", "-y"}} {
		if out, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
}

func TestAgentRunIsPromptWithAnAgent(t *testing.T) {
	f := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
		if c.is("GET", "/agents") {
			return 200, `{"data":[{"id":"` + boxID + `","name":"reviewer"}]}`, true
		}
		return 0, "", false
	})
	defer commands.SetRunPollForTest(time.Millisecond)()
	if _, err := run(t, "agent", "run", "reviewer", "review", "PR", "42", "-s", "my-box"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", "/runs")[0].Body
	if b["agent_definition_id"] != boxID || b["instruction"] != "review PR 42" || b["runner"] != nil {
		t.Fatalf("body = %v (an agent run takes its harness from the agent)", b)
	}
}

func TestNewUUIDIsV4(t *testing.T) {
	a, b := commands.NewUUIDForTest(), commands.NewUUIDForTest()
	if a == b || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(a) {
		t.Fatalf("uuids %q %q", a, b)
	}
}
