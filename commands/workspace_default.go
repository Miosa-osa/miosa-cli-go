package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// builtinDefaultWorkspace is what config.Load reports for a profile that never
// chose a workspace. It is the platform's own default, not a user choice, so a
// create leaves the field out and the API applies its default.
const builtinDefaultWorkspace = "default"

// defaultWorkspaceRef is the workspace a create lands in when the command does
// not name one: $MIOSA_WORKSPACE, then the selected profile's
// default_workspace. It returns "" when neither names a real choice.
func defaultWorkspaceRef() string {
	if v := strings.TrimSpace(os.Getenv("MIOSA_WORKSPACE")); v != "" {
		return v
	}
	cfg, err := config.Load()
	if err != nil {
		return ""
	}
	ws := strings.TrimSpace(cfg.DefaultWorkspace)
	if ws == "" || ws == builtinDefaultWorkspace {
		return ""
	}
	return ws
}

// settleBucketWorkspace is the Before hook of 'storage create': an explicit
// --workspace-id wins, otherwise --workspace or the shell/profile default.
func settleBucketWorkspace(cmd *cobra.Command, c *api.Client) error {
	if fl := cmd.Flags().Lookup("workspace-id"); fl != nil && fl.Changed {
		return settleWorkspace("workspace-id", false)(cmd, c)
	}
	return settleWorkspace("workspace", true)(cmd, c)
}

// settleWorkspace is a Before hook for a create command whose --<flag> takes a
// workspace slug, name or id: it resolves the reference to an id and writes it
// back, so the request carries workspace_id whichever form was typed. With
// useDefault, an unset flag falls back to $MIOSA_WORKSPACE and the profile's
// default workspace; without it (a filter such as a webhook's scope) an unset
// flag stays unset.
func settleWorkspace(flag string, useDefault bool) func(*cobra.Command, *api.Client) error {
	return func(cmd *cobra.Command, c *api.Client) error {
		fl := cmd.Flags().Lookup(flag)
		if fl == nil {
			return nil
		}
		ref := strings.TrimSpace(fl.Value.String())
		if ref == "" && useDefault {
			ref = defaultWorkspaceRef()
		}
		if ref == "" {
			return nil
		}
		id, err := resolveWorkspaceRef(cmd.Context(), c, ref)
		if err != nil {
			return die(err)
		}
		return cmd.Flags().Set(flag, id)
	}
}

// before runs hooks in order and stops at the first error.
func before(hooks ...func(*cobra.Command, *api.Client) error) func(*cobra.Command, *api.Client) error {
	return func(cmd *cobra.Command, c *api.Client) error {
		for _, h := range hooks {
			if err := h(cmd, c); err != nil {
				return err
			}
		}
		return nil
	}
}

// reportWorkspaceLanding is an After hook that says which workspace a create
// actually landed in, from the API's answer, so a create that fell back to the
// organization's default workspace never looks like it honored a flag. --json
// output already carries workspace_id.
func reportWorkspaceLanding(cmd *cobra.Command, resp any) error {
	if isJSON() {
		return nil
	}
	if id := workspaceIDOf(resp); id != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "workspace: "+id)
	}
	return nil
}
