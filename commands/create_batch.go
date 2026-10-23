package commands

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// parseSandboxVars reads -e KEY=VALUE flags. A value may hold "=" but not a
// line break or NUL, which the platform cannot deliver as a variable.
func parseSandboxVars(kvs []string) (map[string]string, error) {
	vars := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("-e expects KEY=VALUE (got %q)", kv)
		}
		if strings.ContainsAny(v, "\n\r\x00") {
			return nil, fmt.Errorf("-e %s: a value cannot contain a line break or NUL", k)
		}
		vars[k] = v
	}
	return vars, nil
}

// runCreateBatch is `create --count N`: one batch request (POST
// /sandboxes/batch) with the single create's settings, so N sandboxes come up
// under the server's admission and concurrency rules.
func runCreateBatch(cmd *cobra.Command, c *api.Client, in miosa.CreateSandboxInput, opts createOptions, envName string, noEnv bool) error {
	if opts.count < 1 {
		return usagef("--count must be at least 1")
	}
	single := []struct {
		flag string
		set  bool
	}{
		{"--env", envName != ""},
		{"--no-env", noEnv},
		{"--runtime", in.Runtime != ""},
		{"--meta", len(opts.meta) > 0},
		{"--expires", strings.TrimSpace(opts.expires) != ""},
	}
	for _, s := range single {
		if s.set {
			return usagef("%s applies to one sandbox; a --count batch cannot take it", s.flag)
		}
	}
	body := map[string]any{"count": opts.count}
	if opts.concurrency > 0 {
		body["concurrency"] = opts.concurrency
	}
	if in.TemplateID != "" {
		body["template_id"] = in.TemplateID
	}
	if in.Size != "" {
		body["size"] = string(in.Size)
	}
	if in.Name != "" {
		body["name_prefix"] = in.Name
	}
	if in.WorkspaceID != "" {
		body["workspace_id"] = in.WorkspaceID
	}
	if in.TimeoutSec > 0 {
		body["timeout_sec"] = in.TimeoutSec
	}
	if in.IdleTimeoutSec > 0 {
		body["idle_timeout_sec"] = in.IdleTimeoutSec
	}
	if in.AlwaysOn {
		body["always_on"] = true
	}
	if len(in.Env) > 0 {
		body["env"] = in.Env
	}
	if len(in.Tags) > 0 {
		body["tags"] = in.Tags
	}
	if in.SetupFile != "" {
		body["setup_file"] = in.SetupFile
	}
	resp, err := c.JSONAny(cmd.Context(), reqPost("/sandboxes/batch", body))
	if err != nil {
		return die(err)
	}
	obj, _ := unwrapObject(resp).(map[string]any)
	id := fmt.Sprint(obj["id"])
	p := printerFor(cmd)
	if !opts.wait {
		if isJSON() {
			return p.JSON(resp)
		}
		p.Success("Creating %s in batch %s", sandboxCount(opts.count), id)
		p.Line("Follow it with 'miosa batch watch %s'; 'miosa list' shows them as they come up.", id)
		return nil
	}
	progress := startWaitProgress(cmd, fmt.Sprintf("%s to come up", sandboxCount(opts.count)))
	final, err := watchBatch(cmd.Context(), c, id, opts.waitTimeout, func(s map[string]any) {
		progress.Set(batchProgressLine(s))
	})
	progress.Stop()
	if err != nil {
		return die(err)
	}
	if isJSON() {
		return p.JSON(final)
	}
	if err := batchOutcome(final); err != nil {
		return die(err)
	}
	p.Success("Created %s (batch %s)", sandboxCount(opts.count), id)
	return nil
}

func sandboxCount(n int) string {
	if n == 1 {
		return "1 sandbox"
	}
	return fmt.Sprintf("%d sandboxes", n)
}

// createFromSnapshot is `create --from`: a snapshot id forks that snapshot, a
// name deploys the named snapshot (it outlives the machine it came from). The
// new sandbox has the snapshot's size and template, so those flags do not
// apply; --wait blocks until it is ready.
func createFromSnapshot(cmd *cobra.Command, opts createOptions) error {
	for _, f := range []string{"size", "template", "count", "runtime", "env", "no-env", "setup-file", "var"} {
		if fl := cmd.Flags().Lookup(f); fl != nil && fl.Changed {
			return usagef("--%s does not apply with --from: the sandbox starts as the snapshot is", f)
		}
	}
	route := "/named-snapshots/" + url.PathEscape(opts.from) + "/deploy"
	if api.IsUUID(opts.from) {
		route = "/snapshots/" + url.PathEscape(opts.from) + "/fork"
	}
	sc, err := newSnapshotAPI()
	if err != nil {
		return die(err)
	}
	var out struct {
		Data struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			State  string `json:"state"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if _, err := sc.callKeyed(cmd.Context(), http.MethodPost, route, map[string]string{}, &out); err != nil {
		return die(err)
	}
	d := out.Data
	if opts.wait && d.ID != "" {
		c, _, err := buildClient()
		if err != nil {
			return die(err)
		}
		progress := startWaitProgress(cmd, firstNonEmpty(d.Name, shortID(d.ID))+" to be ready")
		st, err := waitSandbox(cmd.Context(), c.API, d.ID, "ready", opts.waitTimeout, progress.Set)
		progress.Stop()
		if err != nil {
			return die(err)
		}
		d.State = st.State
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(d)
	}
	p.Success("Created sandbox %s from %s (%s)", firstNonEmpty(d.Name, d.ID), opts.from, firstNonEmpty(d.State, d.Status, "provisioning"))
	return nil
}
