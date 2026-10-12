package commands_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const catalogAll = `{"data":[{"value":"claude-code"},{"value":"codex"},{"value":"pi"},{"value":"opencode"},{"value":"prime"},{"value":"kimi"},{"value":"mistral"},{"value":"hermes"},{"value":"osa"},{"value":"custom"}]}`

func TestPromptAcceptsEveryHarnessThePlatformListsAndPassesTheModel(t *testing.T) {
	for _, harness := range []string{"claude-code", "codex", "osa", "opencode", "pi", "prime", "kimi", "mistral", "hermes"} {
		f := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
			if c.is("GET", "/agents/harnesses") {
				return 200, catalogAll, true
			}
			return 0, "", false
		})
		restore := commands.SetRunPollForTest(time.Millisecond)
		_, err := run(t, "prompt", "-s", "my-box", "--harness", harness, "--model", "some-model", "do it")
		restore()
		if err != nil {
			t.Fatalf("%s: %v", harness, err)
		}
		b := f.find("POST", "/runs")[0].Body
		if b["runner"] != harness || b["model"] != "some-model" {
			t.Fatalf("%s: body = %v", harness, b)
		}
	}
}

func TestPromptReadsTheHarnessesFromThePlatformSoANewOneNeedsNoRelease(t *testing.T) {
	f := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
		if c.is("GET", "/agents/harnesses") {
			return 200, `{"data":[{"value":"claude-code"},{"value":"zeta-agent"},{"value":"custom"}]}`, true
		}
		return 0, "", false
	})
	defer commands.SetRunPollForTest(time.Millisecond)()
	if _, err := run(t, "prompt", "-s", "my-box", "--harness", "zeta-agent", "do it"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/runs")[0].Body; b["runner"] != "zeta-agent" {
		t.Fatalf("body = %v", b)
	}
	// What the platform does not list is refused, naming what it does.
	_, err := run(t, "prompt", "-s", "my-box", "--harness", "opencode", "do it")
	if err == nil || commands.ExitCode(err) != commands.ExitUsage || !strings.Contains(err.Error(), "claude-code, zeta-agent") {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptFallsBackToTheBuiltInHarnessesWhenTheCatalogIsUnavailable(t *testing.T) {
	f := runAPI(t, "succeeded", 0, nil) // /agents/harnesses answers 404
	defer commands.SetRunPollForTest(time.Millisecond)()
	if _, err := run(t, "prompt", "-s", "my-box", "--harness", "opencode", "do it"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/runs")[0].Body; b["runner"] != "opencode" {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "prompt", "-s", "my-box", "--harness", "gemini", "do it"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("an unknown harness is a usage error: %v", err)
	}
}

func TestPromptHarnessAliasesAndTheCustomGateway(t *testing.T) {
	f := runAPI(t, "succeeded", 0, func(c call) (int, string, bool) {
		if c.is("GET", "/agents/harnesses") {
			return 200, catalogAll, true
		}
		return 0, "", false
	})
	defer commands.SetRunPollForTest(time.Millisecond)()
	if _, err := run(t, "prompt", "-s", "my-box", "--harness", "Claude", "do it"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", "/runs")[0].Body; b["runner"] != "claude-code" {
		t.Fatalf("body = %v", b)
	}
	_, err := run(t, "prompt", "-s", "my-box", "--harness", "custom", "do it")
	if err == nil || commands.ExitCode(err) != commands.ExitUsage || !strings.Contains(err.Error(), "--agent") {
		t.Fatalf("custom is not a prompt harness: %v", err)
	}
}

func TestConnectionsUseChoosesTheConnectionsAHarnessRunsOn(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings":                         `{"data":{"harnesses":[{"id":"claude-code","mode":"single"},{"id":"opencode","mode":"multi"},{"id":"custom","mode":"gateway"}]}}`,
		"PATCH /agents/settings/harnesses/claude-code": `{"data":{}}`,
		"PATCH /agents/settings/harnesses/opencode":    `{"data":{}}`,
	})
	if _, err := run(t, "connections", "use", "claude-code", "claude", "--model", "claude-sonnet-5-5", "--effort", "high"); err != nil {
		t.Fatal(err)
	}
	b := f.find("PATCH", "/agents/settings/harnesses/claude-code")[0].Body
	if b["auth_method"] != "claude_subscription" || b["default_model"] != "claude-sonnet-5-5" || b["default_effort"] != "high" {
		t.Fatalf("single harness body = %v", b)
	}

	if _, err := run(t, "connections", "use", "opencode", "chatgpt", "openrouter", "--workspace-id", "w1"); err != nil {
		t.Fatal(err)
	}
	b = f.find("PATCH", "/agents/settings/harnesses/opencode")[0].Body
	got, _ := b["enabled_providers"].([]any)
	if len(got) != 2 || got[0] != "chatgpt_subscription" || got[1] != "openrouter" || b["workspace_id"] != "w1" {
		t.Fatalf("multi harness body = %v", b)
	}

	if _, err := run(t, "connections", "use", "opencode", "--all"); err != nil {
		t.Fatal(err)
	}
	b = f.find("PATCH", "/agents/settings/harnesses/opencode")[1].Body
	if v, ok := b["enabled_providers"]; !ok || v != nil {
		t.Fatalf("--all clears the list (every connected provider): %v", b)
	}
}

func TestConnectionsUseRefusesWhatTheHarnessCannotTake(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/settings": `{"data":{"harnesses":[{"id":"claude-code","mode":"single"},{"id":"custom","mode":"gateway"}]}}`,
	})
	for _, args := range [][]string{
		{"connections", "use", "claude-code"},                        // nothing to set
		{"connections", "use", "claude-code", "claude", "anthropic"}, // one at a time
		{"connections", "use", "claude-code", "--all"},               // one at a time
		{"connections", "use", "claude-code", "claude", "--all"},     // both
		{"connections", "use", "custom", "anthropic"},                // no connections to choose
		{"connections", "use", "nope", "anthropic"},                  // unknown
	} {
		if _, err := run(t, args...); err == nil || commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	for _, c := range f.calls {
		if c.Method != "GET" {
			t.Fatalf("a refused change left the client: %+v", c)
		}
	}
}

func TestConnectionsHarnessesShowsTheModelsOfOne(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /agents/models": `{"data":{"harness":"opencode","default_model":"gpt-6.1-sol","usable_credentials":["chatgpt_subscription"],"models":[
			{"id":"gpt-6.1-sol","name":"GPT-6.1 Sol","available":true},
			{"id":"claude-sonnet-5-5","name":"Claude Sonnet 5.5","available":false,"unavailable_reason":"connect an Anthropic key"},
			{"id":"openrouter:x/y","name":"x/y","available":true,"via":"openrouter"}]}}`,
	})
	out, err := run(t, "connections", "harnesses", "opencode", "--external-user", "cus_123")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gpt-6.1-sol", "default", "no: connect an Anthropic key", "yes, via openrouter"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	q := qv(f.find("GET", "/agents/models")[0])
	if q.Get("harness") != "opencode" || q.Get("external_user_id") != "cus_123" {
		t.Fatalf("query = %v", q)
	}
}
