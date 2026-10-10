package commands

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// pollRunEvery is the base interval of run polling; tests shorten it.
var pollRunEvery = time.Second

func init() {
	rootCmd.AddCommand(newPromptCmd())
}

type promptOptions struct {
	harness     string
	model       string
	agent       string
	chat        string
	cont        bool
	newChat     bool
	sandbox     string
	computer    string
	newMachine  bool
	workspace   string
	size        string
	template    string
	machineKind string
	afterRun    string
	reuse       string
	maxTime     int
	notifyEmail bool
	notifyHook  string
	idem        string
	file        string
	noFollow    bool
	source      string
}

func newPromptCmd() *cobra.Command {
	var o promptOptions
	cmd := &cobra.Command{
		Use:   "prompt [text...]",
		Short: "Run an agent on a sandbox or computer and follow it",
		Long: `Send an instruction to a coding agent (Claude Code, Codex or OSA) running in a
sandbox or computer, stream what it does, and print its answer. The run uses
YOUR model connection: add one with 'miosa connections add models'. MIOSA never
falls back to its own keys; without a connection the run is refused.

The target is --sandbox/--computer, the current sandbox ('miosa use'), or a new
machine MIOSA provisions for the run with --new-machine. The prompt is the
arguments, "-" for standard input, or --file.

Follow-ups: --chat ID names a chat and continues it (the agent keeps its memory
on the machine); --continue resumes the machine's most recent run; --new-chat
starts a chat and prints its id.

  miosa prompt "add a health check to the api" --sandbox my-box
  miosa prompt --harness codex --model gpt-6.1-sol "fix the failing test"
  miosa prompt --new-machine --workspace acme "write a README for this repo"
  miosa prompt --chat 3f0c... "now add tests"
  git diff | miosa prompt --sandbox my-box -     # the diff is the prompt
  miosa prompt --agent reviewer --no-follow "review PR 42"   # prints the run id`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPrompt(cmd, args, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.harness, "harness", "", "Harness: claude-code, codex or osa (default: the agent's, or claude-code)")
	f.StringVar(&o.model, "model", "", "Model for the harness (default: the harness default)")
	f.StringVar(&o.agent, "agent", "", "Run a saved agent (name or id) instead of an ad hoc one")
	f.StringVar(&o.chat, "chat", "", "Chat id; continues the chat if it has runs")
	f.BoolVar(&o.cont, "continue", false, "Continue the most recent run on the target")
	f.BoolVar(&o.newChat, "new-chat", false, "Start a chat (a new chat id is generated and printed)")
	f.StringVarP(&o.sandbox, "sandbox", "s", "", "Sandbox to run in (default: the current sandbox)")
	f.StringVar(&o.computer, "computer", "", "Computer to run in")
	f.BoolVar(&o.newMachine, "new-machine", false, "Provision a machine for this run")
	f.StringVar(&o.workspace, "workspace", "", "Workspace (slug or id) for a new machine")
	f.StringVar(&o.size, "size", "", "Size of a new machine: "+strings.Join(sandboxSizes, ", "))
	f.StringVar(&o.template, "template", "", "Template of a new machine")
	f.StringVar(&o.machineKind, "machine", "", "Kind of new machine: sandbox or computer")
	f.StringVar(&o.afterRun, "after-run", "", "What happens to a new machine when the run ends: destroy, pause or keep")
	f.StringVar(&o.reuse, "reuse", "", "New machine lifetime: run (one per run) or chat (one per --chat, files persist)")
	f.IntVar(&o.maxTime, "max-time", 0, "Stop the run after this many seconds")
	f.BoolVar(&o.notifyEmail, "notify-email", false, "Email you when the run finishes")
	f.StringVar(&o.notifyHook, "notify-webhook", "", "Webhook id to notify when the run finishes")
	f.StringVar(&o.idem, "idempotency-key", "", "Replay-safe key: the same key returns the same run")
	f.StringVar(&o.file, "file", "", "Read the prompt from a file")
	f.BoolVar(&o.noFollow, "no-follow", false, "Print the run id and return without waiting")
	f.StringVar(&o.source, "source", "", "Label recorded on the run (max 40 characters)")
	_ = cmd.RegisterFlagCompletionFunc("harness", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"claude-code", "codex", "osa"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("sandbox", func(c *cobra.Command, _ []string, p string) ([]string, cobra.ShellCompDirective) {
		return completeKind(c, "sandbox", p)
	})
	return cmd
}

// newUUID returns a random version-4 UUID for chat ids.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func readPrompt(cmd *cobra.Command, args []string, file string) (string, error) {
	switch {
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	case len(args) == 1 && args[0] == "-":
		b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1<<20))
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return strings.TrimSpace(strings.Join(args, " ")), nil
	}
}

func runPrompt(cmd *cobra.Command, args []string, o promptOptions) error {
	text, err := readPrompt(cmd, args, o.file)
	if err != nil {
		return die(err)
	}
	if text == "" {
		return usagef("give the agent something to do: miosa prompt \"<instruction>\" (or - to read standard input)")
	}
	if o.newMachine && (o.sandbox != "" || o.computer != "") {
		return usagef("--new-machine cannot be combined with --sandbox or --computer")
	}
	if o.sandbox != "" && o.computer != "" {
		return usagef("choose one of --sandbox or --computer")
	}
	if o.chat != "" && o.newChat {
		return usagef("--chat and --new-chat cannot be combined")
	}
	switch o.afterRun {
	case "", "destroy", "pause", "keep":
	default:
		return usagef("--after-run must be destroy, pause or keep")
	}
	if o.reuse != "" && o.reuse != "run" && o.reuse != "chat" {
		return usagef("--reuse must be run or chat")
	}
	if o.harness != "" {
		switch o.harness {
		case "claude-code", "codex", "osa":
		case "claude":
			o.harness = "claude-code"
		default:
			return usagef("--harness must be claude-code, codex or osa (got %q)", o.harness)
		}
	}

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	body := map[string]any{"instruction": text, "source": orDefault(o.source, "cli")}
	if o.harness != "" {
		body["runner"] = o.harness
	} else if o.agent == "" {
		body["runner"] = "claude-code"
	}
	if o.model != "" {
		body["model"] = o.model
	}
	if o.maxTime > 0 {
		body["timeout_sec"] = o.maxTime
	}
	if o.agent != "" {
		id, err := c.API.ResolveAgent(ctx, o.agent)
		if err != nil {
			return die(err)
		}
		body["agent_definition_id"] = id
	}

	// Target.
	var targetID string
	switch {
	case o.newMachine:
		body["target"] = "new"
		machine := map[string]any{}
		if o.machineKind != "" {
			machine["target"] = o.machineKind
		}
		if o.size != "" {
			if _, err := parseSandboxSize(o.size); err != nil {
				return usagef("%v", err)
			}
			machine["size"] = o.size
		}
		if o.template != "" {
			machine["template"] = o.template
		}
		if o.reuse != "" {
			machine["reuse"] = o.reuse
		}
		if o.afterRun != "" {
			machine["after_run"] = o.afterRun
		}
		if len(machine) > 0 {
			body["machine"] = machine
		}
		ws := orDefault(o.workspace, "")
		if ws != "" {
			id, err := resolveWorkspaceID(ctx, c, ws)
			if err != nil {
				return die(err)
			}
			body["workspace_id"] = id
		}
	case o.computer != "":
		id, err := c.API.ResolveComputer(ctx, o.computer)
		if err != nil {
			return die(err)
		}
		body["computer_id"], targetID = id, id
	default:
		ref := orDefault(o.sandbox, cfg.CurrentSandbox)
		if ref == "" && o.agent == "" {
			return usagef("no target: pass --sandbox NAME, --computer NAME or --new-machine (or run 'miosa use <name>')")
		}
		if ref != "" {
			id, err := c.API.ResolveSandbox(ctx, ref)
			if err != nil {
				return die(err)
			}
			body["sandbox_id"], targetID = id, id
		}
	}

	// Conversation.
	chatID := o.chat
	if o.newChat {
		chatID = newUUID()
	}
	if chatID != "" {
		body["chat_id"] = chatID
		if prev, err := latestRun(ctx, c.API, url.Values{"chat_id": {chatID}}); err == nil && prev != "" {
			body["continue_from_run_id"] = prev
		}
	} else if o.cont {
		q := url.Values{}
		if targetID != "" {
			q.Set("target_id", targetID)
		}
		if r, _ := body["runner"].(string); r != "" {
			q.Set("runner", r)
		}
		prev, err := latestRun(ctx, c.API, q)
		if err != nil {
			return die(err)
		}
		if prev == "" {
			return die(&api.Error{Status: 404, Code: "NOT_FOUND", Message: "no earlier run on this target to continue"})
		}
		body["continue_from_run_id"] = prev
	}

	if o.notifyEmail || o.notifyHook != "" {
		n := map[string]any{}
		if o.notifyEmail {
			n["email"] = true
		}
		if o.notifyHook != "" {
			n["webhook_id"] = o.notifyHook
		}
		body["notify"] = n
	}

	req := api.Request{Method: "POST", Path: "/runs", Body: body, IdempotencyKey: o.idem}
	resp, err := c.API.JSONAny(ctx, req)
	if err != nil {
		return die(err)
	}
	run, _ := unwrapObject(resp).(map[string]any)
	id, _ := run["id"].(string)
	if id == "" {
		return die(errors.New("the server did not return a run id"))
	}
	if chatID == "" {
		if cid, _ := run["chat_id"].(string); cid != "" {
			chatID = cid
		}
	}

	if o.noFollow {
		if isJSON() {
			return p.JSON(run)
		}
		p.Line("%s", id)
		if chatID != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "chat: %s\n", chatID)
		}
		return nil
	}
	if !isJSON() {
		fmt.Fprintf(cmd.ErrOrStderr(), "run %s (%s)\n", id, formatValue(run["status"], ""))
		if chatID != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "chat: %s  (continue with --chat %s)\n", chatID, chatID)
		}
	}
	return followRun(cmd, c.API, id)
}

// latestRun returns the id of the newest finished run matching q ("" if none).
func latestRun(ctx context.Context, c *api.Client, q url.Values) (string, error) {
	q.Set("limit", "5")
	var out struct {
		Data []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/runs", q, &out); err != nil {
		return "", err
	}
	for _, r := range out.Data { // newest first
		if r.Status == "succeeded" || r.Status == "failed" || r.Status == "canceled" {
			return r.ID, nil
		}
	}
	return "", nil
}

func terminalStatus(s string) bool {
	return s == "succeeded" || s == "failed" || s == "canceled"
}

// followRun streams a run's events until it finishes and prints its answer.
// Interrupting detaches without stopping the run.
func followRun(cmd *cobra.Command, c *api.Client, id string) error {
	p := printerFor(cmd)
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	seen := map[string]bool{}
	interval := pollRunEvery
	var run map[string]any
	for {
		var ev struct {
			Data []map[string]any `json:"data"`
		}
		if err := c.Get(ctx, "/runs/"+id+"/events", nil, &ev); err != nil && ctx.Err() == nil {
			return die(err)
		}
		for _, e := range ev.Data {
			eid, _ := e["id"].(string)
			if eid != "" && seen[eid] {
				continue
			}
			seen[eid] = true
			printRunEvent(cmd, p, e)
		}
		resp, err := c.JSONAny(ctx, api.Request{Method: "GET", Path: "/runs/" + id})
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return die(err)
		}
		run, _ = unwrapObject(resp).(map[string]any)
		if st, _ := run["status"].(string); terminalStatus(st) {
			// One last read so events written with the final status are shown.
			var last struct {
				Data []map[string]any `json:"data"`
			}
			if c.Get(context.Background(), "/runs/"+id+"/events", nil, &last) == nil {
				for _, e := range last.Data {
					if eid, _ := e["id"].(string); eid == "" || !seen[eid] {
						seen[eid] = true
						printRunEvent(cmd, p, e)
					}
				}
			}
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
		if ctx.Err() != nil {
			break
		}
		if interval < 3*time.Second {
			interval += interval / 2
		}
	}
	if ctx.Err() != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "\nDetached; run %s keeps going. Follow it with: miosa run follow %s\n", id, id)
		return &ExitError{Code: 130}
	}
	return finishRun(cmd, c, p, id, run)
}

func printRunEvent(cmd *cobra.Command, p interface {
	JSONLine(v interface{}) error
}, e map[string]any) {
	if isJSON() {
		_ = p.JSONLine(map[string]any{"event": e["type"], "data": e})
		return
	}
	msg, _ := e["message"].(string)
	typ, _ := e["type"].(string)
	if msg == "" {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s: %s\n", typ, msg)
}

// finishRun prints the run's answer and result and maps its status to an
// exit code: 0 when it succeeded, 1 otherwise.
func finishRun(cmd *cobra.Command, c *api.Client, p interface {
	JSON(v interface{}) error
	JSONLine(v interface{}) error
}, id string, run map[string]any) error {
	status, _ := run["status"].(string)
	var outputs map[string]any
	if resp, err := c.JSONAny(context.Background(), api.Request{Method: "GET", Path: "/runs/" + id + "/outputs"}); err == nil {
		outputs, _ = unwrapObject(resp).(map[string]any)
	}
	if isJSON() {
		return finishRunJSON(p, run, outputs, status)
	}
	if status == "succeeded" {
		if text := answerText(outputs); text != "" {
			fmt.Fprintln(cmd.OutOrStdout(), text)
		}
	}
	line := fmt.Sprintf("%s run %s", status, id)
	if cost, _ := run["cost"].(map[string]any); cost != nil {
		if total, ok := toFloat(cost["total_credits"]); ok {
			line += fmt.Sprintf(" - %.0f credits", total)
		}
	}
	fmt.Fprintln(cmd.ErrOrStderr(), line)
	if status == "succeeded" {
		return nil
	}
	code, _ := run["error_code"].(string)
	msg, _ := run["error_message"].(string)
	msg = summarizeRunError(msg, status)
	if hint := credentialHint(code, msg); hint != "" {
		msg += " - " + hint
	}
	msg += fmt.Sprintf(" (details: miosa run diagnostics %s, output: miosa run logs %s)", id, id)
	return die(&api.Error{Status: 422, Code: code, Message: msg}) // exit 5: the run itself failed
}

func finishRunJSON(p interface {
	JSON(v interface{}) error
}, run, outputs map[string]any, status string) error {
	out := map[string]any{"run": run}
	if outputs != nil {
		out["outputs"] = outputs
	}
	if err := p.JSON(out); err != nil {
		return err
	}
	if status != "succeeded" {
		return &Failure{Err: &api.Error{Status: 422, Message: "run " + status}, quiet: true}
	}
	return nil
}

// answerText picks the agent's final message from a run's outputs.
func answerText(outputs map[string]any) string {
	if outputs == nil {
		return ""
	}
	if t, _ := lookupPath(outputs, "result.text").(string); t != "" {
		return t
	}
	if m, _ := outputs["message"].(string); m != "" {
		return m
	}
	if msgs, _ := outputs["messages"].([]any); len(msgs) > 0 {
		if last, _ := msgs[len(msgs)-1].(map[string]any); last != nil {
			for _, k := range []string{"text", "content", "message"} {
				if s, _ := last[k].(string); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

// summarizeRunError reduces a failed run's message to one readable line. Harness
// errors arrive as JSON lines; the last "error" or "turn.failed" message wins.
func summarizeRunError(msg, status string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "the run " + status
	}
	best := ""
	// The harness prints one JSON event per line, but the API may join them
	// with spaces: decode values one after another.
	dec := json.NewDecoder(strings.NewReader(msg))
	for {
		var ev map[string]any
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if m, _ := lookupPath(ev, "error.message").(string); m != "" {
			best = m
		} else if t, _ := ev["type"].(string); t == "error" {
			if m, _ := ev["message"].(string); m != "" && !strings.HasPrefix(m, "Reconnecting") {
				best = m
			}
		}
	}
	if best == "" && strings.Contains(msg, `"message":"`) {
		// The API clips long messages, which can cut the last event in half:
		// read message fields by pattern instead.
		var last string
		for _, m := range messageField.FindAllStringSubmatch(msg, -1) {
			text := m[1]
			if inner := reconnecting.FindStringSubmatch(text); inner != nil {
				text = inner[1]
				if last == "" {
					last = text
				}
				continue
			}
			last = text
		}
		best = last
	}
	if best == "" {
		// Not JSON lines (or nothing readable in them): the first line, bounded.
		best = strings.Join(strings.Fields(strings.SplitN(msg, "\n", 2)[0]), " ")
	}
	return clipText(best, 220)
}

// credentialHint explains a refused or rejected credential. MIOSA never lends
// its own provider keys to a user's agent, so the way forward is always the
// user's own connection.
func credentialHint(code, msg string) string {
	l := strings.ToLower(msg)
	if code == "OWN_CREDENTIALS_REQUIRED" || strings.Contains(l, "401") && strings.Contains(l, "unauthorized") ||
		strings.Contains(l, "missing bearer") || strings.Contains(l, "invalid api key") || strings.Contains(l, "authentication") && strings.Contains(l, "failed") {
		return "add your own model connection with 'miosa connections add models <anthropic|openai|...>' (MIOSA does not supply one)"
	}
	return ""
}

var (
	messageField = regexp.MustCompile(`"message":"((?:[^"\\]|\\.)*)`)
	reconnecting = regexp.MustCompile(`^Reconnecting\.\.\. \d+/\d+ \((.*?)(?:, url:.*)?\)?$`)
)
