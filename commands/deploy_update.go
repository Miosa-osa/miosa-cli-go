package commands

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

// newDeployUpdateCmd is `miosa deploy update`: change a deployment's source
// (attach a Forge repository), its branch, auto-deploy, build command and root
// directory. Only the flags given are sent.
func newDeployUpdateCmd() *cobra.Command {
	var forge, branch, buildCommand, rootDir string
	var auto, noAuto bool
	cmd := &cobra.Command{
		Use:   "update <deployment>",
		Short: "Change a deployment's Forge source, branch, auto-deploy or build settings",
		Long: `Changes only what you pass.

--forge attaches a Forge repository to an uploaded deployment (or moves the
deployment to another repository). --auto builds and releases a new version on
every push or merge to the branch; --no-auto stops that (use 'miosa deploy
redeploy' to build by hand).`,
		Example: "  miosa deploy update my-site --forge my-repo --branch main --auto\n  miosa deploy update my-site --no-auto\n  miosa deploy update my-site --build-command 'npm run build' --root-directory web",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if auto && noAuto {
				return usagef("pass --auto or --no-auto, not both")
			}
			body := map[string]any{}
			for k, v := range map[string]string{"forge_repository": forge, "branch": branch, "build_command": buildCommand, "root_directory": rootDir} {
				if v != "" {
					body[k] = v
				}
			}
			if auto {
				body["auto_deploy"] = true
			}
			if noAuto {
				body["auto_deploy"] = false
			}
			if len(body) == 0 {
				return usagef("nothing to change - pass --forge, --branch, --auto/--no-auto, --build-command or --root-directory")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var resp struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Patch(cmd.Context(), "/deployments/"+url.PathEscape(args[0]), body, &resp); err != nil {
				return die(fmt.Errorf("deployment %s: %w", args[0], err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(resp)
			}
			p.Success("Updated %s", firstNonEmpty(fmt.Sprint(resp.Data["name"]), args[0]))
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&forge, "forge", "", "Forge repository to build from (id, org/name or name)")
	fs.StringVar(&branch, "branch", "", "Branch to build")
	fs.BoolVar(&auto, "auto", false, "Build and release on every push/merge to the branch")
	fs.BoolVar(&noAuto, "no-auto", false, "Stop building on pushes")
	fs.StringVar(&buildCommand, "build-command", "", "Build command")
	fs.StringVar(&rootDir, "root-directory", "", "The app's directory inside the repository")
	return cmd
}
