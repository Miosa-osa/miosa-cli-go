package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// optimalRun is one turn of a chat, as Optimal records it (GET /runs).
type optimalRun struct {
	ID               string `json:"id"`
	ChatID           string `json:"chat_id"`
	Scope            string `json:"scope"`
	WorkspaceID      string `json:"workspace_id"`
	State            string `json:"state"`
	AwaitingApproval int    `json:"awaiting_approval"`
	CancelRequested  bool   `json:"cancel_requested"`
	Error            string `json:"error,omitempty"`
	Credits          *int64 `json:"credits"`
	StartedAt        string `json:"started_at"`
	FinishedAt       string `json:"finished_at,omitempty"`
}

func (r optimalRun) label() string {
	if r.State == "running" && r.AwaitingApproval > 0 {
		return "waiting for approval"
	}
	return r.State
}

func newOptimalRunsCmd() *cobra.Command {
	var workspace, state string
	var orgWide, all bool
	var limit int
	cmd := &cobra.Command{
		Use:   "runs",
		Short: "List Optimal's runs, newest first (running ones by default)",
		Long: `A run is one turn of a chat: what Optimal did for one message, and how it ended
(running, succeeded, failed or canceled). By default only
running runs are listed; --all lists every recent run, --state one state.

  miosa optimal runs --org-wide
  miosa optimal runs --workspace staging --all --json
  miosa optimal run <run>
  miosa optimal cancel <run>`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			oc, scope, err := optimalClient(cmd, workspace, orgWide)
			if err != nil {
				return err
			}
			q := url.Values{"limit": {fmt.Sprint(limit)}}
			switch {
			case state != "":
				q.Set("state", state)
			case !all:
				q.Set("state", "running")
			}
			var page struct {
				Runs []optimalRun `json:"runs"`
			}
			if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/runs", Query: q, Verbatim: true}, &page); err != nil {
				return die(err)
			}
			if isJSON() {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"data": page.Runs, "scope": scope.label(), "workspace_id": scope.workspaceID})
			}
			if len(page.Runs) == 0 {
				if all || state != "" {
					fmt.Fprintln(cmd.OutOrStdout(), "No runs.")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "Optimal is not working on anything right now.")
				}
				return nil
			}
			table := make([][]string, 0, len(page.Runs))
			for _, r := range page.Runs {
				table = append(table, []string{r.ID, r.label(), r.ChatID, firstNonEmpty(r.WorkspaceID, "-"), r.StartedAt, optimalCredits(r.Credits)})
			}
			printerFor(cmd).Table([]string{"ID", "STATE", "CHAT", "WORKSPACE", "STARTED", "CREDITS"}, table)
			return nil
		},
	}
	addOptimalScopeFlags(cmd, &workspace, &orgWide)
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "How many runs")
	cmd.Flags().BoolVar(&all, "all", false, "Every recent run, not only running ones")
	cmd.Flags().StringVar(&state, "state", "", "Only runs in this state: running, succeeded, failed, canceled")
	return cmd
}

func optimalCredits(c *int64) string {
	if c == nil {
		return "-"
	}
	return fmt.Sprint(*c)
}

func getOptimalRun(cmd *cobra.Command, oc *api.Client, id string) (optimalRun, error) {
	var run optimalRun
	err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/runs/" + url.PathEscape(id), Verbatim: true}, &run)
	return run, err
}

func printOptimalRun(cmd *cobra.Command, run optimalRun) error {
	if isJSON() {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(run)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Run %s: %s\n", run.ID, run.label())
	fmt.Fprintf(out, "  chat       %s (%s)\n", run.ChatID, firstNonEmpty(run.Scope, "workspace"))
	fmt.Fprintf(out, "  workspace  %s\n", firstNonEmpty(run.WorkspaceID, "-"))
	fmt.Fprintf(out, "  started    %s\n", firstNonEmpty(run.StartedAt, "-"))
	fmt.Fprintf(out, "  finished   %s\n", firstNonEmpty(run.FinishedAt, "-"))
	fmt.Fprintf(out, "  credits    %s\n", optimalCredits(run.Credits))
	if run.Error != "" {
		fmt.Fprintf(out, "  error      %s\n", run.Error)
	}
	if run.AwaitingApproval > 0 {
		fmt.Fprintf(out, "Waiting on %d approval(s): miosa optimal approvals --chat %s\n", run.AwaitingApproval, run.ChatID)
	}
	return nil
}

func newOptimalRunCmd() *cobra.Command {
	var workspace string
	var orgWide bool
	cmd := &cobra.Command{
		Use:   "run <run>",
		Short: "Show one run: its state, chat, workspace and credits",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			oc, _, err := optimalClient(cmd, workspace, orgWide)
			if err != nil {
				return err
			}
			run, err := getOptimalRun(cmd, oc, args[0])
			if err != nil {
				return die(err)
			}
			return printOptimalRun(cmd, run)
		},
	}
	addOptimalScopeFlags(cmd, &workspace, &orgWide)
	return cmd
}

func newOptimalCancelCmd() *cobra.Command {
	var workspace string
	var orgWide bool
	cmd := &cobra.Command{
		Use:   "cancel <run|chat>",
		Short: "Stop a run (or the running turn of a chat)",
		Long: `Stops a run. Pass a run id (run_...) from 'miosa optimal runs', or a chat id to stop
whatever that chat is running. A run waiting on an approval stops too: its pending
approvals are declined.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			oc, scope, err := optimalClient(cmd, workspace, orgWide)
			if err != nil {
				return err
			}
			id := args[0]
			if !strings.HasPrefix(id, "run_") {
				if id, err = optimalRunOfChat(cmd, oc, id); err != nil {
					return err
				}
			}
			var out struct {
				Run optimalRun `json:"run"`
			}
			if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodPost, Path: "/runs/" + url.PathEscape(id) + "/cancel", Body: map[string]any{}, Verbatim: true, NoRetry: true}, &out); err != nil {
				return die(err)
			}
			// The turn ends at its next step; read the run back for a few seconds.
			run := out.Run
			for attempt := 0; attempt < 5 && run.State == "running"; attempt++ {
				time.Sleep(time.Second)
				if again, err := getOptimalRun(cmd, oc, id); err == nil {
					run = again
				}
			}
			if run.ID == "" {
				run.ID = id
			}
			if isJSON() {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"id": run.ID, "chat_id": run.ChatID, "state": run.label(), "scope": scope.label()})
			}
			if run.State == "running" {
				fmt.Fprintf(cmd.OutOrStdout(), "Run %s: cancel requested\n", run.ID)
				fmt.Fprintln(cmd.ErrOrStderr(), "Optimal has not stopped yet; it stops at its next step. Check with: miosa optimal run "+run.ID)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Run %s: %s\n", run.ID, run.label())
			return nil
		},
	}
	addOptimalScopeFlags(cmd, &workspace, &orgWide)
	return cmd
}

// optimalRunOfChat is the running run of a chat, for `cancel <chat>`.
func optimalRunOfChat(cmd *cobra.Command, oc *api.Client, chatID string) (string, error) {
	var page struct {
		Runs []optimalRun `json:"runs"`
	}
	q := url.Values{"state": {"running"}, "limit": {"200"}}
	if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/runs", Query: q, Verbatim: true}, &page); err != nil {
		return "", die(err)
	}
	for _, r := range page.Runs {
		if r.ChatID == chatID {
			return r.ID, nil
		}
	}
	return "", usagef("chat %s is not running anything", chatID)
}

func newOptimalApprovalsCmd() *cobra.Command {
	var workspace, chat string
	var orgWide bool
	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "List the approvals Optimal is waiting on",
		Long: `Every change Optimal makes waits for an approval in the workspace it changes.
In a workspace (--workspace) this lists that workspace's pending approvals. Org-wide
(--org-wide) it lists the approvals of your running org-wide chats, each in its own
workspace; --chat narrows to one chat.

  miosa optimal approvals --workspace staging
  miosa optimal approve <approval> --workspace staging`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			oc, scope, err := optimalClient(cmd, workspace, orgWide)
			if err != nil {
				return err
			}
			var list []optimalApproval
			switch {
			case chat != "":
				list = optimalPendingApprovals(cmd.Context(), oc, chat)
			case scope.orgWide:
				// The org scope holds no approvals itself: each lives in its chat's workspace.
				var page struct {
					Runs []optimalRun `json:"runs"`
				}
				q := url.Values{"state": {"running"}, "limit": {"200"}}
				if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/runs", Query: q, Verbatim: true}, &page); err != nil {
					return die(err)
				}
				for _, r := range page.Runs {
					if r.AwaitingApproval > 0 {
						list = append(list, optimalPendingApprovals(cmd.Context(), oc, r.ChatID)...)
					}
				}
			default:
				var page struct {
					Approvals []optimalApproval `json:"approvals"`
				}
				if err := oc.JSON(cmd.Context(), api.Request{Method: http.MethodGet, Path: "/approvals", Query: url.Values{"status": {"pending"}}, Verbatim: true}, &page); err != nil {
					return die(err)
				}
				list = page.Approvals
			}
			if isJSON() {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"data": list, "scope": scope.label(), "workspace_id": scope.workspaceID})
			}
			if len(list) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing is waiting for an approval.")
				return nil
			}
			table := make([][]string, 0, len(list))
			for _, a := range list {
				table = append(table, []string{a.ID, firstNonEmpty(a.WorkspaceName, a.WorkspaceID, "-"), a.SessionID, clipText(firstNonEmpty(a.Title, a.Description), 60)})
			}
			printerFor(cmd).Table([]string{"ID", "WORKSPACE", "CHAT", "CHANGE"}, table)
			return nil
		},
	}
	addOptimalScopeFlags(cmd, &workspace, &orgWide)
	cmd.Flags().StringVar(&chat, "chat", "", "Only this chat's approvals")
	return cmd
}

// newOptimalDecideCmd is `approve` or `reject`. The decision is made in the approval's own
// workspace (--workspace), or through its org-wide chat (--org-wide --chat).
func newOptimalDecideCmd(verb, route, done string) *cobra.Command {
	var workspace, chat, reason string
	var orgWide bool
	cmd := &cobra.Command{
		Use:   verb + " <approval>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a change Optimal is waiting on",
		Long: `Decides one approval from 'miosa optimal approvals'. Use the workspace the change is
in (--workspace), or --org-wide --chat <chat> for an org-wide chat's approval.
The API key needs the osa:approve scope; a key without it can chat but not decide.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if orgWide && chat == "" {
				return usagef("an org-wide approval is decided through its chat: add --chat <chat>")
			}
			oc, _, err := optimalClient(cmd, workspace, orgWide)
			if err != nil {
				return err
			}
			body := map[string]any{}
			if reason != "" {
				body["reason"] = reason
			}
			path := "/approvals/" + url.PathEscape(args[0]) + "/" + route
			req := api.Request{Method: http.MethodPost, Path: path, Body: body, Verbatim: true, NoRetry: true}
			if chat != "" {
				req.Query = url.Values{"session_id": {chat}}
			}
			var out optimalApproval
			if err := oc.JSON(cmd.Context(), req, &out); err != nil {
				if api.IsStatus(err, http.StatusForbidden) && strings.Contains(err.Error(), "approval_scope_required") {
					return die(fmt.Errorf("this API key cannot approve or reject changes; mint a key with the osa:approve scope"))
				}
				return die(err)
			}
			if isJSON() {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Approval %s: %s\n", firstNonEmpty(out.ID, args[0]), firstNonEmpty(out.Status, done))
			return nil
		},
	}
	addOptimalScopeFlags(cmd, &workspace, &orgWide)
	cmd.Flags().StringVar(&chat, "chat", "", "The org-wide chat the approval belongs to")
	cmd.Flags().StringVar(&reason, "reason", "", "Why (kept with the decision)")
	return cmd
}
