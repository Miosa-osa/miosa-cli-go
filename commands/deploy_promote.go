package commands

import (
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newDeployPromoteCmd promotes a VERSION: the id 'miosa deploy push
// --no-promote' printed, made live without touching the deployment's id, URL
// or domains. Two arguments keep the older release-level form working.
func newDeployPromoteCmd() *cobra.Command {
	var deployment, dir string

	cmd := &cobra.Command{
		Use:   "promote <version>",
		Short: "Make a version the live one",
		Long: `Put a version on the air.

  miosa deploy promote 4bacc5df                        # in a linked folder
  miosa deploy promote 4bacc5df --deployment my-site  # from anywhere
  miosa deploy promote my-site <release-id>           # promote a release (full id, or a unique prefix of 6+ characters)

A version uploaded with 'miosa deploy push --no-promote' is not serving yet.
This makes it live; the deployment keeps its id, URL and domains, and the
previous version stays available to roll back to.

The deployment comes from --deployment, or from the .miosa/deployment.json a
first deploy wrote in the folder (--dir, default the current directory).`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				return promoteRelease(cmd, args[0], args[1])
			}
			return promoteVersion(cmd, args[0], deployment, dir)
		},
	}

	cmd.Flags().StringVar(&deployment, "deployment", "", "The deployment the version belongs to (default: the folder link)")
	cmd.Flags().StringVar(&dir, "dir", "", "Folder whose .miosa/deployment.json names the deployment (default: the current directory)")
	return cmd
}

// promoteVersion makes one version of a deployment live.
func promoteVersion(cmd *cobra.Command, version, deployment, dir string) error {
	p := printerFor(cmd)

	if deployment == "" {
		root := dir
		if root == "" {
			root = "."
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return usagef("--dir %s: %v", root, err)
		}
		link, ok := readDeploymentLink(abs)
		if !ok {
			return usagef("no deployment to promote in: pass --deployment <deployment>, "+
				"or run this in a folder already deployed (no %s there)", deploymentLinkPath(root))
		}
		deployment = link.DeploymentID
	}

	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}

	var resp struct {
		Data deployResponse `json:"data"`
	}
	path := fmt.Sprintf("/deployments/%s/versions/%s/promote",
		url.PathEscape(deployment), url.PathEscape(version))
	if err := c.API.Post(cmd.Context(), path, nil, &resp); err != nil {
		return die(err)
	}

	id := firstNonEmpty(resp.Data.ID, deployment)
	if isJSON() {
		return p.JSON(map[string]any{
			"deployment_id": id,
			"version_id":    version,
			"promoted":      true,
			"state":         resp.Data.State,
			"public_url":    resp.Data.PublicURL,
		})
	}

	p.Success("Promoted version %s of %q", shortRef(version), orDefault(resp.Data.Name, id))
	rows := [][2]string{
		{"Deployment", id},
		{"Version", version},
	}
	if resp.Data.State != "" {
		rows = append(rows, [2]string{"State", resp.Data.State})
	}
	if resp.Data.PublicURL != "" {
		rows = append(rows, [2]string{"URL", resp.Data.PublicURL})
	}
	p.Fields(rows)
	return nil
}

// promoteRelease keeps the release-level promote that shipped before versions
// could be promoted: 'miosa deploy promote <deployment> <release>'.
func promoteRelease(cmd *cobra.Command, deployment, release string) error {
	p := printerFor(cmd)
	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	path := fmt.Sprintf("/deployments/%s/releases/%s/promote",
		url.PathEscape(deployment), url.PathEscape(release))
	if err := c.API.Post(cmd.Context(), path, nil, nil); err != nil {
		return die(err)
	}
	if isJSON() {
		return p.JSON(map[string]any{
			"deployment": deployment,
			"release_id": release,
			"promoted":   true,
		})
	}
	p.Line("Promoted release %s", release)
	return nil
}
