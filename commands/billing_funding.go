package commands

import (
	"fmt"
	"math"

	"github.com/spf13/cobra"
)

// Reading the plans. Prices come from GET /pricing, the list
// miosa.ai/pricing reads; nothing here is typed in.

func plansCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "plans",
		Short: "List the plans and credit packs with their prices",
		Long: `The plans and credit packs as miosa.ai/pricing lists them. A paid plan's monthly
fee is credited to your usage, so you pay the fee or your usage, whichever is
higher. 'miosa billing plan' shows the plan you are on.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}

			var out map[string]any
			if err := c.API.Get(cmd.Context(), "/pricing", nil, &out); err != nil {
				return die(err)
			}
			out = unwrapData(out)

			if isJSON() {
				return p.JSON(map[string]any{"plans": out["plans"], "credit_packs": out["credit_packs"], "price_version": out["price_version"]})
			}

			plans, _ := out["plans"].([]any)
			rows := make([][]string, 0, len(plans))
			for _, item := range plans {
				plan, ok := item.(map[string]any)
				if !ok {
					continue
				}
				rows = append(rows, []string{stringOf(plan["name"]), dollarsPerMonth(plan["price_cents"]), includedUsage(plan)})
			}
			p.Table([]string{"PLAN", "PRICE", "INCLUDED USAGE"}, rows)

			packs, _ := out["credit_packs"].([]any)
			if len(packs) > 0 {
				p.Line("")
				packRows := make([][]string, 0, len(packs))
				for _, item := range packs {
					pack, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if cents, ok := number(pack["cents"]); ok {
						packRows = append(packRows, []string{fmt.Sprintf("$%s", trimCents(cents)), fmt.Sprintf("%.0f credits", cents)})
					}
				}
				p.Table([]string{"CREDIT PACK", "ADDS"}, packRows)
			}
			return nil
		},
	}
}

func dollarsPerMonth(value any) string {
	cents, ok := number(value)
	if !ok {
		return "-"
	}
	return fmt.Sprintf("$%s/mo", trimCents(cents))
}

// includedUsage is Free's monthly grant, or a paid plan's fee credited as usage.
func includedUsage(plan map[string]any) string {
	if grant, ok := number(plan["monthly_grant_credits"]); ok && grant > 0 {
		return fmt.Sprintf("$%s of usage every month", trimCents(grant))
	}
	if budget, ok := number(plan["budget_cents"]); ok && budget > 0 {
		return fmt.Sprintf("$%s of usage every month", trimCents(budget))
	}
	return "-"
}

// trimCents writes 2000 cents as "20" and 150 as "1.50", with thousands separated.
func trimCents(cents float64) string {
	if math.Mod(cents, 100) == 0 {
		return groupThousands(int64(cents / 100))
	}
	return fmt.Sprintf("%s.%02d", groupThousands(int64(cents/100)), int64(math.Mod(cents, 100)))
}

func groupThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
