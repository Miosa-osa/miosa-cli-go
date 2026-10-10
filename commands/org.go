package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

// `miosa org` is about which organization pays for the sandboxes and computers
// you create, and the people and limits inside that organization.
//
// The billing account here is the organization: a machine charges the
// organization it is created under and keeps charging it for its whole life.
// `miosa org bill` chooses that organization once for your account (the
// console, the CLI and the API all follow it); the global --org flag (or
// MIOSA_ORG) overrides it for a single command.

func init() {
	rootCmd.AddCommand(newOrgCmd())
}

func newOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Which organization pays for new machines, and its members and limits",
		Long: `Show and choose the organization that pays for new sandboxes and computers.

  miosa org                       who pays right now
  miosa org list                  organizations you belong to
  miosa org bill acme             new machines bill acme, in the console, the CLI and the API
  miosa org bill --clear          bill the organization your key belongs to again
  miosa --org acme create my-box  bill one create to acme without changing the setting
  miosa org members               members of the organization your key belongs to
  miosa org invite dana@example.com
  miosa org caps                  per-member spend and concurrency caps
  miosa org caps set dana --spend 25 --concurrent 3

A machine keeps billing the organization it was created under, even if you
change this later. API keys issued in an organization other than your personal
one are locked to that organization and cannot bill another.`,
		Args: cobra.NoArgs,
		RunE: runOrgShow,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List the organizations you can bill",
			Args:    cobra.NoArgs,
			RunE:    runOrgList,
		},
		newOrgBillCmd(),
		&cobra.Command{
			Use:   "members",
			Short: "List the members of the organization your key belongs to",
			Args:  cobra.NoArgs,
			RunE:  runOrgMembers,
		},
		newOrgInviteCmd(),
		newOrgCapsCmd(),
	)
	return cmd
}

// ─── show / list ──────────────────────────────────────────────────────────────

func runOrgShow(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return err
	}
	state, err := c.GetBillTo(cmd.Context())
	if err != nil {
		return err
	}
	if isJSON() {
		return p.JSON(state)
	}
	p.Line("Bills to   %s (%s, %s)", state.Billing.Name, state.Billing.Type, state.Billing.Role)
	p.Line("Source     %s", describeBillToSource(state))
	if viewing := viewingOrg(state); viewing != nil && viewing.ID != state.Billing.ID {
		p.Line("Viewing    %s", viewing.Name)
	}
	if state.SettingStale {
		p.Line("Note       the organization you pinned is no longer available to you")
	}
	return nil
}

func describeBillToSource(state *client.BillTo) string {
	switch {
	case state.Source == "request":
		return "named by this command (--org)"
	case state.SettingPinned:
		return "pinned for your account (miosa org bill --clear to undo)"
	default:
		return "the organization your key belongs to"
	}
}

func viewingOrg(state *client.BillTo) *client.BillToOrganization {
	for i := range state.Options {
		if state.Options[i].Viewing {
			return &state.Options[i]
		}
	}
	return nil
}

func runOrgList(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return err
	}
	state, err := c.GetBillTo(cmd.Context())
	if err != nil {
		return err
	}
	if isJSON() {
		return p.JSON(state.Options)
	}
	rows := make([][]string, 0, len(state.Options))
	for _, option := range state.Options {
		marks := []string{}
		if option.Billing {
			marks = append(marks, "bills")
		}
		if option.Viewing {
			marks = append(marks, "key org")
		}
		rows = append(rows, []string{option.Name, option.Slug, option.Type, option.Role, strings.Join(marks, ", "), option.ID})
	}
	p.Table([]string{"NAME", "SLUG", "TYPE", "ROLE", "", "ID"}, rows)
	return nil
}

// ─── bill ─────────────────────────────────────────────────────────────────────

func newOrgBillCmd() *cobra.Command {
	var unpin bool
	cmd := &cobra.Command{
		Use:   "bill [org]",
		Short: "Choose the organization that pays for new machines",
		Long: `Pin the organization (id, slug or name) that pays for new sandboxes and
computers, for your whole account. With --clear, go back to billing the
organization your key belongs to.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if unpin == (len(args) == 1) {
				return fmt.Errorf("give an organization to bill, or --clear (not both)")
			}
			c, _, err := buildClient()
			if err != nil {
				return err
			}
			var state *client.BillTo
			if unpin {
				state, err = c.ClearBillTo(cmd.Context())
			} else {
				state, err = c.SetBillTo(cmd.Context(), args[0])
			}
			if err != nil {
				return err
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(state)
			}
			p.Success("New machines now bill %s", state.Billing.Name)
			return nil
		},
	}
	cmd.Flags().BoolVar(&unpin, "clear", false, "Stop pinning an organization")
	return cmd
}

// ─── members / invite ─────────────────────────────────────────────────────────

// scopedOrg resolves the organization that members, invites and caps act on:
// the one the credential belongs to. A --org that names a different
// organization is refused, because those calls are only authorized for the
// organization the credential is scoped to.
func scopedOrg(ctx context.Context, c *client.Client) (*client.BillToOrganization, error) {
	state, err := c.GetBillTo(ctx)
	if err != nil {
		return nil, err
	}
	viewing := viewingOrg(state)
	if viewing == nil {
		return nil, fmt.Errorf("could not tell which organization this key belongs to")
	}
	if want := effectiveOrgFlag(); want != "" && !matchesOrg(*viewing, want) {
		return nil, fmt.Errorf("this key belongs to %q; members, invites and caps are managed for that organization (use a key issued in %q)", viewing.Name, want)
	}
	return viewing, nil
}

func matchesOrg(org client.BillToOrganization, ref string) bool {
	ref = strings.ToLower(strings.TrimSpace(ref))
	return strings.ToLower(org.ID) == ref || strings.ToLower(org.Slug) == ref || strings.ToLower(org.Name) == ref
}

func runOrgMembers(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return err
	}
	org, err := scopedOrg(cmd.Context(), c)
	if err != nil {
		return err
	}
	members, err := c.ListOrgMembers(cmd.Context(), org.ID)
	if err != nil {
		return err
	}
	if isJSON() {
		return p.JSON(members)
	}
	rows := make([][]string, 0, len(members))
	for _, member := range members {
		who := member.UserName
		if who == "" {
			who = member.UserEmail
		}
		rows = append(rows, []string{who, member.UserEmail, member.Role, member.Status})
	}
	p.Table([]string{"NAME", "EMAIL", "ROLE", "STATUS"}, rows)
	return nil
}

func newOrgInviteCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "invite <email>",
		Short: "Invite someone to the organization your key belongs to",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return err
			}
			org, err := scopedOrg(cmd.Context(), c)
			if err != nil {
				return err
			}
			invite, err := c.InviteOrgMember(cmd.Context(), org.ID, args[0], role)
			if err != nil {
				return err
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(invite)
			}
			p.Success("Invited %s to %s as %s", invite.Email, org.Name, invite.Role)
			p.Line("Invite link: %s", invite.InviteURL)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "member", "Role to give: owner, admin or member")
	return cmd
}

// ─── caps ─────────────────────────────────────────────────────────────────────

func newOrgCapsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "caps",
		Short: "Per-member spend and concurrency caps (owners and admins)",
		Long: `Everyone in an organization shares its balance. Caps limit what one member's
sandboxes can spend in a billing period and how many they can run at once.
A member over a cap cannot start or resume that organization's machines, and
their running machines there are snapshot-stopped within about a minute. Other
members, and that member's machines in other organizations, are not affected.`,
		Args: cobra.NoArgs,
		RunE: runOrgCapsList,
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:     "list",
			Aliases: []string{"ls"},
			Short:   "List members with their caps and usage",
			Args:    cobra.NoArgs,
			RunE:    runOrgCapsList,
		},
		newOrgCapsSetCmd(),
		&cobra.Command{
			Use:   "clear <member>",
			Short: "Remove both caps from a member",
			Args:  cobra.ExactArgs(1),
			RunE:  runOrgCapsClear,
		},
	)
	return cmd
}

func runOrgCapsList(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return err
	}
	org, err := scopedOrg(cmd.Context(), c)
	if err != nil {
		return err
	}
	members, windowEnd, err := c.ListMemberCaps(cmd.Context(), org.ID)
	if err != nil {
		return err
	}
	if isJSON() {
		return p.JSON(members)
	}
	rows := make([][]string, 0, len(members))
	for _, member := range members {
		rows = append(rows, []string{
			memberLabel(member),
			member.Role,
			formatCents(member.UsageCents),
			capCentsLabel(member.UsageCapCents),
			strconv.FormatInt(member.RunningSandboxes, 10),
			capCountLabel(member.MaxConcurrentSandboxes),
		})
	}
	p.Table([]string{"MEMBER", "ROLE", "SPENT", "SPEND CAP", "RUNNING", "LIMIT"}, rows)
	if windowEnd != "" {
		p.Line("Usage resets %s", windowEnd)
	}
	return nil
}

func newOrgCapsSetCmd() *cobra.Command {
	var spend, concurrent string
	var noSpend, noConcurrent bool
	cmd := &cobra.Command{
		Use:   "set <member>",
		Short: "Set a member's caps",
		Long: `Set a member's spend cap (dollars per billing period) and/or the most machines
they can run at once. <member> is an email, name or user id. A flag you leave
out keeps its current value.`,
		Example: `  miosa org caps set dana@example.com --spend 25 --concurrent 3
  miosa org caps set dana@example.com --no-spend-cap`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			update, err := buildCapUpdate(spend, concurrent, noSpend, noConcurrent)
			if err != nil {
				return err
			}
			c, _, err := buildClient()
			if err != nil {
				return err
			}
			org, err := scopedOrg(cmd.Context(), c)
			if err != nil {
				return err
			}
			member, err := findMemberCap(cmd.Context(), c, org.ID, args[0])
			if err != nil {
				return err
			}
			updated, err := c.UpdateMemberCap(cmd.Context(), org.ID, member.UserID, update)
			if err != nil {
				return err
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(updated)
			}
			p.Success("%s: spend cap %s, running limit %s", memberLabel(*updated),
				capCentsLabel(updated.UsageCapCents), capCountLabel(updated.MaxConcurrentSandboxes))
			return nil
		},
	}
	cmd.Flags().StringVar(&spend, "spend", "", "Spend cap in dollars per billing period, for example 25 or 25.50")
	cmd.Flags().StringVar(&concurrent, "concurrent", "", "Most machines the member can run at once")
	cmd.Flags().BoolVar(&noSpend, "no-spend-cap", false, "Remove the spend cap")
	cmd.Flags().BoolVar(&noConcurrent, "no-concurrency-cap", false, "Remove the running-machines limit")
	return cmd
}

func runOrgCapsClear(cmd *cobra.Command, args []string) error {
	c, _, err := buildClient()
	if err != nil {
		return err
	}
	org, err := scopedOrg(cmd.Context(), c)
	if err != nil {
		return err
	}
	member, err := findMemberCap(cmd.Context(), c, org.ID, args[0])
	if err != nil {
		return err
	}
	if _, err := c.ClearMemberCap(cmd.Context(), org.ID, member.UserID); err != nil {
		return err
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(map[string]string{"status": "cleared", "user_id": member.UserID})
	}
	p.Success("Removed the caps on %s", memberLabel(*member))
	return nil
}

func findMemberCap(ctx context.Context, c *client.Client, orgID, ref string) (*client.MemberCap, error) {
	members, _, err := c.ListMemberCaps(ctx, orgID)
	if err != nil {
		return nil, err
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	var matches []client.MemberCap
	for _, member := range members {
		if strings.ToLower(member.UserID) == ref || strings.ToLower(member.Email) == ref || strings.ToLower(member.Name) == ref {
			matches = append(matches, member)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no member matches %q (try their email address)", ref)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("more than one member matches %q; use an email address", ref)
	}
}

func buildCapUpdate(spend, concurrent string, noSpend, noConcurrent bool) (client.MemberCapUpdate, error) {
	var update client.MemberCapUpdate
	if spend != "" && noSpend {
		return update, fmt.Errorf("--spend and --no-spend-cap cannot be used together")
	}
	if concurrent != "" && noConcurrent {
		return update, fmt.Errorf("--concurrent and --no-concurrency-cap cannot be used together")
	}
	if spend != "" {
		cents, err := parseDollarsToCents(spend)
		if err != nil {
			return update, err
		}
		update.UsageCapCents = &cents
	}
	update.ClearUsageCap = noSpend
	if concurrent != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(concurrent), 10, 64)
		if err != nil || n < 1 {
			return update, fmt.Errorf("--concurrent must be a whole number of at least 1")
		}
		update.MaxConcurrentSandboxes = &n
	}
	update.ClearConcurrency = noConcurrent
	if update.UsageCapCents == nil && !update.ClearUsageCap && update.MaxConcurrentSandboxes == nil && !update.ClearConcurrency {
		return update, fmt.Errorf("give at least one of --spend, --concurrent, --no-spend-cap, --no-concurrency-cap")
	}
	return update, nil
}

// ─── formatting ───────────────────────────────────────────────────────────────

func memberLabel(member client.MemberCap) string {
	switch {
	case member.Name != "" && member.Email != "":
		return member.Name + " <" + member.Email + ">"
	case member.Email != "":
		return member.Email
	case member.Name != "":
		return member.Name
	default:
		return member.UserID
	}
}

func formatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

func capCentsLabel(cents *int64) string {
	if cents == nil {
		return "no cap"
	}
	return formatCents(*cents)
}

func capCountLabel(n *int64) string {
	if n == nil {
		return "no limit"
	}
	return strconv.FormatInt(*n, 10)
}

// parseDollarsToCents accepts "25", "25.5" and "$25.50".
func parseDollarsToCents(input string) (int64, error) {
	text := strings.TrimPrefix(strings.TrimSpace(input), "$")
	whole, frac, hasFrac := strings.Cut(text, ".")
	if whole == "" || (hasFrac && (len(frac) == 0 || len(frac) > 2)) {
		return 0, fmt.Errorf("--spend must be a dollar amount like 25 or 25.50")
	}
	dollars, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || dollars < 0 {
		return 0, fmt.Errorf("--spend must be a dollar amount like 25 or 25.50")
	}
	var cents int64
	if hasFrac {
		if len(frac) == 1 {
			frac += "0"
		}
		cents, err = strconv.ParseInt(frac, 10, 64)
		if err != nil || cents < 0 {
			return 0, fmt.Errorf("--spend must be a dollar amount like 25 or 25.50")
		}
	}
	total := dollars*100 + cents
	if total < 1 {
		return 0, fmt.Errorf("--spend must be more than $0 (use --no-spend-cap to remove the cap)")
	}
	return total, nil
}
