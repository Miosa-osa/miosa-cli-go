package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the CLI version",
		RunE:  runVersion,
	}
}

func runVersion(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)

	if isJSON() {
		return p.JSON(map[string]string{
			"version": cliVersion,
			"go":      runtime.Version(),
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
		})
	}

	fmt.Printf("miosa version %s (%s/%s, %s)\n",
		cliVersion, runtime.GOOS, runtime.GOARCH, runtime.Version())
	return nil
}
