package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// newOrgTransferCmd hands the organization's ownership to another member
// (POST /tenant/transfer-ownership). Only the owner may; the old owner stays a
// member. It acts on the organization of the command (--org, else the key's).
func newOrgTransferCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "transfer <member>",
		Short: "Make another member the owner of the organization",
		Long: `Hand the organization's ownership to another member, named by email or user
id. Only the current owner can do this, and only the new owner can hand it
back. It asks you to type the organization's slug unless --yes.

  miosa org transfer dana@example.com
  miosa --org acme org transfer dana@example.com --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			member, err := findMember(cmd, c.API, args[0])
			if err != nil {
				return die(err)
			}
			var who struct {
				Organization struct {
					Slug string `json:"slug"`
					Name string `json:"name"`
				} `json:"organization"`
			}
			_ = c.API.Get(cmd.Context(), "/whoami", nil, &who)
			slug := who.Organization.Slug
			if !yes {
				what := fmt.Sprintf("%s becomes the owner of %s; you stay a member.", member.Email, orDash(slug))
				if err := confirmByTypedName(cmd, what, slug); err != nil {
					return err
				}
			}
			var out map[string]any
			body := map[string]any{"new_owner_user_id": member.UserID}
			if err := c.API.Post(cmd.Context(), "/tenant/transfer-ownership", body, &out); err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"organization": slug, "new_owner": member.Email, "new_owner_user_id": member.UserID})
			}
			p.Success("%s is now the owner of %s.", member.Email, orDash(slug))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	return cmd
}

type orgMember struct {
	UserID string `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

// findMember resolves an email (any case) or a user id to a member.
func findMember(cmd *cobra.Command, c *api.Client, ref string) (orgMember, error) {
	var out struct {
		Data []orgMember `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/tenant/members", nil, &out); err != nil {
		return orgMember{}, err
	}
	ref = strings.TrimSpace(ref)
	for _, m := range out.Data {
		if m.UserID == ref || strings.EqualFold(m.Email, ref) {
			return m, nil
		}
	}
	return orgMember{}, usageErr{fmt.Errorf("%s is not a member of this organization (see 'miosa org members')", ref)}
}
