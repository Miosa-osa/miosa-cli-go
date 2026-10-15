package commands

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// domainWaitInterval is how often 'domain-verify --wait' re-checks. A var so
// tests do not sleep.
var domainWaitInterval = 10 * time.Second

// domainLiveProbe fetches the public URL once DNS and TLS are right. A var so
// tests need no network.
var domainLiveProbe = func(ctx context.Context, host string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "miosa-cli domain-verify --wait")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode, nil
}

type domainCheck struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Expected []string `json:"expected"`
	Observed []string `json:"observed"`
	OK       bool     `json:"ok"`
	Note     string   `json:"note"`
}

type domainReport struct {
	ID       string        `json:"id"`
	Hostname string        `json:"hostname"`
	Status   string        `json:"status"`
	Verified bool          `json:"verified"`
	Ready    bool          `json:"ready"`
	Stage    string        `json:"stage"`
	Checks   []domainCheck `json:"checks"`
}

// waitDomainBefore is the Before hook of 'deploy domain-verify': without --wait
// it does nothing (the one-shot verify request goes ahead); with --wait it
// polls until the domain is live or the timeout passes, and handles the whole
// command itself.
func waitDomainBefore(cmd *cobra.Command, c *api.Client) error {
	if wait, _ := cmd.Flags().GetBool("wait"); !wait {
		return nil
	}
	args := cmd.Flags().Args()
	if len(args) < 2 {
		return usagef("domain-verify --wait needs <deployment> <domain>")
	}
	d, _ := cmd.Flags().GetString("timeout")
	timeout, err := time.ParseDuration(d)
	if err != nil || timeout <= 0 {
		return usagef("--timeout must be a duration such as 10m (got %q)", d)
	}
	if err := waitForDomain(cmd, c, args[0], args[1], timeout); err != nil {
		return err
	}
	return errRequestHandled
}

func waitForDomain(cmd *cobra.Command, c *api.Client, deployment, domain string, timeout time.Duration) error {
	out := cmd.OutOrStdout()
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	var last *domainReport
	verifyPath := ""
	for attempt := 1; ; attempt++ {
		rep, err := fetchDomainReport(ctx, c, domain)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return die(err)
		}
		last = rep
		if verifyPath == "" {
			verifyPath = "/deployments/" + url.PathEscape(deployment) + "/domains/" + url.PathEscape(rep.ID) + "/verify"
		}
		dnsOK := rep.Stage != "dns"

		// DNS is right but the domain is still pending: ask the platform to verify
		// it now rather than waiting for its own sweep.
		if dnsOK && !rep.Verified {
			if _, err := c.JSONAny(ctx, api.Request{Method: http.MethodPost, Path: verifyPath, Body: map[string]any{}}); err != nil && ctx.Err() == nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "verify: %v\n", err)
			}
			rep.Verified = true
		}

		if rep.Ready {
			code, perr := domainLiveProbe(ctx, rep.Hostname)
			if perr == nil && code > 0 && code < 500 {
				fmt.Fprintf(out, "live https://%s (HTTP %d)\n", rep.Hostname, code)
				return nil
			}
			if perr != nil {
				fmt.Fprintf(out, "[%d] DNS and TLS are right; waiting for the site to answer: %v\n", attempt, perr)
			} else {
				fmt.Fprintf(out, "[%d] DNS and TLS are right; the site answered HTTP %d, retrying\n", attempt, code)
			}
		} else {
			printFailingChecks(out, attempt, rep)
		}

		select {
		case <-ctx.Done():
			return domainTimeout(cmd, last, timeout)
		case <-time.After(domainWaitInterval):
		}
	}
	return domainTimeout(cmd, last, timeout)
}

func fetchDomainReport(ctx context.Context, c *api.Client, domain string) (*domainReport, error) {
	var wrapper struct {
		Data domainReport `json:"data"`
	}
	if err := c.JSON(ctx, api.Request{Method: http.MethodGet, Path: "/domains/" + url.PathEscape(domain) + "/check"}, &wrapper); err != nil {
		return nil, err
	}
	return &wrapper.Data, nil
}

func printFailingChecks(out io.Writer, attempt int, rep *domainReport) {
	for _, ck := range rep.Checks {
		if ck.OK {
			continue
		}
		fmt.Fprintf(out, "[%d] waiting on %s: %s\n", attempt, rep.Stage, describeCheck(ck))
	}
}

// describeCheck is the exact missing record: type, name, what is expected and
// what the resolver (or the edge) returned.
func describeCheck(ck domainCheck) string {
	observed := "nothing"
	if len(ck.Observed) > 0 {
		observed = strings.Join(ck.Observed, ", ")
	}
	s := fmt.Sprintf("%s %s expected %s, observed %s", ck.Type, ck.Name, strings.Join(ck.Expected, " or "), observed)
	if ck.Note != "" {
		s += " (" + ck.Note + ")"
	}
	return s
}

func domainTimeout(cmd *cobra.Command, rep *domainReport, timeout time.Duration) error {
	if rep == nil {
		return die(fmt.Errorf("timed out after %s before the first check completed", timeout))
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "timed out after %s; %s is not live yet (stage: %s)\n", timeout, rep.Hostname, rep.Stage)
	for _, ck := range rep.Checks {
		if !ck.OK {
			fmt.Fprintf(out, "  missing: %s\n", describeCheck(ck))
		}
	}
	return die(fmt.Errorf("domain %s is not live", rep.Hostname))
}
