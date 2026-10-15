package commands

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

func newDeployDomainUpdateCmd() *cobra.Command {
	var redirectTo string
	var noRedirect bool
	cmd := &cobra.Command{
		Use:   "domain-update <hostname>",
		Short: "Set or clear the redirect on a custom domain that is already attached",
		Long: `Changes a custom domain in place, without removing and re-adding it (no DNS
change, no re-verification).

--redirect-to H makes this host answer with a permanent redirect to H, keeping
the path and query. H must be this name with or without "www." and must also be
attached to the same deployment and verified. --no-redirect makes the host serve
the site again.`,
		Example: "  miosa deploy domain-update www.example.com --redirect-to example.com\n  miosa deploy domain-update www.example.com --no-redirect",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			host := args[0]
			if (redirectTo == "") == !noRedirect {
				return usagef("pass exactly one of --redirect-to <hostname> or --no-redirect")
			}
			policy := "none"
			if redirectTo != "" {
				p, err := redirectPolicyFor(host, redirectTo)
				if err != nil {
					return usagef("%v", err)
				}
				policy = p
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var dom struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/domains/"+url.PathEscape(host), nil, &dom); err != nil {
				return die(fmt.Errorf("domain %s: %w", host, err))
			}
			depID, _ := dom.Data["deployment_id"].(string)
			if depID == "" {
				return usagef("domain %s is not attached to a deployment - attach it with 'miosa deploy domain-add <deployment> %s'", host, host)
			}
			var out struct {
				Data map[string]any `json:"data"`
			}
			body := map[string]any{"deployment_id": depID, "redirect_policy": policy}
			if err := c.API.Post(cmd.Context(), "/domains/"+url.PathEscape(host)+"/assign", body, &out); err != nil {
				return die(fmt.Errorf("domain %s: %w", host, err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(out.Data)
			}
			got, _ := out.Data["redirect_policy"].(string)
			if got == "" || got == "none" {
				p.Success("%s serves the site (no redirect)", host)
			} else {
				p.Success("%s now redirects to %s (%s)", host, redirectTo, got)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&redirectTo, "redirect-to", "", "Redirect this host to the other form of the same name (www.example.com <-> example.com)")
	cmd.Flags().BoolVar(&noRedirect, "no-redirect", false, "Stop redirecting: this host serves the site")
	return cmd
}
