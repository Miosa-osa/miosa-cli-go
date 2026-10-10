package commands

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(runCmd(), chatCmd())
}

var runCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "HARNESS", Path: "runner"},
	{Head: "MODEL", Path: "model"}, {Head: "AGE", Path: "created_at", Fmt: "age"}, {Head: "CHAT", Path: "chat_id", Fmt: "short"},
	{Head: "INSTRUCTION", Path: "instruction", Fmt: "clip"},
}

var runDetail = []Col{
	{Head: "Run", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Harness", Path: "runner"}, {Head: "Model", Path: "model"},
	{Head: "Target", Path: "target_kind"}, {Head: "Target id", Path: "target_id"}, {Head: "Chat", Path: "chat_id"},
	{Head: "Session", Path: "harness_session_id"}, {Head: "Continued from", Path: "continued_from_run_id"},
	{Head: "Agent", Path: "agent_definition_id"}, {Head: "Overdrive", Path: "overdrive"}, {Head: "Source", Path: "source"},
	{Head: "Started", Path: "started_at", Fmt: "age"}, {Head: "Finished", Path: "finished_at", Fmt: "age"},
	{Head: "Credits", Path: "cost.total_credits"}, {Head: "Error", Path: "error_code"}, {Head: "Error message", Path: "error_message"},
	{Head: "Instruction", Path: "instruction"},
}

func runCmd() *cobra.Command {
	cmd := group("run", "List, follow and stop agent runs",
		`A run is one instruction executed by an agent harness on a machine. 'miosa prompt'
starts one; these commands inspect runs started from anywhere (the console, the
API, triggers, workflows).

  miosa run list --status running
  miosa run follow <id>
  miosa run stop <id>
  miosa run usage --from 2026-10-01 --group-by runner

With a command after -- it is a one-shot instead: a fresh sandbox runs the command,
its output is printed, and --rm removes the sandbox afterwards. miosa exits with
the command's status.

  miosa run --rm -- python -c 'print(6 * 7)'
  miosa run --rm --size small --set TOKEN=abc --cwd /home/user -- ./build.sh`, []string{"runs"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List runs, newest first",
			Method: "GET", Path: "/runs", Cols: runCols,
			Flags: []Flag{
				{Name: "status", Usage: "Only runs in this status: queued, running, succeeded, failed or canceled"},
				{Name: "harness", Usage: "Only runs of this harness: claude-code, codex, osa or custom"},
				{Name: "runner", Usage: "Filter by runner name", Hidden: true},
				{Name: "chat", Usage: "Only runs of this chat", Field: "chat_id"},
				{Name: "agent-id", Usage: "Only runs of this agent (id)", Field: "agent_definition_id"},
				{Name: "target", Usage: "Only runs on this machine (id)", Field: "target_id"},
				{Name: "source", Usage: "Only runs from this source: console, api, workflow, trigger, test or a client label"},
				{Name: "workspace-id", Usage: "Only runs in this workspace", Field: "workspace_id"},
				{Name: "limit", Short: "n", Type: "int", Usage: "How many runs", Default: "20"},
			},
		},
		Op{
			Use: "get <run>", Aliases: []string{"show"}, Short: "Show one run",
			Method: "GET", Path: "/runs/{0}", Args: []Arg{{Name: "run"}}, Detail: runDetail,
		},
		Op{
			Use: "stop <run>", Aliases: []string{"cancel"}, Short: "Stop a running run",
			Method: "POST", Path: "/runs/{0}/cancel", Args: []Arg{{Name: "run"}}, Detail: runDetail,
		},
		Op{
			Use: "events <run>", Short: "List a run's events",
			Method: "GET", Path: "/runs/{0}/events", Args: []Arg{{Name: "run"}},
			Cols: []Col{{Head: "TIME", Path: "created_at", Fmt: "age"}, {Head: "TYPE", Path: "type"}, {Head: "STATUS", Path: "status"}, {Head: "MESSAGE", Path: "message", Fmt: "clip"}},
		},
		Op{
			Use: "outputs <run>", Short: "Show a run's result, messages and files",
			Method: "GET", Path: "/runs/{0}/outputs", Args: []Arg{{Name: "run"}},
		},
		Op{
			Use: "messages <run>", Short: "Show the agent's messages",
			Method: "GET", Path: "/runs/{0}/messages", Args: []Arg{{Name: "run"}},
		},
		Op{
			Use: "files <run>", Short: "List files the run produced",
			Method: "GET", Path: "/runs/{0}/files", Args: []Arg{{Name: "run"}},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "PATH", Path: "path"}, {Head: "KIND", Path: "kind"}, {Head: "SIZE", Path: "size_bytes", Fmt: "bytes"}},
		},
		Op{
			Use: "diagnostics <run>", Short: "Show why a run failed",
			Method: "GET", Path: "/runs/{0}/diagnostics", Args: []Arg{{Name: "run"}},
			Cols: []Col{{Head: "CODE", Path: "code"}, {Head: "EXIT", Path: "exit_code"}, {Head: "RETRYABLE", Path: "retryable"}, {Head: "MESSAGE", Path: "message", Fmt: "clip"}},
		},
		Op{
			Use: "usage", Short: "Token usage and estimated cost of runs",
			Method: "GET", Path: "/runs/usage",
			Flags: []Flag{
				{Name: "from", Usage: "Start date, 2026-10-01 or RFC 3339"},
				{Name: "to", Usage: "End date"},
				{Name: "group-by", Usage: "Group results by runner, model or agent", Field: "group_by"},
			},
			Detail: []Col{
				{Head: "From", Path: "from"}, {Head: "To", Path: "to"}, {Head: "Runs", Path: "totals.runs"},
				{Head: "Input tokens", Path: "totals.input_tokens"}, {Head: "Output tokens", Path: "totals.output_tokens"},
				{Head: "Cache read", Path: "totals.cache_read_tokens"}, {Head: "Estimated USD", Path: "totals.estimated_usd"},
				{Head: "Subscription runs", Path: "totals.subscription_runs"},
			},
		},
	)
	cmd.AddCommand(
		&cobra.Command{
			Use: "follow <run>", Aliases: []string{"watch"}, Short: "Stream a run until it finishes",
			Long: "Stream the run's events and print its answer when it ends. Ctrl-C detaches without stopping the run.",
			Args: cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, _, err := buildClient()
				if err != nil {
					return die(err)
				}
				return followRun(cmd, c.API, args[0])
			},
		},
		newRunLogsCmd(),
		newRunDownloadCmd(),
	)
	cmd.AddCommand(newRunSteerCmd(), newRunInterruptCmd())
	attachOneShot(cmd)
	return cmd
}

func newRunLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <run>",
		Short: "Print the command output of a run (stdout, then stderr)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: "/runs/" + url.PathEscape(args[0]) + "/command-output"})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			if s, _ := obj["stdout"].(string); s != "" {
				fmt.Fprint(cmd.OutOrStdout(), strings.TrimRight(s, "\n")+"\n")
			}
			if s, _ := obj["stderr"].(string); s != "" {
				fmt.Fprint(cmd.ErrOrStderr(), strings.TrimRight(s, "\n")+"\n")
			}
			if code, ok := toFloat(obj["exit_code"]); ok && code != 0 {
				return &ExitError{Code: int(code)}
			}
			return nil
		},
	}
}

func newRunDownloadCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "download <run> <file-id>",
		Short: "Download a file a run produced",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.Do(cmd.Context(), api.Request{Method: "GET", Path: "/runs/" + url.PathEscape(args[0]) + "/files/" + url.PathEscape(args[1]) + "/download"})
			if err != nil {
				return die(err)
			}
			defer resp.Body.Close()
			var w io.Writer = cmd.OutOrStdout()
			dest := "-"
			if out != "" && out != "-" {
				f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
				if err != nil {
					return die(err)
				}
				defer f.Close()
				w, dest = f, filepath.Clean(out)
			}
			n, err := io.Copy(w, resp.Body)
			if err != nil {
				return die(err)
			}
			if dest != "-" && !isJSON() {
				p.Success("Saved %s (%s)", dest, formatBytes(n))
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output-file", "O", "", "Write to this file (default: standard output)")
	return cmd
}

// ─── chat ─────────────────────────────────────────────────────────────────────

func chatCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chat",
		Short: "Chats with an agent that remembers",
		Long: `A chat is a series of runs that share a chat id: each follow-up resumes the
harness's own session on the same machine, so the agent keeps its context.

  miosa chat start --sandbox my-box      # interactive; prints the chat id
  miosa chat list
  miosa chat show 3f0c...
  miosa prompt --chat 3f0c... "and add tests"`,
	}
	cmd.AddCommand(newChatListCmd(), newChatShowCmd(), newChatStartCmd(),
		Op{
			Use: "context <chat-id>", Short: "Show how full the agent's context is",
			Long: `Shows how many tokens the chat has used of the harness window, how many
compactions it has had, and whether it can be compacted now. When it cannot, the
reason is one of: run_in_progress, no_session, harness_unsupported.`,
			Method: "GET", Path: "/agents/chats/{0}/context", Args: []Arg{{Name: "chat-id"}},
			Detail: []Col{
				{Head: "Chat", Path: "chat_id"}, {Head: "Harness", Path: "harness"}, {Head: "Runs", Path: "runs"},
				{Head: "Used tokens", Path: "context.used_tokens"}, {Head: "Window", Path: "context.window_tokens"},
				{Head: "Remaining", Path: "context.remaining_tokens"}, {Head: "Used", Path: "context.used_ratio"},
				{Head: "Compactions", Path: "compactions", Fmt: "count"},
				{Head: "Can compact", Path: "compactable"}, {Head: "Why not", Path: "compactable_reason"},
				{Head: "Machine", Path: "machine.id"}, {Head: "Latest run", Path: "latest_run.id"},
			},
		}.command("chat"),
		Op{
			Use: "compact <chat-id>", Short: "Compact the agent's context so the chat can go on",
			Long: `Starts a run in the same chat, on the same machine, that compacts the harness's
context. It needs the latest run to be finished and the harness to support
compaction (Claude Code does). Follow it with 'miosa run follow <run-id>'.`,
			Method: "POST", Path: "/agents/chats/{0}/compact", Args: []Arg{{Name: "chat-id"}},
			Flags: []Flag{{Name: "focus", Usage: "What the summary should keep"}},
			Detail: []Col{
				{Head: "Run", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Chat", Path: "chat_id"}, {Head: "Harness", Path: "runner"},
			},
		}.command("chat"),
	)
	return cmd
}

type chatSummary struct {
	ID      string `json:"chat_id"`
	Runs    int    `json:"runs"`
	Runner  string `json:"harness"`
	Status  string `json:"last_status"`
	Last    string `json:"last_run_at"`
	First   string `json:"first_instruction"`
	Target  string `json:"target_id"`
	LastRun string `json:"last_run_id"`
}

func newChatListCmd() *cobra.Command {
	var limit int
	var sandbox string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List recent chats",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			q := url.Values{}
			q.Set("limit", fmt.Sprint(limit))
			if sandbox != "" {
				id, err := c.API.ResolveSandbox(cmd.Context(), sandbox)
				if err != nil {
					return die(err)
				}
				q.Set("target_id", id)
			}
			var out struct {
				Data []struct {
					ID          string `json:"id"`
					ChatID      string `json:"chat_id"`
					Runner      string `json:"runner"`
					Status      string `json:"status"`
					CreatedAt   string `json:"created_at"`
					Instruction string `json:"instruction"`
					TargetID    string `json:"target_id"`
				} `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/runs", q, &out); err != nil {
				return die(err)
			}
			byChat := map[string]*chatSummary{}
			var order []string
			for _, r := range out.Data { // newest first
				if r.ChatID == "" {
					continue
				}
				cs, ok := byChat[r.ChatID]
				if !ok {
					cs = &chatSummary{ID: r.ChatID, Runner: r.Runner, Status: r.Status, Last: r.CreatedAt, Target: r.TargetID, LastRun: r.ID}
					byChat[r.ChatID] = cs
					order = append(order, r.ChatID)
				}
				cs.Runs++
				cs.First = r.Instruction // ends as the oldest run's instruction
			}
			list := make([]chatSummary, 0, len(order))
			for _, id := range order {
				list = append(list, *byChat[id])
			}
			if isJSON() {
				return p.JSON(map[string]any{"data": list})
			}
			if len(list) == 0 {
				p.Line("No chats in the last %d runs. Start one with: miosa chat start", limit)
				return nil
			}
			rows := make([][]string, 0, len(list))
			for _, cs := range list {
				rows = append(rows, []string{cs.ID, fmt.Sprint(cs.Runs), cs.Runner, cs.Status, formatAge(cs.Last), clipText(cs.First, 60)})
			}
			p.Table([]string{"CHAT", "RUNS", "HARNESS", "STATUS", "LAST", "FIRST PROMPT"}, rows)
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 100, "How many recent runs to group, 1-100")
	cmd.Flags().StringVarP(&sandbox, "sandbox", "s", "", "Only chats on this sandbox")
	return cmd
}

func clipText(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func newChatShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <chat-id>",
		Short: "Print a chat, oldest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			q := url.Values{}
			q.Set("chat_id", args[0])
			q.Set("limit", "200")
			var out struct {
				Data []map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/runs", q, &out); err != nil {
				return die(err)
			}
			runs := out.Data
			sort.SliceStable(runs, func(i, j int) bool {
				a, _ := runs[i]["created_at"].(string)
				b, _ := runs[j]["created_at"].(string)
				return a < b
			})
			type turn struct {
				Run         map[string]any `json:"run"`
				Instruction string         `json:"instruction"`
				Answer      string         `json:"answer,omitempty"`
			}
			turns := make([]turn, 0, len(runs))
			for _, r := range runs {
				t := turn{Run: r}
				t.Instruction, _ = r["instruction"].(string)
				if id, _ := r["id"].(string); id != "" {
					if resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: "/runs/" + id + "/outputs"}); err == nil {
						obj, _ := unwrapObject(resp).(map[string]any)
						t.Answer = answerText(obj)
					}
				}
				turns = append(turns, t)
			}
			if isJSON() {
				return p.JSON(map[string]any{"chat_id": args[0], "turns": turns})
			}
			if len(turns) == 0 {
				p.Line("No runs with chat id %s.", args[0])
				return nil
			}
			for _, t := range turns {
				fmt.Fprintf(p.Writer(), "> %s\n", t.Instruction)
				if t.Answer != "" {
					fmt.Fprintf(p.Writer(), "%s\n", t.Answer)
				} else {
					fmt.Fprintf(p.Writer(), "(%s)\n", formatValue(t.Run["status"], ""))
				}
				fmt.Fprintln(p.Writer())
			}
			return nil
		},
	}
}

func newChatStartCmd() *cobra.Command {
	var o promptOptions
	cmd := &cobra.Command{
		Use:     "start [chat-id]",
		Aliases: []string{"resume"},
		Short:   "Chat with an agent interactively",
		Long: `Read prompts line by line and run each as a follow-up in one chat. The
agent keeps its memory between lines. Give a chat id to resume an earlier chat.
End with /exit or Ctrl-D.

  miosa chat start --sandbox my-box
  miosa chat resume 3f0c...`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				o.chat = args[0]
			} else if o.chat == "" {
				o.newChat = true
			}
			return runChat(cmd, o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.harness, "harness", "", "Harness: claude-code, codex or osa")
	f.StringVar(&o.model, "model", "", "Model")
	f.StringVar(&o.agent, "agent", "", "Saved agent (name or id)")
	f.StringVarP(&o.sandbox, "sandbox", "s", "", "Sandbox (default: the current sandbox)")
	f.StringVar(&o.computer, "computer", "", "Computer")
	f.StringVar(&o.chat, "chat", "", "Chat id")
	return cmd
}

func runChat(cmd *cobra.Command, o promptOptions) error {
	if o.newChat {
		o.chat = newUUID()
		o.newChat = false
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "chat %s - type a prompt, /exit to leave\n", o.chat)
	sc := bufio.NewScanner(cmd.InOrStdin())
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for {
		fmt.Fprint(cmd.ErrOrStderr(), "> ")
		if !sc.Scan() {
			return nil
		}
		line := strings.TrimSpace(sc.Text())
		switch line {
		case "":
			continue
		case "/exit", "/quit", "exit":
			return nil
		}
		if err := runPrompt(cmd, []string{line}, o); err != nil {
			if ee, ok := err.(*ExitError); ok && ee.Code == 130 {
				return err
			}
			// A failed turn does not end the conversation.
			writeError(cmd.ErrOrStderr(), err, false)
		}
	}
}
