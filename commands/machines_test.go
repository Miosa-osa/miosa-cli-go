package commands_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const boxPath = "/sandboxes/" + boxID

// boxRoutes serves by-name for "my-box" plus whatever the test adds.
func boxRoutes(t *testing.T, routes map[string]any) *fakeAPI {
	t.Helper()
	all := map[string]any{"GET /sandboxes/by-name/my-box": `{"id":"` + boxID + `"}`}
	for k, v := range routes {
		all[k] = v
	}
	return fakeRoutes(t, all)
}

func TestLifecycleVerbs(t *testing.T) {
	cases := []struct {
		args     []string
		method   string
		path     string
		reply    string
		wantBody map[string]any
		wantOut  string
	}{
		{[]string{"pause", "my-box"}, "POST", boxPath + "/pause", `{"id":"` + boxID + `","state":"paused"}`, nil, "paused"},
		{[]string{"resume", "my-box"}, "POST", boxPath + "/resume", `{"id":"` + boxID + `","state":"running"}`, nil, "running"},
		{[]string{"stop", "my-box"}, "POST", boxPath + "/stop", `{"id":"` + boxID + `","state":"paused","persistent":true}`, nil, "paused"},
		{[]string{"recover", "my-box"}, "POST", boxPath + "/recover", `{"id":"new-id","state":"provisioning","restored_from":"disk"}`, nil, "new-id"},
		{[]string{"fork", "my-box", "--ttl", "10m", "--template", "t1"}, "POST", boxPath + "/fork", `{"id":"fork-id","state":"provisioning"}`,
			map[string]any{"timeout_sec": float64(600), "template_id": "t1"}, "fork-id"},
		{[]string{"extend", "my-box", "--for", "2h"}, "POST", boxPath + "/extend", `{"state":"running","timeout_sec":7200,"timeout_remaining_ms":7199000}`,
			map[string]any{"timeout_sec": float64(7200)}, "7200"},
		{[]string{"usage", "my-box"}, "GET", boxPath + "/usage", `{"data":{"state":"running","runtime_sec":50,"timeout_remaining_ms":3598854,"billing_rate_cents_per_hour":15.518699999999999,"estimated_cost_cents":0.22}}`, nil, "15.5187"},
		{[]string{"ports", "my-box"}, "GET", boxPath + "/ports", `{"count":1,"data":[{"port":8080,"protocol":"tcp","address":"0.0.0.0","process":{"name":"python3","pid":12}}]}`, nil, "python3"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			f := boxRoutes(t, map[string]any{tc.method + " " + tc.path: tc.reply})
			out, err := run(t, tc.args...)
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			if !strings.Contains(out, tc.wantOut) {
				t.Errorf("output lacks %q:\n%s", tc.wantOut, out)
			}
			calls := f.find(tc.method, tc.path)
			if len(calls) != 1 {
				t.Fatalf("calls = %+v", f.calls)
			}
			for k, v := range tc.wantBody {
				if calls[0].Body[k] != v {
					t.Errorf("body[%s] = %v, want %v (body %v)", k, calls[0].Body[k], v, calls[0].Body)
				}
			}
			jout, err := run(t, append(tc.args, "--json")...)
			var parsed any
			if err != nil || json.Unmarshal([]byte(jout), &parsed) != nil {
				t.Errorf("--json not valid: %v\n%s", err, jout)
			}
		})
	}
}

func TestLifecycleVerbsDefaultToTheCurrentSandbox(t *testing.T) {
	f := boxRoutes(t, map[string]any{"POST " + boxPath + "/pause": `{"id":"` + boxID + `","state":"paused"}`})
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \"my-box\"\n")
	if out, err := run(t, "pause"); err != nil || !strings.Contains(out, "paused") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.find("POST", boxPath+"/pause")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	// With nothing set, the alias explains itself.
	writeConfigFile(t, "api_key = \"msk_u_test\"\n")
	_, err := run(t, "pause")
	if err == nil || !strings.Contains(err.Error(), "miosa use") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtendValidatesTheDuration(t *testing.T) {
	boxRoutes(t, nil)
	for _, bad := range []string{"soon", "-5", "0", "1x"} {
		_, err := run(t, "extend", "my-box", "--for", bad)
		if err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("--for %q: err=%v exit=%d", bad, err, commands.ExitCode(err))
		}
	}
	if _, err := run(t, "extend", "my-box"); err == nil {
		t.Error("--for is required")
	}
}

func TestTagMergesAndRemoves(t *testing.T) {
	f := boxRoutes(t, map[string]any{
		"GET " + boxPath:             `{"id":"` + boxID + `","tags":{"keep":"1","drop":"2"}}`,
		"PATCH " + boxPath + "/tags": `{}`,
	})
	out, err := run(t, "tag", "my-box", "--set", "env=staging", "--remove", "drop")
	if err != nil {
		t.Fatal(err)
	}
	p := f.find("PATCH", boxPath+"/tags")
	if len(p) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	tags := p[0].Body["tags"].(map[string]any)
	if tags["keep"] != "1" || tags["env"] != "staging" || tags["drop"] != nil || len(tags) != 2 {
		t.Fatalf("tags sent = %v (existing tags must be merged, not replaced)", tags)
	}
	if !strings.Contains(out, "staging") {
		t.Fatalf("out = %s", out)
	}
	// Reading changes nothing.
	f2 := boxRoutes(t, map[string]any{"GET " + boxPath: `{"tags":{"a":"b"}}`})
	if _, err := run(t, "tag", "my-box"); err != nil || len(f2.find("PATCH", boxPath+"/tags")) != 0 {
		t.Fatalf("read-only tag must not PATCH: %v", err)
	}
	if _, err := run(t, "tag", "my-box", "--set", "novalue"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad --set: %v", err)
	}
}

func TestWaitPollsUntilReady(t *testing.T) {
	n := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/sandboxes/by-name/my-box"):
			return 200, `{"id":"` + boxID + `"}`
		case c.is("GET", boxPath):
			n++
			if n < 3 {
				return 200, `{"state":"provisioning","ready":false}`
			}
			return 200, `{"state":"running","ready":true}`
		}
		return 404, `{}`
	})
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	out, err := run(t, "wait", "my-box")
	if err != nil || !strings.Contains(out, "ready") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.find("GET", boxPath)) != 3 {
		t.Fatalf("polls = %d", len(f.find("GET", boxPath)))
	}
}

func TestWaitFailsOnErrorStateAndTimeout(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", boxPath) {
			return 200, `{"state":"error"}`
		}
		return 404, `{}`
	})
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	_, err := run(t, "wait", boxID)
	if err == nil || !strings.Contains(err.Error(), "error state") {
		t.Fatalf("err = %v", err)
	}
	newFakeAPI(t, func(c call) (int, string) { return 200, `{"state":"provisioning"}` })
	_, err = run(t, "wait", boxID, "--timeout", "20ms")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timeout err = %v", err)
	}
	if _, err := run(t, "wait", boxID, "--for", "bogus"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad --for: %v", err)
	}
}

func TestWaitForDestroyedAcceptsNotFound(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) { return 404, `{"error":{"code":"NOT_FOUND"}}` })
	if out, err := run(t, "wait", boxID, "--for", "destroyed"); err != nil || !strings.Contains(out, "destroyed") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestLogsPrintsLines(t *testing.T) {
	f := boxRoutes(t, map[string]any{"GET " + boxPath + "/logs": `{"count":2,"logs":[{"line":"booted","stream":"stdout","t":"2026-10-09T00:00:00Z"},{"line":"ready","stream":"stdout","t":"2026-10-09T00:00:01Z"}]}`})
	out, err := run(t, "logs", "my-box", "-n", "2")
	if err != nil || out != "booted\nready\n" {
		t.Fatalf("%v %q", err, out)
	}
	if q := f.find("GET", boxPath+"/logs")[0].Query; q != "lines=2" {
		t.Fatalf("query = %q", q)
	}
	jout, _ := run(t, "logs", "my-box", "--json")
	if !strings.Contains(jout, `{"line":"booted"`) {
		t.Fatalf("json lines = %q", jout)
	}
}

func TestLogsFollowStreamsWithATicket(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/sandboxes/by-name/my-box"):
			return 200, `{"id":"` + boxID + `"}`
		case c.is("GET", boxPath+"/logs"):
			return 200, `{"logs":[{"line":"old"}]}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"tkt-1","expires_in":3600}`
		case c.is("GET", boxPath+"/logs/stream"):
			return 200, "data: {\"line\":\"live one\"}\n\ndata: plain text\n\n"
		}
		return 404, `{}`
	})
	out, err := run(t, "logs", "my-box", "-f")
	if err != nil {
		t.Fatal(err)
	}
	if out != "old\nlive one\nplain text\n" {
		t.Fatalf("out = %q", out)
	}
	if q := f.find("GET", boxPath+"/logs/stream")[0].Query; q != "ticket=tkt-1" {
		t.Fatalf("stream query = %q", q)
	}
}

func TestEventsStreamsJSONLines(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/sandboxes/by-name/my-box"):
			return 200, `{"id":"` + boxID + `"}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"t"}`
		case c.is("GET", boxPath+"/events"):
			return 200, "event: state\nid: 1\ndata: {\"state\":\"running\"}\n\n"
		}
		return 404, `{}`
	})
	out, err := run(t, "events", "my-box", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var ev map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &ev) != nil || ev["event"] != "state" || ev["data"].(map[string]any)["state"] != "running" {
		t.Fatalf("out = %q", out)
	}
	text, _ := run(t, "events", "my-box")
	if !strings.HasPrefix(text, "state  ") {
		t.Fatalf("text = %q", text)
	}
}

func TestListFiltersAndPagination(t *testing.T) {
	page := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		if !c.is("GET", "/sandboxes") {
			return 404, `{}`
		}
		q, _ := url.ParseQuery(c.Query)
		page++
		switch q.Get("page") {
		case "", "1":
			return 200, `{"data":[{"id":"` + boxID + `","name":"one","state":"running","size":"small","template_id":"t","created_at":"2026-10-09T00:00:00Z"}],"meta":{"total":2,"page":1,"limit":1,"total_pages":2,"has_next_page":true}}`
		default:
			return 200, `{"data":[{"id":"22222222-2222-2222-2222-222222222222","name":"two","state":"paused","size":"small","template_id":"t","created_at":"2026-10-09T00:00:00Z"}],"meta":{"total":2,"page":2,"limit":1,"total_pages":2,"has_next_page":false}}`
		}
	})
	out, err := run(t, "list", "--state", "running", "--template", "t", "--search", "on", "--tag", "env=prod", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(f.calls[0].Query)
	if q.Get("state") != "running" || q.Get("template_id") != "t" || q.Get("search") != "on" || q.Get("tags[env]") != "prod" || q.Get("limit") != "1" {
		t.Fatalf("query = %v", q)
	}
	if !strings.Contains(out, "Showing 1 of 2") || !strings.Contains(out, "--page 2") {
		t.Fatalf("pagination hint missing:\n%s", out)
	}

	page = 0
	all, err := run(t, "list", "--all", "--ids")
	if err != nil || strings.TrimSpace(all) != boxID+"\n22222222-2222-2222-2222-222222222222" {
		t.Fatalf("%v %q", err, all)
	}
	if _, err := run(t, "list", "--limit", "500"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("limit: %v", err)
	}
	if _, err := run(t, "list", "--tag", "bad"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("tag: %v", err)
	}
}

func TestListMarksTheCurrentSandboxAndHandlesUnnamedOnes(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /sandboxes": `{"data":[
	 {"id":"` + boxID + `","name":"","state":"running","size":"small","template_id":"t","created_at":"2026-10-09T00:00:00Z"},
	 {"id":"22222222-2222-2222-2222-222222222222","name":"named","state":"paused","size":"small","template_id":"t","created_at":"2026-10-09T00:00:00Z"}],"meta":{"total":2}}`})
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \"named\"\n")
	out, err := run(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, " *") != 1 || !strings.Contains(out, "named *") {
		t.Fatalf("only the current sandbox gets the marker (unnamed rows must not match an empty current):\n%s", out)
	}
}

func TestCreateWaitsAndSendsOptions(t *testing.T) {
	polls := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/sandboxes"):
			return 201, `{"id":"` + boxID + `","name":"web","state":"provisioning","size":"quad","template_id":"miosa-sandbox"}`
		case c.is("GET", boxPath):
			polls++
			if polls < 2 {
				return 200, `{"state":"provisioning","ready":false}`
			}
			return 200, `{"state":"running","ready":true}`
		}
		return 404, `{}`
	})
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	out, err := run(t, "create", "web", "--size", "quad", "--wait", "--ttl", "2h", "--idle-timeout", "15m", "--meta", "team=infra")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	body := f.find("POST", "/sandboxes")[0].Body
	if body["size"] != "quad" || body["timeout_sec"] != float64(7200) || body["idle_timeout_sec"] != float64(900) || body["metadata"].(map[string]any)["team"] != "infra" {
		t.Fatalf("body = %v", body)
	}
	if polls != 2 || !strings.Contains(out, "running") {
		t.Fatalf("polls=%d out=%s", polls, out)
	}
	if body["wait"] != float64(120) {
		t.Fatalf("--wait must ask the server to wait (capped at 120 s): %v", body)
	}
	for _, size := range []string{"micro", "tiny", "xs", "small", "quad", "medium", "large", "xl"} {
		if _, err := run(t, "create", "x", "--size", size); err != nil {
			t.Errorf("size %s: %v", size, err)
		}
	}
	if _, err := run(t, "create", "x", "--size", "huge"); err == nil || commands.ExitCode(err) != commands.ExitUsage || !strings.Contains(err.Error(), "micro") {
		t.Fatalf("bad size: %v", err)
	}
	if _, err := run(t, "create", "x", "--ttl", "never"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad ttl: %v", err)
	}
}

func TestDestroyNeedsConfirmationOffATerminal(t *testing.T) {
	f := boxRoutes(t, map[string]any{"DELETE " + boxPath: `{"id":"` + boxID + `","state":"destroying"}`})
	_, err := run(t, "destroy", "my-box")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("DELETE", boxPath)) != 0 {
		t.Fatal("must not delete without confirmation")
	}
	for _, flag := range []string{"--yes", "-y", "--force"} {
		if out, err := run(t, "destroy", "my-box", flag); err != nil || !strings.Contains(out, "Destroyed") {
			t.Fatalf("%s: %v %s", flag, err, out)
		}
	}
	// JSON never prompts (scripts were already relying on that).
	if _, err := run(t, "destroy", "my-box", "--json"); err != nil {
		t.Fatalf("json: %v", err)
	}
}

func TestDestroyManyReportsEachAndExitsNonZeroOnFailure(t *testing.T) {
	other := "22222222-2222-2222-2222-222222222222"
	f := fakeRoutes(t, map[string]any{
		"DELETE " + boxPath:           `{"state":"destroying"}`,
		"DELETE /sandboxes/" + other:  resp{404, `{"error":{"code":"NOT_FOUND","message":"sandbox not found"}}`},
		"GET /sandboxes/by-name/gone": resp{404, `{}`},
	})
	out, err := run(t, "destroy", boxID, other, "--yes", "--json")
	if err == nil || commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("err = %v", err)
	}
	var res struct {
		Data []struct{ Sandbox, Status string }
	}
	if json.Unmarshal([]byte(out), &res) != nil || len(res.Data) != 2 || res.Data[0].Status != "destroyed" || res.Data[1].Status != "not_found" {
		t.Fatalf("out = %s", out)
	}
	if len(f.find("DELETE", boxPath)) != 1 {
		t.Fatal("the good one must still be destroyed")
	}
}

func TestDestroyClearsCurrentSandbox(t *testing.T) {
	boxRoutes(t, map[string]any{"DELETE " + boxPath: `{}`})
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \"my-box\"\n")
	if _, err := run(t, "destroy", "my-box", "-y"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readConfig(t), `current_sandbox = ""`) {
		t.Fatalf("config = %s", readConfig(t))
	}
}

func TestPreviewCommands(t *testing.T) {
	prev := `{"id":"b3bf411a-0f7b-4422-bf23-6a34ab00ea52","name":"web","port":8080,"state":"active","url":"https://x.example","visibility":"private"}`
	f := boxRoutes(t, map[string]any{
		"GET " + boxPath + "/previews":             `{"data":[` + prev + `]}`,
		"POST " + boxPath + "/previews":            resp{201, `{"data":` + prev + `}`},
		"GET " + boxPath + "/previews/p1":          `{"data":` + prev + `}`,
		"GET " + boxPath + "/previews/p1/status":   `{"process":{"status":"ok","http_status":200,"latency_ms":18},"tls":{"status":"ok"},"sandbox":{"state":"running"}}`,
		"DELETE " + boxPath + "/previews/p1":       `{"data":{}}`,
		"POST " + boxPath + "/previews/p1/share":   resp{201, `{"data":` + prev + `}`},
		"DELETE " + boxPath + "/previews/p1/share": `{"data":{}}`,
	})
	if out, err := run(t, "preview", "list", "my-box"); err != nil || !strings.Contains(out, "https://x.example") || !strings.Contains(out, "b3bf411a") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	// Both argument orders: with and without the sandbox.
	if out, err := run(t, "preview", "create", "my-box", "8080", "--name", "web", "--visibility", "private", "--ttl", "1h"); err != nil || !strings.Contains(out, "https://x.example") {
		t.Fatalf("create: %v\n%s", err, out)
	}
	c := f.find("POST", boxPath+"/previews")[0].Body
	if c["port"] != float64(8080) || c["name"] != "web" || c["visibility"] != "private" || c["ttl_seconds"] != float64(3600) {
		t.Fatalf("create body = %v", c)
	}
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \"my-box\"\n")
	if _, err := run(t, "preview", "create", "3000"); err != nil {
		t.Fatalf("create with current sandbox: %v", err)
	}
	if got := f.find("POST", boxPath+"/previews"); got[1].Body["port"] != float64(3000) {
		t.Fatalf("calls = %+v", got)
	}
	for _, args := range [][]string{{"preview", "get", "my-box", "p1"}, {"preview", "status", "my-box", "p1"}, {"preview", "rm", "my-box", "p1"},
		{"preview", "share", "my-box", "p1", "--ttl", "30m"}, {"preview", "unshare", "my-box", "p1"}} {
		if out, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if s := f.find("POST", boxPath+"/previews/p1/share"); len(s) != 1 || s[0].Body["ttl_seconds"] != float64(1800) {
		t.Fatalf("share = %+v", s)
	}
	for _, bad := range []string{"0", "70000", "web"} {
		if _, err := run(t, "preview", "create", "my-box", bad); err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("port %q: %v", bad, err)
		}
	}
	if _, err := run(t, "preview", "create", "my-box", "80", "--visibility", "secret"); err == nil {
		t.Error("bad visibility must fail")
	}
}

func TestPreviewPortNotListeningShowsTheAPIReason(t *testing.T) {
	boxRoutes(t, map[string]any{"POST " + boxPath + "/previews": resp{409, `{"error":{"code":"SANDBOX_PORT_NOT_LISTENING","message":"no service is listening on the requested internal port"}}`}})
	_, err := run(t, "preview", "create", "my-box", "9999")
	if err == nil || !strings.Contains(err.Error(), "SANDBOX_PORT_NOT_LISTENING") || commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("err = %v", err)
	}
}

func TestProcessCommands(t *testing.T) {
	proc := `{"id":"40e8f269-4b05-4cee-b02a-3730ce06d296","pid":4977,"status":"running","command":"python3 -m http.server 8080","started_at":"2026-10-09T18:50:37Z","cwd":"/home/user"}`
	f := boxRoutes(t, map[string]any{
		"GET " + boxPath + "/processes":         `{"data":[` + proc + `]}`,
		"POST " + boxPath + "/processes":        resp{201, `{"data":` + proc + `}`},
		"GET " + boxPath + "/processes/p1":      `{"data":` + proc + `}`,
		"GET " + boxPath + "/processes/p1/logs": `{"data":{"count":2,"lines":["one","two"]}}`,
		"DELETE " + boxPath + "/processes/p1":   `{"data":{"status":"stopped"}}`,
	})
	if out, err := run(t, "process", "list", "my-box"); err != nil || !strings.Contains(out, "http.server") || !strings.Contains(out, "4977") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if out, err := run(t, "ps", "my-box"); err != nil || !strings.Contains(out, "4977") {
		t.Fatalf("ps: %v\n%s", err, out)
	}
	if out, err := run(t, "process", "logs", "my-box", "p1", "-n", "5"); err != nil || out != "one\ntwo\n" {
		t.Fatalf("logs: %v %q", err, out)
	}
	if q := f.find("GET", boxPath+"/processes/p1/logs")[0].Query; q != "tail=5" {
		t.Fatalf("logs query = %q", q)
	}
	if out, err := run(t, "process", "kill", "my-box", "p1"); err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("kill: %v %s", err, out)
	}
	out, err := run(t, "process", "start", "my-box", "--name", "api", "--cwd", "/workspace", "--env", "A=b", "--sudo", "--", "python3", "-m", "http.server", "8080")
	if err != nil || !strings.Contains(out, "Started process") {
		t.Fatalf("start: %v\n%s", err, out)
	}
	b := f.find("POST", boxPath+"/processes")[0].Body
	if b["command"] != "python3 -m http.server 8080" || b["name"] != "api" || b["cwd"] != "/workspace" || b["sudo"] != true || b["env"].(map[string]any)["A"] != "b" {
		t.Fatalf("start body = %v", b)
	}
	if _, err := run(t, "process", "start", "my-box"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("start without command: %v", err)
	}
}

func TestServicesCommands(t *testing.T) {
	svc := `{"id":"s-id","name":"web","status":"running","pid":9,"command":"npm start","cwd":"/workspace"}`
	f := boxRoutes(t, map[string]any{
		"GET " + boxPath + "/services":              `{"data":[` + svc + `]}`,
		"POST " + boxPath + "/services":             resp{201, `{"data":` + svc + `}`},
		"GET " + boxPath + "/services/web":          `{"data":` + svc + `}`,
		"POST " + boxPath + "/services/web/restart": `{"data":` + svc + `}`,
		"GET " + boxPath + "/services/web/logs":     `{"data":{"service":"web","lines":["ready"],"count":1}}`,
		"DELETE " + boxPath + "/processes/s-id":     `{}`,
	})
	if out, err := run(t, "services", "list", "my-box"); err != nil || !strings.Contains(out, "npm start") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	if _, err := run(t, "services", "create", "my-box", "--name", "web", "--command", "npm start", "--cwd", "/workspace", "--env", "PORT=3000", "--sudo"); err != nil {
		t.Fatal(err)
	}
	b := f.find("POST", boxPath+"/services")[0].Body
	if b["name"] != "web" || b["command"] != "npm start" || b["cwd"] != "/workspace" || b["sudo"] != true || b["env"].(map[string]any)["PORT"] != "3000" {
		t.Fatalf("create body = %v", b)
	}
	for _, args := range [][]string{{"services", "get", "my-box", "web"}, {"services", "restart", "my-box", "web"}, {"services", "start", "my-box", "web"}} {
		if _, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if out, err := run(t, "services", "logs", "my-box", "web", "--tail", "20"); err != nil || out != "ready\n" {
		t.Fatalf("logs: %v %q", err, out)
	}
	if out, err := run(t, "services", "stop", "my-box", "web"); err != nil || !strings.Contains(out, "Stopped service") {
		t.Fatalf("stop: %v %s", err, out)
	}
	if len(f.find("DELETE", boxPath+"/processes/s-id")) != 1 {
		t.Fatalf("stop must kill the service process: %+v", f.calls)
	}
	if _, err := run(t, "services", "create", "my-box", "--name", "x"); err == nil {
		t.Fatal("--command is required")
	}
}

func TestDesktopCommands(t *testing.T) {
	f := boxRoutes(t, map[string]any{
		"POST " + boxPath + "/desktop": resp{201, `{"url":"https://desktop.example/?t=1"}`},
		"GET " + boxPath + "/desktop":  `{"url":"https://desktop.example/?t=1","state":"running"}`,
	})
	if out, err := run(t, "desktop", "start", "my-box"); err != nil || !strings.Contains(out, "desktop.example") {
		t.Fatalf("start: %v\n%s", err, out)
	}
	if out, err := run(t, "desktop", "url", "my-box"); err != nil || !strings.Contains(out, "desktop.example") {
		t.Fatalf("url: %v\n%s", err, out)
	}
	if len(f.find("POST", boxPath+"/desktop")) != 1 || len(f.find("GET", boxPath+"/desktop")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestPolicyIsAComputerFeature(t *testing.T) {
	cid := "33333333-3333-3333-3333-333333333333"
	f := fakeRoutes(t, map[string]any{
		"GET /computers/" + cid + "/network-policy":    `{"mode":"allowlist","allow":["github.com"]}`,
		"PUT /computers/" + cid + "/network-policy":    `{}`,
		"DELETE /computers/" + cid + "/network-policy": `{}`,
		"GET /computers/ghost/network-policy":          resp{400, `{"error":{"code":"INVALID_ID"}}`},
	})
	if out, err := run(t, "policy", "show", cid); err != nil || !strings.Contains(out, "github.com") {
		t.Fatalf("show: %v\n%s", err, out)
	}
	file := filepath.Join(t.TempDir(), "p.yaml")
	os.WriteFile(file, []byte("mode: allowlist\nallow:\n  - example.com\n"), 0o600)
	if _, err := run(t, "policy", "set", cid, "-f", file); err != nil {
		t.Fatal(err)
	}
	if put := f.find("PUT", "/computers/"+cid+"/network-policy"); len(put) != 1 || put[0].Body["mode"] != "allowlist" {
		t.Fatalf("put = %+v", put)
	}
	if _, err := run(t, "policy", "reset", cid); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "policy", "show", "ghost")
	if err == nil || !strings.Contains(err.Error(), "sandboxes use 'miosa egress'") {
		t.Fatalf("not-a-computer error should explain: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	os.WriteFile(bad, []byte(": : :"), 0o600)
	if _, err := run(t, "policy", "set", cid, "-f", bad); err == nil {
		t.Fatal("unparseable policy must fail")
	}
}

func TestConsoleRequiresATerminalAndRejectsStrayArgs(t *testing.T) {
	f := boxRoutes(t, nil)
	_, err := run(t, "console", "my-box")
	if err == nil || commands.ExitCode(err) != commands.ExitUsage || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("err = %v", err)
	}
	if _, err := run(t, "console", "my-box", "htop"); err == nil || !strings.Contains(err.Error(), "after --") {
		t.Fatalf("stray arg: %v", err)
	}
	if _, err := run(t, "console", "my-box", "--shell", "zsh", "--", "htop"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("--shell with command: %v", err)
	}
	if len(f.find("POST", boxPath+"/terminal")) != 0 {
		t.Fatal("no terminal session may be opened without a TTY")
	}
}

func TestTerminalRequestBody(t *testing.T) {
	if got := commands.TerminalRequestForTest(100, 30, "", "/workspace", []string{"htop", "-d", "5"}); got["command"] != "htop" || got["cwd"] != "/workspace" || got["shell"] != nil {
		t.Fatalf("command form = %v", got)
	}
	args, _ := commands.TerminalRequestForTest(100, 30, "", "", []string{"htop", "-d", "5"})["args"].([]string)
	if len(args) != 2 || args[0] != "-d" {
		t.Fatalf("args = %v", args)
	}
	if got := commands.TerminalRequestForTest(80, 24, "zsh", "", nil); got["shell"] != "zsh" || got["cols"] != 80 || got["rows"] != 24 || got["command"] != nil {
		t.Fatalf("shell form = %v", got)
	}
}

func TestCommandsRegisterHelpGroups(t *testing.T) {
	out, err := run(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Account and setup:", "Sandboxes and computers:", "pause", "preview", "process", "whoami"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
}

func TestComputerCommands(t *testing.T) {
	cid := "33333333-3333-3333-3333-333333333333"
	comp := `{"id":"` + cid + `","name":"desk","status":"running","size":"medium","template_type":"desktop","created_at":"2026-10-09T00:00:00Z"}`
	f := fakeRoutes(t, map[string]any{
		"GET /computers":                      `{"total":1,"computers":[` + comp + `]}`,
		"GET /computers/" + cid:               comp,
		"POST /computers/" + cid + "/start":   `{}`,
		"POST /computers/" + cid + "/stop":    `{}`,
		"POST /computers/" + cid + "/restart": `{}`,
		"DELETE /computers/" + cid:            `{}`,
		"GET /computers/" + cid + "/urls":     `{"desktop":"https://desk.example/?t=1"}`,
		"GET /computers/" + cid + "/ports":    `{"data":[{"port":3000,"protocol":"tcp","visibility":"public"}]}`,
	})
	if out, err := run(t, "computer", "list"); err != nil || !strings.Contains(out, "desk") || !strings.Contains(out, "medium") {
		t.Fatalf("list: %v\n%s", err, out)
	}
	// A computer may be given by name; the CLI looks the id up.
	if out, err := run(t, "computer", "get", "desk"); err != nil || !strings.Contains(out, cid) {
		t.Fatalf("get by name: %v\n%s", err, out)
	}
	for _, verb := range []string{"start", "stop", "restart"} {
		if _, err := run(t, "computer", verb, "desk"); err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		if len(f.find("POST", "/computers/"+cid+"/"+verb)) != 1 {
			t.Fatalf("%s did not reach the API: %+v", verb, f.calls)
		}
	}
	if out, err := run(t, "computer", "urls", "desk"); err != nil || !strings.Contains(out, "desk.example") {
		t.Fatalf("urls: %v\n%s", err, out)
	}
	if out, err := run(t, "computer", "ports", cid); err != nil || !strings.Contains(out, "3000") {
		t.Fatalf("ports: %v\n%s", err, out)
	}
	if _, err := run(t, "computer", "rm", "desk"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("rm without --yes: %v", err)
	}
	if _, err := run(t, "computer", "rm", "desk", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("DELETE", "/computers/"+cid)) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestCreateWaitReturnsWithoutPollingWhenTheServerWaited(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/sandboxes"):
			return 201, `{"id":"` + boxID + `","name":"web","state":"running","ready":true}`
		}
		return 404, `{}`
	})
	if out, err := run(t, "create", "web", "--wait", "--wait-timeout", "45s"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.find("GET", "/sandboxes/"+boxID)) != 0 {
		t.Fatalf("a sandbox the server already waited for must not be polled: %+v", f.calls)
	}
	if body := f.find("POST", "/sandboxes")[0].Body; body["wait"] != float64(45) {
		t.Fatalf("body = %v", body)
	}
	run(t, "create", "x")
	if body := f.find("POST", "/sandboxes")[1].Body; body["wait"] != nil {
		t.Fatalf("no --wait, no wait field: %v", body)
	}
}
