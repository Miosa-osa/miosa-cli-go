package commands_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// prFailuresAPI serves a pull request whose head has three finished
// workflows: lint fails and passed on main (new), test fails and failed on main
// too (already on main), build passes; e2e is still running.
func prFailuresAPI(t *testing.T, collaborationCalls *int) *httptest.Server {
	t.Helper()
	step := func(id, name, status string, exit int) map[string]any {
		return map[string]any{"id": id, "name": name, "status": status, "exit_code": exit}
	}
	runRow := func(id, wf, sha, ref, status string, steps ...map[string]any) map[string]any {
		if steps == nil {
			steps = []map[string]any{}
		}
		return map[string]any{"id": id, "workflow": wf, "workflow_id": wf, "commit_sha": sha, "ref": ref, "status": status, "steps": steps}
	}
	runs := []any{
		runRow("r-e2e", "e2e", "head1234567", "refs/heads/fix", "running"),
		runRow("r-lint", "lint", "head1234567", "refs/heads/fix", "failed",
			step("s-fmt", "gofmt", "passed", 0), step("s-vet", "golangci", "failed", 1)),
		runRow("r-test", "test", "head1234567", "refs/heads/fix", "failed", step("s-test", "go test", "failed", 2)),
		runRow("r-build", "build", "head1234567", "refs/heads/fix", "passed", step("s-b", "go build", "passed", 0)),
		// Older head run of lint: superseded by r-lint, never reported.
		runRow("r-lint-old", "lint", "head1234567", "refs/heads/fix", "failed", step("s-old", "golangci", "failed", 1)),
		runRow("m-lint-running", "lint", "main999", "refs/heads/main", "running"),
		runRow("m-lint", "lint", "main888", "refs/heads/main", "passed"),
		runRow("m-test", "test", "main888", "refs/heads/main", "failed"),
		runRow("m-test-old", "test", "main777", "refs/heads/main", "passed"),
	}
	var logLines []string
	for i := 1; i <= 30; i++ {
		logLines = append(logLines, fmt.Sprintf("line %d", i))
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"data": v}) }
		switch r.URL.Path {
		case "/forge/repositories":
			reply([]any{forgeRepoJSON()})
		case "/forge/repositories/repo-1/pull-requests/12":
			reply(map[string]any{"number": 12, "head_sha": "head1234567", "base_branch": "main", "head_branch": "fix"})
		case "/forge/repositories/repo-1/pull-requests/12/collaboration":
			*collaborationCalls++
			reply(map[string]any{})
		case "/forge/repositories/repo-1/automation-runs":
			if r.URL.Query().Get("limit") != "100" {
				t.Errorf("runs limit = %q", r.URL.Query().Get("limit"))
			}
			reply(runs)
		case "/forge/repositories/repo-1/automation-runs/r-lint/steps/s-vet/receipt":
			reply(map[string]any{"stdout": strings.Join(logLines, "\n"), "stderr": "lint: 1 issue\n", "exit_code": 1})
		case "/forge/repositories/repo-1/automation-runs/r-test/steps/s-test/receipt":
			reply(map[string]any{"stdout": "--- FAIL: TestX\n", "stderr": "", "exit_code": 2, "truncated": true})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func TestForgePRStatusFailuresMarksNewAndAlreadyOnMain(t *testing.T) {
	var collab int
	srv := prFailuresAPI(t, &collab)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := run(t, "forge", "pr", "status", "platform", "12", "--failures", "--json")
	if err != nil {
		t.Fatalf("status --failures --json: %v\n%s", err, out)
	}
	var envelope struct {
		Data struct {
			HeadSHA  string   `json:"head_sha"`
			Runs     int      `json:"runs_on_head"`
			Pending  []string `json:"pending"`
			Failures []struct {
				Workflow   string   `json:"workflow"`
				Step       string   `json:"step"`
				ExitCode   int      `json:"exit_code"`
				Origin     string   `json:"origin"`
				BaseRunID  string   `json:"base_run_id"`
				BaseCommit string   `json:"base_commit_sha"`
				Log        []string `json:"log"`
				Truncated  bool     `json:"log_truncated"`
			} `json:"failures"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	report := envelope.Data
	if report.Runs != 4 || len(report.Pending) != 1 || report.Pending[0] != "e2e" || len(report.Failures) != 2 {
		t.Fatalf("report = %+v\n%s", report, out)
	}
	lint, test := report.Failures[0], report.Failures[1]
	if lint.Workflow != "lint" || lint.Step != "golangci" || lint.ExitCode != 1 || lint.Origin != "new" || lint.BaseRunID != "m-lint" || lint.BaseCommit != "main888" {
		t.Fatalf("lint = %+v", lint)
	}
	if len(lint.Log) != 20 || lint.Log[19] != "lint: 1 issue" || lint.Log[0] != "line 12" {
		t.Fatalf("lint log = %q", lint.Log)
	}
	if test.Origin != "already_on_main" || test.BaseRunID != "m-test" || !test.Truncated || len(test.Log) != 1 {
		t.Fatalf("test = %+v", test)
	}
	if collab != 0 {
		t.Fatalf("--failures also fetched the snapshot %d time(s)", collab)
	}

	text, err := run(t, "forge", "pr", "status", "platform", "12", "--failures")
	if err != nil {
		t.Fatalf("status --failures: %v\n%s", err, text)
	}
	for _, want := range []string{
		"2 failing check(s)", "FAIL lint [new: passed on main", "step: golangci, exit 1", "| lint: 1 issue",
		"FAIL test [already on main: failed at", "cut at its size limit", "Still running: e2e",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("text output lacks %q:\n%s", want, text)
		}
	}
}

func TestForgePRStatusWithoutFailuresShowsSnapshot(t *testing.T) {
	var collab int
	srv := prFailuresAPI(t, &collab)
	defer srv.Close()
	defer setupEnv(t, srv)()

	if out, err := run(t, "forge", "pr", "status", "platform", "12", "--json"); err != nil || collab != 1 {
		t.Fatalf("status: %v (collaboration calls %d)\n%s", err, collab, out)
	}
}
