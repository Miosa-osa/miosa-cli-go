package commands

import (
	"fmt"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	"github.com/spf13/cobra"
)

// Auto-reload and the card it charges. Credits are cents, as everywhere else in
// this CLI; the API calls the same numbers *_credits.

// The bounds the API holds a reload to: $5 to $5,000 each time, at a trigger of $1 or more.
const (
	minReloadCredits    = 500
	maxReloadCredits    = 500_000
	minThresholdCredits = 100
)

func autoReloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto-reload",
		Short: "Add credits when the balance is low",
		Long: `Auto-reload adds credits when the balance falls below a threshold.

  miosa billing auto-reload
  miosa billing auto-reload set --threshold 500 --amount 5000 --monthly-cap 20000
  miosa billing auto-reload off

Numbers are credits; 1 credit is 1 cent, so --amount 5000 adds $50. A reload is
500 to 500000 credits ($5 to $5,000) at a threshold of 100 credits ($1) or more.
'off' keeps the amounts and turns the request off. The card is saved once, in the
auto-reload setup checkout in the console; until then 'enabled' stays false and
the reasons say what is missing. Changing it needs an API key with billing:write.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showAutoReload(cmd)
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "get",
			Short: "Show the current settings and whether a charge can run",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return showAutoReload(cmd)
			},
		},
		autoReloadSetCmd(),
		&cobra.Command{
			Use:   "off",
			Short: "Turn auto-reload off",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return writeAutoReload(cmd, map[string]any{"auto_recharge_enabled": false})
			},
		},
	)

	return cmd
}

func autoReloadSetCmd() *cobra.Command {
	var threshold, amount, cap int

	cmd := &cobra.Command{
		Use:   "set",
		Short: "Turn auto-reload on with these amounts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if threshold <= 0 || amount <= 0 || cap <= 0 {
				return usagef("--threshold, --amount and --monthly-cap are required and must be greater than zero")
			}
			if threshold < minThresholdCredits {
				return usagef("--threshold must be at least %d credits ($1)", minThresholdCredits)
			}
			if amount < minReloadCredits || amount > maxReloadCredits {
				return usagef("--amount must be %d to %d credits ($5 to $5,000)", minReloadCredits, maxReloadCredits)
			}
			if cap < amount {
				return usagef("--monthly-cap must be at least --amount, or auto-reload can never add anything")
			}

			return writeAutoReload(cmd, map[string]any{
				"auto_recharge_enabled":             true,
				"auto_recharge_threshold_credits":   threshold,
				"auto_recharge_amount_credits":      amount,
				"auto_recharge_monthly_cap_credits": cap,
			})
		},
	}

	cmd.Flags().IntVar(&threshold, "threshold", 0, "Balance that triggers a reload, in credits (1 credit = 1 cent; at least 100)")
	cmd.Flags().IntVar(&amount, "amount", 0, "Credits to add each time (500 to 500000, i.e. $5 to $5,000)")
	cmd.Flags().IntVar(&cap, "monthly-cap", 0, "Most credits to add in a month (at least --amount)")
	return cmd
}

// unwrapData reads a body the API sends flat, or inside a {"data": ...} envelope.
func unwrapData(body map[string]any) map[string]any {
	if inner, ok := body["data"].(map[string]any); ok {
		return inner
	}
	return body
}

func showAutoReload(cmd *cobra.Command) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	var settings, status map[string]any
	if err := c.API.Get(cmd.Context(), "/billing/settings", nil, &settings); err != nil {
		return die(err)
	}
	settings = unwrapData(settings)
	// What could stop a charge: the card, the approval for automatic charges. An older
	// platform without this read still shows the settings.
	if err := c.API.Get(cmd.Context(), "/billing/auto-topup", nil, &status); err != nil {
		status = nil
	}
	status = unwrapData(status)

	if isJSON() {
		return p.JSON(map[string]any{"settings": settings, "status": status})
	}

	state := "off"
	if truthy(settings["auto_recharge_enabled"]) {
		state = "on"
	}
	readiness, _ := status["readiness"].(map[string]any)
	cardReady := truthy(settings["auto_recharge_payment_ready"]) || truthy(readiness["ready"])
	fields := [][2]string{
		{"Auto-reload", state},
		{"Requested", yesNo(truthy(settings["auto_recharge_requested"]) || truthy(status["requested"]))},
		{"Card ready", yesNo(cardReady)},
		{"Add below", credits(settings["auto_recharge_threshold_credits"])},
		{"Add", credits(settings["auto_recharge_amount_credits"])},
		{"Monthly cap", credits(settings["auto_recharge_monthly_cap_credits"])},
		{"Spent this month", credits(settings["auto_recharge_monthly_spent_credits"])},
		{"Left this month", credits(settings["auto_recharge_monthly_remaining_credits"])},
	}
	if reasons := stringList(readiness["reasons"]); len(reasons) > 0 && !cardReady {
		fields = append(fields, [2]string{"Not ready", strings.Join(reasons, ", ")})
	}
	if next := stringOf(status["next_action"]); next != "" && !cardReady {
		fields = append(fields, [2]string{"Next step", nextStepText(next)})
	}
	p.Fields(fields)

	return nil
}

func nextStepText(action string) string {
	if action == "enroll" {
		return "save a card in the auto-reload setup checkout ('miosa open billing', Budgets & limits)"
	}
	return action
}

func writeAutoReload(cmd *cobra.Command, body map[string]any) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	var out map[string]any
	if err := c.API.Put(cmd.Context(), "/billing/settings", body, &out); err != nil {
		return die(err)
	}
	out = unwrapData(out)

	if isJSON() {
		return p.JSON(out)
	}

	state := "off"
	if truthy(out["auto_recharge_enabled"]) {
		state = "on"
	}
	p.Line("Auto-reload is %s.", state)
	if state == "on" {
		p.Line("Below %s, add %s, at most %s this month.",
			credits(out["auto_recharge_threshold_credits"]),
			credits(out["auto_recharge_amount_credits"]),
			credits(out["auto_recharge_monthly_cap_credits"]))
	} else if body["auto_recharge_enabled"] == true && !truthy(out["auto_recharge_payment_ready"]) {
		p.Line("No card is ready to charge yet: save one in the auto-reload setup checkout ('miosa open billing', Budgets & limits).")
	}

	return nil
}

func paymentMethodsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "payment-methods",
		Short: "List the cards on file, or choose the default",
		Long: `  miosa billing payment-methods list
  miosa billing payment-methods default <id>

Adding or removing a card happens in the console: card details never reach this
CLI, which only ever sees the brand, last four digits and expiry.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return listPaymentMethods(cmd)
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List the cards on file",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return listPaymentMethods(cmd)
			},
		},
		&cobra.Command{
			Use:   "default <id>",
			Short: "Charge this card by default",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				p := printerFor(cmd)
				c, _, err := buildClient()
				if err != nil {
					return die(err)
				}

				var out struct {
					Data []map[string]any `json:"data"`
				}
				path := "/billing/payment-methods/" + args[0] + "/default"
				if err := c.API.Put(cmd.Context(), path, nil, &out); err != nil {
					return die(err)
				}

				if isJSON() {
					return p.JSON(out.Data)
				}
				p.Line("Default card changed.")
				return printPaymentMethods(p, out.Data)
			},
		},
	)

	return cmd
}

func listPaymentMethods(cmd *cobra.Command) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/billing/payment-methods", nil, &out); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(out.Data)
	}
	return printPaymentMethods(p, out.Data)
}

func printPaymentMethods(p *output.Printer, methods []map[string]any) error {
	if len(methods) == 0 {
		p.Line("No card on file. Add one in the console.")
		return nil
	}

	rows := make([][]string, 0, len(methods))
	for _, m := range methods {
		rows = append(rows, []string{
			stringOf(m["id"]),
			stringOf(m["brand"]),
			"****" + stringOf(m["last4"]),
			fmt.Sprintf("%s/%s", stringOf(m["exp_month"]), stringOf(m["exp_year"])),
			yesNo(truthy(m["is_default"])),
		})
	}
	p.Table([]string{"ID", "BRAND", "CARD", "EXPIRES", "DEFAULT"}, rows)
	return nil
}

func credits(value any) string {
	n, ok := number(value)
	if !ok {
		return "-"
	}
	whole := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", n), "0"), ".")
	return fmt.Sprintf("%s credits ($%.2f)", whole, n/100)
}

func truthy(value any) bool {
	b, ok := value.(bool)
	return ok && b
}
