package commands

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// The agent group at the top level, as boat has it: steer, interrupt and
// conversations take the sandbox as their first argument, and events follows
// the agent's chat with --agent or --convo. They are the same API as the
// 'run' and 'chat' commands.

func init() {
	rootCmd.AddCommand(newTopSteerCmd(), newTopInterruptCmd(), newConversationsCmd())
}

// sandboxNamed reports whether ref names a sandbox the key can see, so a
// leading argument can be a sandbox or the first word of a message.
func sandboxNamed(ctx context.Context, c *api.Client, ref string) (string, bool) {
	if ref == "current" || ref == "self" || api.IsUUID(ref) {
		id, err := c.ResolveSandbox(ctx, ref)
		return id, err == nil
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := c.Get(ctx, "/sandboxes/by-name/"+url.PathEscape(ref), nil, &out); err != nil || out.ID == "" {
		return "", false
	}
	return out.ID, true
}

// agentSandbox resolves an optional sandbox argument (default: the current one).
func agentSandbox(cmd *cobra.Command, c *api.Client, ref, current string) (string, error) {
	ref, err := requireSandbox(ref, current)
	if err != nil {
		return "", usagef("%v", err)
	}
	id, err := c.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return "", die(err)
	}
	return id, nil
}

// latestRun is the newest run matching q (a chat, a machine), or nil.
func newestRun(ctx context.Context, c *api.Client, q url.Values) (map[string]any, error) {
	q.Set("limit", "1")
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Get(ctx, "/runs", q, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, nil
	}
	return out.Data[0], nil
}

func newTopSteerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "steer [sandbox] <message>",
		Short: "Send the agent running in a sandbox a new message without stopping it",
		Long: `Adds a message as the next turn of the agent running in a sandbox (default: the
current one): it finishes what it is doing, then continues the same chat with your
message. With no agent running this fails with NO_AGENT_RUN. "-" reads the message
from standard input (up to 64 KiB).

  miosa steer my-box "also add a changelog entry"
  miosa steer "use the existing helper instead"
  git diff | miosa steer my-box -`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			ref := ""
			if len(args) > 1 {
				if _, ok := sandboxNamed(cmd.Context(), c.API, args[0]); ok {
					ref, args = args[0], args[1:]
				}
			}
			msg := strings.Join(args, " ")
			if msg == "-" {
				b, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 65536+1))
				if err != nil {
					return die(err)
				}
				msg = string(b)
			}
			if strings.TrimSpace(msg) == "" {
				return usagef("the message is empty")
			}
			id, err := agentSandbox(cmd, c.API, ref, cfg.CurrentSandbox)
			if err != nil {
				return err
			}
			resp, err := c.API.JSONAny(cmd.Context(), reqPost("/sandboxes/"+id+"/agent/prompts", map[string]string{"prompt": msg}))
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return printerFor(cmd).JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			printerFor(cmd).Success("Queued after run %s (%s)", formatValue(obj["after_run_id"], "short"), formatValue(obj["runner"], ""))
			return nil
		},
	}
	return cmd
}

func newTopInterruptCmd() *cobra.Command {
	var convo string
	cmd := &cobra.Command{
		Use:   "interrupt [sandbox]",
		Short: "Stop the agent running in a sandbox, or one chat's running turn",
		Long: `Stops the agent run in a sandbox (default: the current one). --convo <chat-id>
stops the running turn of one chat instead, wherever it runs.

  miosa interrupt my-box
  miosa interrupt --convo 3f0c...`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if convo != "" {
				run, err := newestRun(cmd.Context(), c.API, url.Values{"chat_id": {convo}})
				if err != nil {
					return die(err)
				}
				st, _ := run["status"].(string)
				if run == nil || terminalStatus(st) {
					return die(&api.Error{Status: 409, Code: "NO_RUNNING_TURN", Message: "chat " + convo + " has no running turn"})
				}
				rid := fmt.Sprint(run["id"])
				resp, err := c.API.JSONAny(cmd.Context(), reqPost("/runs/"+rid+"/cancel", nil))
				if err != nil {
					return die(err)
				}
				if isJSON() {
					return p.JSON(resp)
				}
				p.Success("Stopped run %s of chat %s", shortID(rid), convo)
				return nil
			}
			ref := ""
			if len(args) > 0 {
				ref = args[0]
			}
			id, err := agentSandbox(cmd, c.API, ref, cfg.CurrentSandbox)
			if err != nil {
				return err
			}
			resp, err := c.API.JSONAny(cmd.Context(), reqPost("/sandboxes/"+id+"/agent/stop", nil))
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			p.Success("Stopped run %s (%s)", formatValue(obj["id"], "short"), formatValue(obj["status"], ""))
			return nil
		},
	}
	cmd.Flags().StringVar(&convo, "convo", "", "Stop this chat's running turn (a chat id)")
	cmd.Flags().StringVar(&convo, "conversation", "", "Same as --convo")
	_ = cmd.Flags().MarkHidden("conversation")
	return cmd
}

// conversation is one chat as `conversations` lists it.
type conversation struct {
	ID         string `json:"id"`
	LastAt     string `json:"last_activity"`
	Prompts    int    `json:"prompt_count"`
	Running    bool   `json:"running"`
	Harness    string `json:"harness"`
	Model      string `json:"model"`
	LastPrompt string `json:"last_prompt"`
	Sandbox    string `json:"sandbox_id"`
	LastRun    string `json:"last_run_id"`
}

func newConversationsCmd() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:     "conversations [sandbox]",
		Aliases: []string{"convos"},
		Short:   "List agent chats: last activity, prompts, running, harness/model, last prompt",
		Long: `Lists agent chats, newest first: when each was last active, how many prompts
it has, whether a turn is running, its harness and model, and its last prompt.
Without a sandbox every recent chat is listed. Continue one with
'miosa prompt --chat <id> "..."' and stop its turn with 'miosa interrupt --convo <id>'.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			q := url.Values{"limit": {fmt.Sprint(limit)}}
			if len(args) == 1 {
				id, err := c.API.ResolveSandbox(cmd.Context(), args[0])
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
					Model       string `json:"model"`
					Status      string `json:"status"`
					CreatedAt   string `json:"created_at"`
					Instruction string `json:"instruction"`
					TargetID    string `json:"target_id"`
				} `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/runs", q, &out); err != nil {
				return die(err)
			}
			byID := map[string]*conversation{}
			var order []string
			for _, r := range out.Data { // newest first
				if r.ChatID == "" {
					continue
				}
				cv, ok := byID[r.ChatID]
				if !ok {
					cv = &conversation{ID: r.ChatID, LastAt: r.CreatedAt, Harness: r.Runner, Model: r.Model,
						LastPrompt: r.Instruction, Sandbox: r.TargetID, LastRun: r.ID}
					byID[r.ChatID] = cv
					order = append(order, r.ChatID)
				}
				cv.Prompts++
				cv.Running = cv.Running || !terminalStatus(r.Status)
			}
			list := make([]conversation, 0, len(order))
			for _, id := range order {
				list = append(list, *byID[id])
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(list)
			}
			if len(list) == 0 {
				p.Line("No chats in the last %d runs. Start one with: miosa prompt --new-chat \"...\"", limit)
				return nil
			}
			rows := make([][]string, 0, len(list))
			for _, cv := range list {
				hm := cv.Harness
				if cv.Model != "" {
					hm += "/" + cv.Model
				}
				rows = append(rows, []string{cv.ID, formatAge(cv.LastAt), fmt.Sprint(cv.Prompts), yesNo(cv.Running), hm, clipText(cv.LastPrompt, 50)})
			}
			p.Table([]string{"ID", "LAST ACTIVITY", "PROMPTS", "RUNNING", "HARNESS/MODEL", "LAST PROMPT"}, rows)
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 100, "How many recent runs to group, 1-100")
	return cmd
}

// agentEvents prints the agent's events for `events --agent/--convo`: the
// newest run of the conversation, or of the sandbox; with follow, until it ends.
func agentEvents(cmd *cobra.Command, c *api.Client, sandboxID, convo string, follow bool) error {
	q := url.Values{}
	if convo != "" {
		q.Set("chat_id", convo)
	} else {
		q.Set("target_id", sandboxID)
	}
	run, err := newestRun(cmd.Context(), c, q)
	if err != nil {
		return die(err)
	}
	if run == nil {
		return die(&api.Error{Status: 404, Code: "NO_AGENT_RUN", Message: "no agent run yet; start one with 'miosa prompt'"})
	}
	id := fmt.Sprint(run["id"])
	if follow {
		return followRun(cmd, c, id)
	}
	var ev struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/runs/"+id+"/events", nil, &ev); err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	for _, e := range ev.Data {
		if isJSON() {
			_ = p.JSONLine(map[string]any{"event": e["type"], "data": e})
			continue
		}
		msg, _ := e["message"].(string)
		typ, _ := e["type"].(string)
		if msg != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", typ, msg)
		}
	}
	return nil
}
