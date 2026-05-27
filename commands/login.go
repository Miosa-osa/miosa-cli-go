package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	miosa "github.com/Miosa-osa/miosa-go"
)

func newLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login",
		Short: "Authenticate with your MIOSA API key",
		Long: `Authenticate the CLI with a MIOSA API key.

You will be prompted to enter your API key (msk_u_...).
Keys can be created at https://miosa.ai/settings/api`,
		RunE: runLogin,
	}
}

func runLogin(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)

	p.Line("Enter your MIOSA API key (msk_u_...) — input is hidden:")

	var apiKey string

	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return die(fmt.Errorf("reading API key: %w", err))
		}
		apiKey = strings.TrimSpace(string(raw))
		fmt.Println() // newline after hidden input
	} else {
		// Non-TTY fallback (piped input).
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			apiKey = strings.TrimSpace(scanner.Text())
		}
	}

	if apiKey == "" {
		return die(fmt.Errorf("no API key provided"))
	}

	if !strings.HasPrefix(apiKey, "msk_") {
		output.Warn("key does not start with 'msk_' — proceeding anyway")
	}

	// Validate the key by calling the API.
	p.Line("Verifying key…")
	sdk := miosa.NewClient(apiKey)
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	defer cancel()

	if _, err := sdk.Credits.Balance(ctx); err != nil {
		return die(fmt.Errorf("key validation failed: %w", err))
	}

	// Save to config.
	cfg, _ := config.Load()
	cfg.APIKey = apiKey
	if cfg.APIURL == "" {
		cfg.APIURL = config.DefaultBaseURL
	}

	if err := config.Save(cfg); err != nil {
		return die(fmt.Errorf("saving config: %w", err))
	}

	if isJSON() {
		return p.JSON(map[string]string{"status": "authenticated"})
	}
	p.Success("Authenticated. Config saved to ~/.miosa/config.toml")
	return nil
}
