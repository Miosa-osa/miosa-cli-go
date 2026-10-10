package commands

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() { rootCmd.AddCommand(serviceAccountCmd()) }

var serviceAccountCols = []Col{
	{Head: "ID", Path: "id"}, {Head: "NAME", Path: "name"}, {Head: "DESCRIPTION", Path: "description"}, {Head: "CREATED", Path: "created_at", Fmt: "age"},
}

// serviceAccountCmd manages service accounts: non-human principals that own
// scope-limited API keys, for pipelines and programs that should not run as a
// person. Owners and admins only.
func serviceAccountCmd() *cobra.Command {
	cmd := group("service-account", "Non-human accounts that own API keys for pipelines and programs",
		`A service account is not a person: it owns keys you scope narrowly, so a
pipeline or a program does not run with someone's own access. Deleting the
account revokes every key it owns. Only organization owners and admins manage
service accounts. <account> is an id.

  miosa service-account new deploy-pipeline --description "release pipeline"
  miosa service-account key new <account> --preset ci --expires-in 90 --quiet-key
  miosa service-account keys <account>
  miosa service-account rm <account>

Keys minted here are rotated, restricted and revoked with 'miosa api-key'.`,
		[]string{"service-accounts", "sa"},
		Op{Use: "list", Aliases: []string{"ls"}, Short: "List service accounts", Method: "GET", Path: "/service-accounts", Cols: serviceAccountCols},
		Op{
			Use: "new <name>", Aliases: []string{"create"}, Short: "Create a service account", Method: "POST", Path: "/service-accounts",
			Args:  []Arg{{Name: "name"}},
			Flags: []Flag{{Name: "description", Usage: "What the account is for"}},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				body["name"] = args[0]
				return body, nil
			},
			Detail: []Col{{Head: "Id", Path: "id"}, {Head: "Name", Path: "name"}, {Head: "Description", Path: "description"}},
		},
		Op{
			Use: "update <account>", Short: "Rename a service account or change its description", Method: "PATCH", Path: "/service-accounts/{0}",
			Args:   []Arg{{Name: "account"}},
			Flags:  []Flag{{Name: "name", Usage: "New name"}, {Name: "description", Usage: "New description"}},
			Detail: []Col{{Head: "Id", Path: "id"}, {Head: "Name", Path: "name"}, {Head: "Description", Path: "description"}},
		},
		Op{
			Use: "rm <account>", Aliases: []string{"delete"}, Short: "Delete a service account and revoke its keys", Method: "DELETE", Path: "/service-accounts/{0}",
			Args: []Arg{{Name: "account"}}, Confirm: "Delete service account {0}? Every key it owns stops working.", Done: "Deleted {0}",
		},
		Op{
			Use: "keys <account>", Short: "List the keys a service account owns", Method: "GET", Path: "/service-accounts/{0}/keys",
			Args: []Arg{{Name: "account"}}, Cols: keyCols, Rows: hideRevoked,
			Flags: []Flag{{Name: "all", Type: "bool", Usage: "Include revoked keys", In: "query", Field: "all"}},
		},
	)
	key := &cobra.Command{Use: "key", Short: "Mint a key for a service account"}
	key.AddCommand(newKeyMintCmd("new <account> [name]", "Create a key owned by a service account", cobra.RangeArgs(1, 2),
		func(ctx context.Context, c *api.Client, args []string) (string, map[string]any, error) {
			body := map[string]any{}
			if len(args) > 1 {
				body["name"] = args[1]
			}
			return "/service-accounts/" + args[0] + "/keys", body, nil
		}))
	cmd.AddCommand(key)
	return cmd
}
