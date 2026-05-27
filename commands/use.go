package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

func newUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name|id>",
		Short: "Set the current sandbox",
		Long: `Set a sandbox as the current default for commands that accept an optional name/id.

The current sandbox is stored in ~/.miosa/config.toml and used whenever
a command is run without an explicit sandbox argument.

Example:
  miosa use my-box`,
		Args: cobra.ExactArgs(1),
		RunE: runUse,
	}
}

func runUse(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)
	nameOrID := args[0]

	cfg, err := config.Load()
	if err != nil {
		return die(fmt.Errorf("loading config: %w", err))
	}

	cfg.CurrentSandbox = nameOrID
	if err := config.Save(cfg); err != nil {
		return die(fmt.Errorf("saving config: %w", err))
	}

	if isJSON() {
		return p.JSON(map[string]string{"current_sandbox": nameOrID})
	}
	p.Success("Current sandbox set to %q", nameOrID)
	return nil
}
