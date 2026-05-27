package commands

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/config"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

func newAPICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "api <path> [-- curl-style options]",
		Short: "Make an authenticated API request",
		Long: `Issue a raw authenticated HTTP request to the MIOSA API.

The path is relative to the API base URL. The Authorization header is set
automatically from your stored credentials.

Examples:
  miosa api /computers
  miosa api /computers/abc123
  miosa api /credits/balance`,
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE:               runAPI,
	}
}

func runAPI(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return die(fmt.Errorf("path argument required"))
	}

	// With DisableFlagParsing=true all args come through raw.
	// First arg is the path; we ignore any curl-style options for now
	// (full curl passthrough would require exec'ing curl, which we avoid).
	path := args[0]
	if len(args) > 1 {
		output.Warn("additional arguments after the path are not yet supported — only the path is used")
	}

	cfg, err := config.Load()
	if err != nil {
		return die(fmt.Errorf("loading config: %w", err))
	}

	apiKey := strings.TrimSpace(os.Getenv("MIOSA_API_KEY"))
	if apiKey == "" {
		apiKey = cfg.APIKey
	}
	if apiKey == "" {
		return die(fmt.Errorf("not authenticated (run 'miosa login')"))
	}

	baseURL := os.Getenv("MIOSA_BASE_URL")
	if baseURL == "" {
		baseURL = cfg.APIURL
	}
	if baseURL == "" {
		baseURL = config.DefaultBaseURL
	}

	url := strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return die(fmt.Errorf("building request: %w", err))
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "miosa-cli/"+cliVersion)

	httpClient := &http.Client{Timeout: 30 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return die(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return die(fmt.Errorf("reading response: %w", err))
	}

	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "miosa: API returned %d\n", resp.StatusCode)
	}
	os.Stdout.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		fmt.Fprintln(os.Stdout)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error %d", resp.StatusCode)
	}
	return nil
}
