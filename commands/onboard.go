package commands

import (
	"bufio"
	"errors"
	"fmt"
	"net/http"
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

// onboardWait bounds how long onboard waits for a plan checkout; tests shorten both.
var onboardWait = struct{ every, within time.Duration }{3 * time.Second, 15 * time.Minute}

type onboardPlan struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	PriceCents       int    `json:"price_cents"`
	ApprovalRequired bool   `json:"approval_required"`
}

func newOnboardCmd() *cobra.Command {
	var (
		plan         string
		noOpen       bool
		noWait, wait bool
		method       signInMethod
	)
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Get set up: sign in, then only what the account still lacks",
		Long: `Walks a new machine or a new account to a working setup, and is safe to run
again: each step that is already done is only reported.

  1. Sign in, when this CLI is not signed in (the browser flow of 'miosa login').
  2. Show the organization and the workspace new sandboxes land in.
  3. Show the plan. On Free, offer the paid plans; picking one opens its
     checkout and waits for the payment (Ctrl-C stops waiting, not the payment).

  miosa onboard                       # interactive
  miosa onboard --github              # sign in with GitHub
  miosa onboard --signup              # create an account first
  miosa onboard --plan developer      # go straight to that plan's checkout

For an agent, onboarding is two steps like 'login': the first prints the link
and code for a person to approve and returns, the second waits for the approval
and finishes the rest.

  miosa onboard --json                # {"status":"pending","url":...,"next":"miosa onboard --wait"}
  miosa onboard --wait --json         # after the person approved`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if noWait && wait {
				return usagef("--no-wait returns early and --wait waits; give one")
			}
			m, err := method.resolve()
			if err != nil {
				return err
			}
			agent := isJSON() || !stdinIsTerminal(cmd)
			signedInNow := false
			if !onboardSignedIn(cmd) {
				o := loginOptions{method: m, noBrowser: noOpen, silent: true, next: "miosa onboard --wait"}
				if agent && !wait {
					return startPendingLogin(cmd, o)
				}
				if _, pending := loadPendingLogin(); wait && pending {
					o.wait = true
				} else {
					o.device = true
				}
				if err := runLogin(cmd, o); err != nil {
					return err
				}
				signedInNow = true
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			return finishOnboard(cmd, c, onboardState{
				signedInNow: signedInNow, agent: agent, plan: plan, noOpen: noOpen, noWait: noWait,
			})
		},
	}
	cmd.Flags().StringVar(&plan, "plan", "", "Plan to subscribe to (e.g. developer); opens its checkout")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "Print links instead of opening a browser")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Return once the checkout exists, without waiting for the payment")
	cmd.Flags().BoolVar(&wait, "wait", false, "Finish the sign-in a first 'onboard' started, then the rest")
	method.register(cmd)
	return cmd
}

// onboardSignedIn says whether the stored (or given) key still works.
func onboardSignedIn(cmd *cobra.Command) bool {
	c, _, err := buildClient()
	if err != nil {
		return false
	}
	var who map[string]any
	err = c.API.Get(cmd.Context(), "/whoami", nil, &who)
	return err == nil || api.IsStatus(err, http.StatusNotFound)
}

type onboardState struct {
	signedInNow, agent, noOpen, noWait bool
	plan                               string
}

func finishOnboard(cmd *cobra.Command, c *client.Client, st onboardState) error {
	p := printerFor(cmd)
	ctx := cmd.Context()

	var who struct {
		User         map[string]any `json:"user"`
		Organization map[string]any `json:"organization"`
	}
	if err := c.API.Get(ctx, "/whoami", nil, &who); err != nil && !api.IsStatus(err, http.StatusNotFound) {
		return die(err)
	}
	workspace := onboardWorkspace(cmd, c)
	current, err := currentPlan(cmd, c.API)
	if err != nil {
		return die(err)
	}
	plans := onboardPaidPlans(cmd, c.API)

	result := map[string]any{
		"signed_in": true, "signed_in_now": st.signedInNow,
		"user": orNil(who.User["email"]), "organization": orNil(who.Organization["slug"]),
		"workspace": workspace, "plan": current,
		"next": []string{"miosa create", "miosa docs"},
	}
	if !isJSON() {
		if st.signedInNow {
			p.Success("Signed in as %s.", fmt.Sprint(orNil(who.User["email"])))
		} else {
			p.Line("Signed in as %s.", fmt.Sprint(orNil(who.User["email"])))
		}
		p.Fields([][2]string{
			{"Organization", fmt.Sprint(orNil(who.Organization["name"])) + " (" + fmt.Sprint(orNil(who.Organization["slug"])) + ")"},
			{"Workspace", fmt.Sprint(orNil(workspace["name"]))},
			{"Plan", current},
		})
	}

	choice := strings.TrimSpace(st.plan)
	if choice == "" && current == "free" && !st.agent && len(plans) > 0 {
		choice = promptPlan(cmd, plans)
	}
	if current == "free" && choice == "" {
		ids := make([]string, 0, len(plans))
		for _, pl := range plans {
			ids = append(ids, pl.ID)
		}
		result["plans"] = ids
	}
	if choice != "" && !strings.EqualFold(choice, current) {
		url, changed, err := onboardCheckout(cmd, c, choice, st)
		if err != nil {
			return die(err)
		}
		result["checkout_url"] = url
		result["plan_changed"] = changed
		if changed {
			result["plan"] = strings.ToLower(choice)
		}
	}

	if isJSON() {
		return p.JSON(result)
	}
	if ids, ok := result["plans"].([]string); ok && len(ids) > 0 {
		p.Line("You are on Free. Upgrade any time with: miosa onboard --plan <%s>", strings.Join(ids, "|"))
	}
	p.Line("Next: 'miosa create' starts a sandbox; 'miosa docs' lists the guides.")
	return nil
}

// onboardWorkspace is the organization's default workspace (where a create
// lands when it names none), or nil when the list is unavailable.
func onboardWorkspace(cmd *cobra.Command, c *client.Client) map[string]any {
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/workspaces", nil, &out); err != nil {
		return nil
	}
	if ref := defaultWorkspaceRef(); ref != "" {
		for _, ws := range out.Data {
			if ws["id"] == ref || ws["slug"] == ref || ws["name"] == ref {
				return map[string]any{"id": ws["id"], "name": ws["name"], "source": "profile"}
			}
		}
	}
	for _, ws := range out.Data {
		if ws["is_default"] == true {
			return map[string]any{"id": ws["id"], "name": ws["name"], "source": "organization"}
		}
	}
	return nil
}

// currentPlan is the organization's plan id (GET /tenant/plan).
func currentPlan(cmd *cobra.Command, c *api.Client) (string, error) {
	var out struct {
		Plan struct {
			Name string `json:"name"`
		} `json:"plan"`
	}
	if err := c.Get(cmd.Context(), "/tenant/plan", nil, &out); err != nil {
		return "", err
	}
	if out.Plan.Name == "" {
		return "free", nil
	}
	return strings.ToLower(out.Plan.Name), nil
}

// onboardPaidPlans are the plans a person can buy online (GET /pricing).
func onboardPaidPlans(cmd *cobra.Command, c *api.Client) []onboardPlan {
	var out struct {
		Plans []onboardPlan `json:"plans"`
	}
	if err := c.Get(cmd.Context(), "/pricing", nil, &out); err != nil {
		return nil
	}
	paid := make([]onboardPlan, 0, len(out.Plans))
	for _, pl := range out.Plans {
		if pl.PriceCents > 0 && !pl.ApprovalRequired {
			paid = append(paid, pl)
		}
	}
	return paid
}

// promptPlan lists the paid plans and reads a choice; Enter stays on Free.
func promptPlan(cmd *cobra.Command, plans []onboardPlan) string {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Plans:")
	for i, pl := range plans {
		fmt.Fprintf(out, "  %d) %-10s $%d/month  %s\n", i+1, pl.ID, pl.PriceCents/100, pl.Description)
	}
	fmt.Fprint(out, "Pick a plan (number or name, Enter to stay on Free): ")
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	line = strings.TrimSpace(line)
	if n, err := strconv.Atoi(line); err == nil && n >= 1 && n <= len(plans) {
		return plans[n-1].ID
	}
	return line
}

// onboardCheckout opens the plan's checkout and, unless --no-wait, waits for
// the organization's plan to become it.
func onboardCheckout(cmd *cobra.Command, c *client.Client, plan string, st onboardState) (string, bool, error) {
	p := printerFor(cmd)
	var out struct {
		CheckoutURL string `json:"checkout_url"`
	}
	body := map[string]any{"type": "subscription", "plan": strings.ToLower(plan)}
	if err := c.API.Post(cmd.Context(), "/billing/checkout", body, &out); err != nil {
		return "", false, err
	}
	if out.CheckoutURL == "" {
		return "", false, errors.New("the server returned no checkout link")
	}
	opened := !st.noOpen && !isJSON() && openBrowser(out.CheckoutURL) == nil
	if !isJSON() {
		if opened {
			p.Line("Opened the %s checkout in your browser.", plan)
		} else {
			p.Line("Subscribe to %s here: %s", plan, out.CheckoutURL)
		}
	}
	if st.noWait {
		return out.CheckoutURL, false, nil
	}
	if !isJSON() {
		p.Line("Waiting for the payment (Ctrl-C stops waiting)...")
	}
	changed, err := waitForPlan(cmd, c, plan)
	if err != nil {
		return out.CheckoutURL, false, err
	}
	if !isJSON() {
		if changed {
			p.Success("You are on %s now.", plan)
		} else {
			p.Line("No payment yet. 'miosa billing plan' shows the plan once it changes.")
		}
	}
	return out.CheckoutURL, changed, nil
}

func waitForPlan(cmd *cobra.Command, c *client.Client, plan string) (bool, error) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	deadline := time.Now().Add(onboardWait.within)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(onboardWait.every):
		}
		now, err := currentPlan(cmd, c.API)
		if err != nil {
			var te *api.TransportError
			if errors.As(err, &te) {
				continue
			}
			return false, err
		}
		if strings.EqualFold(now, plan) {
			return true, nil
		}
	}
	return false, nil
}
