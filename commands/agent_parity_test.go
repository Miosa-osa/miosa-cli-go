package commands_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const agentRunsJSON = `{"data":[
{"id":"r3","chat_id":"c1","runner":"claude-code","model":"opus","status":"running","created_at":"2026-10-23T10:03:00Z","instruction":"and add tests","target_id":"` + boxID + `"},
{"id":"r2","chat_id":"c1","runner":"claude-code","model":"opus","status":"succeeded","created_at":"2026-10-23T10:02:00Z","instruction":"fix the bug","target_id":"` + boxID + `"},
{"id":"r1","chat_id":"c2","runner":"codex","status":"failed","created_at":"2026-10-23T09:00:00Z","instruction":"hello","target_id":"` + boxID + `"}]}`

func TestConversationsListsPromptsRunningAndLastPrompt(t *testing.T) {
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if c.is("GET", "/runs") {
			return 200, agentRunsJSON, true
		}
		return 0, "", false
	})
	out, err := run(t, "convos", "my-box")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[1], "c1") || !strings.Contains(lines[1], "2") || !strings.Contains(lines[1], "yes") ||
		!strings.Contains(lines[1], "claude-code/opus") || !strings.Contains(lines[1], "and add tests") {
		t.Fatalf("out =\n%s", out)
	}
	if q := f.find("GET", "/runs")[0].Query; !strings.Contains(q, "target_id="+boxID) {
		t.Fatalf("query = %q", q)
	}
	jout, _ := run(t, "conversations", "--json")
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal([]byte(jout), &env) != nil || len(env.Data) != 2 || env.Data[0]["prompt_count"] != float64(2) || env.Data[0]["running"] != true {
		t.Fatalf("json = %s", jout)
	}
}

func TestInterruptConvoCancelsItsRunningTurn(t *testing.T) {
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("GET", "/runs"):
			return 200, `{"data":[{"id":"r3","chat_id":"c1","status":"running"}]}`, true
		case c.is("POST", "/runs/r3/cancel"):
			return 200, `{"data":{"id":"r3","status":"canceled"}}`, true
		}
		return 0, "", false
	})
	if _, err := run(t, "interrupt", "--convo", "c1"); err != nil {
		t.Fatal(err)
	}
	if q := f.find("GET", "/runs")[0].Query; !strings.Contains(q, "chat_id=c1") || len(f.find("POST", "/runs/r3/cancel")) != 1 {
		t.Fatalf("calls: %+v", f.calls)
	}
}

func TestInterruptSandboxStopsItsAgent(t *testing.T) {
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if c.is("POST", boxPath+"/agent/stop") {
			return 200, `{"data":{"id":"r3","status":"canceled"}}`, true
		}
		return 0, "", false
	})
	if _, err := run(t, "interrupt", "my-box"); err != nil || len(f.find("POST", boxPath+"/agent/stop")) != 1 {
		t.Fatalf("%v %+v", err, f.calls)
	}
}

func TestSteerTakesTheSandboxFirstWhenItExists(t *testing.T) {
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if c.is("POST", boxPath+"/agent/prompts") {
			return 201, `{"data":{"after_run_id":"r3","runner":"claude-code"}}`, true
		}
		return 0, "", false
	})
	if _, err := run(t, "steer", "my-box", "also", "add", "a", "changelog"); err != nil {
		t.Fatal(err)
	}
	if got := f.find("POST", boxPath+"/agent/prompts")[0].Body["prompt"]; got != "also add a changelog" {
		t.Fatalf("prompt = %v", got)
	}
}

func TestEventsConvoPrintsTheAgentsEvents(t *testing.T) {
	sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("GET", "/runs"):
			return 200, `{"data":[{"id":"r2","chat_id":"c1","status":"succeeded"}]}`, true
		case c.is("GET", "/runs/r2/events"):
			return 200, `{"data":[{"id":"e1","type":"run.running","message":"Run running"},{"id":"e2","type":"tool","message":"edited main.go"}]}`, true
		}
		return 0, "", false
	})
	out, err := run(t, "events", "--convo", "c1")
	if err != nil || !strings.Contains(out, "tool: edited main.go") {
		t.Fatalf("%v %q", err, out)
	}
	if _, err := run(t, "interrupt", "--convo", "c1"); commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("a finished conversation has no turn to stop: %v", err)
	}
}

func TestPromptResumeAndNewAreAliases(t *testing.T) {
	cmd, _, err := commands.Root().Find([]string{"prompt"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"resume", "new", "chat", "new-chat"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("prompt has no --%s", name)
		}
	}
}

func TestPromptSendsTheReasoningEffort(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/runs") {
			return 201, `{"data":{"id":"r9","status":"queued"}}`
		}
		return 404, `{}`
	})
	_, _ = run(t, "prompt", "--sandbox", boxID, "--harness", "codex", "--reasoning-effort", "high", "--no-follow", "fix it")
	runs := f.find("POST", "/runs")
	if len(runs) != 1 || runs[0].Body["reasoning_effort"] != "high" {
		t.Fatalf("calls: %+v", f.calls)
	}
}
