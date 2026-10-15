package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// cloudflareAPI is the API base; a var so tests can point it at a fake.
var cloudflareAPI = "https://api.cloudflare.com/client/v4"

// dnsRecord is one entry of the response's dns_records.
type dnsRecord struct {
	Type     string
	Name     string // relative: "www", "@", "_miosa-verify"
	FQDN     string // set for verification records
	Value    string
	Purpose  string
	Optional bool
}

// domainAddDNS is the After hook of 'deploy domain-add': it acts on the records
// the API returned, as chosen by --dns. Nothing happens without the flag.
func domainAddDNS(cmd *cobra.Command, resp any) error {
	data, _ := resp.(map[string]any)
	if inner, ok := data["data"].(map[string]any); ok {
		data = inner
	}

	// Before anything else: the API answers 201 for a hostname that is already
	// attached by returning the row it has, and it does not apply the
	// redirect_policy it was sent. Without this the command looked like it had
	// set a redirect it had not.
	warnIfRedirectNotApplied(cmd, data)

	mode, _ := cmd.Flags().GetString("dns")
	if mode == "" {
		return nil
	}
	host, _ := data["hostname"].(string)
	records := parseDNSRecords(data["dns_records"])
	if host == "" || len(records) == 0 {
		return usagef("the API returned no DNS records to act on")
	}
	switch mode {
	case "zone":
		if !isJSON() {
			fmt.Fprint(cmd.OutOrStdout(), zoneLines(host, records))
		}
		return nil
	case "cloudflare":
		return cloudflareApply(cmd, host, records)
	}
	return usagef("--dns %s is not supported: use zone or cloudflare", mode)
}

func parseDNSRecords(v any) []dnsRecord {
	items, _ := v.([]any)
	var out []dnsRecord
	for _, it := range items {
		m, _ := it.(map[string]any)
		str := func(k string) string { s, _ := m[k].(string); return s }
		opt, _ := m["optional"].(bool)
		out = append(out, dnsRecord{Type: str("type"), Name: str("name"), FQDN: str("fqdn"), Value: str("value"), Purpose: str("purpose"), Optional: opt})
	}
	return out
}

// fullName is the record's absolute name.
func (r dnsRecord) fullName(host string) string {
	switch {
	case r.FQDN != "":
		return r.FQDN
	case r.Name == "@" || r.Name == "":
		return host
	case strings.Contains(r.Name, "."):
		return r.Name
	default:
		// "www" for www.example.com: the host already ends the same way.
		return host
	}
}

// zoneLines renders BIND zone-file lines with absolute names, so they paste into
// any zone without knowing its origin. ALIAS/ANAME is not standard zone syntax
// and is printed as a comment.
func zoneLines(host string, records []dnsRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "; DNS records for %s - paste into your zone\n", host)
	for _, r := range records {
		name := r.fullName(host) + "."
		switch strings.ToUpper(r.Type) {
		case "CNAME":
			fmt.Fprintf(&b, "%s 300 IN CNAME %s.\n", name, strings.TrimSuffix(r.Value, "."))
		case "TXT":
			fmt.Fprintf(&b, "%s 300 IN TXT \"%s\"\n", name, r.Value)
		case "A", "AAAA":
			fmt.Fprintf(&b, "%s 300 IN %s %s\n", name, strings.ToUpper(r.Type), r.Value)
		default:
			fmt.Fprintf(&b, "; %s %s -> %s (add this with your DNS provider's ALIAS/ANAME record)\n", r.Type, name, r.Value)
		}
	}
	return b.String()
}

// ─── Cloudflare ───────────────────────────────────────────────────────────────

type cfResponse struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func cloudflareToken() string {
	for _, k := range []string{"CLOUDFLARE_API_TOKEN", "CF_API_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func cfCall(method, path string, body any) (*cfResponse, error) {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, cloudflareAPI+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cloudflareToken())
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out cfResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("cloudflare answered HTTP %d with a body that is not JSON", resp.StatusCode)
	}
	if !out.Success {
		msgs := make([]string, 0, len(out.Errors))
		for _, e := range out.Errors {
			msgs = append(msgs, fmt.Sprintf("%s (%d)", e.Message, e.Code))
		}
		return &out, fmt.Errorf("cloudflare: %s", strings.Join(msgs, "; "))
	}
	return &out, nil
}

// cloudflareZone finds the zone that holds host: the longest suffix of the host
// that is a zone on this account.
func cloudflareZone(host string) (id, name string, err error) {
	labels := strings.Split(host, ".")
	for i := 0; i < len(labels)-1; i++ {
		candidate := strings.Join(labels[i:], ".")
		res, err := cfCall(http.MethodGet, "/zones?name="+url.QueryEscape(candidate), nil)
		if err != nil {
			return "", "", err
		}
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(res.Result, &zones)
		if len(zones) > 0 {
			return zones[0].ID, zones[0].Name, nil
		}
	}
	return "", "", fmt.Errorf("no Cloudflare zone for %s on the account the token belongs to", host)
}

func cloudflareApply(cmd *cobra.Command, host string, records []dnsRecord) error {
	if cloudflareToken() == "" {
		return usagef("--dns cloudflare needs an API token in CLOUDFLARE_API_TOKEN (Zone:Read + DNS:Edit)")
	}
	zoneID, zoneName, err := cloudflareZone(host)
	if err != nil {
		return die(err)
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "Cloudflare zone %s\n", zoneName)

	hasAddress := false
	for _, r := range records {
		if t := strings.ToUpper(r.Type); t == "A" || t == "AAAA" {
			hasAddress = true
		}
	}
	for _, r := range records {
		t := strings.ToUpper(r.Type)
		if t == "ALIAS" {
			// An apex cannot hold a CNAME in plain DNS; Cloudflare flattens one.
			// Only needed when no address record was published.
			if hasAddress {
				continue
			}
			t = "CNAME"
		}
		name := r.fullName(host)
		content := r.Value
		if t == "CNAME" {
			content = strings.TrimSuffix(content, ".")
		}
		if err := cloudflareUpsert(out, zoneID, t, name, content); err != nil {
			return die(fmt.Errorf("%s %s: %w", t, name, err))
		}
	}
	return nil
}

func cloudflareUpsert(out io.Writer, zoneID, rtype, name, content string) error {
	res, err := cfCall(http.MethodGet, fmt.Sprintf("/zones/%s/dns_records?type=%s&name=%s", zoneID, rtype, url.QueryEscape(name)), nil)
	if err != nil {
		return err
	}
	var existing []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal(res.Result, &existing)

	record := map[string]any{"type": rtype, "name": name, "content": content, "ttl": 1, "proxied": false}
	for _, e := range existing {
		if strings.EqualFold(strings.Trim(e.Content, `"`), content) {
			fmt.Fprintf(out, "  %s %s already set\n", rtype, name)
			return nil
		}
	}
	if len(existing) > 0 && rtype != "TXT" {
		if _, err := cfCall(http.MethodPut, fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, existing[0].ID), record); err != nil {
			return err
		}
		fmt.Fprintf(out, "  %s %s updated (was %s)\n", rtype, name, existing[0].Content)
		return nil
	}
	if _, err := cfCall(http.MethodPost, fmt.Sprintf("/zones/%s/dns_records", zoneID), record); err != nil {
		return err
	}
	fmt.Fprintf(out, "  %s %s created\n", rtype, name)
	return nil
}

// redirectPolicyFor maps `--redirect-to` onto the redirect policies the API
// serves: www.example.com -> example.com is www_to_apex and example.com ->
// www.example.com is apex_to_www. Redirecting to any other name is not
// supported, so it is refused here instead of being stored and ignored.
// warnIfRedirectNotApplied says so on stderr when the domain the API returned
// does not carry the redirect_policy --redirect-to asked for. It stays quiet
// when it matches, and when no redirect was requested.
func warnIfRedirectNotApplied(cmd *cobra.Command, data map[string]any) {
	target, _ := cmd.Flags().GetString("redirect-to")
	if target == "" {
		return
	}
	host, _ := data["hostname"].(string)
	want, err := redirectPolicyFor(host, target)
	if err != nil {
		return // the request body already refused this combination
	}
	got, _ := data["redirect_policy"].(string)
	if got == want {
		return
	}

	fmt.Fprintf(cmd.ErrOrStderr(),
		"warning: %s already exists with redirect_policy %q, so --redirect-to %s (%q) was not applied. "+
			"Detach the domain and attach it again to change its redirect.\n",
		host, orDefault(got, "none"), target, want)
}

func redirectPolicyFor(host, target string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	target = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(target), "."))
	switch {
	case host == target:
		return "", fmt.Errorf("--redirect-to %s is the host itself", target)
	case "www."+target == host:
		return "www_to_apex", nil
	case "www."+host == target:
		return "apex_to_www", nil
	}
	return "", fmt.Errorf("--redirect-to supports only %s <-> %s; %s is neither (attach it as its own domain instead)",
		strings.TrimPrefix(host, "www."), "www."+strings.TrimPrefix(host, "www."), target)
}
