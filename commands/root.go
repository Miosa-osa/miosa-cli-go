// Package commands implements all miosa CLI subcommands.
package commands

import (
	"context"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

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
	// Profile selects a named profile from ~/.miosa/config.toml.
	Profile string
	// JSON is the --json shorthand for --output json.
	JSON bool
	// Retries is the API retry count; negative keeps the default (3).
	Retries int
	// NoUpdateCheck disables the new-version notice.
	NoUpdateCheck bool
	// NoUpdate skips the opt-in automatic update for this run.
	NoUpdate bool
}

var rootCmd = &cobra.Command{
	Use:     "miosa",
	Version: cliVersion,
	Short:   "miosa — the official CLI for MIOSA",
	Long: `miosa is the command-line tool for MIOSA: sandboxes and computers, files and
previews, agents and their runs, deployments, API keys and webhooks.

  miosa login
  miosa create my-box --wait
  miosa exec my-box -- echo hello
  miosa destroy my-box --yes

Anywhere a sandbox is expected you may give its name, its id, "current" (the one
set with 'miosa use') or "self" (inside a sandbox, from MIOSA_SANDBOX_ID).

Output: --json (or -o json) prints machine-readable JSON; streams print one
JSON object per line. When standard output is a pipe or a file, JSON is the
default; -o text or MIOSA_OUTPUT=text keeps text. Commands that print raw
content (exec, ssh, logs, files cat, api, ...) always keep text. Errors go to
stderr with the API error code, and in JSON mode as {"error":{...}}.

Exit codes: 0 ok, 1 error, 2 usage, 3 not signed in or forbidden, 4 not found,
5 conflict or invalid input, 6 rate limited, 7 server or network error. A command
that ran remotely (exec, ssh) exits with the remote command's status.

Environment: MIOSA_API_KEY, MIOSA_BASE_URL, MIOSA_PROFILE, MIOSA_ORG,
MIOSA_BILL_TO, MIOSA_OUTPUT, MIOSA_RETRIES, MIOSA_NO_UPDATE_NOTICE.

Docs: https://miosa.ai/docs/cli`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		applyGlobals(cmd)
		return nil
	},
}

// applyGlobals resolves the flags that depend on other flags or the
// environment: the profile, --json, MIOSA_OUTPUT, a piped standard output and
// MIOSA_RETRIES.
func applyGlobals(cmd *cobra.Command) {
	config.UseProfile(globalFlags.Profile)
	if globalFlags.JSON {
		globalFlags.Output = "json"
	} else if f := cmd.Flags().Lookup("output"); f == nil || !f.Changed {
		if env := os.Getenv("MIOSA_OUTPUT"); env != "" {
			globalFlags.Output = env
		} else if autoJSON(cmd) {
			globalFlags.Output = "json"
		}
	}
	if globalFlags.Retries < 0 {
		if v := os.Getenv("MIOSA_RETRIES"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				globalFlags.Retries = n
			}
		}
	}
}

// Execute runs the root command, prints a failure once (text, or JSON with
// --json), and returns the error. Use ExitCode to turn it into a status.
func Execute() error {
	finalize()
	err := rootCmd.Execute()
	if err != nil {
		writeError(os.Stderr, err, isJSON())
		return err
	}
	maybeNotifyUpdate(os.Stderr)
	maybeAutoUpdate(os.Stderr)
	return nil
}

// Root returns the root Cobra command. Intended for use in tests only.
func Root() *cobra.Command { finalize(); return rootCmd }

// ResetForTest resets global flag state to defaults. Call before each test run
// to prevent flag value leakage between test cases.
func ResetForTest() {
	globalFlags.APIKey = ""
	globalFlags.APIURL = ""
	globalFlags.Output = "text"
	globalFlags.Quiet = false
	globalFlags.Timeout = 60
	globalFlags.Profile = ""
	globalFlags.JSON = false
	globalFlags.Retries = 0 // tests never wait on a retry
	globalFlags.NoUpdateCheck = true
	globalFlags.NoUpdate = true
	autoJSONEnabled = false // go test's stdout is a pipe; tests opt in
	config.UseProfile("")
	// Reset persistent flags on the root command.
	pf := rootCmd.PersistentFlags()
	_ = pf.Set("api-key", "")
	_ = pf.Set("api-url", "")
	_ = pf.Set("output", "text")
	_ = pf.Set("quiet", "false")
	_ = pf.Set("timeout", "60")
	_ = pf.Set("profile", "")
	_ = pf.Set("json", "false")
	_ = pf.Set("org", "")
	_ = pf.Set("bill-to", "")
	actingOrgChecked = false
	whereItLandsShown = false
	failFast = false
	_ = pf.Set("retries", "0")
	_ = pf.Set("no-update", "true")
	// Reset local flags on all subcommands so flag values don't leak between tests.
	resetCommandFlags(rootCmd)
	pf.VisitAll(func(f *pflag.Flag) { f.Changed = false })
	globalFlags.Retries = 0 // after the flag reset above, which restores the -1 default
}

// resetCommandFlags walks the command tree and resets every flag to its default.
func resetCommandFlags(cmd *cobra.Command) {
	// A parsed "--" leaves ArgsLenAtDash set on the flag set, so a later
	// `exec` with no args would slice by the earlier count. Init clears it
	// (cobra names the set after the command and continues on error).
	cmd.Flags().Init(cmd.Name(), pflag.ContinueOnError)
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Changed {
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				_ = sv.Replace(nil) // Set(DefValue) would append "[]" to a slice flag
			} else {
				_ = f.Value.Set(f.DefValue)
			}
			f.Changed = false
		}
	})
	for _, sub := range cmd.Commands() {
		resetCommandFlags(sub)
	}
}

func init() {
	rootCmd.SetVersionTemplate("miosa version {{.Version}}\n")
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&globalFlags.APIKey, "api-key", "", "API key (overrides MIOSA_API_KEY and config)")
	pf.StringVar(&globalFlags.APIURL, "api-url", "", "API base URL (overrides MIOSA_BASE_URL and config)")
	pf.StringVarP(&globalFlags.Output, "output", "o", "text", "Output format: text or json")
	pf.BoolVarP(&globalFlags.Quiet, "quiet", "q", false, "Suppress informational output")
	pf.IntVar(&globalFlags.Timeout, "timeout", 60, "Request timeout in seconds (0 = no timeout); for exec also the command timeout (over 300 or 0 streams the output), for ssh 1-300")
	pf.StringVar(&globalFlags.Profile, "profile", "", "Config profile to use (env: MIOSA_PROFILE)")
	pf.BoolVar(&globalFlags.JSON, "json", false, "Print machine-readable JSON (same as --output json)")
	pf.IntVar(&globalFlags.Retries, "retries", -1, "Retries on 429 and transient 5xx errors (default 3, env: MIOSA_RETRIES)")
	pf.BoolVar(&globalFlags.NoUpdateCheck, "no-update-notice", false, "Silence the new-version notice (env: MIOSA_NO_UPDATE_NOTICE=1)")
	pf.BoolVar(&globalFlags.NoUpdate, "no-update", false, "Skip the automatic update for this run, when it is on (env: MIOSA_NO_UPDATE=1)")

	// Register all subcommands.
	rootCmd.AddCommand(
		newLoginCmd(),
		newOnboardCmd(),
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
		newMoveCmd(),
		newModuleCmd(),
		newCheckpointCmd(),
		newRestoreCmd(),
		newDaemonCmd(),
		serviceCmd(),
		newPolicyCmd(),
		newCatalogCmd(),
		newRuntimesCmd(),
		newBrowserCmd(),
		newForgeCmd(),
		newAPICmd(),
		newEnvCmd(),
		newInfoCmd(),
		newComputerCmd(),
		newEngineCmd(),
		newSSHCmd(),
		newUpdateCmd(),
		newVersionCmd(),
		newWhoamiCmd(),
		newProfileCmd(),
		newConfigCmd(),
		newDoctorCmd(),
		newOpenCmd(),
		newDashboardCmd(),
		newSandboxCmd(),
		newAdminCmd(),
		newLinkCmd(),
		newUnlinkCmd(),
		newVariablesCmd(),
		newUpCmd(),
		newRedeployCmd(),
		newRollbackCmd(),
		newDomainCmd(),
		newEnvironmentCmd(),
		newMiddlewareCmd(),
		newDrainsCmd(),
		newProtectionCmd(),
		newWAFCmd(),
	)
}

// buildClient constructs the API client from global flags + config and
// requires a key, so a signed-out run fails with one clear message instead of
// a 401 from whichever endpoint it happened to call first.
func buildClient() (*client.Client, config.Config, error) {
	return buildClientTimeout(0)
}

// buildClientTimeout is buildClient with an explicit HTTP timeout for the SDK
// client. Zero keeps the SDK default.
func buildClientTimeout(d time.Duration) (*client.Client, config.Config, error) {
	c, cfg, err := buildPublicClientTimeout(d)
	if err != nil {
		return nil, cfg, err
	}
	if err := c.MustAuthenticated(); err != nil {
		return nil, cfg, err
	}
	if err := checkActingOrg(context.Background(), c); err != nil {
		return nil, cfg, die(err)
	}
	return c, cfg, nil
}

// buildPublicClient is buildClient for endpoints that need no key (the
// catalog, health, device login).
func buildPublicClient() (*client.Client, config.Config, error) {
	return buildPublicClientTimeout(0)
}

func buildPublicClientTimeout(d time.Duration) (*client.Client, config.Config, error) {
	client.UserAgent = "miosa-cli/" + cliVersion
	return client.New(client.ResolveOptions{
		APIKey:  globalFlags.APIKey,
		APIURL:  globalFlags.APIURL,
		Timeout: d,
		Retries: globalFlags.Retries,
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

// Help groups. Commands not listed here appear under "Additional Commands".
var helpGroups = []struct {
	ID, Title string
	Commands  []string
}{
	{"account", "Account and setup:", []string{"login", "onboard", "logout", "whoami", "profile", "config", "doctor", "update", "version", "open", "org", "limits", "data-retention", "feedback"}},
	{"machines", "Sandboxes and computers:", []string{"create", "list", "info", "use", "destroy", "pause", "resume", "restart", "stop", "recover", "fork", "extend", "tag",
		"exec", "ssh", "console", "files", "push", "logs", "events", "usage", "wait", "ports", "preview", "process", "desktop", "computer", "daemon", "services", "policy", "proxy", "url", "env", "snapshot", "checkpoint", "restore", "catalog", "runtimes", "device", "sandbox"}},
	{"agents", "Agents:", []string{"prompt", "steer", "interrupt", "conversations", "run", "chat", "agent", "connections"}},
	{"platform", "Platform:", []string{"api-key", "webhook", "deploy", "template", "workspace", "move", "billing", "audit", "forge", "api", "mcp"}},
}

// sandboxArgCommands take a sandbox as their first argument.
var sandboxArgCommands = map[string]bool{
	"info": true, "use": true, "destroy": true, "exec": true, "ssh": true, "console": true, "url": true,
	"pause": true, "resume": true, "restart": true, "stop": true, "recover": true, "fork": true, "extend": true, "tag": true,
	"logs": true, "events": true, "usage": true, "wait": true, "ports": true, "proxy": true, "restore": true,
}

var finalizeOnce sync.Once

// finalize runs once after every init() has registered its commands.
func finalize() { finalizeOnce.Do(organizeCommands) }

// organizeCommands assigns help groups and shell completion.
func organizeCommands() {
	present := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		present[c.Name()] = true
	}
	for _, g := range helpGroups {
		for _, name := range g.Commands {
			if present[name] {
				rootCmd.AddGroup(&cobra.Group{ID: g.ID, Title: g.Title})
				break
			}
		}
	}
	for _, c := range rootCmd.Commands() {
		for _, g := range helpGroups {
			for _, name := range g.Commands {
				if c.Name() == name {
					c.GroupID = g.ID
				}
			}
		}
		if sandboxArgCommands[c.Name()] && c.ValidArgsFunction == nil {
			c.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
				if len(args) > 0 {
					return nil, cobra.ShellCompDirectiveNoFileComp
				}
				return completeKind(cmd, "sandbox", toComplete)
			}
		}
	}
}
