package commands

import (
	"context"
	"fmt"
	"net/url"

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
