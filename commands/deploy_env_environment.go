package commands

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// Deployment variables belong to one environment: production (the default,
// what the live deployment and its releases use), preview (unpromoted versions
// and pull-request previews) or development (pulled for local work).

var deployEnvironments = []string{"production", "preview", "development"}

func environmentFlag() Flag {
	return Flag{
		Name:  "environment",
		Short: "e",
		Usage: "Environment: production (default), preview or development",
		Enum:  deployEnvironments,
	}
}

// environmentSupported refuses a non-production --environment on a server that
// predates environments. Such a server ignores the parameter, so `env-set
// --environment preview` would write PRODUCTION. A server that knows
// environments answers an unknown one with 422 INVALID_ENVIRONMENT; anything
// else means it would ignore the flag, and nothing is sent.
func environmentSupported(cmd *cobra.Command, c *api.Client) error {
	env, _ := cmd.Flags().GetString("environment")
	if env == "" || env == "production" {
		return nil
	}
	display := argsOf(cmd)
	if len(display) == 0 {
		return nil
	}
	q := url.Values{"environment": {"__probe__"}}
	err := c.Get(cmd.Context(), "/deployments/"+url.PathEscape(display[0])+"/env", q, nil)
	ae, isAPI := asAPIError(err)
	switch {
	case isAPI && ae.Status == http.StatusUnprocessableEntity && ae.Code == "INVALID_ENVIRONMENT":
		return nil // the server knows environments
	case err != nil && !(isAPI && ae.Status == http.StatusUnprocessableEntity):
		return die(err) // a real failure, such as an unknown deployment
	default:
		return die(fmt.Errorf("this MIOSA server does not support --environment yet, so nothing was changed (it would have used production)"))
	}
}
