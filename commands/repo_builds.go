package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// sourceBuildUnavailable is the API's code for a git or Forge service source
// while the platform cannot build one (miosa-compute #3433).
const sourceBuildUnavailable = "SOURCE_BUILD_UNAVAILABLE"

// serviceRepoBuilds reports whether a Services service can build from a git or
// Forge repository here. GET /project-capabilities says so with source_builds;
// a server that does not say (older than the field, or the call failed) cannot:
// its git service deployments sat queued until their deadline. A repository
// deploys today as a deployment ('miosa deploy create --git'), which builds.
func serviceRepoBuilds(ctx context.Context, c *api.Client) bool {
	var out struct {
		Data map[string]any `json:"data"`
	}
	if err := c.Get(ctx, "/project-capabilities", nil, &out); err != nil {
		return false
	}
	return out.Data["source_builds"] == true
}

// isRepoSource reports whether a service source is a git or Forge repository.
func isRepoSource(source map[string]any) bool {
	t, _ := source["type"].(string)
	return t == "git" || t == "forge"
}

// repoBuildRefusal says a service cannot build a repository yet and gives the
// one command that deploys it today, into the same project. kind is the
// source type, git or forge.
func repoBuildRefusal(project, name, kind, repo, branch string) error {
	flag := "--git"
	if kind == "forge" {
		flag = "--forge"
	}
	cmd := fmt.Sprintf("miosa deploy create %s %s --project %s", flag, shellArg(repo), shellArg(project))
	if name != "" {
		cmd += " --name " + shellArg(name)
	}
	if branch != "" {
		cmd += " --branch " + shellArg(branch)
	}
	return usagef("services cannot build from a repository yet, so nothing was changed. Deploy it with:\n  %s\nIt builds, runs and shows on the project canvas", cmd)
}

// shellArg quotes a value only when a shell would split or expand it.
func shellArg(s string) string {
	if s == "" || strings.ContainsAny(s, " \t'\"$`\\") {
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}
