package commands

import (
	"strings"

	"github.com/spf13/cobra"
)

// newOrgCreateCmd is 'miosa org create': POST /tenants with the signed-in person as owner.
func newOrgCreateCmd() *cobra.Command {
	return Op{
		Use:   "create <name>",
		Short: "Create an organization you own",
		Long: `Create a new organization with you as its owner. Its URL slug is made from the
name. You can own one company organization (and have one personal organization).

The key you signed in with stays in its own organization. To work in the new one,
run 'miosa login' and pick it.

  miosa org create "Acme Labs"`,
		Method: "POST", Path: "/tenants",
		Args:  []Arg{{Name: "name"}},
		Flags: []Flag{{Name: "personal", Type: "bool", Usage: "Create your personal organization instead of a company one"}},
		Body: func(args []string, body map[string]any) (map[string]any, error) {
			name := strings.TrimSpace(args[0])
			if name == "" {
				return nil, usagef("the organization needs a name")
			}
			kind := "company"
			if personal, _ := body["personal"].(bool); personal {
				kind = "personal"
			}
			// The server derives the slug from the name and refuses a slug that differs.
			return map[string]any{"name": name, "slug": name, "organization_type": kind}, nil
		},
		Detail: []Col{{Head: "Organization", Path: "name"}, {Head: "Slug", Path: "slug"}, {Head: "ID", Path: "id"}},
	}.command("org")
}

// newOrgDeleteCmd answers 'miosa org delete': an organization is never deleted with an API key.
func newOrgDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete [org]",
		Short: "Organizations cannot be deleted from the CLI",
		Long: `Deleting an organization removes every machine, database and key in it, so it is
never done with an API key. Its owner can ask MIOSA support to delete it
('miosa feedback' or the dashboard's Help menu).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return usagef("organizations cannot be deleted with an API key; the owner can ask MIOSA support to delete it (miosa feedback)")
		},
	}
}
