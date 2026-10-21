package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// optimalBaseURL is Optimal's own API. MIOSA_OPTIMAL_URL points it elsewhere (staging, tests).
func optimalBaseURL() string {
	if v := strings.TrimSpace(os.Getenv("MIOSA_OPTIMAL_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://optimal.miosa.ai/api/v1"
}

func init() {
	rootCmd.AddCommand(newOptimalCmd())
}

// optimalScope is where a chat lives: one workspace, or the whole organization ("org-wide").
type optimalScope struct {
	orgWide     bool
	workspaceID string
}

func (s optimalScope) label() string {
	if s.orgWide {
		return "org-wide"
	}
	return "workspace"
}

func newOptimalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "optimal",
		Short: "Chat with Optimal and manage its chats from the terminal",
		Long: `Optimal is MIOSA's operator: it reads your workspaces and acts on them with
your approval. These commands use your API key (osa:write). Optimal gets exactly your own
access for a few minutes at a time, never more.

A chat lives in one workspace (--workspace), or reads every workspace you can
open (--org-wide). An org-wide chat never guesses where to write: it asks which
workspace, and every change it makes becomes an approval in that workspace.

  miosa optimal chat "how many sandboxes are in each workspace?" --org-wide
  miosa optimal chat "create a sandbox named web in staging" --org-wide --json
  miosa optimal chat "and its size?" --workspace staging --chat <id>
  miosa optimal chats --org-wide
  miosa optimal runs --workspace staging
  miosa optimal cancel <run>
  miosa optimal approvals --workspace staging
  miosa optimal approve <approval> --workspace staging

Deciding an approval needs a key with the osa:approve scope.`,
	}
	cmd.AddCommand(
		newOptimalChatCmd(), newOptimalChatsCmd(),
		newOptimalRunsCmd(), newOptimalRunCmd(), newOptimalCancelCmd(),
		newOptimalApprovalsCmd(),
		newOptimalDecideCmd("approve", "approve", "approved"),
		newOptimalDecideCmd("reject", "reject", "rejected"),
	)
	return cmd
}

func addOptimalScopeFlags(cmd *cobra.Command, workspace *string, orgWide *bool) {
	cmd.Flags().StringVarP(workspace, "workspace", "w", "", "The workspace (name or id) the chat lives in")
	cmd.Flags().BoolVar(orgWide, "org-wide", false, "Read every workspace you can open (an organization chat)")
}

// optimalClient resolves the scope and returns a client for Optimal's API, authenticated
// with a credential minted from the API key.
func optimalClient(cmd *cobra.Command, workspace string, orgWide bool) (*api.Client, optimalScope, error) {
	if orgWide && workspace != "" {
		return nil, optimalScope{}, usagef("pass --workspace or --org-wide, not both")
	}
	if !orgWide && workspace == "" {
		return nil, optimalScope{}, usagef("say where the chat lives: --workspace <name|id> or --org-wide")
	}
	scope := optimalScope{orgWide: orgWide}
	if !orgWide {
		c, _, err := buildClient()
		if err != nil {
			return nil, scope, err
		}
		id, err := c.API.ResolveWorkspace(cmd.Context(), workspace)
		if err != nil {
			return nil, scope, die(err)
		}
		scope.workspaceID = id
	}
	oc, err := mintOptimal(cmd, scope)
	return oc, scope, err
}

// mintOptimal exchanges the API key for an Optimal credential of this scope: the organization, or one workspace.
func mintOptimal(cmd *cobra.Command, scope optimalScope) (*api.Client, error) {
	c, _, err := buildClient()
	if err != nil {
		return nil, err
	}
	var out struct {
		Token string `json:"token"`
	}
	if scope.workspaceID == "" {
		err = c.API.Post(cmd.Context(), "/optimal/org-token", map[string]any{}, &out)
	} else {
		err = c.API.Post(cmd.Context(), "/optimal/workspace-token", map[string]any{"workspace_id": scope.workspaceID}, &out)
	}
	if err != nil {
		return nil, die(err)
	}
	if out.Token == "" {
		return nil, die(errors.New("the server did not open Optimal for this key"))
	}
	oc := api.New(optimalBaseURL(), out.Token)
	oc.UserAgent = "miosa-cli/" + cliVersion
	return oc, nil
}

// chatWorkspace is the workspace an org-wide chat was bound to ("" while it has none).
func chatWorkspace(cmd *cobra.Command, oc *api.Client, chatID string) string {
	var info struct {
		Workspace struct {
			ID string `json:"id"`
		} `json:"workspace"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/sessions/" + url.PathEscape(chatID), Verbatim: true}, &info); err != nil {
		return ""
	}
	return firstNonEmpty(info.Workspace.ID, info.WorkspaceID)
}

// optimalEvent is one line of `optimal chat --json`: the stream event, the chat it belongs to and its scope.
type optimalEvent struct {
	Type        string `json:"type"`
	ChatID      string `json:"chat_id"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Role        string `json:"role,omitempty"`
	Content     string `json:"content,omitempty"`
	Data        any    `json:"data,omitempty"`
}

// Events that end a turn, and events that pause it for the person.
var (
	optimalTurnEnd   = map[string]bool{"response_complete": true, "agent_completed": true, "done": true, "agent_error": true, "error": true}
	optimalTurnPause = map[string]bool{"permission_required": true, "needs_workspace": true, "pending_workspace": true}
	optimalNoise     = map[string]bool{"keepalive": true, "ping": true, "streaming_token": true, "thinking_delta": true}
)

func newOptimalChatCmd() *cobra.Command {
	var workspace, chatID, actIn string
	var orgWide bool
	var wait time.Duration
	cmd := &cobra.Command{
		Use:   "chat <message...>",
		Short: "Send Optimal a message and stream its answer",
		Long: `Send a message and stream what Optimal does until it answers, asks you something
(which workspace, or an approval), or fails. Without --chat a new chat starts and
its id is printed; pass it to --chat to continue.

An org-wide chat asks which workspace before it changes anything ("needs_workspace").
Answer with --act-in <workspace> on the same chat; the change then waits for your
approval in that workspace.

With --json each event is one line: {"type", "chat_id", "scope", ...}. The answer
is the line with "type":"message"; "scope" is "org-wide" or "workspace".`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			message := strings.TrimSpace(strings.Join(args, " "))
			if message == "-" {
				data, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return die(err)
				}
				message = strings.TrimSpace(string(data))
			}
			if message == "" {
				return usagef("the message is empty")
			}
			if actIn != "" && !orgWide {
				return usagef("--act-in answers an org-wide chat's workspace question; use it with --org-wide")
			}
			oc, scope, err := optimalClient(cmd, workspace, orgWide)
			if err != nil {
				return err
			}
			if actIn != "" && chatID == "" {
				return usagef("--act-in needs the chat that asked: pass --chat <id>")
			}
			if orgWide && chatID != "" {
				// An org-wide chat that has its workspace works there from now on, with that workspace's credential
				// (the console does the same): its changes and approvals belong to that workspace.
				bound := chatWorkspace(cmd, oc, chatID)
				if actIn != "" && bound == "" {
					if bound, err = bindOptimalWorkspace(cmd, oc, chatID, actIn); err != nil {
						return err
					}
				}
				if bound != "" {
					scope = optimalScope{workspaceID: bound}
					if oc, err = mintOptimal(cmd, scope); err != nil {
						return err
					}
				}
			}
			return runOptimalChat(cmd, oc, scope, chatID, message, wait)
		},
	}
	addOptimalScopeFlags(cmd, &workspace, &orgWide)
	cmd.Flags().StringVar(&chatID, "chat", "", "Continue this chat (its id) instead of starting one")
	cmd.Flags().StringVar(&actIn, "act-in", "", "Org-wide chat: the workspace (name or id) its changes apply to")
	cmd.Flags().DurationVar(&wait, "timeout", 10*time.Minute, "Stop following after this long (the chat keeps working)")
	return cmd
}

// bindOptimalWorkspace answers an org-wide chat's "which workspace?" (POST /sessions/:id/workspace).
func bindOptimalWorkspace(cmd *cobra.Command, oc *api.Client, chatID, workspace string) (string, error) {
	c, _, err := buildClient()
	if err != nil {
		return "", err
	}
	id, err := c.API.ResolveWorkspace(cmd.Context(), workspace)
	if err != nil {
		return "", die(err)
	}
	var out struct {
		Workspace struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"workspace"`
	}
	if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodPost, Path: "/sessions/" + url.PathEscape(chatID) + "/workspace", Body: map[string]any{"workspace_id": id}, Verbatim: true, NoRetry: true}, &out); err != nil {
		return "", die(err)
	}
	if out.Workspace.ID == "" {
		return "", die(errors.New("Optimal did not confirm the workspace"))
	}
	if !isJSON() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Changes in this chat apply to workspace %s\n", firstNonEmpty(out.Workspace.Name, out.Workspace.ID))
	}
	return out.Workspace.ID, nil
}

func runOptimalChat(cmd *cobra.Command, oc *api.Client, scope optimalScope, chatID, message string, wait time.Duration) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	out := cmd.OutOrStdout()
	jsonOut := isJSON()

	if chatID == "" {
		var created struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		}
		if err := oc.JSON(ctx, api.Request{Method: http.MethodPost, Path: "/sessions", Body: map[string]any{"resume": false}, Verbatim: true}, &created); err != nil {
			return die(err)
		}
		chatID = firstNonEmpty(created.ID, created.SessionID)
		if chatID == "" {
			return die(errors.New("Optimal returned no chat id"))
		}
		if !jsonOut {
			fmt.Fprintf(cmd.ErrOrStderr(), "Chat %s (%s)\n", chatID, scope.label())
		}
	}

	emit := func(ev optimalEvent) {
		ev.ChatID, ev.Scope, ev.WorkspaceID = chatID, scope.label(), scope.workspaceID
		// Optimal says on every event which scope the chat is in now (an org-wide chat bound to a workspace
		// mid-turn carries that workspace from then on): that wins over the flags the command started with.
		if s, ws, ok := optimalEventChat(ev.Data); ok {
			ev.Scope, ev.WorkspaceID = s, ws
		}
		line, _ := json.Marshal(ev)
		fmt.Fprintln(out, string(line))
	}

	// A chat parked on an approval does nothing with a new message until it is decided (the message would
	// only queue), so say what it waits for instead of sending.
	if chatID != "" {
		if pending := optimalPendingApprovals(ctx, oc, chatID); len(pending) > 0 {
			for _, a := range pending {
				if jsonOut {
					emit(optimalEvent{Type: "permission_required", Content: a.Title, Data: a})
				} else {
					fmt.Fprintf(out, "Waiting for your approval in workspace %s: %s (%s)\n", firstNonEmpty(a.WorkspaceName, a.WorkspaceID, "-"), firstNonEmpty(a.Description, a.Title), a.ID)
				}
			}
			if !jsonOut {
				fmt.Fprintln(cmd.ErrOrStderr(), "Approve or reject it with 'miosa optimal approve|reject <id>' or in the console; the chat continues after.")
			}
			return nil
		}
	}

	// Open the stream before sending, so nothing the turn does is missed.
	// The key rides in the Authorization header, never in the URL, where proxies log it.
	resp, err := oc.Stream(ctx, api.Request{Method: http.MethodGet, Path: "/sessions/" + url.PathEscape(chatID) + "/stream", Headers: map[string]string{"Accept": "text/event-stream"}, Verbatim: true})
	if err != nil {
		return die(err)
	}
	defer resp.Body.Close()

	var sent struct {
		Status string `json:"status"`
		TurnID string `json:"turn_id"`
	}
	if err := oc.JSON(ctx, api.Request{Method: http.MethodPost, Path: "/sessions/" + url.PathEscape(chatID) + "/message", Body: map[string]any{"message": message}, Verbatim: true, NoRetry: true}, &sent); err != nil {
		return die(err)
	}

	var outcome error
	// Optimal sends the answer twice (the event and its envelope); print it once.
	lastAnswer := ""
	// A new stream first replays the chat's recent events ("connected" says how many): they belong to
	// earlier turns, so an old question or answer is never read as this message's outcome.
	replay := 0
	readErr := api.ReadSSE(ctx, resp.Body, func(e api.Event) error {
		name := strings.ToLower(e.Name)
		var data map[string]any
		_ = json.Unmarshal([]byte(e.Data), &data)
		if name == "connected" {
			if n, ok := data["replayed"].(float64); ok && n > 0 {
				replay = int(n)
			}
			return nil
		}
		if replay > 0 {
			replay--
			return nil
		}
		if name == "" || optimalNoise[name] || name == "replay_gap" {
			return nil
		}
		text := optimalText(data)
		switch {
		case name == "agent_response" || name == "message":
			if text == lastAnswer {
				return nil
			}
			lastAnswer = text
			if jsonOut {
				emit(optimalEvent{Type: "message", Role: "assistant", Content: text, Data: data})
			} else if text != "" {
				fmt.Fprintln(out, text)
			}
		case optimalTurnPause[name]:
			if jsonOut {
				emit(optimalEvent{Type: name, Content: text, Data: data})
			} else {
				fmt.Fprintln(out, optimalPauseLine(name, data))
			}
			return io.EOF
		case optimalTurnEnd[name]:
			if name == "agent_error" || name == "error" {
				outcome = fmt.Errorf("Optimal stopped: %s", firstNonEmpty(text, "an error"))
			}
			if jsonOut {
				emit(optimalEvent{Type: name, Content: text, Data: data})
			}
			return io.EOF
		default:
			if jsonOut {
				emit(optimalEvent{Type: name, Content: text, Data: data})
			} else if strings.Contains(name, "tool") && name != "tool_output" && name != "tool_progress" {
				if tool := optimalString(data, "tool", "name"); tool != "" && (name == "tool_call" || name == "tool_start") {
					fmt.Fprintf(cmd.ErrOrStderr(), "  · %s\n", tool)
				}
			}
		}
		return nil
	})
	if readErr != nil && !errors.Is(readErr, io.EOF) && ctx.Err() == nil {
		return die(readErr)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(cmd.ErrOrStderr(), "Still working after %s; follow it in the console or continue with --chat %s\n", wait, chatID)
	}
	if !jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "Continue: miosa optimal chat --chat %s ...\n", chatID)
	}
	if outcome != nil {
		return die(outcome)
	}
	return nil
}

type optimalApproval struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	SessionID     string `json:"session_id"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceName string `json:"workspace_name"`
	Status        string `json:"status"`
}

// optimalPendingApprovals are the approvals this chat waits on (GET /approvals?status=pending).
func optimalPendingApprovals(ctx context.Context, oc *api.Client, chatID string) []optimalApproval {
	var page struct {
		Approvals []optimalApproval `json:"approvals"`
	}
	q := url.Values{"status": {"pending"}, "session_id": {chatID}}
	if err := oc.JSON(ctx, api.Request{Method: http.MethodGet, Path: "/approvals", Query: q, Verbatim: true}, &page); err != nil {
		return nil
	}
	var mine []optimalApproval
	for _, a := range page.Approvals {
		if a.SessionID == chatID && a.Status == "pending" {
			mine = append(mine, a)
		}
	}
	return mine
}

func optimalPauseLine(name string, data map[string]any) string {
	switch name {
	case "permission_required":
		what := firstNonEmpty(optimalString(data, "reason", "summary", "description", "tool"), "an action")
		where := optimalString(data, "workspace_name", "workspace_id")
		if where != "" {
			return fmt.Sprintf("Waiting for your approval in workspace %s: %s", where, what)
		}
		return "Waiting for your approval: " + what
	default:
		var names []string
		if list, ok := data["candidates"].([]any); ok {
			for _, entry := range list {
				if m, ok := entry.(map[string]any); ok {
					names = append(names, firstNonEmpty(optimalString(m, "name"), optimalString(m, "id")))
				}
			}
		}
		line := "Optimal needs to know which workspace to act in. Nothing was changed."
		if len(names) > 0 {
			line += " Choose one: " + strings.Join(names, ", ") + " (answer with --workspace, or name it in your message)."
		}
		return line
	}
}

// optimalEventChat is the event's own `chat: {scope, workspace_id}`, when it carries one.
func optimalEventChat(data any) (scope, workspaceID string, ok bool) {
	m, _ := data.(map[string]any)
	chat, _ := m["chat"].(map[string]any)
	s, _ := chat["scope"].(string)
	if s != "workspace" && s != "org-wide" {
		return "", "", false
	}
	ws, _ := chat["workspace_id"].(string)
	return s, ws, true
}

// optimalText is the human text an event carries, wherever Optimal put it.
func optimalText(data map[string]any) string {
	if data == nil {
		return ""
	}
	if inner, ok := data["data"].(map[string]any); ok {
		if t := optimalText(inner); t != "" {
			return t
		}
	}
	return optimalString(data, "response", "content", "message", "text", "question")
}

func optimalString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

type optimalChatRow struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	WorkspaceID      string `json:"workspace_id"`
	WorkspaceName    string `json:"workspace_name"`
	Running          bool   `json:"running"`
	AwaitingApproval int    `json:"awaiting_approval"`
	MessageCount     int    `json:"message_count"`
	LastActive       string `json:"last_active"`
}

func (r optimalChatRow) state() string {
	switch {
	case r.AwaitingApproval > 0:
		return "waiting for approval"
	case r.Running:
		return "running"
	default:
		return "idle"
	}
}

func listOptimalChats(cmd *cobra.Command, workspace string, orgWide bool, limit int) ([]optimalChatRow, optimalScope, error) {
	oc, scope, err := optimalClient(cmd, workspace, orgWide)
	if err != nil {
		return nil, scope, err
	}
	var page struct {
		Sessions []optimalChatRow `json:"sessions"`
	}
	q := url.Values{"limit": {fmt.Sprint(limit)}, "per_page": {fmt.Sprint(limit)}}
	if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/sessions", Query: q, Verbatim: true}, &page); err != nil {
		return nil, scope, die(err)
	}
	return page.Sessions, scope, nil
}

func printOptimalChats(cmd *cobra.Command, rows []optimalChatRow, scope optimalScope) error {
	if isJSON() {
		enc := json.NewEncoder(cmd.OutOrStdout())
		return enc.Encode(map[string]any{"data": rows, "scope": scope.label(), "workspace_id": scope.workspaceID})
	}
	if len(rows) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No chats.")
		return nil
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		table = append(table, []string{r.ID, r.state(), firstNonEmpty(r.WorkspaceName, r.WorkspaceID, "-"), fmt.Sprint(r.MessageCount), clipText(r.Title, 60)})
	}
	printerFor(cmd).Table([]string{"ID", "STATE", "WORKSPACE", "MESSAGES", "TITLE"}, table)
	return nil
}

func newOptimalChatsCmd() *cobra.Command {
	var workspace string
	var orgWide bool
	var limit int
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "List Optimal chats, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, scope, err := listOptimalChats(cmd, workspace, orgWide, limit)
			if err != nil {
				return err
			}
			return printOptimalChats(cmd, rows, scope)
		},
	}
	addOptimalScopeFlags(cmd, &workspace, &orgWide)
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "How many chats")
	return cmd
}
