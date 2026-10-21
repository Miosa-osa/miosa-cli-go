package commands

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

// connShareOpts are the flags of 'connections share' and 'connections unshare'.
type connShareOpts struct {
	Workspaces []string
	All        bool
	From       string
}

func newConnShareCmd() *cobra.Command   { return newConnShareLike(true) }
func newConnUnshareCmd() *cobra.Command { return newConnShareLike(false) }

func newConnShareLike(share bool) *cobra.Command {
	use, short := "share <connection>", "Share a model connection with other workspaces"
	long := `Share a model connection (an API key or subscription) with other workspaces.

A share is a grant, never a copy: the secret stays in the workspace that holds
it and a run in a shared workspace uses it when it runs. Revoke it with
'miosa connections unshare'; it takes effect on the next run.

<connection> is the connection id from 'miosa connections list --json' (for
example openrouter, or agent_credential:openrouter). The connection must be held
by a workspace itself; one held by the organization already reaches every
workspace. The holding workspace is found for you, or name it with --from.

  miosa connections share openrouter --workspace research,ops
  miosa connections share claude_subscription --all-workspaces
  miosa connections share openrouter --workspace ops --from models

--all-workspaces skips workspaces flagged private unless you name them with
--workspace.`
	if !share {
		use, short = "unshare <connection>", "Stop sharing a model connection with workspaces"
		long = `Revoke a share made with 'miosa connections share'. Runs in the workspace use
their own connection, or none, from their next run.

  miosa connections unshare openrouter --workspace research
  miosa connections unshare openrouter --all-workspaces`
	}
	var o connShareOpts
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConnShare(cmd, args[0], o, share)
		},
	}
	cmd.Flags().StringSliceVar(&o.Workspaces, "workspace", nil, "Workspace(s) to "+map[bool]string{true: "share with", false: "stop sharing with"}[share]+" (slug or id, comma separated)")
	cmd.Flags().BoolVar(&o.All, "all-workspaces", false, "Every workspace of the organization except private ones")
	cmd.Flags().StringVar(&o.From, "from", "", "The workspace that holds the connection (default: the one that does)")
	return cmd
}

func runConnShare(cmd *cobra.Command, connection string, o connShareOpts, share bool) error {
	credential := strings.TrimPrefix(strings.TrimSpace(connection), "agent_credential:")
	if credential == "" {
		return usagef("name the connection to share (see 'miosa connections list')")
	}
	if o.All == (len(o.Workspaces) > 0) {
		return usagef("name the workspaces with --workspace a,b, or use --all-workspaces (not both)")
	}
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	source, err := shareSource(ctx, c, credential, o.From)
	if err != nil {
		return die(err)
	}
	ids := make([]string, 0, len(o.Workspaces))
	for _, w := range o.Workspaces {
		id, err := resolveWorkspaceID(ctx, c, strings.TrimSpace(w))
		if err != nil {
			return die(err)
		}
		ids = append(ids, id)
	}
	path := "/agents/credentials/" + url.PathEscape(credential) + "/shares"
	req := api.Request{Method: "POST", Path: path}
	if share {
		body := map[string]any{"workspace_id": source}
		if o.All {
			body["all_workspaces"] = true
		} else {
			body["workspaces"] = ids
		}
		req.Body = body
	} else {
		q := url.Values{"workspace_id": {source}}
		if o.All {
			q.Set("all_workspaces", "true")
		} else {
			q.Set("workspaces", strings.Join(ids, ","))
		}
		req.Method, req.Query = "DELETE", q
	}
	resp, err := c.API.JSONAny(ctx, req)
	if err != nil {
		return die(sessionHint(err))
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(resp)
	}
	where := "all workspaces"
	if !o.All {
		where = fmt.Sprintf("%d workspace(s)", len(ids))
	}
	if share {
		p.Success("Shared %s with %s. Runs there use it from their next run.", credential, where)
	} else {
		p.Success("Stopped sharing %s with %s. It takes effect on the next run.", credential, where)
	}
	return nil
}

// shareSource is the workspace that holds the connection itself: --from, else
// the one workspace whose own view lists it as connected and not inherited.
func shareSource(ctx context.Context, c *client.Client, credential, from string) (string, error) {
	if from != "" {
		return resolveWorkspaceID(ctx, c, from)
	}
	list, err := c.Workspaces.List(ctx)
	if err != nil {
		return "", fmt.Errorf("finding the workspace that holds %q: %w", credential, err)
	}
	var holders []string
	for _, w := range list {
		resp, err := c.API.JSONAny(ctx, api.Request{Method: "GET", Path: "/agents/credentials", Query: url.Values{"workspace_id": {w.ID}}})
		if err != nil {
			continue
		}
		if holdsCredential(resp, credential) {
			holders = append(holders, w.ID)
		}
	}
	switch len(holders) {
	case 0:
		return "", errors.New("no workspace holds " + credential + " itself, so there is nothing to share (one connected for the whole organization already reaches every workspace). Connect it in a workspace with 'miosa connections add models ... --workspace-id <workspace>'")
	case 1:
		return holders[0], nil
	}
	return "", fmt.Errorf("%d workspaces hold %s; say which with --from <workspace> (%s)", len(holders), credential, strings.Join(holders, ", "))
}

// holdsCredential reports whether a GET /agents/credentials answer lists the
// credential as connected at the workspace itself (not inherited).
func holdsCredential(resp any, credential string) bool {
	m, ok := resp.(map[string]any)
	if !ok {
		return false
	}
	items, _ := m["data"].([]any)
	for _, it := range items {
		row, _ := it.(map[string]any)
		if row["id"] != credential {
			continue
		}
		connected, _ := row["connected"].(bool)
		inherited, _ := row["inherited"].(bool)
		return connected && !inherited && row["scope"] == "workspace"
	}
	return false
}
