package commands

import (
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(newLimitsCmd())
}

func newLimitsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "limits",
		Short: "Limits of the organization a new machine would bill",
		Long: `Show what the next create would be charged to and whether it can start: the
organization that pays, its available credits, running-machine limit, sandbox
spend cap, and your own caps in it. It changes nothing.

Use --org to ask about another organization you can bill.`,
		Args: cobra.NoArgs,
		RunE: runLimits,
	}
}

func runLimits(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return err
	}
	limits, err := c.GetLimits(cmd.Context())
	if err != nil {
		return err
	}
	if isJSON() {
		return p.JSON(limits)
	}
	p.Line("Bills to      %s (%s)", limits.Organization.Name, limits.Organization.Type)
	if limits.CanStart {
		p.Line("Can start     yes")
	} else {
		p.Line("Can start     no: %s", strings.Join(describeBlocked(limits.BlockedReasons), "; "))
	}
	p.Line("Credits       %s available", formatCents(limits.Credits.AvailableCents))
	if limits.Plan.Name != "" {
		p.Line("Plan          %s", limits.Plan.Name)
	}
	p.Line("Running       %d of %s", limits.Concurrency.Running, capCountLabel(limits.Concurrency.Limit))
	p.Line("Sandbox spend %s of %s this period", formatCents(limits.Spend.AccountedCents), capCentsLabel(limits.Spend.CapCents))
	if limits.Member != nil {
		p.Line("Your caps     %s of %s spent, %d of %s running",
			formatCents(limits.Member.UsageCents), capCentsLabel(limits.Member.UsageCapCents),
			limits.Member.RunningSandboxes, capCountLabel(limits.Member.MaxConcurrentSandboxes))
	}
	return nil
}

var blockedLabels = map[string]string{
	"insufficient_credits":           "no credits available",
	"concurrency_limit_reached":      "the plan's running-sandbox limit is reached",
	"spend_cap_reached":              "the organization's sandbox spend cap is reached",
	"member_usage_cap_reached":       "you reached your usage cap in this organization",
	"member_concurrency_cap_reached": "you are running as many machines as the owner allows",
}

func describeBlocked(reasons []string) []string {
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if label, ok := blockedLabels[reason]; ok {
			out = append(out, label)
		} else {
			out = append(out, reason)
		}
	}
	if len(out) == 0 {
		out = append(out, "blocked ("+strconv.Itoa(len(reasons))+" reasons)")
	}
	return out
}
