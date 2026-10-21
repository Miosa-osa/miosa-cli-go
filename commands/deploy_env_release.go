package commands

import (
	"net/url"

	"github.com/spf13/cobra"
)

// The server cuts a config release for a production env change unless the request says release=false.
// These helpers make that visible: the cut release is printed, and --no-release opts out of it.

func noReleaseFlag() Flag {
	return Flag{
		Name:  "no-release",
		Type:  "bool",
		Usage: "Store the change without cutting a release; it applies at the next release",
		Field: "no_release",
	}
}

// noReleaseBody turns --no-release into the server's release=false.
func noReleaseBody(_ []string, body map[string]any) (map[string]any, error) {
	if v, ok := body["no_release"]; ok {
		delete(body, "no_release")
		if on, _ := v.(bool); on {
			body["release"] = false
		}
	}
	return body, nil
}

// noReleaseQuery does the same for a request that carries no body (DELETE).
func noReleaseQuery(_ []string, q url.Values) error {
	v := q.Get("no_release")
	q.Del("no_release")
	if v == "true" {
		q.Set("release", "false")
	}
	return nil
}

type lineWriter interface {
	Success(format string, a ...any)
	Line(format string, a ...any)
}

// printEnvRelease says what the server did with the change: the release it cut, or that it cut none.
func printEnvRelease(p lineWriter, resp any, deployment string) {
	top, _ := resp.(map[string]any)
	release, _ := top["release"].(map[string]any)
	if release == nil {
		p.Line("No release was cut: the change applies at the next release ('miosa deploy redeploy %s')", deployment)
		return
	}
	id, _ := release["id"].(string)
	state, _ := release["state"].(string)
	p.Line("Release %s cut (%s); the change is live once it is", id, state)
}

func renderEnvSet(cmd *cobra.Command, args []string, resp any) error {
	p := printerFor(cmd)
	p.Success("Updated the environment of %s", args[0])
	printEnvRelease(p, resp, args[0])
	return nil
}
