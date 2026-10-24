package commands

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

// The global --org flag (or MIOSA_ORG) names the organization a command works
// in. It travels as X-Miosa-Org, and before the first request the CLI checks
// it against the organization the key acts in: naming another one is refused
// with exit 3, never answered with the key's own organization's data (2.3.9
// listed Lunivate's deployments for `deploy list --org osa`).
//
// --bill-to (or MIOSA_BILL_TO) names the organization a NEW machine bills,
// without changing the account-wide setting from `miosa org bill`. It travels
// as X-Miosa-Bill-To on every request the command makes, so
// `miosa --bill-to acme create my-box` bills acme and
// `miosa --bill-to acme limits` reports acme's limits.
//
// Both are installed on the process-wide default transport (the SDK and the
// REST client both use it) so no command needs to know about them.

const (
	billToHeader = "X-Miosa-Bill-To"
	orgHeader    = "X-Miosa-Org"
)

var (
	orgFlag       string
	billToFlag    string
	baseTransport = http.DefaultTransport
)

func init() {
	rootCmd.PersistentFlags().StringVar(&orgFlag, "org", "",
		"Organization to work in: id, slug or name; refused if this key does not act in it (env MIOSA_ORG)")
	rootCmd.PersistentFlags().StringVar(&billToFlag, "bill-to", "",
		"Organization a new machine bills: id, slug or name (env MIOSA_BILL_TO)")
	cobra.OnInitialize(installOrgHeaders)
}

// effectiveOrgFlag is the organization commands work in: --org, then
// MIOSA_ORG, then the profile's active org (`miosa org use`).
func effectiveOrgFlag() string {
	org, _ := effectiveOrg()
	return org
}

// effectiveOrg is effectiveOrgFlag with where it came from: "flag", "env",
// "profile" or "" (the key's own org).
func effectiveOrg() (string, string) {
	if org := strings.TrimSpace(orgFlag); org != "" {
		return org, "flag"
	}
	if org := strings.TrimSpace(os.Getenv("MIOSA_ORG")); org != "" {
		return org, "env"
	}
	if cfg, err := config.Load(); err == nil && strings.TrimSpace(cfg.ActiveOrg) != "" {
		return strings.TrimSpace(cfg.ActiveOrg), "profile"
	}
	return "", ""
}

// orgHeaderOff stops X-Miosa-Org while signing in: a new key decides its own
// organizations.
var orgHeaderOff bool

// effectiveBillTo is the organization named by --bill-to, or MIOSA_BILL_TO.
func effectiveBillTo() string {
	if org := strings.TrimSpace(billToFlag); org != "" {
		return org
	}
	return strings.TrimSpace(os.Getenv("MIOSA_BILL_TO"))
}

// installOrgHeaders puts the org transport in place. The values are read on
// each request, after --profile and the flags are applied.
func installOrgHeaders() {
	http.DefaultTransport = &orgTransport{base: baseTransport}
}

type orgTransport struct {
	base http.RoundTripper
}

func (t *orgTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Say where a change lands before the first one is sent.
	if req.Method != http.MethodGet && req.Method != http.MethodHead && !whereItLandsShown &&
		!orgHeaderOff && !isJSON() && !strings.Contains(req.URL.Path, "/auth/") {
		whereItLandsShown = true
		if line := whereItLands(); line != "" {
			fmt.Fprintln(rootCmd.ErrOrStderr(), line)
		}
	}
	org, billTo, fast := effectiveOrgFlag(), effectiveBillTo(), failFastRequest(req)
	if (org == "" || orgHeaderOff) && billTo == "" && !fast {
		return t.base.RoundTrip(req)
	}
	// RoundTrip must not modify the caller's request.
	clone := req.Clone(req.Context())
	if org != "" && !orgHeaderOff {
		clone.Header.Set(orgHeader, org)
	}
	if billTo != "" {
		clone.Header.Set(billToHeader, billTo)
	}
	if fast {
		clone.Header.Set(failFastHeader, "true")
	}
	return t.base.RoundTrip(clone)
}

// actingOrgChecked remembers that this process already checked --org.
var actingOrgChecked bool

// checkActingOrg refuses --org (or MIOSA_ORG) when it names an organization
// other than the one the key acts in: exit 3, before anything is read or
// changed. Servers since R13 refuse the same request themselves
// (ORG_MISMATCH); this check also protects against older ones.
func checkActingOrg(ctx context.Context, c *client.Client) error {
	want := effectiveOrgFlag()
	if want == "" || actingOrgChecked {
		return nil
	}
	state, err := c.GetBillTo(ctx)
	if err != nil {
		return err
	}
	actingOrgChecked = true
	acting := viewingOrg(state)
	if acting == nil {
		return nil // the server did not say; it enforces X-Miosa-Org itself
	}
	if matchesOrg(*acting, want) {
		return nil
	}
	actingName := orDefault(acting.Slug, acting.Name)
	for _, option := range state.Options {
		if matchesOrg(option, want) {
			return &api.Error{Status: http.StatusForbidden, Code: "ORG_MISMATCH",
				Message: fmt.Sprintf("this key acts in %s; you are not working in %s. Use a key or sign-in for %s, or drop --org to work in %s (to bill %s for a new machine, use --bill-to %s)",
					actingName, orDefault(option.Slug, option.Name), orDefault(option.Slug, option.Name), actingName, orDefault(option.Slug, option.Name), orDefault(option.Slug, option.Name))}
		}
	}
	return &api.Error{Status: http.StatusForbidden, Code: "NOT_A_MEMBER",
		Message: fmt.Sprintf("you are not a member of %s; this key acts in %s", want, actingName)}
}
