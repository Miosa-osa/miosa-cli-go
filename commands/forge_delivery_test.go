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
			_ = json.NewEncoder(w).Encode(results["state"])
			return
		}
		action := strings.TrimPrefix(r.URL.Path, "/api/")
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, deliveryCall{action: action, org: r.URL.Query().Get("org"), body: body})
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": results[action]})
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
	if !strings.Contains(out, "MIO-81 → Platform · P1") {
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

	out, err := runDelivery(t, "", "forge", "edit", "MIO-12", "--title", "New title")
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
	defer func() { _ = os.Chdir(wd) }()
	_ = os.Chdir(dir)

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
	for _, want := range []string{"decision trains: Keep the trains?", "0) Yes (recommended)", "question MIO-4 backend: Merge now?", "miosa forge answer MIO-4"} {
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

// A closed local port: where nothing listens.
func closedLocalURL(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	return url
}

// apiRequest is one request the fake MIOSA API received.
type apiRequest struct {
	method, path, auth, org string
	body                    map[string]any
}

// fakeDeliveryAPI is the MIOSA API with Forge Delivery: it answers state and every action, the
// way forge-local does, and records what it was sent.
func fakeDeliveryAPI(t *testing.T, results map[string]any) *[]apiRequest {
	t.Helper()
	var requests []apiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		requests = append(requests, apiRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Miosa-Org"), body})
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": results[strings.TrimPrefix(r.URL.Path, "/forge/delivery/")]})
			return
		}
		action, ok := strings.CutPrefix(r.URL.Path, "/forge/delivery/actions/")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "NOT_FOUND", "detail": "Not found"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": results[action]})
	}))
	t.Cleanup(server.Close)
	t.Setenv("FORGE_LOCAL_URL", "")
	t.Setenv("FORGE_ORG", "")
	t.Setenv("FORGE_RUNNER", "")
	t.Setenv("FORGE_ACTOR", "claude-code")
	t.Setenv("MIOSA_BASE_URL", server.URL)
	t.Setenv("MIOSA_API_KEY", "msk_test_delivery")
	t.Setenv("MIOSA_ORG", "osa")
	return &requests
}

func TestForgeDeliveryDefaultsToTheMiosaAPIWithTheKeyAndOrganization(t *testing.T) {
	requests := fakeDeliveryAPI(t, map[string]any{"add": []any{map[string]any{"id": 7, "kind": "added", "summary": "Platform · P2"}}})

	out, err := runDelivery(t, "", "forge", "add", "Ship", "it", "--owner", "platform")
	if err != nil {
		t.Fatalf("add: %v %s", err, out)
	}
	if !strings.Contains(out, "MIO-7") {
		t.Fatalf("add printed %q", out)
	}
	var sent *apiRequest
	for i := range *requests {
		if (*requests)[i].path == "/forge/delivery/actions/add" {
			sent = &(*requests)[i]
		}
	}
	if sent == nil {
		t.Fatalf("add never reached the Delivery API: %+v", *requests)
	}
	if sent.method != http.MethodPost || sent.auth != "Bearer msk_test_delivery" || sent.org != "osa" {
		t.Fatalf("sent %s with auth %q org %q", sent.method, sent.auth, sent.org)
	}
	if sent.body["text"] != "Ship it" || sent.body["owner"] != "platform" || sent.body["by"] != "claude-code" {
		t.Fatalf("payload %#v", sent.body)
	}
}

func TestForgeDeliveryReadsTheBoardFromTheAPI(t *testing.T) {
	// The board as the API names it: Issues, Phases, Domains, snake_case.
	state := map[string]any{
		"missions": []any{map[string]any{"id": "m1", "title": "Ship it", "status": "active", "acceptance_criteria": []any{"It ships"}}},
		"domains":  []any{map[string]any{"id": "client-issues", "title": "Client issues", "status": "open"}},
		"phases":   []any{map[string]any{"id": "m1:a", "mission": "m1", "title": "A. Trunk", "acceptance_criteria": "Green"}},
		"issues": []any{
			map[string]any{"id": 4, "key": "MIO-4", "title": "Merge now", "state": "todo", "owner": "backend", "priority": "P1", "mission": "m1", "phase": "m1:a", "in_mission": true, "spec_state": "approved"},
			map[string]any{"id": 5, "key": "MIO-5", "title": "Old work", "state": "released", "owner": "backend", "priority": "P2", "mission": "m1", "in_mission": true},
			map[string]any{"id": 6, "key": "MIO-6", "title": "Customer bug", "state": "todo", "owner": "backend", "priority": "P1", "domain": "client-issues", "mission": nil, "in_mission": true},
		},
	}
	requests := fakeDeliveryAPI(t, map[string]any{"state": state})

	out, err := runDelivery(t, "", "forge", "ls")
	if err != nil {
		t.Fatalf("ls: %v %s", err, out)
	}
	if !strings.Contains(out, "Merge now") || !strings.Contains(out, "Customer bug") || strings.Contains(out, "Old work") {
		t.Fatalf("ls printed %q", out)
	}
	if out, err = runDelivery(t, "", "forge", "ls", "--all"); err != nil || !strings.Contains(out, "Old work") {
		t.Fatalf("a released Issue is shipped: %v %q", err, out)
	}
	if got := (*requests)[len(*requests)-1]; got.method != http.MethodGet || got.path != "/forge/delivery/state" {
		t.Fatalf("read %s %s", got.method, got.path)
	}
}

func TestForgeDeliveryActionRefusedByTheAPIShowsItsMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "MIO-64 has no approved spec yet."})
	}))
	defer server.Close()
	fakeDeliveryAPI(t, nil)
	t.Setenv("MIOSA_BASE_URL", server.URL)

	_, err := runDelivery(t, "", "forge", "approve-spec", "WO-64")
	if err == nil || !strings.Contains(err.Error(), "MIO-64 has no approved spec yet.") {
		t.Fatalf("want the API's message, got %v", err)
	}
}

func TestForgeDeliveryWithoutTheAPIRouteSaysOnceThatItIsNotAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "FORGE_DISABLED", "detail": "Forge is not enabled for this organization", "retryable": false}})
	}))
	defer server.Close()
	fakeDeliveryAPI(t, nil)
	t.Setenv("MIOSA_BASE_URL", server.URL)

	for _, args := range [][]string{{"forge", "status"}, {"forge", "add", "Ship it"}} {
		_, err := runDelivery(t, "", args...)
		if err == nil || err.Error() != "the Forge Delivery API isn't available for this account yet" {
			t.Fatalf("%v: error %v", args, err)
		}
	}
}

func TestForgeDeliveryNeedsASignInWithoutForgeLocal(t *testing.T) {
	fakeDeliveryAPI(t, nil)
	t.Setenv("MIOSA_API_KEY", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, err := runDelivery(t, "", "forge", "status")
	if err == nil || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("want a sign-in error, got %v", err)
	}
}

func TestForgeDeliveryMissionImportSendsThePlanFile(t *testing.T) {
	requests := fakeDeliveryAPI(t, map[string]any{"importPlan": map[string]any{
		"mission": "move-the-code-onto-forge", "mission_created": false,
		"phases":  []any{map[string]any{"title": "A. Trunk", "done": 1, "total": 2}},
		"created": 0, "updated": 2, "moved": 1, "held": []any{"A2"},
	}})
	dir := t.TempDir()
	plan := filepath.Join(dir, "Move the code onto Forge.md")
	text := "# Move the code onto Forge\n\n### A. Trunk\n\n| # | R | State | E |\n|---|---|---|---|\n| A1 | One trunk | **Done** | ok |\n| A2 | No trains | **Built** | PR |\n"
	if err := os.WriteFile(plan, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	owners := filepath.Join(dir, "owners.json")
	if err := os.WriteFile(owners, []byte(`{"sections":{"A":"platform"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := runDelivery(t, "", "forge", "mission", "import", plan, "--owner-map", owners)
	if err != nil {
		t.Fatalf("import: %v %s", err, out)
	}
	for _, want := range []string{"A. Trunk: 1/2 Done", "Updated move-the-code-onto-forge: 0 new Issues, 2 updated, 1 changed state, A2 held by agents"} {
		if !strings.Contains(out, want) {
			t.Fatalf("import printed %q, missing %q", out, want)
		}
	}
	sent := (*requests)[len(*requests)-1]
	if sent.path != "/forge/delivery/actions/importPlan" {
		t.Fatalf("posted to %s", sent.path)
	}
	if sent.body["text"] != text || sent.body["source"] != "Move the code onto Forge.md" || sent.body["title"] != "Move the code onto Forge" || sent.body["by"] != "claude-code" {
		t.Fatalf("payload %#v", sent.body)
	}
	if sections, _ := sent.body["owners"].(map[string]any)["sections"].(map[string]any); sections["A"] != "platform" {
		t.Fatalf("owners %#v", sent.body["owners"])
	}
	if _, has := sent.body["goal"]; has {
		t.Fatalf("an unset goal must be left out: %#v", sent.body)
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
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": map[string]any{}})
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

func TestForgeDeliveryHookReportsToTheAPIAndNeverFailsSignedOut(t *testing.T) {
	requests := fakeDeliveryAPI(t, nil)
	t.Setenv("FORGE_RUNNER", "platform-5")

	input := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`
	if _, err := runDelivery(t, input, "forge", "hook"); err != nil {
		t.Fatalf("hook: %v", err)
	}
	sent := (*requests)[0]
	if sent.path != "/forge/delivery/actions/harness" || sent.auth != "Bearer msk_test_delivery" || sent.body["runner"] != "platform-5" || sent.body["summary"] != "go test ./..." {
		t.Fatalf("hook sent %+v", sent)
	}
	t.Setenv("MIOSA_API_KEY", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := runDelivery(t, input, "forge", "hook"); err != nil {
		t.Fatalf("a hook must never fail its harness, even signed out: %v", err)
	}
}

func TestForgeDeliveryIssueTypeOnAddEditAndLs(t *testing.T) {
	state := map[string]any{"issues": []any{
		map[string]any{"id": 1, "key": "MIO-1", "title": "Crash on start", "type": "bug", "state": "todo", "in_mission": true},
		map[string]any{"id": 2, "key": "MIO-2", "title": "Dark mode", "type": "feature", "state": "todo", "in_mission": true},
	}}
	requests := fakeDeliveryAPI(t, map[string]any{"state": state, "add": []any{map[string]any{"id": 3, "kind": "created", "summary": "Backend · P2"}}})

	if _, err := runDelivery(t, "", "forge", "add", "Fix", "it", "--type", "Bug"); err != nil {
		t.Fatal(err)
	}
	if _, err := runDelivery(t, "", "forge", "edit", "MIO-2", "--type", "task"); err != nil {
		t.Fatal(err)
	}
	var sent []map[string]any
	for _, request := range *requests {
		if request.method == http.MethodPost {
			sent = append(sent, request.body)
		}
	}
	if len(sent) != 2 || sent[0]["type"] != "bug" || sent[1]["type"] != "task" || sent[1]["id"] != 2.0 {
		t.Fatalf("sent %#v", sent)
	}
	if _, err := runDelivery(t, "", "forge", "add", "x", "--type", "epic"); err == nil || !strings.Contains(err.Error(), "bug, feature or task") {
		t.Fatalf("an unknown type must be refused: %v", err)
	}
	out, err := runDelivery(t, "", "forge", "ls", "--type", "bug")
	if err != nil || !strings.Contains(out, "Crash on start") || strings.Contains(out, "Dark mode") {
		t.Fatalf("ls --type bug: %v %q", err, out)
	}
}

func TestForgeDeliveryApproveSpecSignsWithTheActorAndNamesTheOwner(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"approveSpec": map[string]any{"approved": []any{12}}})
	defer stop()

	if out, err := runDelivery(t, "", "forge", "approve-spec", "WO-12", "--owner", "Roberto"); err != nil {
		t.Fatalf("approve-spec: %v %s", err, out)
	}
	body := (*calls)[0].body
	if body["id"] != float64(12) || body["by"] != "claude-code" || body["owner"] != "Roberto" {
		t.Fatalf("approve-spec payload %#v: the service refuses agents only if it is told who approves", body)
	}
}

func TestForgeDeliveryContextShowsThePrimaryOwnerAndTheDelegate(t *testing.T) {
	state := map[string]any{
		"orders": []any{map[string]any{"id": 4, "title": "Fix it", "owner": "backend", "humanOwner": "Roberto", "priority": "P2", "specState": "approved", "inMission": true}},
	}
	_, stop := fakeDelivery(t, map[string]any{"state": state})
	defer stop()

	for _, args := range [][]string{{"forge", "context", "WO-4"}, {"forge", "show", "WO-4"}} {
		out, err := runDelivery(t, "", args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, "Primary owner: Roberto") || !strings.Contains(out, "delegate backend") {
			t.Fatalf("%v missing the primary owner and delegate:\n%s", args, out)
		}
	}
}

func TestForgeDeliveryInitiativeNewAddShow(t *testing.T) {
	state := map[string]any{
		"missions":    []map[string]any{{"id": "m1", "title": "Move the code onto Forge", "status": "active"}, {"id": "m2", "title": "Forge Delivery v1", "status": "planned"}},
		"initiatives": []map[string]any{{"id": "one-delivery-system", "title": "One delivery system", "goal": "One pipeline", "missions": []string{"m1", "m2"}, "counts": map[string]int{"planned": 120, "plannedShipped": 60, "added": 7, "discovered": 3}}},
	}
	calls, stop := fakeDelivery(t, map[string]any{"state": state, "createInitiative": map[string]any{"id": "one-delivery-system"}, "initiativeAdd": map[string]any{"ok": true}})
	defer stop()

	out, err := runDelivery(t, "", "forge", "initiative", "new", "One", "delivery", "system", "--goal", "One pipeline", "--missions", "m1,m2")
	if err != nil || !strings.Contains(out, "Initiative one-delivery-system") {
		t.Fatalf("new: %v %q", err, out)
	}
	body := (*calls)[0].body
	if body["title"] != "One delivery system" || body["goal"] != "One pipeline" || body["by"] != "claude-code" {
		t.Fatalf("new payload %#v", body)
	}
	if _, err := runDelivery(t, "", "forge", "initiative", "add", "one-delivery-system", "m2"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if (*calls)[1].action != "initiativeAdd" || (*calls)[1].body["mission"] != "m2" {
		t.Fatalf("add payload %#v", (*calls)[1])
	}
	out, err = runDelivery(t, "", "forge", "initiative", "show", "one-delivery-system")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	for _, want := range []string{"One delivery system", "50% · 60 of 120 planned shipped · added 7 · discovered 3", "- m1 (active) Move the code onto Forge", "- m2 (planned) Forge Delivery v1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("show missing %q:\n%s", want, out)
		}
	}
	if _, err := runDelivery(t, "", "forge", "initiative", "show", "nope"); err == nil || !strings.Contains(err.Error(), "no Initiative nope") {
		t.Fatalf("show unknown: %v", err)
	}
}
