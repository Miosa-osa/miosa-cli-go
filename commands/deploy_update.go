package commands

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// newDeployUpdateCmd is `miosa deploy update`: change a deployment's source
// (attach a Forge repository), its branch, auto-deploy, build command and root
// directory, or its runtime size. Only the flags given are sent.
func newDeployUpdateCmd() *cobra.Command {
	var forge, branch, buildCommand, rootDir, size, aiAccess string
	var auto, noAuto bool
	cmd := &cobra.Command{
		Use:     "update <deployment>",
		Aliases: []string{"settings"},
		Short:   "Change a deployment's source, branch, build or agent access",
		Long: `Changes only what you pass.

--forge attaches a Forge repository to an uploaded deployment (or moves the
deployment to another repository). --auto builds and releases a new version on
every push or merge to the branch; --no-auto stops that (use 'miosa deploy
redeploy' to build by hand).

--size small|medium sets the runtime size of the deployment's services (small is
2 vCPU / 4 GiB, medium is 4 vCPU / 8 GiB). A running instance keeps the size it
started with; the next release or restart uses the new one.

--ai-access allow|block says whether agents may read the published site.
allow (the default) serves a generated robots.txt and llms.txt unless your files
have their own. block asks agents to stay away (robots.txt, X-Robots-Tag) and
answers 403 to a request whose User-Agent names one; that check is UA-based, so
it is a strong request, not a guarantee.`,
		Example: "  miosa deploy update my-site --forge my-repo --branch main --auto\n  miosa deploy update my-site --no-auto\n  miosa deploy update my-site --build-command 'npm run build' --root-directory web\n  miosa deploy update my-app --size small\n  miosa deploy settings my-site --ai-access block",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			size = strings.ToLower(strings.TrimSpace(size))
			if size != "" && size != "small" && size != "medium" && size != "xs" {
				return usagef("--size must be small or medium")
			}
			if auto && noAuto {
				return usagef("pass --auto or --no-auto, not both")
			}
			body := map[string]any{}
			for k, v := range map[string]string{"forge_repository": forge, "branch": branch, "build_command": buildCommand, "root_directory": rootDir, "size": size} {
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
			if err := checkAIAccess(aiAccess); err != nil {
				return err
			}
			if aiAccess != "" {
				body["ai_access"] = aiAccess
			}
			if len(body) == 0 {
				return usagef("nothing to change - pass --forge, --branch, --auto/--no-auto, --build-command, --root-directory, --size or --ai-access")
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
			name := firstNonEmpty(fmt.Sprint(resp.Data["name"]), args[0])
			if set, ok := resp.Data["size"].(map[string]any); ok {
				p.Success("Updated %s: size %v (%v MB), %v", name, set["size"], set["memory_mb"], set["applies"])
				return nil
			}
			p.Success("Updated %s", name)
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
	fs.StringVar(&size, "size", "", "Runtime size: small or medium")
	fs.StringVar(&aiAccess, "ai-access", "", "Whether agents may read the published site: allow or block")
	return cmd
}
