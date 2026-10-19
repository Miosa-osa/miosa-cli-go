package commands

import (
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

	cmd.RunE = func(c *cobra.Command, args []string) error {
		return runURL(c, args)
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
