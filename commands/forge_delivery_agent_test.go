package commands_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// inGitRepo runs the test inside a fresh git repository on branch agent/MIO-12-fix with one commit,
// and keeps the hook's session files in a temporary folder.
func inGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())
	for _, args := range [][]string{
		{"init", "-q", "-b", "agent/MIO-12-fix"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "start"},
	} {
		run := exec.Command("git", args...)
		run.Dir = dir
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	wd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	return resolved
}

func boardWith(runner string, held int) map[string]any {
	runners := []any{}
	if runner != "" {
		entry := map[string]any{"id": runner, "role": "backend"}
		if held != 0 {
			entry["workOrder"] = held
		}
		runners = append(runners, entry)
	}
	return map[string]any{"state": map[string]any{
		"runners": runners,
		"orders":  []any{map[string]any{"id": 12, "title": "Fix the login loop", "doneWhen": "the login test passes"}},
	}}
}

func hookInput(event string, fields map[string]any) string {
	fields["hook_event_name"] = event
	fields["session_id"] = "s-1"
	raw, _ := json.Marshal(fields)
	return string(raw)
}

func hookJSON(t *testing.T, out string) map[string]any {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var reply map[string]any
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		t.Fatalf("the hook printed something that isn't hook JSON: %q", out)
	}
	return reply
}

func decision(reply map[string]any) (string, string) {
	inner, _ := reply["hookSpecificOutput"].(map[string]any)
	d, _ := inner["permissionDecision"].(string)
	r, _ := inner["permissionDecisionReason"].(string)
	if r == "" {
		r, _ = inner["additionalContext"].(string)
	}
	return d, r
}

func TestForgeJoinSavesTheAgentForThisWorktreeAndLeaveForgetsIt(t *testing.T) {
	calls, stop := fakeDelivery(t, map[string]any{"join": map[string]any{"runner": "backend-2", "mission": "m1"}, "take": map[string]any{"id": 12, "title": "Fix"}})
	defer stop()
	inGitRepo(t)

	out, err := runDelivery(t, "", "forge", "join", "--as", "backend", "--harness", "claude-code")
	if err != nil || !strings.Contains(out, "saved for this worktree") {
		t.Fatalf("join: %v %q", err, out)
	}
	if _, err := runDelivery(t, "", "forge", "take"); err != nil {
		t.Fatalf("take: %v", err)
	}
	if take := (*calls)[1]; take.action != "take" || take.body["runner"] != "backend-2" {
		t.Fatalf("take without FORGE_RUNNER must use the saved agent: %s %#v", take.action, take.body)
	}
	if _, err := runDelivery(t, "", "forge", "leave"); err != nil {
		t.Fatalf("leave: %v", err)
	}
	if _, err := runDelivery(t, "", "forge", "take"); err == nil {
		t.Fatal("after leave the worktree has no agent, so take must ask to join first")
	}
}

func TestForgeHookSessionStartBriefsTheAgentOnItsIssue(t *testing.T) {
	_, stop := fakeDelivery(t, boardWith("backend-2", 12))
	defer stop()
	inGitRepo(t)
	t.Setenv("FORGE_RUNNER", "backend-2")

	out, err := runDelivery(t, hookInput("SessionStart", map[string]any{"source": "startup"}), "forge", "hook")
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	inner, _ := hookJSON(t, out)["hookSpecificOutput"].(map[string]any)
	context, _ := inner["additionalContext"].(string)
	for _, want := range []string{"hold MIO-12: Fix the login loop", "Done when: the login test passes", "miosa forge context MIO-12", "miosa forge prime"} {
		if !strings.Contains(context, want) {
			t.Fatalf("session briefing misses %q:\n%s", want, context)
		}
	}
}

func TestForgeHookSessionStartTellsAnUnjoinedAgentToJoin(t *testing.T) {
	_, stop := fakeDelivery(t, boardWith("", 0))
	defer stop()
	inGitRepo(t)

	out, _ := runDelivery(t, hookInput("SessionStart", map[string]any{}), "forge", "hook")
	inner, _ := hookJSON(t, out)["hookSpecificOutput"].(map[string]any)
	if context, _ := inner["additionalContext"].(string); !strings.Contains(context, "miosa forge join --as <role>") {
		t.Fatalf("an unjoined agent must be told to join: %q", context)
	}
}

func TestForgeHookGuardRefusesEditsWithoutAHeldIssue(t *testing.T) {
	_, stop := fakeDelivery(t, boardWith("backend-2", 0))
	defer stop()
	dir := inGitRepo(t)
	t.Setenv("FORGE_RUNNER", "backend-2")
	t.Setenv("FORGE_GUARD", "block")

	edit := hookInput("PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": filepath.Join(dir, "main.go")}})
	out, err := runDelivery(t, edit, "forge", "hook")
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	if d, reason := decision(hookJSON(t, out)); d != "deny" || !strings.Contains(reason, "miosa forge take") {
		t.Fatalf("an edit with no held Issue must be refused with the way out, got %q %q", d, reason)
	}

	outside := hookInput("PreToolUse", map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": filepath.Join(t.TempDir(), "notes.md")}})
	if out, _ := runDelivery(t, outside, "forge", "hook"); hookJSON(t, out) != nil {
		t.Fatalf("a file outside the repository is not the Issue's business: %q", out)
	}
	read := hookInput("PreToolUse", map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": filepath.Join(dir, "main.go")}})
	if out, _ := runDelivery(t, read, "forge", "hook"); hookJSON(t, out) != nil {
		t.Fatalf("reading is never guarded: %q", out)
	}

	t.Setenv("FORGE_GUARD", "warn")
	out, _ = runDelivery(t, edit, "forge", "hook")
	if d, reason := decision(hookJSON(t, out)); d != "" || !strings.Contains(reason, "hold no Issue") {
		t.Fatalf("warn mode tells the agent without refusing, got %q %q", d, reason)
	}
}

func TestForgeHookGuardLetsAnAgentWithAnIssueWork(t *testing.T) {
	_, stop := fakeDelivery(t, boardWith("backend-2", 12))
	defer stop()
	dir := inGitRepo(t)
	t.Setenv("FORGE_RUNNER", "backend-2")
	t.Setenv("FORGE_GUARD", "block")

	edit := hookInput("PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": filepath.Join(dir, "main.go")}})
	if out, _ := runDelivery(t, edit, "forge", "hook"); hookJSON(t, out) != nil {
		t.Fatalf("an agent holding an Issue edits freely: %q", out)
	}
}

func TestForgeHookGuardNeverBlocksWhenTheBoardIsDown(t *testing.T) {
	inGitRepo(t)
	t.Setenv("FORGE_LOCAL_URL", "http://127.0.0.1:1")
	t.Setenv("FORGE_RUNNER", "backend-2")
	t.Setenv("FORGE_GUARD", "block")

	edit := hookInput("PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": "main.go"}})
	out, err := runDelivery(t, edit, "forge", "hook")
	if err != nil || hookJSON(t, out) != nil {
		t.Fatalf("a board that doesn't answer must not stop work: %v %q", err, out)
	}
}

func TestForgeHookRecordsAGitPushOnTheHeldIssue(t *testing.T) {
	calls, stop := fakeDelivery(t, boardWith("backend-2", 12))
	defer stop()
	inGitRepo(t)
	t.Setenv("FORGE_RUNNER", "backend-2")

	push := hookInput("PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "git push -u origin agent/MIO-12-fix"}})
	if _, err := runDelivery(t, push, "forge", "hook"); err != nil {
		t.Fatalf("hook: %v", err)
	}
	var recorded *deliveryCall
	for i := range *calls {
		if (*calls)[i].action == "push" {
			recorded = &(*calls)[i]
		}
	}
	if recorded == nil || recorded.body["id"] != float64(12) || recorded.body["branch"] != "agent/MIO-12-fix" || recorded.body["sha"] == nil {
		t.Fatalf("the push must be recorded on MIO-12 with branch and commit: %#v", *calls)
	}
}

func TestForgeHookAsksForProgressBeforeAnAgentThatChangedCodeStops(t *testing.T) {
	_, stop := fakeDelivery(t, boardWith("backend-2", 12))
	defer stop()
	dir := inGitRepo(t)
	t.Setenv("FORGE_RUNNER", "backend-2")

	edit := hookInput("PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": filepath.Join(dir, "main.go")}})
	_, _ = runDelivery(t, edit, "forge", "hook")
	out, _ := runDelivery(t, hookInput("Stop", map[string]any{"stop_hook_active": false}), "forge", "hook")
	reply := hookJSON(t, out)
	if reply["decision"] != "block" || !strings.Contains(reply["reason"].(string), "miosa forge log MIO-12") {
		t.Fatalf("an agent that changed code and said nothing must log first: %#v", reply)
	}
	if out, _ := runDelivery(t, hookInput("Stop", map[string]any{"stop_hook_active": true}), "forge", "hook"); hookJSON(t, out) != nil {
		t.Fatalf("the reminder comes once; a second stop goes through: %q", out)
	}
}

func TestForgeHookLetsAnAgentThatReportedStop(t *testing.T) {
	_, stop := fakeDelivery(t, boardWith("backend-2", 12))
	defer stop()
	dir := inGitRepo(t)
	t.Setenv("FORGE_RUNNER", "backend-2")

	_, _ = runDelivery(t, hookInput("PreToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": filepath.Join(dir, "main.go")}}), "forge", "hook")
	_, _ = runDelivery(t, hookInput("PostToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "miosa forge log MIO-12 \"login loop fixed, test next\""}}), "forge", "hook")
	if out, _ := runDelivery(t, hookInput("Stop", map[string]any{}), "forge", "hook"); hookJSON(t, out) != nil {
		t.Fatalf("an agent that logged progress stops freely: %q", out)
	}
}

func TestForgeConnectWiresTheRepositoryOnceAndKeepsOtherSettings(t *testing.T) {
	_, stop := fakeDelivery(t, boardWith("", 0))
	defer stop()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := inGitRepo(t)
	file := filepath.Join(dir, ".claude", "settings.local.json")
	_ = os.MkdirAll(filepath.Dir(file), 0o755)
	_ = os.WriteFile(file, []byte(`{"permissions":{"allow":["Bash(ls)"]},"hooks":{"PostToolUse":[{"matcher":"Agent|Task","hooks":[{"type":"command","command":"\"/old/miosa\" forge hook","timeout":3}]}]}}`), 0o644)

	for i := 0; i < 2; i++ {
		out, err := runDelivery(t, "", "forge", "connect", "--as", "codex")
		if err != nil || !strings.Contains(out, "Signed in") || !strings.Contains(out, "miosa forge join") {
			t.Fatalf("connect: %v %q", err, out)
		}
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		Permissions map[string]any               `json:"permissions"`
		Env         map[string]string            `json:"env"`
		Hooks       map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Permissions == nil || settings.Env["FORGE_ACTOR"] != "codex" || settings.Env["FORGE_GUARD"] != "block" {
		t.Fatalf("connect keeps other settings and signs work with --as: %s", raw)
	}
	for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse", "SubagentStart", "SubagentStop", "Stop"} {
		if len(settings.Hooks[event]) != 1 {
			t.Fatalf("%s has %d hook groups after two connects and an older install: %s", event, len(settings.Hooks[event]), raw)
		}
	}
	if !strings.Contains(string(settings.Hooks["PostToolUse"][0]), "Bash") {
		t.Fatalf("an older install's PostToolUse matcher is upgraded to see git pushes: %s", raw)
	}
	if exec.Command("git", "check-ignore", "-q", file).Run() != nil {
		t.Fatal("connect keeps settings.local.json out of commits")
	}
}
