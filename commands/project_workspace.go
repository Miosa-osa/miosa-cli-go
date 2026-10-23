package commands

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
	"github.com/spf13/cobra"
)

// settleProjectWorkspace fills --workspace-id for 'project create', which the
// API refuses without one (WORKSPACE_REQUIRED). A given slug or name becomes
// its id. Without the flag it picks what the console would: the profile's
// default workspace, else the organization's default, else its only one.
// settleProjectWorkspace fills the workspace a new project goes into.
//
// The API requires it: a create without `workspace_id` answers 400
// WORKSPACE_REQUIRED, so this runs before the request and never guesses. The
// order is the one the console uses: the profile's default ('miosa workspace
// use'), then the workspace the organization marks default, then a workspace
// whose slug is "default", then the organization's only workspace.
func settleProjectWorkspace(cmd *cobra.Command, c *api.Client) error {
	if cmd.Flags().Lookup("workspace") == nil {
		return nil
	}

	// `--workspace` carries the body field; `--workspace-id` is the older
	// spelling and still works, so a script that used it keeps working.
	ref := strings.TrimSpace(cmd.Flags().Lookup("workspace").Value.String())
	if legacy := cmd.Flags().Lookup("workspace-id"); legacy != nil {
		if v := strings.TrimSpace(legacy.Value.String()); v != "" {
			ref = v
		}
	}
	if ref != "" {
		// Resolve against the list, not through ResolveWorkspace: that helper
		// hands an unknown ref back unchanged so the server can answer for it,
		// and a project create would then send a slug where the API wants an id.
		id, err := resolveWorkspaceRef(cmd.Context(), c, ref)
		if err != nil {
			return err
		}
		return cmd.Flags().Set("workspace", id)
	}

	list, err := listWorkspaces(cmd, c)
	if err != nil {
		return die(fmt.Errorf(
			"creating a project needs a workspace and the list could not be read: %w; pass --workspace <slug>",
			err))
	}

	pick := func(match func(w workspaceRow) bool) (workspaceRow, bool) {
		for _, w := range list {
			if match(w) {
				return w, true
			}
		}
		return workspaceRow{}, false
	}

	cfg, _ := config.Load()
	var chosen workspaceRow
	var ok bool
	if want := strings.TrimSpace(cfg.DefaultWorkspace); want != "" {
		chosen, ok = pick(func(w workspaceRow) bool { return w.Slug == want || w.ID == want || w.Name == want })
	}
	if !ok {
		chosen, ok = pick(func(w workspaceRow) bool { return w.IsDefault })
	}
	if !ok {
		chosen, ok = pick(func(w workspaceRow) bool { return strings.EqualFold(w.Slug, "default") })
	}
	if !ok {
		chosen, ok = pick(func(w workspaceRow) bool { return strings.EqualFold(w.Name, "default") })
	}
	if !ok && len(list) == 1 {
		chosen, ok = list[0], true
	}
	if !ok {
		slugs := make([]string, 0, len(list))
		for _, w := range list {
			slugs = append(slugs, w.Slug)
		}
		sort.Strings(slugs)
		return usagef("this organization has no default workspace; pass --workspace (one of: %s)", strings.Join(slugs, ", "))
	}

	if !isJSON() {
		fmt.Fprintf(cmd.ErrOrStderr(), "Using workspace %s (pass --workspace to choose another).\n", chosen.Slug)
	}
	return cmd.Flags().Set("workspace", chosen.ID)
}

type workspaceRow struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	IsDefault bool   `json:"is_default"`
}

// listWorkspaces reads the organization's workspaces, once, with one retry: a
// create should not fail because a read hiccuped, and the answer is small.
func listWorkspaces(cmd *cobra.Command, c *api.Client) ([]workspaceRow, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		var resp struct {
			Data json.RawMessage `json:"data"`
		}
		if err := c.Get(cmd.Context(), "/workspaces", nil, &resp); err != nil {
			lastErr = err
			continue
		}
		list, err := decodeWorkspaces(resp.Data)
		if err != nil {
			return nil, err
		}
		return list, nil
	}
	return nil, lastErr
}

// decodeWorkspaces accepts both shapes the endpoint has answered with: a bare
// array, and an object carrying the array under "workspaces".
func decodeWorkspaces(raw json.RawMessage) ([]workspaceRow, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var list []workspaceRow
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var wrapped struct {
		Workspaces []workspaceRow `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("the workspace list is not a shape this CLI knows: %w", err)
	}
	return wrapped.Workspaces, nil
}

// settleDBProject is the Before hook of `db create`: --project (a name, slug or
// id) becomes the project's id, and with no --workspace the database goes to
// the project's workspace rather than the default one. The workspace is then
// settled as for any create.
func settleDBProject(cmd *cobra.Command, c *api.Client) error {
	fl := cmd.Flags().Lookup("project")
	if fl != nil && strings.TrimSpace(fl.Value.String()) != "" {
		ref := strings.TrimSpace(fl.Value.String())
		var out struct {
			Data []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				Slug        string `json:"slug"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"data"`
		}
		if err := c.Get(cmd.Context(), "/projects", nil, &out); err != nil {
			return die(err)
		}
		found := false
		for _, p := range out.Data {
			if strings.EqualFold(p.ID, ref) || strings.EqualFold(p.Slug, ref) || strings.EqualFold(p.Name, ref) {
				if err := cmd.Flags().Set("project", p.ID); err != nil {
					return err
				}
				if p.WorkspaceID != "" && cmd.Flags().Lookup("workspace") != nil && !workspaceFlagChanged(cmd) {
					if err := cmd.Flags().Set("workspace", p.WorkspaceID); err != nil {
						return err
					}
				}
				found = true
				break
			}
		}
		if !found {
			return die(&api.Error{Status: 404, Code: "NOT_FOUND", Message: fmt.Sprintf("no project named %q; 'miosa project list' shows them", ref)})
		}
	}
	return settleCreateWorkspace(cmd, c)
}

// workspaceFlagChanged reports whether either workspace flag was given.
func workspaceFlagChanged(cmd *cobra.Command) bool {
	for _, name := range []string{"workspace", "workspace-id"} {
		if fl := cmd.Flags().Lookup(name); fl != nil && fl.Changed {
			return true
		}
	}
	return false
}
