package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newURLCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "url [name|id]",
		Short: "Print the public URL for a sandbox",
		Long: `Print the HTTPS URL for a sandbox's public endpoint.

Example:
  miosa url my-box
  miosa url            # uses current sandbox`,
		Args: cobra.MaximumNArgs(1),
	}

	updateCmd := &cobra.Command{
		Use:   "update [name|id]",
		Short: "Update URL visibility for a sandbox",
		Long: `Update the visibility (auth policy) for a sandbox's public URL.

  --auth public    Anyone with the URL can access
  --auth tenant    Only authenticated tenant members can access
  --auth key       Access requires a secret key in the URL`,
		Args: cobra.MaximumNArgs(1),
	}

	var auth string
	updateCmd.Flags().StringVar(&auth, "auth", "tenant", "Auth policy: public, tenant, or key")

	cmd.AddCommand(updateCmd)

	cmd.RunE = func(c *cobra.Command, args []string) error {
		return runURL(c, args)
	}

	updateCmd.RunE = func(c *cobra.Command, args []string) error {
		return runURLUpdate(c, args, auth)
	}

	return cmd
}

func runURL(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID := ""
	if len(args) > 0 {
		nameOrID = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	sandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(nameOrID))
	if err != nil {
		return die(err)
	}
	sandboxURL := sandbox.PublicURL()
	if sandboxURL == "" {
		return die(sandboxNativeFeatureUnavailable(
			"sandbox URL",
			"start a preview server and call POST /api/v1/sandboxes/{id}/expose",
		))
	}

	if isJSON() {
		return p.JSON(map[string]string{
			"url":  sandboxURL,
			"id":   sandbox.ID,
			"name": sandbox.Name,
		})
	}
	p.Line("%s", sandboxURL)
	return nil
}

func runURLUpdate(cmd *cobra.Command, args []string, auth string) error {
	_, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID := ""
	if len(args) > 0 {
		nameOrID = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	switch auth {
	case "public", "tenant", "key":
	default:
		return die(fmt.Errorf("invalid auth policy %q: must be public, tenant, or key", auth))
	}

	return die(sandboxNativeFeatureUnavailable(
		"sandbox URL visibility updates",
		"use sandbox.preview.expose() for preview URLs; deployment/custom-domain visibility belongs to the Deployments API",
	))
}
