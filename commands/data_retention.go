package commands

import (
	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() { rootCmd.AddCommand(newDataRetentionCmd()) }

var dataRetentionRows = []Col{{Head: "Mode", Path: "mode"}, {Head: "Protected", Path: "protected"}}

func newDataRetentionCmd() *cobra.Command {
	return group("data-retention", "Keep or delete a sandbox's data when it stops",
		`An organization keeps a stopped sandbox's disk (mode "keep", the default), so it can be
resumed later. With zero data retention (mode "delete_on_stop") nothing is kept once a
sandbox stops: a stop destroys the sandbox and its disk, a sandbox stopped for idleness
is destroyed shortly after, and pauses and snapshots are refused. Only organization
owners and administrators can change it.

  miosa data-retention status
  miosa data-retention enable --yes
  miosa data-retention disable`,
		nil,
		Op{Use: "status", Short: "Show the organization's data retention", Method: "GET", Path: "/data-retention",
			Detail: dataRetentionRows},
		Op{Use: "enable", Short: "Delete a sandbox's data when it stops (zero retention)", Method: "PUT", Path: "/data-retention",
			Long: `Turn on zero data retention. From then on a stop destroys the sandbox and its disk,
and pauses and snapshots are refused. Sandboxes already stopped are not touched.
--yes is required.`,
			Flags:  []Flag{{Name: "yes", Short: "y", Type: "bool", Usage: "Confirm that stops will destroy sandboxes", In: "local"}},
			Before: requireYes("zero data retention destroys every sandbox you stop; run again with --yes to turn it on"),
			Body: func(_ []string, body map[string]any) (map[string]any, error) {
				return map[string]any{"mode": "delete_on_stop", "confirm": true}, nil
			},
			Detail: dataRetentionRows},
		Op{Use: "disable", Short: "Keep a stopped sandbox's disk again (the default)", Method: "PUT", Path: "/data-retention",
			Body: func(_ []string, body map[string]any) (map[string]any, error) {
				return map[string]any{"mode": "keep"}, nil
			},
			Detail: dataRetentionRows},
	)
}

// requireYes refuses (exit 2) unless --yes was given.
func requireYes(message string) func(*cobra.Command, *api.Client) error {
	return func(cmd *cobra.Command, _ *api.Client) error {
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			return usagef("%s", message)
		}
		return nil
	}
}
