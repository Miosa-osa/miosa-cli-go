package commands

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/spf13/cobra"
)

// latestBuildFailure returns the newest build of a deployment when that build
// failed, with the reason the server recorded. A build that fails before it
// produces a version leaves nothing in the runtime log, so 'deploy logs' and
// 'deploy create --wait' read the reason here.
func latestBuildFailure(ctx context.Context, c *api.Client, deployment string) (map[string]any, bool) {
	var resp struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Get(ctx, "/deployments/"+url.PathEscape(deployment)+"/builds", nil, &resp); err != nil || len(resp.Data) == 0 {
		return nil, false
	}
	newest := resp.Data[0]
	if newest["state"] != "failed" {
		return nil, false
	}
	return newest, true
}

func buildFailureLine(build map[string]any) string {
	reason := formatValue(build["error_message"], "")
	if reason == "" {
		reason = "no reason was recorded"
	}
	return fmt.Sprintf("Build %s failed %s: %s", formatValue(build["id"], "short"), formatAge(formatValue(build["created_at"], "")), reason)
}

// showBuildFailure is the After hook of 'deploy logs': it names a failed newest
// build above the runtime lines.
func showBuildFailure(cmd *cobra.Command, resp any) error {
	if isJSON() {
		return nil
	}
	display := argsOf(cmd)
	if len(display) == 0 {
		return nil
	}
	c, _, err := buildClient()
	if err != nil {
		return nil
	}
	if build, ok := latestBuildFailure(cmd.Context(), c.API, display[0]); ok {
		fmt.Fprintln(cmd.ErrOrStderr(), buildFailureLine(build))
	}
	return nil
}

// proveExitStatus makes `deploy prove --json` exit 1 when a check fails, after
// printing the proof, the same as the text form.
func proveExitStatus(cmd *cobra.Command, resp any) error {
	proof, _ := unwrapObject(resp).(map[string]any)
	if !isJSON() || proof == nil || proof["ok"] == true {
		return nil
	}
	_ = printerFor(cmd).JSON(resp)
	return &Failure{Err: fmt.Errorf("the deployment did not prove out"), quiet: true}
}

// renderProof prints each check and exits 1 when any failed.
func renderProof(cmd *cobra.Command, args []string, resp any) error {
	proof, _ := unwrapObject(resp).(map[string]any)
	p := printerFor(cmd)
	if proof == nil {
		return p.JSON(resp)
	}
	checks, _ := proof["checks"].([]any)
	rows := make([][]string, 0, len(checks))
	failed := 0
	for _, c := range checks {
		check, _ := c.(map[string]any)
		mark := "ok"
		if check["ok"] != true {
			mark, failed = "FAIL", failed+1
		}
		rows = append(rows, []string{mark, formatValue(check["id"], ""), formatValue(check["message"], "")})
	}
	p.Table([]string{"RESULT", "CHECK", "DETAIL"}, rows)
	if actions, _ := proof["next_actions"].([]any); failed > 0 && len(actions) > 0 {
		p.Line("")
		for _, a := range actions {
			p.Line("Next: %v", a)
		}
	}
	if failed > 0 {
		return die(fmt.Errorf("%s did not prove out: %d of %d checks failed", args[0], failed, len(checks)))
	}
	p.Success("%s is live: %d of %d checks passed", args[0], len(checks), len(checks))
	return nil
}

// renderEnvRemoval prints which variables `deploy env-rm` removed.
func renderEnvRemoval(cmd *cobra.Command, args []string, resp any) error {
	result, _ := unwrapObject(resp).(map[string]any)
	p := printerFor(cmd)
	deleted, missing := stringList(result["deleted"]), stringList(result["missing"])
	if len(deleted) > 0 {
		p.Success("Removed %s from %s", strings.Join(deleted, ", "), args[0])
		printEnvRelease(p, resp, args[0])
	}
	if len(missing) > 0 {
		p.Line("Not set, nothing removed: %s", strings.Join(missing, ", "))
	}
	return nil
}
