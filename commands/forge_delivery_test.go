package commands_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// deliveryCall is one action the fake Delivery service received.
type deliveryCall struct {
	action string
	org    string
	body   map[string]any
}

// fakeDelivery answers every action with result and records what it was sent.
func fakeDelivery(t *testing.T, results map[string]any) (*[]deliveryCall, func()) {
	t.Helper()
	var calls []deliveryCall
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-Miosa-Org") != "" {
			t.Errorf("Delivery requests must not carry the platform org header")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/state" {
			json.NewEncoder(w).Encode(results["state"])
			return
		}
		action := strings.TrimPrefix(r.URL.Path, "/api/")
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, deliveryCall{action: action, org: r.URL.Query().Get("org"), body: body})
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": results[action]})
	}))
	t.Setenv("FORGE_LOCAL_URL", server.URL)
	t.Setenv("FORGE_ORG", "acme")
	t.Setenv("FORGE_ACTOR", "claude-code")
	t.Setenv("FORGE_RUNNER", "")
	return &calls, server.Close
}

func runDelivery(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	commands.ResetForTest()
	root := commands.Root()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestForgeDeliveryAddSendsFlagsInAnyOrderAndSignsWithTheActor(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"add": []any{map[string]any{"id": 81, "kind": "added", "summary": "Platform · P1"}}})
	defer stop()

	out, err := runDelivery(t, "", "forge", "add", "--owner", "platform", "Ship", "the", "CLI", "--priority", "P1", "--new")
	if err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if !strings.Contains(out, "WO-81 → Platform · P1") {
		t.Fatalf("add printed %q", out)
	}
	call := (*calls)[0]
	if call.action != "add" || call.org != "acme" {
		t.Fatalf("sent %s for org %q", call.action, call.org)
	}
	if call.body["text"] != "Ship the CLI" || call.body["owner"] != "platform" || call.body["priority"] != "P1" || call.body["isNew"] != true || call.body["by"] != "claude-code" {
		t.Fatalf("add payload %#v", call.body)
	}
	if _, sent := call.body["milestone"]; sent {
		t.Fatalf("an unset flag must be left out, not sent as null: %#v", call.body)
	}
}

func TestForgeDeliveryEditLeavesOutWhatIsNotChanged(t *testing.T) {
	calls, stop := fakeDelivery(t, nil)
	defer stop()

	out, err := runDelivery(t, "", "forge", "edit", "WO-12", "--title", "New title")
	if err != nil || strings.TrimSpace(out) != "✓" {
		t.Fatalf("edit: %v %q", err, out)
	}
	body := (*calls)[0].body
	if body["id"] != float64(12) || body["title"] != "New title" {
		t.Fatalf("edit payload %#v", body)
	}
	for _, field := range []string{"detail", "doneWhen", "milestone", "owner"} {
		if _, sent := body[field]; sent {
			t.Fatalf("edit sent %s it was not given: %#v", field, body)
		}
	}
}

func TestForgeDeliveryAskSendsEachOptionAndTheRecommendationFromOne(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"ask": map[string]any{"gated": false}})
	defer stop()

	_, err := runDelivery(t, "", "forge", "ask", "WO-7", "Ship", "it?", "--default", "yes", "--option", "Yes :: ship now", "--option", "No :: hold", "--recommend", "2")
	if err != nil {
		t.Fatalf("ask: %v", err)
	}
	body := (*calls)[0].body
	options, _ := body["options"].([]any)
	if body["question"] != "Ship it?" || body["fallback"] != "yes" || len(options) != 2 || body["recommended"] != float64(1) {
		t.Fatalf("ask payload %#v", body)
	}
	if first := options[0].(map[string]any); first["label"] != "Yes" || first["detail"] != "ship now" {
		t.Fatalf("first option %#v", first)
	}
}

func TestForgeDeliveryCheckRunRecordsTheResultAndExitsWithTheCommandsCode(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"check": map[string]any{"id": "run-1"}})
	defer stop()

	out, err := runDelivery(t, "", "forge", "check", "run", "WO-9", "--", "echo", "checking;", "exit", "4")
	var exit *commands.ExitError
	if !errors.As(err, &exit) || exit.Code != 4 || commands.ExitCode(err) != 4 {
		t.Fatalf("want exit 4, got %v", err)
	}
	if !strings.Contains(out, "checking") || !strings.Contains(out, "Check failed") {
		t.Fatalf("check output %q", out)
	}
	if len(*calls) != 2 || (*calls)[0].body["state"] != "running" {
		t.Fatalf("calls %#v", *calls)
	}
	final := (*calls)[1].body
	output, _ := final["output"].([]any)
	if final["state"] != "failed" || final["run"] != "run-1" || final["command"] != "echo checking; exit 4" || len(output) != 1 || output[0] != "checking" {
		t.Fatalf("recorded %#v", final)
	}
}

func TestForgeDeliveryCheckRunNeedsACommand(t *testing.T) {
	_, stop := fakeDelivery(t, nil)
	defer stop()

	if _, err := runDelivery(t, "", "forge", "check", "run", "WO-9"); err == nil || !strings.Contains(err.Error(), "after --") {
		t.Fatalf("want a usage error, got %v", err)
	}
}

func TestForgeDeliverySpecReadsStdinAndLiftsTheOrderFields(t *testing.T) {
	calls, stop := fakeDelivery(t, nil)
	defer stop()

	spec := `{"approach":"Do it","files":["a.go"],"owner":"backend","priority":"P1","doneWhen":"it works","blockedBy":[3]}`
	if _, err := runDelivery(t, spec, "forge", "spec", "WO-5", "--json", "-"); err != nil {
		t.Fatalf("spec: %v", err)
	}
	body := (*calls)[0].body
	inner, _ := body["spec"].(map[string]any)
	if body["owner"] != "backend" || body["doneWhen"] != "it works" || inner["approach"] != "Do it" {
		t.Fatalf("spec payload %#v", body)
	}
	if _, kept := inner["owner"]; kept {
		t.Fatalf("owner belongs to the Work Order, not the spec: %#v", inner)
	}
}

func TestForgeDeliveryHookReportsTheToolAndNeverFails(t *testing.T) {
	calls, stop := fakeDelivery(t, nil)
	defer stop()
	t.Setenv("FORGE_RUNNER", "backend-2")

	input := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"mix test"}}`
	if _, err := runDelivery(t, input, "forge", "hook"); err != nil {
		t.Fatalf("hook: %v", err)
	}
	body := (*calls)[0].body
	if (*calls)[0].action != "harness" || body["runner"] != "backend-2" || body["event"] != "self" || body["summary"] != "mix test" {
		t.Fatalf("hook sent %s %#v", (*calls)[0].action, body)
	}
	if _, err := runDelivery(t, "not json", "forge", "hook"); err != nil {
		t.Fatalf("a hook must never fail its harness: %v", err)
	}
}

func TestForgeDeliveryHooksInstallIsIdempotent(t *testing.T) {
	_, stop := fakeDelivery(t, nil)
	defer stop()
	dir := t.TempDir()
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)

	for i := 0; i < 2; i++ {
		if _, err := runDelivery(t, "", "forge", "hooks", "install"); err != nil {
			t.Fatalf("install: %v", err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Hooks map[string][]any `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PostToolUse", "SubagentStart", "SubagentStop"} {
		if len(settings.Hooks[event]) != 1 {
			t.Fatalf("%s has %d entries after two installs", event, len(settings.Hooks[event]))
		}
	}
	if !strings.Contains(string(raw), "forge hook") {
		t.Fatalf("the hook command is this CLI: %s", raw)
	}
}

func TestForgeDeliveryInboxListsWhatNeedsYou(t *testing.T) {
	question := "Merge now?"
	state := map[string]any{
		"decisions": []any{map[string]any{"id": "trains", "question": "Keep the trains?", "options": []any{"Yes", "No"}, "recommended": 0, "chosen": nil}},
		"orders":    []any{map[string]any{"id": 4, "title": "Fix it", "owner": "backend", "question": question, "inMission": true}},
	}
	_, stop := fakeDelivery(t, map[string]any{"state": state})
	defer stop()

	out, err := runDelivery(t, "", "forge", "inbox")
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	for _, want := range []string{"decision trains: Keep the trains?", "0) Yes (recommended)", "question WO-4 backend: Merge now?", "miosa forge answer WO-4"} {
		if !strings.Contains(out, want) {
			t.Fatalf("inbox missing %q:\n%s", want, out)
		}
	}
}

func TestForgeDeliverySaysHowToStartTheServiceWhenItIsDown(t *testing.T) {
	t.Setenv("FORGE_LOCAL_URL", "http://127.0.0.1:1")
	if _, err := runDelivery(t, "", "forge", "status"); err == nil || !strings.Contains(err.Error(), "isn't running") {
		t.Fatalf("want a clear error, got %v", err)
	}
}

// A closed local port: where nothing listens, as on a customer machine with no Delivery service.
func closedLocalURL(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	return url
}

func TestForgeDeliveryWithoutAServiceSaysOnceThatItIsNotAvailable(t *testing.T) {
	t.Setenv("FORGE_LOCAL_URL", "")
	t.Setenv("FORGE_ORG", "")
	t.Setenv("FORGE_RUNNER", "")
	t.Setenv("MIOSA_API_KEY", "sk-test-secret-key")
	defer commands.SetDeliveryDefaultURLForTest(closedLocalURL(t))()

	for _, args := range [][]string{
		{"forge", "status"},
		{"forge", "ls"},
		{"forge", "prime"},
		{"forge", "inbox"},
		{"forge", "add", "Ship", "it"},
		{"forge", "mission", "new", "A mission"},
		{"forge", "show", "WO-1"},
	} {
		started := time.Now()
		out, err := runDelivery(t, "", args...)
		if err == nil {
			t.Fatalf("%v: expected an error, got output %q", args, out)
		}
		if err.Error() != "Forge Delivery isn't available for this account yet" {
			t.Fatalf("%v: error %q", args, err.Error())
		}
		for _, leak := range []string{"127.0.0.1", "localhost", "refused", "connect", "server.mjs", "sk-test-secret-key"} {
			if strings.Contains(out+err.Error(), leak) {
				t.Fatalf("%v: output must not mention %q: %q %q", args, leak, out, err)
			}
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("%v: took %s", args, elapsed)
		}
	}
}

func TestForgeDeliveryDefaultURLAnsweredByAnotherServiceIsStillNotAvailable(t *testing.T) {
	t.Setenv("FORGE_LOCAL_URL", "")
	t.Setenv("FORGE_ORG", "")
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "<html>some other dev server</html>", http.StatusNotFound)
	}))
	defer other.Close()
	defer commands.SetDeliveryDefaultURLForTest(other.URL)()

	for _, args := range [][]string{{"forge", "status"}, {"forge", "add", "Ship it"}} {
		_, err := runDelivery(t, "", args...)
		if err == nil || err.Error() != "Forge Delivery isn't available for this account yet" {
			t.Fatalf("%v: error %v", args, err)
		}
	}
}

func TestForgeDeliveryNamedByTheOperatorStillReportsItsAddress(t *testing.T) {
	url := closedLocalURL(t)
	t.Setenv("FORGE_LOCAL_URL", url)
	t.Setenv("FORGE_ORG", "acme")

	_, err := runDelivery(t, "", "forge", "status")
	if err == nil || !strings.Contains(err.Error(), url) {
		t.Fatalf("an operator who set FORGE_LOCAL_URL should see where it looked: %v", err)
	}
}

func TestForgeDeliveryNeverSendsAMiosaKeyToTheService(t *testing.T) {
	t.Setenv("MIOSA_API_KEY", "sk-test-secret-key")
	t.Setenv("MIOSA_ORG", "acme-platform")
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, values := range r.Header {
			seen = append(seen, name+"="+strings.Join(values, ","))
		}
		seen = append(seen, r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
	}))
	defer server.Close()
	t.Setenv("FORGE_LOCAL_URL", server.URL)
	t.Setenv("FORGE_ORG", "acme")

	_, _ = runDelivery(t, "", "forge", "pause", "builder")
	if len(seen) == 0 {
		t.Fatal("the service was not called")
	}
	joined := strings.Join(seen, "\n")
	if strings.Contains(joined, "sk-test-secret-key") || strings.Contains(strings.ToLower(joined), "authorization") {
		t.Fatalf("a MIOSA credential reached the Delivery service:\n%s", joined)
	}
}
