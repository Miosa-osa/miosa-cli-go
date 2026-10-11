package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"
)

// newRuntimesCmd is the parent 'miosa runtimes' command: it describes the
// sandbox runtimes the platform can schedule a sandbox on.
func newRuntimesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtimes",
		Short: "Show the sandbox runtimes the platform offers",
		Long: `Show the sandbox runtimes the platform can schedule a sandbox on.

A runtime is the engine a sandbox boots on: Firecracker, the default; SOMA,
the faster start for supported Linux images; or Auto, which lets the platform
pick. Availability is a live gate: a runtime the platform has turned off is
listed with the reason it is unavailable.

Pass a runtime to 'miosa create' with --runtime <id>.`,
	}
	cmd.AddCommand(newRuntimesListCmd())
	return cmd
}

func newRuntimesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the sandbox runtimes and their availability",
		Long: `List every sandbox runtime with its live availability and largest shape.

  miosa runtimes list
  miosa runtimes list --json | jq -r '.data[] | select(.available) | .id'`,
		Annotations: map[string]string{routeAnnotation: "GET /sandboxes/runtimes"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRuntimesList(cmd)
		},
	}
}

func runRuntimesList(cmd *cobra.Command) error {
	p := printerFor(cmd)

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	runtimes, err := c.SDK.Runtimes.List(cmd.Context())
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(map[string]interface{}{"data": runtimes})
	}

	if len(runtimes) == 0 {
		p.Line("No runtimes reported.")
		return nil
	}

	rows := make([][]string, 0, len(runtimes))
	for _, rt := range runtimes {
		rows = append(rows, []string{
			rt.ID,
			rt.Label,
			yesNo(rt.Available),
			derefString(rt.UnavailableReason),
			formatRuntimeShape(rt.Capabilities.MaxShape),
		})
	}
	p.Table([]string{"ID", "LABEL", "AVAILABLE", "REASON", "MAX SHAPE"}, rows)
	return nil
}

// formatRuntimeShape renders a runtime's largest shape, for example
// "16 vCPU / 32768 MB / 81920 MB".
func formatRuntimeShape(s miosa.SandboxRuntimeShape) string {
	return fmt.Sprintf("%d vCPU / %d MB / %d MB", s.CPUCount, s.MemoryMB, s.DiskSizeMB)
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// derefString returns the pointed-to string, or "" when the pointer is nil.
// The table renderer shows "-" for an empty cell.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
