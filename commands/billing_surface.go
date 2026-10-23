package commands

import (
	"fmt"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	"github.com/spf13/cobra"
)

// Auto top-up and the card it charges. Credits are cents, as everywhere else in
// this CLI; the API calls the same numbers *_credits.

func autoReloadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auto-reload",
		Short: "Add credits when the balance is low",
		Long: `Auto-reload adds credits when the balance falls below a threshold.

  miosa billing auto-reload
  miosa billing auto-reload set --threshold 500 --amount 5000 --monthly-cap 20000
  miosa billing auto-reload off

Numbers are credits (cents). 'off' keeps the amounts and turns the request off;
the API reports 'enabled' as true only when the request is on, a card is ready
and the amounts make sense, so read the answer rather than the flag.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showAutoTopup(cmd)
		},
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "get",
			Short: "Show the current settings",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return showAutoTopup(cmd)
			},
		},
		autoReloadSetCmd(),
		&cobra.Command{
			Use:   "off",
			Short: "Turn auto-reload off",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				return writeAutoTopup(cmd, map[string]any{"auto_recharge_enabled": false})
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
			if cap < amount {
				return usagef("--monthly-cap must be at least --amount, or the top-up can never run")
			}

			return writeAutoTopup(cmd, map[string]any{
				"auto_recharge_enabled":             true,
				"auto_recharge_threshold_credits":   threshold,
				"auto_recharge_amount_credits":      amount,
				"auto_recharge_monthly_cap_credits": cap,
			})
		},
	}

	cmd.Flags().IntVar(&threshold, "threshold", 0, "Balance that triggers an add, in credits")
	cmd.Flags().IntVar(&amount, "amount", 0, "Credits to add each time")
	cmd.Flags().IntVar(&cap, "monthly-cap", 0, "Most credits to add in a month")
	return cmd
}

func showAutoTopup(cmd *cobra.Command) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/billing/settings", nil, &out); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(out.Data)
	}

	state := "off"
	if truthy(out.Data["auto_recharge_enabled"]) {
		state = "on"
	}
	p.Fields([][2]string{
		{"Auto-reload", state},
		{"Requested", yesNo(truthy(out.Data["auto_recharge_requested"]))},
		{"Card ready", yesNo(truthy(out.Data["auto_recharge_payment_ready"]))},
		{"Add below", credits(out.Data["auto_recharge_threshold_credits"])},
		{"Add", credits(out.Data["auto_recharge_amount_credits"])},
		{"Monthly cap", credits(out.Data["auto_recharge_monthly_cap_credits"])},
		{"Spent this month", credits(out.Data["auto_recharge_monthly_spent_credits"])},
		{"Left this month", credits(out.Data["auto_recharge_monthly_remaining_credits"])},
	})

	return nil
}

func writeAutoTopup(cmd *cobra.Command, body map[string]any) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Put(cmd.Context(), "/billing/settings", body, &out); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(out.Data)
	}

	state := "off"
	if truthy(out.Data["auto_recharge_enabled"]) {
		state = "on"
	}
	p.Line("Auto-reload is %s.", state)
	if state == "on" {
		p.Line("Below %s, add %s, at most %s this month.",
			credits(out.Data["auto_recharge_threshold_credits"]),
			credits(out.Data["auto_recharge_amount_credits"]),
			credits(out.Data["auto_recharge_monthly_cap_credits"]))
	} else if !truthy(out.Data["auto_recharge_payment_ready"]) {
		p.Line("No card is ready to charge; add one with 'miosa billing payment-methods list' and the console.")
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
