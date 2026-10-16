package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// Multi-org CLI access (tasks/multi-org-access-spec-2026-10-16.md): one
// `miosa login` key may cover several organizations. `org use` picks the one
// commands work in, `org select` changes the set (re-approved in the browser),
// and list commands take --all-orgs.

func newOrgUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <org>",
		Short: "Work in this organization from now on (this profile)",
		Long: `Sets the organization commands work in, like 'gh auth switch'. The key must
cover it ('miosa org list' shows which do); --org and MIOSA_ORG still override
it for one command. A key that covers one organization only cannot switch:
sign in with 'miosa login --all-orgs' or add organizations with 'miosa org select'.`,
		Example: "  miosa org use osa\n  miosa org use commas && miosa deploy list",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check it the same way every command will: ask the server, naming it.
			orgFlag, actingOrgChecked = args[0], false
			defer func() { orgFlag = "" }()
			c, _, err := buildClient()
			if err != nil {
				return err
			}
			state, err := c.GetBillTo(cmd.Context())
			if err != nil {
				return die(err)
			}
			acting := viewingOrg(state)
			slug := args[0]
			if acting != nil {
				slug = orDefault(acting.Slug, args[0])
			}
			f, err := config.LoadFile()
			if err != nil {
				return die(err)
			}
			name := f.ActiveName()
			cur, _ := f.Get(name)
			cur.ActiveOrg = slug
			f.Set(name, cur)
			if err := config.SaveFile(f); err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"active_org": slug})
			}
			p.Success("Now working in %s", slug)
			return nil
		},
	}
}

func newOrgSelectCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "select",
		Short: "Change which organizations this terminal may work in (re-approve in the browser)",
		Long: `Opens the sign-in page with the organizations this key covers already ticked,
so you can add or remove some and approve again. The new key replaces the old
one; nothing else changes.`,
		Example: "  miosa org select\n  miosa org select --all",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, _ := config.Load()
			return runLogin(cmd, loginOptions{device: true, orgs: cfg.Orgs, allOrgs: all})
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Pre-select every organization")
	return cmd
}

// coveredOrgs is the organizations the key covers (from the profile, written
// by `miosa login`), or the key's own one for a single-org key.
func coveredOrgs(ctx context.Context, c *api.Client) ([]string, error) {
	cfg, _ := config.Load()
	if len(cfg.Orgs) > 0 {
		return cfg.Orgs, nil
	}
	var me struct {
		Data struct {
			Slug string `json:"slug"`
		} `json:"data"`
		Slug string `json:"slug"`
	}
	if err := c.Get(ctx, "/platform/tenants/current", nil, &me); err != nil {
		return nil, err
	}
	return []string{orDefault(me.Data.Slug, me.Slug)}, nil
}

// fanOutOrgs runs fetch once per covered organization, with X-Miosa-Org set
// to it, and tags every row with its org. An organization that answers with
// an error prints one warning; the others still count.
func fanOutOrgs(cmd *cobra.Command, c *api.Client, fetch func(context.Context) ([]any, error)) ([]any, error) {
	orgs, err := coveredOrgs(cmd.Context(), c)
	if err != nil {
		return nil, err
	}
	saved := orgFlag
	defer func() { orgFlag = saved }()
	var all []any
	for _, org := range orgs {
		orgFlag = org
		rows, err := fetch(cmd.Context())
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "miosa: %s: %s\n", org, friendlyMessage(err))
			continue
		}
		for _, r := range rows {
			if m, ok := r.(map[string]any); ok {
				m["org"] = org
			}
			all = append(all, r)
		}
	}
	return all, nil
}

// whereItLandsShown makes the line print once per command.
var whereItLandsShown bool

// whereItLands is the line every changing command prints before its first
// change (stderr, text mode): which organization and workspace it lands in.
func whereItLands() string {
	org, _ := effectiveOrg()
	cfg, _ := config.Load()
	if org == "" {
		org = cfg.Tenant
	}
	if org == "" {
		return ""
	}
	return fmt.Sprintf("org: %s, workspace: %s", org, orDefault(cfg.DefaultWorkspace, "default"))
}

// orgsLabel lists covered organizations for whoami.
func orgsLabel(orgs []string, scope string) string {
	if len(orgs) == 0 {
		return ""
	}
	label := strings.Join(orgs, ", ")
	if scope == "memberships" {
		label += " (and organizations you join later)"
	}
	return label
}

// listAllOrgs is --all-orgs for a hand-written list command: GET path once per
// covered organization, merged, with an ORG column first.
func listAllOrgs(cmd *cobra.Command, c *api.Client, path string, heads, fields []string) error {
	rows, err := fanOutOrgs(cmd, c, func(ctx context.Context) ([]any, error) {
		var resp any
		if err := c.Get(ctx, path, nil, &resp); err != nil {
			return nil, err
		}
		list, _ := findRows(stripAvatars(resp), "data")
		return list, nil
	})
	if err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(map[string]any{"data": rows})
	}
	if len(rows) == 0 {
		p.Line("No results.")
		return nil
	}
	table := make([][]string, 0, len(rows))
	for _, r := range rows {
		line := []string{formatValue(lookupPath(r, "org"), "")}
		for _, f := range fields {
			line = append(line, formatValue(lookupPath(r, f), ""))
		}
		table = append(table, line)
	}
	p.Table(append([]string{"ORG"}, heads...), table)
	return nil
}
