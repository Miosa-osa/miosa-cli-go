package commands

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// Workspace scoping for the hand-written creates and the exec/files banner
// (WO-20 criteria 1, 3 and 5). The declarative creates use settleWorkspace and
// reportWorkspaceLanding in workspace_default.go.

// workspaceFlagUsage is the one help line every scoped create uses.
const workspaceFlagUsage = "Workspace slug, name or id (default: $MIOSA_WORKSPACE, then the profile's default workspace)"

// createWorkspaceID resolves the workspace a create should land in and returns
// its id, or "" when neither the flag nor the default names one (the API's own
// default then applies). It is the non-declarative twin of settleWorkspace.
func createWorkspaceID(cmd *cobra.Command, c *api.Client, flagValue string) (string, error) {
	ref := flagValue
	if ref == "" {
		ref = defaultWorkspaceRef()
	}
	if ref == "" {
		return "", nil
	}
	id, err := resolveWorkspaceRef(cmd.Context(), c, ref)
	if err != nil {
		return "", usagef("workspace %q: %v", ref, err)
	}
	return id, nil
}

// printTargetScope is the exec and files banner: it names the organization and
// the workspace the target machine really lives in, read from the resource
// itself, so a sandbox in another workspace never reads "default". It writes
// to stderr and never fails the command.
func printTargetScope(cmd *cobra.Command, c *api.Client, kind, id string) {
	if isJSON() || id == "" {
		return
	}
	path := "/sandboxes/" + url.PathEscape(id)
	if kind == "computer" {
		path = "/computers/" + url.PathEscape(id)
	}
	var res map[string]any
	if err := c.Get(cmd.Context(), path, nil, &res); err != nil {
		return
	}
	org := activeOrgSlug()
	ws := ""
	if wsID := workspaceIDOf(res); wsID != "" {
		ws = workspaceSlug(cmd, c, wsID)
	}
	if org == "" && ws == "" {
		return
	}
	// This line supersedes the generic "org: ..., workspace: <profile
	// default>" that the transport prints for the same command.
	whereItLandsShown = true
	switch {
	case org != "" && ws != "":
		fmt.Fprintf(cmd.ErrOrStderr(), "miosa: org %s, workspace %s\n", org, ws)
	case ws != "":
		fmt.Fprintf(cmd.ErrOrStderr(), "miosa: workspace %s\n", ws)
	default:
		fmt.Fprintf(cmd.ErrOrStderr(), "miosa: org %s\n", org)
	}
}

// workspaceSlug resolves an id to its slug for the banner and falls back to the
// id: a banner must not fail because the name could not be read.
func workspaceSlug(cmd *cobra.Command, c *api.Client, id string) string {
	var resp struct {
		Data struct {
			Slug string `json:"slug"`
		} `json:"data"`
		Slug string `json:"slug"`
	}
	if err := c.Get(cmd.Context(), "/workspaces/"+url.PathEscape(id), nil, &resp); err != nil {
		return id
	}
	if resp.Data.Slug != "" {
		return resp.Data.Slug
	}
	if resp.Slug != "" {
		return resp.Slug
	}
	return id
}

// activeOrgSlug is the organization this command acts in: --org when given,
// otherwise the profile's active org. Empty when neither names one.
func activeOrgSlug() string {
	if org := effectiveOrgFlag(); org != "" {
		return org
	}
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	return cfg.ActiveOrg
}
