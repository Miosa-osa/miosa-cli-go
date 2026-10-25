package commands

import "github.com/spf13/cobra"

func init() {
	rootCmd.AddCommand(
		sandboxVerb(Op{
			Use: "share [sandbox]", Short: "Let every member of the organization use a sandbox",
			Long: `Make a private sandbox shared: every member of the organization may run commands in it,
open its terminal, copy files and SSH in again.

Sharing a sandbox that was private withholds its creator's personal logins (their GitHub
token and a MIOSA key in their name) from its next start, because other people can now
use it. A running sandbox keeps what it has until it is stopped and resumed. Only its
creator or an organization owner or admin can share it.

  miosa share my-box`,
			Method: "POST", Path: "/sandboxes/{0}/share", Detail: visibilityRows,
		}),
		sandboxVerb(Op{
			Use: "unshare [sandbox]", Short: "Make a sandbox private to you and the organization's admins",
			Long: `Make a sandbox private. Only its creator and organization owners and admins may use it;
other members can still see it in lists and stop or pause it. SSH keys other members
added to it are revoked.

Only its creator or an organization owner or admin can unshare it.

  miosa unshare my-box`,
			Method: "POST", Path: "/sandboxes/{0}/unshare", Detail: visibilityRows,
		}),
	)
}

var visibilityRows = []Col{
	{Head: "Sandbox", Path: "sandbox_id"}, {Head: "Visibility", Path: "visibility"},
	{Head: "Changed", Path: "changed"}, {Head: "Shared at", Path: "shared_at"},
	{Head: "SSH keys revoked", Path: "revoked_ssh_keys"}, {Head: "Credentials", Path: "credentials"},
}

// newOrgDefaultVisibilityCmd is 'miosa org default-visibility'.
func newOrgDefaultVisibilityCmd() *cobra.Command {
	return group("default-visibility", "Whether new sandboxes start shared or private",
		`New sandboxes start shared: every member of the organization may use them. An
organization owner or admin can make them start private instead. Existing sandboxes keep
theirs; change one with 'miosa share' or 'miosa unshare', or pick at create time with
'miosa create --visibility private'.

  miosa org default-visibility show
  miosa org default-visibility set private`,
		nil,
		Op{Use: "show", Short: "Show the visibility new sandboxes start with", Method: "GET", Path: "/settings/sandbox-visibility",
			Detail: []Col{{Head: "Default", Path: "default"}}},
		Op{Use: "set <shared|private>", Short: "Set the visibility new sandboxes start with (owners and admins)",
			Method: "PUT", Path: "/settings/sandbox-visibility",
			Args: []Arg{{Name: "visibility"}},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				if args[0] != "shared" && args[0] != "private" {
					return nil, usagef("visibility must be shared or private (got %q)", args[0])
				}
				return map[string]any{"default": args[0]}, nil
			},
			Detail: []Col{{Head: "Default", Path: "default"}}},
	)
}
