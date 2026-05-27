package commands

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

const releasesURL = "https://github.com/Miosa-osa/miosa-cli-go/releases/latest"

func newUpgradeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade the miosa CLI to the latest version",
		Long: `Check for and install the latest version of the miosa CLI.

If a package manager was used to install miosa, the appropriate upgrade
command is printed instead of attempting a self-update.

Direct install URL: ` + releasesURL,
		RunE: runUpgrade,
	}
}

func runUpgrade(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)

	current := cliVersion
	p.Line("Current version: %s", current)
	p.Line("")

	// Detect install method and print the right upgrade path.
	// Full self-update requires fetching GitHub releases API and replacing the binary.
	// We scaffold the UX now and wire the download once the release pipeline is live.

	switch runtime.GOOS {
	case "darwin":
		if isJSON() {
			return p.JSON(map[string]string{
				"current":     current,
				"upgrade_cmd": "brew upgrade Miosa-osa/homebrew-tap/miosa",
				"releases":    releasesURL,
			})
		}
		p.Line("If installed via Homebrew:")
		p.Line("  brew upgrade Miosa-osa/homebrew-tap/miosa")
		p.Line("")
		p.Line("Manual download:")
		p.Line("  %s", releasesURL)
	default:
		if isJSON() {
			return p.JSON(map[string]string{
				"current":  current,
				"releases": releasesURL,
			})
		}
		p.Line("Download the latest release for %s/%s:", runtime.GOOS, runtime.GOARCH)
		p.Line("  %s", releasesURL)
		p.Line("")
		p.Line("Or install with the install script:")
		p.Line("  curl -fsSL https://install.miosa.ai | sh")
	}

	if current == "dev" {
		p.Line("")
		output.Warn("running a dev build — version tracking unavailable")
		fmt.Println()
	}

	return nil
}
