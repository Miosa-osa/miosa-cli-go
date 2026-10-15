package commands

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// rmBefore refuses to delete a deployment that still serves custom domains:
// deleting lunivate-site would have taken lunivate.com offline with one "y".
func rmBefore(cmd *cobra.Command, c *api.Client) error {
	display := argsOf(cmd)
	if len(display) == 0 {
		return nil
	}
	ids := display
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/deployments/"+url.PathEscape(ids[0])+"/domains", nil, &resp); err != nil {
		return die(fmt.Errorf("deployment %s: could not list its domains, refusing to delete: %w", display[0], err))
	}
	hosts := make([]string, 0, len(resp.Data))
	for _, d := range resp.Data {
		if h, _ := d["hostname"].(string); h != "" {
			hosts = append(hosts, h)
		}
	}
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "Deployment %s\n", display[0])
	if len(hosts) == 0 {
		fmt.Fprintln(out, "  custom domains: none")
		return nil
	}
	fmt.Fprintf(out, "  custom domains attached: %s\n", strings.Join(hosts, ", "))
	if with, _ := cmd.Flags().GetBool("with-domains"); with {
		fmt.Fprintln(out, "  --with-domains: these domains stop serving when the deployment is deleted")
		return nil
	}
	return usagef("refusing to delete %s: %d custom domain(s) attached (%s). Move them first with 'miosa deploy domain-move %s <other-deployment>', or pass --with-domains to delete anyway",
		display[0], len(hosts), strings.Join(hosts, ", "), hosts[0])
}

// rollbackBefore prints the live version and the exact target before the
// confirmation, and never guesses a target: the API needs an explicit version,
// and "the newest other upload" is not "the previous live one".
func rollbackBefore(cmd *cobra.Command, c *api.Client) error {
	display := argsOf(cmd)
	if len(display) == 0 {
		return nil
	}
	ids := display
	var dep struct {
		Data map[string]any `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/deployments/"+url.PathEscape(ids[0]), nil, &dep); err != nil {
		return die(fmt.Errorf("deployment %s: %w", display[0], err))
	}
	live, _ := dep.Data["active_version_id"].(string)
	var vs struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/deployments/"+url.PathEscape(ids[0])+"/versions", nil, &vs); err != nil {
		return die(fmt.Errorf("deployment %s: could not list versions: %w", display[0], err))
	}
	out := cmd.ErrOrStderr()
	byID := map[string]map[string]any{}
	for _, v := range vs.Data {
		if id, _ := v["id"].(string); id != "" {
			byID[id] = v
		}
	}
	depID, _ := dep.Data["id"].(string)
	fmt.Fprintf(out, "Deployment %s (%s)\n", display[0], shortID(depID))
	if v, ok := byID[live]; ok {
		fmt.Fprintf(out, "  live now:        %s\n", describeVersion(v))
	} else {
		fmt.Fprintln(out, "  live now:        none")
	}
	ref, _ := cmd.Flags().GetString("to")
	if strings.TrimSpace(ref) == "" {
		fmt.Fprintln(out, "  versions:")
		for _, v := range vs.Data {
			mark := ""
			if id, _ := v["id"].(string); id == live {
				mark = "  (live)"
			}
			fmt.Fprintf(out, "    %s%s\n", describeVersion(v), mark)
		}
		return usagef("pass --to <version> (number, short id or id) - rollback never guesses which version you meant")
	}
	target, err := matchVersion(vs.Data, ref)
	if err != nil {
		return usagef("deployment %s: %v", display[0], err)
	}
	tid, _ := target["id"].(string)
	if tid == live {
		return usagef("version %s is already live on %s", shortID(tid), display[0])
	}
	fmt.Fprintf(out, "  will switch to:  %s\n", describeVersion(target))
	return cmd.Flags().Set("to", tid)
}

// matchVersion finds one version by number, full id or id prefix.
func matchVersion(versions []map[string]any, ref string) (map[string]any, error) {
	ref = strings.TrimSpace(ref)
	for _, v := range versions {
		if id, _ := v["id"].(string); id == ref {
			return v, nil
		}
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(ref, "v")); err == nil && len(ref) < 8 {
		for _, v := range versions {
			if num, ok := v["version_number"].(float64); ok && int(num) == n {
				return v, nil
			}
		}
		return nil, fmt.Errorf("no version number %d", n)
	}
	var hits []map[string]any
	for _, v := range versions {
		id, _ := v["id"].(string)
		if id == ref {
			return v, nil
		}
		if len(ref) >= 4 && strings.HasPrefix(id, strings.ToLower(ref)) {
			hits = append(hits, v)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return nil, fmt.Errorf("no version matches %q ('miosa deploy versions' lists them)", ref)
	default:
		ids := make([]string, 0, len(hits))
		for _, h := range hits {
			id, _ := h["id"].(string)
			ids = append(ids, id)
		}
		return nil, fmt.Errorf("%q matches %d versions: %s", ref, len(hits), strings.Join(ids, ", "))
	}
}

func describeVersion(v map[string]any) string {
	id, _ := v["id"].(string)
	parts := []string{}
	if num, ok := v["version_number"].(float64); ok {
		parts = append(parts, fmt.Sprintf("v%d", int(num)))
	}
	parts = append(parts, shortID(id))
	if sha, _ := v["artifact_sha256"].(string); sha != "" {
		parts = append(parts, "sha "+shortID(sha))
	}
	if at, _ := v["created_at"].(string); at != "" {
		parts = append(parts, "created "+at)
	}
	return strings.Join(parts, "  ")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// deploymentIsStatic reports whether the deployment named by the command's
// first argument serves an uploaded static site (its live version's kind).
func deploymentIsStatic(cmd *cobra.Command) bool {
	display := argsOf(cmd)
	if len(display) == 0 {
		return false
	}
	c, _, err := buildClient()
	if err != nil {
		return false
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(display[0]), nil, &resp); err != nil {
		return false
	}
	kind, _ := activeVersionFacts(cmd, c, resp.Data)
	return kind == "static"
}

// emptyResult is true for a response whose rows or lines are all absent.
func emptyResult(resp any) bool {
	m, ok := resp.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"data", "logs"} {
		switch v := m[key].(type) {
		case []any:
			if len(v) > 0 {
				return false
			}
		case map[string]any:
			if lines, ok := v["lines"].([]any); ok && len(lines) > 0 {
				return false
			}
		case nil:
		default:
			return false
		}
	}
	return true
}

// staticEmptyNotice explains an empty answer for a static deployment instead
// of printing nothing (or "No results") for a site that has real traffic.
func staticEmptyNotice(why string) func(cmd *cobra.Command, resp any) error {
	return func(cmd *cobra.Command, resp any) error {
		if !isJSON() && emptyResult(resp) && deploymentIsStatic(cmd) {
			fmt.Fprintln(cmd.ErrOrStderr(), "Note: "+why+".")
		}
		return nil
	}
}

// staticObserve shows latency as n/a for a static deployment, where it is not
// measured, instead of a misleading 0.
func staticObserve(cmd *cobra.Command, resp any) error {
	m, ok := resp.(map[string]any)
	if !ok || !deploymentIsStatic(cmd) {
		return nil
	}
	summary, _ := m["summary"].(map[string]any)
	if summary == nil {
		return nil
	}
	if d, _ := summary["duration_ms"].(map[string]any); d != nil {
		d["p50"], d["p95"] = "n/a", "n/a"
	} else {
		summary["duration_ms"] = map[string]any{"p50": "n/a", "p95": "n/a"}
	}
	return nil
}

// markLiveRelease adds live: true to the release the deployment serves now
// (false to the rest), in the table and the JSON, so each release reads with
// its version and which one is live.
func markLiveRelease(cmd *cobra.Command, resp any) error {
	display := argsOf(cmd)
	rows, _ := findRows(resp, "")
	if len(display) == 0 || len(rows) == 0 {
		return nil
	}
	c, _, err := buildClient()
	if err != nil {
		return nil
	}
	var dep struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(display[0]), nil, &dep); err != nil {
		return nil
	}
	// App Engine deployments report the live release; static ones only the
	// live version, so fall back to the newest release of that version.
	activeRelease, _ := dep.Data["active_release_id"].(string)
	activeVersion, _ := dep.Data["active_version_id"].(string)
	marked := false
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		vid, _ := m["deployment_version_id"].(string)
		live := false
		switch {
		case activeRelease != "":
			live = id == activeRelease
		case activeVersion != "" && !marked:
			live = vid == activeVersion
		}
		marked = marked || live
		m["live"] = live
	}
	return nil
}
