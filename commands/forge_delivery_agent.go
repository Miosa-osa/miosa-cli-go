package commands

// The agent side of Forge Delivery: `miosa forge connect` wires a repository's Claude Code into
// Forge, and `miosa forge hook` is the hook it installs. The hook loads the rules and the agent's
// Issue when a session starts, refuses edits while the agent holds no Issue, records each git push
// on the held Issue, and asks for a progress line before an agent that changed code stops. It
// also reports tools and sub-agents live (hookReport). It never fails its harness.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ---- the running agent of this worktree ----

// runnerFile is where `join` keeps the agent's id: inside this worktree's git directory, so each
// worktree is its own agent and nothing lands in the working tree.
func runnerFile() string {
	dir := gitOutput("rev-parse", "--absolute-git-dir")
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "forge-runner")
}

func savedRunner() string {
	file := runnerFile()
	if file == "" {
		return ""
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// saveRunner reports whether the id was saved (false outside a git repository).
func saveRunner(id string) bool {
	file := runnerFile()
	return file != "" && os.WriteFile(file, []byte(id+"\n"), 0o644) == nil
}

func clearRunner() {
	if file := runnerFile(); file != "" {
		_ = os.Remove(file)
	}
}

// ---- installing the hooks ----

// agentHookEvents are the Claude Code events the hook handles, with each one's tool matcher and
// time budget in seconds. A session start reads the board; a tool use mostly reads a cache.
var agentHookEvents = []struct {
	event, matcher string
	timeout        int
}{
	{"SessionStart", "", 15},
	{"PreToolUse", ".*", 5},
	{"PostToolUse", "Agent|Task|Bash", 10},
	{"SubagentStart", "", 3},
	{"SubagentStop", "", 3},
	{"Stop", "", 10},
}

// installAgentHooks writes the hooks (and env, when given) into dir/.claude/settings.local.json,
// keeping everything else in the file. Run twice, it changes nothing.
func installAgentHooks(dir string, env map[string]string) (string, error) {
	binary, err := os.Executable()
	if err != nil {
		binary = "miosa"
	}
	command := fmt.Sprintf("%q forge hook", binary)
	file := filepath.Join(dir, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return "", err
	}
	settings := map[string]any{}
	if raw, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", fmt.Errorf("%s isn't valid JSON: %w", file, err)
		}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, wanted := range agentHookEvents {
		list, _ := hooks[wanted.event].([]any)
		ours := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": wanted.timeout}}}
		if wanted.matcher != "" {
			ours["matcher"] = wanted.matcher
		}
		replaced := false
		for i, item := range list {
			if isForgeHookGroup(item) {
				list[i], replaced = ours, true
			}
		}
		if !replaced {
			list = append(list, ours)
		}
		hooks[wanted.event] = list
	}
	settings["hooks"] = hooks
	if len(env) > 0 {
		values, _ := settings["env"].(map[string]any)
		if values == nil {
			values = map[string]any{}
		}
		for key, value := range env {
			values[key] = value
		}
		settings["env"] = values
	}
	raw, _ := json.MarshalIndent(settings, "", "  ")
	return file, os.WriteFile(file, append(raw, '\n'), 0o644)
}

// isForgeHookGroup is a hook group this CLI wrote: its command ends in "forge hook", whichever
// binary path an earlier install recorded.
func isForgeHookGroup(item any) bool {
	group, _ := item.(map[string]any)
	inner, _ := group["hooks"].([]any)
	for _, hook := range inner {
		if h, _ := hook.(map[string]any); h != nil {
			if command, _ := h["command"].(string); strings.HasSuffix(command, " forge hook") {
				return true
			}
		}
	}
	return false
}

func deliveryHooksInstall(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	if a.arg(0) != "install" {
		return errors.New("usage: miosa forge hooks install   (in the folder the agent's Claude Code runs in)")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	file, err := installAgentHooks(cwd, nil)
	if err != nil {
		return err
	}
	say(c, "Hooks written to %s. Claude Code started here loads the Forge rules and its Issue, keeps to it, and reports live.", file)
	return nil
}

// ---- miosa forge connect ----

func deliveryConnect(c *deliveryClient, a deliveryArgs, _ *cobra.Command) error {
	guard := firstNonEmpty(a.flag("guard"), "block")
	if guard != "block" && guard != "warn" && guard != "off" {
		return errors.New("--guard is block, warn or off")
	}
	actor := firstNonEmpty(a.flag("as"), os.Getenv("FORGE_ACTOR"), "claude-code")

	if _, err := c.state(); err != nil {
		if errors.Is(err, errDeliveryUnavailable) {
			return err
		}
		return fmt.Errorf("the Forge Delivery API didn't answer: %w\nSign in first: miosa login (then run miosa forge connect again)", err)
	}
	where := "your MIOSA organization"
	if c.local != "" {
		where = "forge-local at " + c.local
	}
	say(c, "✓ Signed in: Forge Delivery answers for %s.", where)

	top := gitOutput("rev-parse", "--show-toplevel")
	if top == "" {
		return errors.New("run miosa forge connect inside your clone of the repository you work on")
	}
	if origin := gitOutput("remote", "get-url", "origin"); !strings.Contains(origin, "forge.miosa.ai") {
		now := "there is no origin remote"
		if origin != "" {
			now = "origin is " + origin
		}
		say(c, "! Code lives on Forge and %s: git remote set-url origin https://forge.miosa.ai/osa/<repo>.git (or add it), then miosa forge auth setup-git", now)
	}

	file, err := installAgentHooks(top, map[string]string{"FORGE_ACTOR": actor, "FORGE_GUARD": guard})
	if err != nil {
		return err
	}
	if gitOutput("check-ignore", file) == "" {
		if exclude := gitOutput("rev-parse", "--git-path", "info/exclude"); exclude != "" {
			if !filepath.IsAbs(exclude) {
				exclude = filepath.Join(top, exclude)
			}
			if f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				_, _ = f.WriteString("\n.claude/settings.local.json\n")
				_ = f.Close()
			}
		}
	}
	say(c, "✓ Claude Code hooks in %s: each session starts with the rules and your Issue, work is signed %q, edits without an Issue: %s.", file, actor, guard)

	agents, _ := os.ReadFile(filepath.Join(top, "AGENTS.md"))
	if !strings.Contains(string(agents), "miosa forge") {
		say(c, "! AGENTS.md here doesn't point agents at Forge Delivery. Codex and other agents without these hooks only learn the flow from it.")
	}

	if runner := savedRunner(); runner != "" {
		say(c, "✓ You are %s in this worktree.", runner)
	} else {
		say(c, "\nNext:\n  miosa forge join --as <role> --harness claude-code   (backend, frontend, platform, ops)")
	}
	say(c, "  miosa forge take [MIO-<n>]   hold an Issue (or add the work first: miosa forge add \"<what>\" --owner <role> --priority P2)\n  miosa forge prime            the full rules")
	return nil
}

// ---- the hook ----

// hookSession is what one Claude Code session's hook remembers between calls.
type hookSession struct {
	Edited    bool   `json:"edited"`
	Reported  bool   `json:"reported"`
	Held      int    `json:"held"`
	HeldTitle string `json:"heldTitle"`
	Done      string `json:"doneWhen"`
	CheckedAt int64  `json:"checkedAt"`
	// Known is false until the board answered once for this agent.
	Known bool `json:"known"`
}

const heldCacheTTL = 60 * time.Second

func hookSessionFile(id string) string {
	safe := regexp.MustCompile(`[^A-Za-z0-9_-]`).ReplaceAllString(id, "")
	if safe == "" {
		return ""
	}
	return filepath.Join(os.TempDir(), "miosa-forge-hook", safe+".json")
}

func loadHookSession(id string) hookSession {
	var session hookSession
	if file := hookSessionFile(id); file != "" {
		if raw, err := os.ReadFile(file); err == nil {
			_ = json.Unmarshal(raw, &session)
		}
	}
	return session
}

func (s hookSession) save(id string) {
	file := hookSessionFile(id)
	if file == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(file), 0o700)
	raw, _ := json.Marshal(s)
	_ = os.WriteFile(file, raw, 0o600)
}

// refreshHeld asks the board which Issue the runner holds, at most once a minute. On no answer
// it leaves Known as it was: a slow board never blocks work.
func (s *hookSession) refreshHeld(c *deliveryClient, runner string, force bool) {
	if !force && s.Known && time.Since(time.UnixMilli(s.CheckedAt)) < heldCacheTTL {
		return
	}
	state, err := c.state()
	if err != nil {
		return
	}
	s.Known, s.CheckedAt, s.Held, s.HeldTitle, s.Done = true, time.Now().UnixMilli(), 0, "", ""
	for _, r := range state.Runners {
		if r.ID == runner && r.WorkOrder != nil {
			s.Held = *r.WorkOrder
			if order := state.order(fmt.Sprint(*r.WorkOrder)); order != nil {
				s.HeldTitle, s.Done = order.Title, order.DoneWhen
			}
		}
	}
}

var (
	forgeProgressCommand = regexp.MustCompile(`\bmiosa\b[^|;&]*\bforge\s+(log|step|steps|push|done|check|ask|discover|merged)\b`)
	forgeHoldCommand     = regexp.MustCompile(`\bmiosa\b[^|;&]*\bforge\s+(take|join|leave)\b`)
	gitPushCommand       = regexp.MustCompile(`\bgit\s+(-\S+\s+)*push\b`)
	editTools            = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}
)

const joinHint = "miosa forge join --as <role> --harness claude-code (backend, frontend, platform or ops)"

// deliveryHook is the Claude Code hook. Anything it prints is Claude Code's hook JSON; any error
// is swallowed, so the harness never fails because of Forge.
func deliveryHook(c *deliveryClient, _ deliveryArgs, cmd *cobra.Command) error {
	var input map[string]any
	if err := json.NewDecoder(cmd.InOrStdin()).Decode(&input); err != nil {
		return nil
	}
	c.wait = 4 * time.Second
	runner := firstNonEmpty(os.Getenv("FORGE_RUNNER"), savedRunner())
	if report := hookReport(input); report != nil && runner != "" {
		report["runner"] = runner
		path := "/harness"
		if c.local == "" {
			path = "/actions/harness"
		}
		_, _ = c.send(path, nil, report, 1500*time.Millisecond)
	}

	sessionID, _ := input["session_id"].(string)
	session := loadHookSession(sessionID)
	reply := hookReply(c, input, runner, &session)
	session.save(sessionID)
	if reply != nil {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetEscapeHTML(false)
		_ = encoder.Encode(reply)
	}
	return nil
}

func hookReply(c *deliveryClient, input map[string]any, runner string, session *hookSession) map[string]any {
	tool, _ := input["tool_name"].(string)
	toolInput, _ := input["tool_input"].(map[string]any)
	switch input["hook_event_name"] {
	case "SessionStart":
		*session = hookSession{}
		if runner != "" {
			session.refreshHeld(c, runner, true)
		}
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "SessionStart", "additionalContext": sessionBriefing(runner, session)}}
	case "PreToolUse":
		if editTools[tool] && insideRepository(toolInput) {
			return guardEdit(c, runner, session)
		}
	case "PostToolUse":
		if tool == "Bash" {
			line, _ := toolInput["command"].(string)
			noteCommand(c, line, runner, session)
		}
	case "Stop":
		active, _ := input["stop_hook_active"].(bool)
		return remindBeforeStop(active, session)
	}
	return nil
}

// guardEdit answers an edit inside the repository: nothing while the agent holds an Issue (or
// the board doesn't answer), otherwise the way back onto the board, refused under FORGE_GUARD=block.
func guardEdit(c *deliveryClient, runner string, session *hookSession) map[string]any {
	session.Edited = true
	guard := firstNonEmpty(os.Getenv("FORGE_GUARD"), "warn")
	if guard == "off" {
		return nil
	}
	reason := ""
	if runner == "" {
		reason = "No Forge agent is joined in this worktree, so this change belongs to no Issue. Join and hold one first: " + joinHint + ", then miosa forge take MIO-<n> (or add the work: miosa forge add \"<what>\" --owner <role> --priority P2)."
	} else if session.refreshHeld(c, runner, false); session.Known && session.Held == 0 {
		reason = "You (" + runner + ") hold no Issue, so this change belongs to nothing on the board. Hold one first: miosa forge take MIO-<n> (or add the work: miosa forge add \"<what>\" --owner <role> --priority P2)."
	}
	if reason == "" {
		return nil
	}
	if guard == "block" {
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": reason}}
	}
	return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "additionalContext": reason}}
}

// noteCommand watches the agent's shell: Forge progress commands count as reporting, take/join/leave
// make the held Issue stale, and a git push is recorded on the held Issue.
func noteCommand(c *deliveryClient, line, runner string, session *hookSession) {
	if forgeHoldCommand.MatchString(line) {
		session.Known = false
	}
	if forgeProgressCommand.MatchString(line) {
		session.Reported = true
	}
	if !gitPushCommand.MatchString(line) || strings.Contains(line, "--dry-run") || runner == "" {
		return
	}
	if session.refreshHeld(c, runner, false); session.Held == 0 {
		return
	}
	branch := gitOutput("rev-parse", "--abbrev-ref", "HEAD")
	if branch != "" && c.call("push", map[string]any{"id": session.Held, "branch": branch, "sha": optional(gitOutput("rev-parse", "HEAD"))}, nil) == nil {
		session.Reported = true
	}
}

// remindBeforeStop holds back, once, an agent that changed code for its Issue and told Forge nothing.
func remindBeforeStop(active bool, session *hookSession) map[string]any {
	if active || !session.Edited || session.Reported || session.Held == 0 {
		return nil
	}
	session.Reported = true
	label := woLabel(session.Held)
	return map[string]any{"decision": "block", "reason": "You changed code for " + label + " but Forge has no word of it this session. Record where it stands before you stop: miosa forge log " + label + " \"<what changed, what is next>\" (or, when it is finished and proven: miosa forge done " + label + " --report -)."}
}

// insideRepository is whether the edited file is in this git repository: edits elsewhere (notes,
// scratch files) are not the Issue's business.
func insideRepository(toolInput map[string]any) bool {
	path, _ := toolInput["file_path"].(string)
	if path == "" {
		path, _ = toolInput["notebook_path"].(string)
	}
	top := gitOutput("rev-parse", "--show-toplevel")
	if path == "" || top == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		return true
	}
	rel, err := filepath.Rel(top, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sessionBriefing(runner string, session *hookSession) string {
	var b strings.Builder
	b.WriteString("This repository's work is tracked in Forge Delivery (MIOSA's tracker, the `miosa forge` commands). Every change belongs to an Issue you hold.\n")
	switch {
	case runner == "":
		b.WriteString("No Forge agent is joined in this worktree. Before changing anything: " + joinHint + ", then miosa forge take MIO-<n>. New work goes on the board first: miosa forge add \"<what>\" --owner <role> --priority P0-P3.\n")
	case session.Known && session.Held != 0:
		label := woLabel(session.Held)
		fmt.Fprintf(&b, "You are %s and hold %s: %s.\n", runner, label, session.HeldTitle)
		if session.Done != "" {
			fmt.Fprintf(&b, "Done when: %s\n", session.Done)
		}
		fmt.Fprintf(&b, "Read its spec and plan before you start: miosa forge context %s\n", label)
	case session.Known:
		fmt.Fprintf(&b, "You are %s and hold no Issue. Take one before changing anything: miosa forge take [MIO-<n>]. New work goes on the board first: miosa forge add \"<what>\" --owner <role> --priority P0-P3.\n", runner)
	default:
		fmt.Fprintf(&b, "You are %s. Forge didn't answer just now; check what you hold with miosa forge status.\n", runner)
	}
	b.WriteString("While you work: plan steps (miosa forge steps MIO-<n> \"a\" \"b\"), tick them off (miosa forge step MIO-<n> <k>), post progress (miosa forge log MIO-<n> \"…\"), file anything off-scope instead of fixing it (miosa forge discover \"…\" --from MIO-<n>), and ask with your own default when unsure (miosa forge ask). Pushes are recorded on your Issue automatically.\n")
	b.WriteString("Finish with proof: miosa forge check run MIO-<n> -- <the real test>, then miosa forge done MIO-<n> --report -. Never approve or merge your own work.\n")
	b.WriteString("Full rules and where things stand: miosa forge prime. The path from a change to live: docs/agents/shipping-to-miosa.md in the miosa repository.")
	return b.String()
}
