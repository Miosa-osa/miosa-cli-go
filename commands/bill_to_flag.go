package commands

import (
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// The global --org flag (or MIOSA_ORG) names the organization to bill for one
// command, without changing the account-wide setting from `miosa org bill`.
// It travels as the X-Miosa-Bill-To header on every request the command makes,
// so `miosa --org acme create my-box` bills acme and `miosa --org acme limits`
// reports acme's limits.
//
// It is installed on the process-wide default transport (the SDK and the REST
// client both use it) so no command needs to know about it.

const billToHeader = "X-Miosa-Bill-To"

var (
	orgFlag       string
	baseTransport = http.DefaultTransport
)

func init() {
	rootCmd.PersistentFlags().StringVar(&orgFlag, "org", "",
		"Organization to bill for this command: id, slug or name (env MIOSA_ORG)")
	cobra.OnInitialize(installBillToHeader)
}

// effectiveOrgFlag is the organization named by --org, or MIOSA_ORG.
func effectiveOrgFlag() string {
	if org := strings.TrimSpace(orgFlag); org != "" {
		return org
	}
	return strings.TrimSpace(os.Getenv("MIOSA_ORG"))
}

func installBillToHeader() {
	if org := effectiveOrgFlag(); org != "" {
		http.DefaultTransport = &billToTransport{base: baseTransport, org: org}
		return
	}
	http.DefaultTransport = baseTransport
}

type billToTransport struct {
	base http.RoundTripper
	org  string
}

func (t *billToTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTrip must not modify the caller's request.
	clone := req.Clone(req.Context())
	clone.Header.Set(billToHeader, t.org)
	return t.base.RoundTrip(clone)
}
