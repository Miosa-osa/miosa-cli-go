package commands

import (
	"errors"
	"math"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

// topupWait bounds how long 'billing add-credits' waits for the payment; tests shorten both.
var topupWait = struct{ every, within time.Duration }{3 * time.Second, 15 * time.Minute}

func billingTopupCmd() *cobra.Command {
	var noOpen, noWait bool
	cmd := &cobra.Command{
		Use:   "add-credits <dollars>",
		Short: "Add credits: opens a Stripe checkout and waits for the payment",
		Long: `Add credits to the organization. A Stripe checkout opens in your browser (or
its link is printed with --no-open); the command then waits, up to 15 minutes,
until the payment lands and says the new balance. Ctrl-C stops waiting, not the
payment. Free organizations add from $5.

  miosa billing add-credits 20
  miosa billing add-credits 50 --no-open      # print the link, e.g. on a server`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dollars, err := strconv.ParseFloat(strings.TrimPrefix(args[0], "$"), 64)
			if err != nil || dollars <= 0 {
				return usagef("the amount is in dollars, e.g. 20 or 12.50 (got %q)", args[0])
			}
			cents := int(math.Round(dollars * 100))
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			before, err := creditBalance(cmd, c.API)
			if err != nil {
				return die(err)
			}
			var out struct {
				CheckoutURL string `json:"checkout_url"`
				Error       string `json:"error"`
			}
			if err := c.API.Post(cmd.Context(), "/billing/checkout", map[string]any{"type": "topup", "amount": cents}, &out); err != nil {
				return die(err)
			}
			if out.CheckoutURL == "" {
				return die(errors.New("the server returned no checkout link"))
			}
			p := printerFor(cmd)
			if !noOpen {
				if err := openBrowser(out.CheckoutURL); err != nil {
					noOpen = true
				}
			}
			if !isJSON() {
				if noOpen {
					p.Line("Pay $%.2f here: %s", float64(cents)/100, out.CheckoutURL)
				} else {
					p.Line("Opened the checkout for $%.2f in your browser.", float64(cents)/100)
				}
			}
			if noWait {
				if isJSON() {
					return p.JSON(map[string]any{"checkout_url": out.CheckoutURL, "amount_cents": cents, "paid": false})
				}
				return nil
			}
			if !isJSON() {
				p.Line("Waiting for the payment (Ctrl-C stops waiting)...")
			}
			after, paid, err := waitForTopup(cmd, c, before)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"checkout_url": out.CheckoutURL, "amount_cents": cents, "paid": paid, "balance_credits": after})
			}
			if !paid {
				p.Line("No payment yet. The balance is %s credits; 'miosa billing balance' shows it when it lands.", formatCredits(after))
				return nil
			}
			p.Success("Paid. The balance is now %s credits.", formatCredits(after))
			return nil
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Print the checkout link instead of opening a browser")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Return once the checkout exists, without waiting for the payment")
	return cmd
}

func billingPortalCmd() *cobra.Command {
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "portal",
		Short: "Open the Stripe billing portal (cards, invoices, receipts)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				URL string `json:"url"`
			}
			if err := c.API.Post(cmd.Context(), "/account/billing/portal-session", nil, &out); err != nil {
				return die(err)
			}
			if out.URL == "" {
				return die(errors.New("the server returned no portal link"))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"url": out.URL})
			}
			if noOpen || openBrowser(out.URL) != nil {
				p.Line("Billing portal: %s", out.URL)
				return nil
			}
			p.Line("Opened the billing portal in your browser.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Print the portal link instead of opening a browser")
	return cmd
}

// creditBalance reads the organization's credit balance.
func creditBalance(cmd *cobra.Command, c *api.Client) (float64, error) {
	resp, err := c.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: "/credits/balance"})
	if err != nil {
		return 0, err
	}
	return num(lookupPath(unwrapObject(resp), "balance_credits")), nil
}

// waitForTopup polls the balance until it rises above before (paid), the wait
// ends, or Ctrl-C.
func waitForTopup(cmd *cobra.Command, c *client.Client, before float64) (float64, bool, error) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := time.Now().Add(topupWait.within)
	last := before
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return last, false, nil
		case <-time.After(topupWait.every):
		}
		now, err := creditBalance(cmd, c.API)
		if err != nil {
			if ctx.Err() != nil {
				return last, false, nil
			}
			continue
		}
		last = now
		if now > before {
			return now, true, nil
		}
	}
	return last, false, nil
}

func formatCredits(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
