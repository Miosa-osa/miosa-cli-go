package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDestroyCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "destroy [name|id]",
		Short: "Permanently destroy a sandbox",
		Long: `Permanently destroy a sandbox. This action cannot be undone.

If no name or ID is provided the current sandbox (set with 'miosa use') is destroyed.

Example:
  miosa destroy my-box
  miosa destroy          # destroys the current sandbox`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDestroy(cmd, args, force)
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	return cmd
}

func runDestroy(cmd *cobra.Command, args []string, force bool) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID := ""
	if len(args) > 0 {
		nameOrID = args[0]
	}
	if nameOrID == "" {
		nameOrID = cfg.CurrentSandbox
	}
	if nameOrID == "" {
		return die(fmt.Errorf("no sandbox specified and no current sandbox set (run 'miosa use <name>')"))
	}

	if !force && !isJSON() {
		p.Line("This will permanently destroy sandbox %q. This cannot be undone.", nameOrID)
		p.Line("Press Enter to confirm, or Ctrl-C to cancel.")
		var confirm string
		fmt.Scanln(&confirm)
	}

	if err := c.SDK.Sandboxes.Delete(cmd.Context(), lookupComputerID(nameOrID)); err != nil {
		return die(err)
	}

	// Clear current sandbox from config if it was the one destroyed.
	if cfg.CurrentSandbox == nameOrID {
		cfg.CurrentSandbox = ""
		_ = saveCurrentConfig(cfg)
	}

	if isJSON() {
		return p.JSON(map[string]string{"status": "destroyed", "sandbox": nameOrID})
	}
	p.Success("Destroyed sandbox %q", nameOrID)
	return nil
}
