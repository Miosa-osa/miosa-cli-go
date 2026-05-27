package commands

import (
	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		Long:  `Clear the API key and other settings from ~/.miosa/config.toml.`,
		RunE:  runLogout,
	}
}

func runLogout(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)

	if err := config.Clear(); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(map[string]string{"status": "logged_out"})
	}
	p.Success("Logged out. Config cleared.")
	return nil
}
