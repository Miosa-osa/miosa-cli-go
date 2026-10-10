package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

func newUseCmd() *cobra.Command {
	var clear bool
	cmd := &cobra.Command{
		Use:   "use [name|id]",
		Short: "Set (or show) the current sandbox",
		Long: `Set a sandbox as the current default for commands that accept an optional name/id.
The current sandbox is stored in the active profile of ~/.miosa/config.toml and is
what "current" means anywhere a sandbox is expected. The sandbox must exist.

  miosa use my-box
  miosa use              # print the current sandbox
  miosa use --clear`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUse(cmd, args, clear)
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "Forget the current sandbox")
	return cmd
}

func runUse(cmd *cobra.Command, args []string, clear bool) error {
	p := printerFor(cmd)
	cfg, err := config.Load()
	if err != nil {
		return die(fmt.Errorf("loading config: %w", err))
	}
	switch {
	case clear:
		cfg.CurrentSandbox = ""
	case len(args) == 0:
		if isJSON() {
			return p.JSON(map[string]string{"current_sandbox": cfg.CurrentSandbox})
		}
		if cfg.CurrentSandbox == "" {
			p.Line("No current sandbox. Set one with: miosa use <name|id>")
		} else {
			p.Line("%s", cfg.CurrentSandbox)
		}
		return nil
	default:
		ref := args[0]
		// Check it exists when we can; a typo should fail now, not in the next command.
		if c, _, cerr := buildClient(); cerr == nil {
			if !api.IsUUID(ref) {
				if id, rerr := c.API.ResolveSandbox(cmd.Context(), ref); rerr != nil {
					return die(rerr)
				} else if id == ref {
					return die(&api.Error{Status: 404, Code: "NOT_FOUND", Message: fmt.Sprintf("no sandbox named %q (use 'miosa list' to see names and ids)", ref)})
				}
			} else if _, gerr := c.SDK.Sandboxes.Get(cmd.Context(), ref); gerr != nil {
				return die(gerr)
			}
		}
		cfg.CurrentSandbox = ref
	}
	if err := config.Save(cfg); err != nil {
		return die(fmt.Errorf("saving config: %w", err))
	}
	if isJSON() {
		return p.JSON(map[string]string{"current_sandbox": cfg.CurrentSandbox})
	}
	if cfg.CurrentSandbox == "" {
		p.Success("Current sandbox cleared")
	} else {
		p.Success("Current sandbox set to %q", cfg.CurrentSandbox)
	}
	return nil
}
