// Package commands implements all miosa CLI subcommands.
package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// cliVersion is the current CLI version, injected at build time via ldflags.
var cliVersion = "dev"

// globalFlags holds values parsed from persistent flags on the root command.
var globalFlags struct {
	APIKey  string
	APIURL  string
	Output  string
	Quiet   bool
	Timeout int
}

var rootCmd = &cobra.Command{
	Use:   "miosa",
	Short: "miosa — the official CLI for MIOSA sandboxes",
	Long: `miosa is the command-line tool for creating, managing, and interacting with
MIOSA sandboxes. Authenticate with 'miosa login' to get started.

  miosa create my-box
  miosa exec my-box -- echo hello
  miosa destroy my-box

Full docs: https://docs.miosa.ai/cli`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		output.Error("%s", err)
		return err
	}
	return nil
}

// Root returns the root Cobra command. Intended for use in tests only.
func Root() *cobra.Command { return rootCmd }

// ResetForTest resets global flag state to defaults. Call before each test run
// to prevent flag value leakage between test cases.
func ResetForTest() {
	globalFlags.APIKey = ""
	globalFlags.APIURL = ""
	globalFlags.Output = "text"
	globalFlags.Quiet = false
	globalFlags.Timeout = 60
	// Reset persistent flags on the root command.
	pf := rootCmd.PersistentFlags()
	_ = pf.Set("api-key", "")
	_ = pf.Set("api-url", "")
	_ = pf.Set("output", "text")
	_ = pf.Set("quiet", "false")
	_ = pf.Set("timeout", "60")
	// Reset local flags on all subcommands so flag values don't leak between tests.
	resetCommandFlags(rootCmd)
}

// resetCommandFlags walks the command tree and resets every flag to its default.
func resetCommandFlags(cmd *cobra.Command) {
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
	for _, sub := range cmd.Commands() {
		resetCommandFlags(sub)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&globalFlags.APIKey, "api-key", "", "API key (overrides MIOSA_API_KEY and config)")
	pf.StringVar(&globalFlags.APIURL, "api-url", "", "API base URL (overrides MIOSA_BASE_URL and config)")
	pf.StringVarP(&globalFlags.Output, "output", "o", "text", "Output format: text or json")
	pf.BoolVarP(&globalFlags.Quiet, "quiet", "q", false, "Suppress informational output")
	pf.IntVar(&globalFlags.Timeout, "timeout", 60, "Request timeout in seconds (0 = no timeout)")

	// Register all subcommands.
	rootCmd.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newCreateCmd(),
		newListCmd(),
		newUseCmd(),
		newDestroyCmd(),
		newExecCmd(),
		newConsoleCmd(),
		newProxyCmd(),
		newURLCmd(),
		newFilesCmd(),
		newWorkspaceCmd(),
		newCheckpointCmd(),
		newRestoreCmd(),
		newServicesCmd(),
		newPolicyCmd(),
		newAPICmd(),
		newUpgradeCmd(),
		newVersionCmd(),
	)
}

// buildClient constructs the API client from global flags + config.
// Commands call this to get a ready-to-use client.
func buildClient() (*client.Client, config.Config, error) {
	return client.New(client.ResolveOptions{
		APIKey: globalFlags.APIKey,
		APIURL: globalFlags.APIURL,
	})
}

// printer returns a Printer that writes to os.Stdout.
// Commands should prefer printerFor(cmd) so tests can capture output.
func printer() *output.Printer {
	return printerFor(nil)
}

// printerFor returns a Printer that writes to cmd.OutOrStdout().
// Pass nil to fall back to os.Stdout.
func printerFor(cmd *cobra.Command) *output.Printer {
	f, err := output.ParseFormat(globalFlags.Output)
	if err != nil {
		output.Warn("%v", err)
		f = output.FormatText
	}
	var w io.Writer = os.Stdout
	if cmd != nil {
		w = cmd.OutOrStdout()
	}
	return output.New(w, f, globalFlags.Quiet)
}

// isJSON reports whether --output json was requested.
func isJSON() bool {
	f, _ := output.ParseFormat(globalFlags.Output)
	return f == output.FormatJSON
}

// die prints a friendly error and exits 1. Used at the top of Run functions.
func die(err error) error {
	output.FriendlyError(err)
	return fmt.Errorf("command failed")
}
