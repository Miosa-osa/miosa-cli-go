package commands

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// `miosa deploy env-pull` writes a deployment's environment variable values to
// a local dotenv file, like `vercel env pull`. The server
// (GET /deployments/:id/env/reveal) admits only an owner or admin signed in as
// themselves and audits every name; this side never prints a value.

type revealedVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func newDeployEnvPullCmd() *cobra.Command {
	var file string
	var force bool
	cmd := &cobra.Command{
		Use:   "env-pull <deployment>",
		Short: "Write the deployment's environment variables to a local .env file",
		Long: `Downloads the deployment's environment variable values into a dotenv file
(default .env.local, written with mode 0600) so the app runs locally with the
same configuration. Only an owner or admin of the organization can, signed in
as themselves ('miosa login'); machine, CI and sandbox keys are refused, and
the server audits every variable name pulled.

The values go to the file only: the command prints names, never values. An
existing file is kept unless you pass --force.`,
		Example: "  miosa deploy env-pull my-app\n  miosa deploy env-pull my-app --file .env --force",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := filepath.Abs(file)
			if err != nil {
				return die(err)
			}
			// Refuse before asking the server, so a refusal reveals nothing.
			if _, err := os.Stat(path); err == nil && !force {
				return usagef("%s already exists; pass --force to overwrite it", file)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var resp struct {
				Data struct {
					Environment string        `json:"environment"`
					Vars        []revealedVar `json:"vars"`
				} `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(args[0])+"/env/reveal", nil, &resp); err != nil {
				// A 404 for a deployment that exists is the route itself: a
				// server that predates env reveal.
				if api.IsStatus(err, http.StatusNotFound) &&
					c.API.Get(cmd.Context(), "/deployments/"+url.PathEscape(args[0]), nil, nil) == nil {
					return die(fmt.Errorf("this MIOSA server does not support env-pull yet; nothing was written"))
				}
				return die(err)
			}
			vars := resp.Data.Vars
			sort.Slice(vars, func(i, j int) bool { return vars[i].Name < vars[j].Name })
			env := orDefault(resp.Data.Environment, "production")
			if err := writeDotenv(path, args[0], env, vars); err != nil {
				return die(err)
			}
			names := make([]string, 0, len(vars))
			for _, v := range vars {
				names = append(names, v.Name)
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"file": path, "environment": env, "names": names})
			}
			p.Success("Wrote %d variable(s) from %s (%s) to %s (mode 0600)", len(names), args[0], env, file)
			if len(names) > 0 {
				p.Line("  %s", strings.Join(names, ", "))
			}
			if tracked := notGitIgnored(path); tracked {
				fmt.Fprintf(cmd.ErrOrStderr(), "miosa: %s is not ignored by git; add it to .gitignore so the values are never committed\n", file)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", ".env.local", "File to write")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite the file if it exists")
	return cmd
}

// writeDotenv writes vars to path with mode 0600, atomically: a temporary file
// in the same directory renamed over the target, so a reader never sees half a
// file and an overwritten file never keeps wider permissions.
func writeDotenv(path, deployment, env string, vars []revealedVar) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Pulled from deployment %s (%s) by 'miosa deploy env-pull' at %s.\n", deployment, env, time.Now().UTC().Format(time.RFC3339))
	b.WriteString("# Secret values: do not commit this file.\n")
	for _, v := range vars {
		fmt.Fprintf(&b, "%s=%s\n", v.Name, dotenvQuote(v.Value))
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".miosa-env-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("writing %s", path), err)
	}
	return nil
}

// dotenvQuote double-quotes a value with the escapes dotenv readers undo
// (backslash, quote, newline, carriage return), as `vercel env pull` writes.
func dotenvQuote(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return `"` + r.Replace(v) + `"`
}

// notGitIgnored is true when path is inside a git work tree and git does not
// ignore it. Outside a repository, or without git, it says nothing.
func notGitIgnored(path string) bool {
	dir := filepath.Dir(path)
	if err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return false
	}
	err := exec.Command("git", "-C", dir, "check-ignore", "-q", path).Run()
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}
