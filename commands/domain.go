package commands

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// serviceDomain is one custom domain of a service, as services-runtime's
// contract returns it.
type serviceDomain struct {
	ID             string             `json:"id"`
	ServiceID      string             `json:"service_id"`
	FQDN           string             `json:"fqdn"`
	Status         string             `json:"status"`
	Reason         string             `json:"reason"`
	Serving        bool               `json:"serving"`
	DNSRecords     []serviceDNSRecord `json:"dns_records"`
	RedirectPolicy string             `json:"redirect_policy"`
	VerifiedAt     *string            `json:"verified_at"`
	TLSIssuedAt    *string            `json:"tls_issued_at"`
	CreatedAt      string             `json:"created_at"`
}

type serviceDNSRecord struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Value   string `json:"value"`
	Purpose string `json:"purpose"`
}

type domainPreview struct {
	Label string `json:"label"`
	Host  string `json:"host"`
	URL   string `json:"url"`
}

// newDomainCmd is `miosa domain`: the linked service's custom domains. The
// generated <label>.<org>.miosa.app address stays under `miosa service domain`.
func newDomainCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domain",
		Short: "Custom domains of the linked service",
		Long: `Add, check, verify and remove custom domains on the linked service.

  miosa link cliniciq --service API
  miosa domain add app.example.com
  miosa domain list
  miosa domain verify app.example.com --wait
  miosa domain remove app.example.com

A domain cannot end in miosa.ai. The DNS records to create are printed on add
and whenever a verify is still pending.`,
	}
	cmd.AddCommand(
		domainListCmd(),
		domainAddCmd(),
		domainVerifyCmd(),
		domainRemoveCmd(),
	)
	return cmd
}

func domainPath(projectID, serviceID string) string {
	return servicePath(projectID, serviceID) + "/domains"
}

// readDomains returns the service's custom domains and its generated preview.
func readDomains(ctx context.Context, c *api.Client, link linkFile) ([]serviceDomain, *domainPreview, error) {
	var out struct {
		Data    []serviceDomain `json:"data"`
		Preview *domainPreview  `json:"preview"`
	}
	if err := c.Get(ctx, domainPath(link.ProjectID, link.ServiceID), nil, &out); err != nil {
		return nil, nil, domainError(err)
	}
	return out.Data, out.Preview, nil
}

// domainError turns the not-yet-live case into something a user can act on.
func domainError(err error) error {
	var ae *api.Error
	if errors.As(err, &ae) && ae.Status == 404 {
		return usagef("the Services custom-domain API is not live on this deployment yet (miosa-compute #3265, #3270, #3275)")
	}
	return die(err)
}

func printDomains(p *output.Printer, domains []serviceDomain, preview *domainPreview) {
	if preview != nil && preview.URL != "" {
		p.Line("Generated address: %s (always on)", preview.URL)
	}
	if len(domains) == 0 {
		p.Line("No custom domains.")
		return
	}
	rows := make([][]string, 0, len(domains))
	for _, d := range domains {
		verified := "-"
		if d.VerifiedAt != nil {
			verified = *d.VerifiedAt
		}
		rows = append(rows, []string{d.FQDN, d.Status, yesNo(d.Serving), verified, shortID(d.ID)})
	}
	p.Table([]string{"DOMAIN", "STATUS", "SERVING", "VERIFIED", "ID"}, rows)
}

func printDNSRecords(p *output.Printer, d serviceDomain) {
	if len(d.DNSRecords) == 0 {
		return
	}
	p.Line("DNS records for %s:", d.FQDN)
	for _, r := range d.DNSRecords {
		purpose := ""
		if r.Purpose != "" {
			purpose = "  (" + r.Purpose + ")"
		}
		p.Line("  %s  %s  %s%s", r.Type, r.Name, r.Value, purpose)
	}
}

func domainListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the linked service's custom domains",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			link, _, err := linkedService(cmd.Context(), c.API)
			if err != nil {
				return err
			}
			domains, preview, err := readDomains(cmd.Context(), c.API, link)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(map[string]any{"domains": domains, "preview": preview})
			}
			printDomains(p, domains, preview)
			return nil
		},
	}
	return cmd
}

func domainAddCmd() *cobra.Command {
	var redirect, wait string
	var noWait bool
	cmd := &cobra.Command{
		Use:   "add <fqdn>",
		Short: "Add a custom domain to the linked service",
		Long: `Add a custom domain and print the DNS records to create. With --wait it then
polls verify until the domain is active or failed.

  miosa domain add app.example.com
  miosa domain add app.example.com --redirect to-primary --wait`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			fqdn := strings.TrimSpace(strings.ToLower(args[0]))
			if err := validateFQDN(fqdn); err != nil {
				return usagef("%v", err)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			link, _, err := linkedService(cmd.Context(), c.API)
			if err != nil {
				return err
			}
			body := map[string]any{"fqdn": fqdn}
			if redirect != "" {
				body["redirect_policy"] = redirect
			}
			var out struct {
				Data serviceDomain `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), domainPath(link.ProjectID, link.ServiceID), body, &out); err != nil {
				return domainError(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Added %s (%s)", out.Data.FQDN, out.Data.Status)
			printDNSRecords(p, out.Data)
			if noWait {
				return nil
			}
			return domainWait(cmd, c.API, link, out.Data)
		},
	}
	cmd.Flags().StringVar(&redirect, "redirect", "", "Redirect policy for this domain")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Add and return without checking DNS")
	_ = wait
	return cmd
}

func domainVerifyCmd() *cobra.Command {
	var noWait bool
	cmd := &cobra.Command{
		Use:   "verify <fqdn|id>",
		Short: "Check a domain's DNS now",
		Long: `Ask the API to verify the domain. Active is immediate when DNS is right; a
pending answer prints the records that are still missing. --wait polls until the
domain is active or failed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			link, _, err := linkedService(cmd.Context(), c.API)
			if err != nil {
				return err
			}
			domain, err := findDomain(cmd.Context(), c.API, link, args[0])
			if err != nil {
				return err
			}
			verified, err := verifyDomain(cmd, c.API, link, domain)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(verified)
			}
			if verified.Status == "active" {
				p.Success("%s is active", verified.FQDN)
				return nil
			}
			p.Line("%s is %s%s", verified.FQDN, verified.Status, reasonSuffix(verified.Reason))
			printDNSRecords(p, verified)
			if noWait {
				return nil
			}
			return domainWait(cmd, c.API, link, verified)
		},
	}
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Check once and return")
	return cmd
}

// verifyDomain posts one verify and returns the domain as the API answers it.
func verifyDomain(cmd *cobra.Command, c *api.Client, link linkFile, domain serviceDomain) (serviceDomain, error) {
	var out struct {
		Data serviceDomain `json:"data"`
	}
	path := domainPath(link.ProjectID, link.ServiceID) + "/" + url.PathEscape(domain.ID) + "/verify"
	if err := c.Post(cmd.Context(), path, map[string]any{}, &out); err != nil {
		var ae *api.Error
		// 202 with a reason is a pending answer, not a failure.
		if errors.As(err, &ae) && ae.Status == 202 {
			out.Data = domain
			out.Data.Reason = ae.Message
			return out.Data, nil
		}
		return serviceDomain{}, domainError(err)
	}
	if out.Data.ID == "" {
		out.Data = domain
	}
	return out.Data, nil
}

// domainWait polls verify until the domain is active or failed, or the time runs
// out. Each pending answer reprints the records that are missing.
func domainWait(cmd *cobra.Command, c *api.Client, link linkFile, domain serviceDomain) error {
	p := printerFor(cmd)
	deadline := time.Now().Add(5 * time.Minute)
	for {
		switch domain.Status {
		case "active":
			p.Success("%s is active%s", domain.FQDN, reasonSuffix(domain.Reason))
			return nil
		case "failed":
			return die(&api.Error{Status: 409, Code: "DOMAIN_FAILED",
				Message: fmt.Sprintf("%s failed%s", domain.FQDN, reasonSuffix(domain.Reason))})
		}
		if time.Now().After(deadline) {
			return die(&api.Error{Status: 408, Code: "DOMAIN_TIMEOUT",
				Message: fmt.Sprintf("%s is still %s; check the DNS records above", domain.FQDN, domain.Status)})
		}
		select {
		case <-cmd.Context().Done():
			return nil
		case <-time.After(15 * time.Second):
		}
		next, err := verifyDomain(cmd, c, link, domain)
		if err != nil {
			return err
		}
		if next.Status != domain.Status && !isJSON() {
			p.Line("%s: %s", next.FQDN, next.Status)
		}
		domain = next
	}
}

func domainRemoveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "remove <fqdn|id>",
		Short: "Remove a custom domain from the linked service",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			link, _, err := linkedService(cmd.Context(), c.API)
			if err != nil {
				return err
			}
			domain, err := findDomain(cmd.Context(), c.API, link, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Remove %s from the service?", domain.FQDN)); err != nil {
					return err
				}
			}
			path := domainPath(link.ProjectID, link.ServiceID) + "/" + url.PathEscape(domain.ID)
			if err := c.API.Delete(cmd.Context(), path, nil, nil); err != nil {
				return domainError(err)
			}
			p.Success("Removed %s", domain.FQDN)
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask")
	return cmd
}

// findDomain resolves an fqdn, an id, or an id prefix.
func findDomain(ctx context.Context, c *api.Client, link linkFile, ref string) (serviceDomain, error) {
	domains, _, err := readDomains(ctx, c, link)
	if err != nil {
		return serviceDomain{}, err
	}
	ref = strings.ToLower(strings.TrimSpace(ref))
	var matches []serviceDomain
	for _, d := range domains {
		if strings.EqualFold(d.FQDN, ref) || strings.EqualFold(d.ID, ref) || strings.HasPrefix(strings.ToLower(d.ID), ref) {
			matches = append(matches, d)
		}
	}
	switch len(matches) {
	case 0:
		return serviceDomain{}, usagef("no domain %q on this service", ref)
	case 1:
		return matches[0], nil
	default:
		return serviceDomain{}, usagef("%q matches %d domains; use the full id", ref, len(matches))
	}
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

// validateFQDN rejects what the API would reject anyway, with a clearer message.
func validateFQDN(fqdn string) error {
	if fqdn == "" {
		return fmt.Errorf("give a domain name")
	}
	if strings.HasSuffix(fqdn, ".miosa.ai") || fqdn == "miosa.ai" {
		return fmt.Errorf("a custom domain cannot end in miosa.ai")
	}
	if strings.ContainsAny(fqdn, " /:@") || !strings.Contains(fqdn, ".") {
		return fmt.Errorf("%q is not a hostname", fqdn)
	}
	labels := strings.Split(fqdn, ".")
	for _, label := range labels {
		if label == "" || len(label) > 63 {
			return fmt.Errorf("%q is not a hostname", fqdn)
		}
	}
	return nil
}
