package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func newDestroyCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "destroy [name|id...]",
		Aliases: []string{"delete", "rm"},
		Short:   "Permanently destroy sandboxes",
		Long: `Permanently destroy one or more sandboxes. This cannot be undone.

On a terminal it asks first. In scripts pass --yes (--json never prompts).
With no name the current sandbox (set with 'miosa use') is destroyed.

  miosa destroy my-box
  miosa destroy a b c --yes
  miosa destroy          # the current sandbox
  miosa list --ids --state paused | xargs miosa destroy --yes`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDestroy(cmd, args, yes)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	cmd.Flags().BoolVarP(&yes, "force", "f", false, "Same as --yes")
	return cmd
}

func runDestroy(cmd *cobra.Command, args []string, yes bool) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	if len(args) == 0 {
		if cfg.CurrentSandbox == "" {
			return usagef("no sandbox specified and no current sandbox set (run 'miosa use <name>')")
		}
		args = []string{cfg.CurrentSandbox}
	}

	if !yes && !isJSON() {
		if err := confirmDestructive(cmd, fmt.Sprintf("Permanently destroy %s? This cannot be undone.", quoteList(args))); err != nil {
			return err
		}
	}

	type result struct {
		Sandbox string `json:"sandbox"`
		ID      string `json:"id,omitempty"`
		Status  string `json:"status"`
		Error   string `json:"error,omitempty"`
	}
	var results []result
	var firstErr error
	for _, ref := range args {
		r := result{Sandbox: ref}
		id, err := c.API.ResolveSandbox(cmd.Context(), ref)
		if err == nil {
			r.ID = id
			err = c.API.Delete(cmd.Context(), "/sandboxes/"+id, nil, nil)
		}
		switch {
		case err == nil:
			r.Status = "destroyed"
			if cfg.CurrentSandbox == ref || (r.ID != "" && cfg.CurrentSandbox == r.ID) {
				cfg.CurrentSandbox = ""
				_ = saveCurrentConfig(cfg)
			}
			if !isJSON() {
				p.Success("Destroyed sandbox %q", ref)
			}
		case api.IsStatus(err, 404):
			r.Status, r.Error = "not_found", friendlyMessage(err)
			if firstErr == nil {
				firstErr = err
			}
			if !isJSON() {
				fmt.Fprintf(cmd.ErrOrStderr(), "miosa: %s: %s\n", ref, r.Error)
			}
		default:
			r.Status, r.Error = "failed", friendlyMessage(err)
			if firstErr == nil {
				firstErr = err
			}
			if !isJSON() {
				fmt.Fprintf(cmd.ErrOrStderr(), "miosa: %s: %s\n", ref, r.Error)
			}
		}
		results = append(results, r)
	}

	if isJSON() {
		if len(results) == 1 {
			// One sandbox keeps the old single-object shape.
			one := map[string]string{"status": results[0].Status, "sandbox": results[0].Sandbox}
			if results[0].Error != "" {
				one["error"] = results[0].Error
			}
			if err := p.JSON(one); err != nil {
				return err
			}
		} else if err := p.JSON(map[string]any{"data": results}); err != nil {
			return err
		}
	}
	if firstErr != nil {
		return &Failure{Err: firstErr, quiet: true}
	}
	return nil
}

func quoteList(items []string) string {
	if len(items) == 1 {
		return fmt.Sprintf("%q", items[0])
	}
	return fmt.Sprintf("%d sandboxes", len(items))
}
