package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestRunSteerQueuesAPromptOnTheSandbox(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/agent/prompts": resp{202, `{"data":{"queued":true,"after_run_id":"11111111-2222-3333-4444-555555555555","runner":"claude-code"}}`},
	})
	out, err := run(t, "run", "steer", "--sandbox", boxID, "also", "add", "a", "changelog")
	if err != nil || !strings.Contains(out, "Queued") || !strings.Contains(out, "claude-code") {
		t.Fatalf("%v\n%s", err, out)
	}
	if b := f.find("POST", "/sandboxes/"+boxID+"/agent/prompts")[0].Body; b["prompt"] != "also add a changelog" {
		t.Fatalf("body = %v", b)
	}
	if _, err := runIn(t, "from stdin\n", "run", "steer", "-s", boxID, "-"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/sandboxes/"+boxID+"/agent/prompts")[1].Body; !strings.HasPrefix(b["prompt"].(string), "from stdin") {
		t.Fatalf("stdin body = %v", b)
	}
	if _, err := runIn(t, "  \n", "run", "steer", "-s", boxID, "-"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("an empty prompt is a usage error: %v", err)
	}
}

func TestRunInterruptStopsTheAttachedRun(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/agent/stop": `{"data":{"id":"11111111-2222-3333-4444-555555555555","status":"canceled"}}`,
	})
	out, err := run(t, "run", "interrupt", "-s", boxID)
	if err != nil || !strings.Contains(out, "Stopped run") || !strings.Contains(out, "canceled") {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(f.find("POST", "/sandboxes/"+boxID+"/agent/stop")) != 1 {
		t.Fatalf("calls: %+v", f.calls)
	}
}

func TestRunSteerExplainsNoAgentRun(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"POST /sandboxes/" + boxID + "/agent/prompts": resp{409, `{"error":{"code":"NO_AGENT_RUN","message":"no agent run is attached to this machine"}}`},
	})
	_, err := run(t, "run", "steer", "-s", boxID, "hi")
	if err == nil || commands.ExitCode(err) != commands.ExitConflict || !strings.Contains(err.Error(), "NO_AGENT_RUN") {
		t.Fatalf("err = %v exit=%d", err, commands.ExitCode(err))
	}
}

func TestRunSteerOnAComputerNeedsAName(t *testing.T) {
	fakeRoutes(t, nil)
	if _, err := run(t, "run", "steer", "--computer", "", "-s", "", "hi"); err == nil {
		// no computer and no current sandbox: a usage error either way
		t.Fatal("expected an error")
	}
}
