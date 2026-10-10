package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"
)

func newExecCmd() *cobra.Command {
	var cwd string
	cmd := &cobra.Command{
		Use:   "exec [name|id] -- <cmd> [args...]",
		Short: "Execute a command in a sandbox",
		Long: `Run a command inside a sandbox and print its output. miosa exits with the
remote command's exit code, so it drops straight into scripts and CI.

The -- separator is required before the command and its arguments.

--cwd sets the working directory, relative to the sandbox home directory (or
absolute). The global --timeout also bounds the command: seconds, 1-300, default
30 when not given, 0 means the 300 second maximum.

A sandbox that is still starting refuses commands; exec retries with backoff
until it is ready (up to 90 seconds).

Examples:
  miosa exec my-box -- echo hello
  miosa exec -- ls -la /home      # uses current sandbox
  miosa exec my-box --cwd my-repo --timeout 120 -- npm run build
  miosa exec my-box -- bash -c 'for i in 1 2 3; do echo $i; done'`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args, cwd, commandTimeout(cmd))
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "Working directory, relative to the sandbox home directory")
	return cmd
}

// commandTimeout reads the global --timeout as the command's synchronous
// timeout, but only when the user passed it: 0 here means "use the API default".
// An explicit 0 follows the flag's "no timeout" meaning: the API maximum.
func commandTimeout(cmd *cobra.Command) int {
	f := cmd.Flags().Lookup("timeout")
	if f == nil || !f.Changed {
		return 0
	}
	if globalFlags.Timeout == 0 {
		return miosa.MaxCommandTimeoutSeconds
	}
	return globalFlags.Timeout
}

// ExitError is returned when the remote command exits non-zero. main exits
// with Code so scripts see the remote status.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("process exited with code %d", e.Code) }

func runExec(cmd *cobra.Command, args []string, cwd string, timeout int) error {
	nameOrID, command, err := parseExecArgs(args, cmd.ArgsLenAtDash())
	if err != nil {
		return die(err)
	}
	in := miosa.CommandInput{Command: command, Cwd: cwd, TimeoutSeconds: timeout}
	if command != "" {
		if err := in.Validate(); err != nil {
			return die(err)
		}
	}
	return runInSandbox(cmd, nameOrID, in, timeout)
}

// runInSandbox resolves the sandbox, runs the command, prints stdout and
// stderr, and turns a non-zero exit into an *ExitError.
func runInSandbox(cmd *cobra.Command, nameOrID string, in miosa.CommandInput, timeout int) error {
	c, cfg, err := buildClientTimeout(execRequestTimeout(timeout))
	if err != nil {
		return die(err)
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}
	if in.Command == "" {
		return die(fmt.Errorf("no command specified - use: miosa exec [name|id] -- <cmd> [args...]"))
	}

	res, err := c.SDK.Commands.Run(cmd.Context(), lookupComputerID(nameOrID), in)
	if err != nil {
		return die(err)
	}
	out := res.Stdout
	if out == "" {
		out = res.Output
	}
	fmt.Fprint(cmd.OutOrStdout(), out)
	if res.Stderr != "" {
		fmt.Fprint(cmd.ErrOrStderr(), res.Stderr)
	}
	if res.ExitCode != 0 {
		return &ExitError{Code: res.ExitCode}
	}
	return nil
}

// execRequestTimeout is the HTTP timeout for a command: the command's own
// timeout plus headroom, never below the default request timeout.
func execRequestTimeout(cmdTimeout int) time.Duration {
	d := time.Duration(globalFlags.Timeout) * time.Second
	if want := time.Duration(cmdTimeout+30) * time.Second; cmdTimeout > 0 && want > d {
		d = want
	}
	if cmdTimeout == 0 && d < 90*time.Second {
		d = 90 * time.Second
	}
	return d
}

// parseExecArgs splits args into (nameOrID, command). dashAt is
// cobra's ArgsLenAtDash: the count of args before "--", or -1 if absent.
// Handles: exec [name] -- cmd args, exec -- cmd args, exec cmd (no "--", no name).
func parseExecArgs(args []string, dashAt int) (nameOrID, command string, err error) {
	if dashAt < 0 {
		// No "--": all args are the command (no sandbox name disambiguation).
		if len(args) == 0 {
			return "", "", nil
		}
		return "", strings.Join(args, " "), nil
	}
	before, after := args[:dashAt], args[dashAt:]
	if len(before) > 1 {
		return "", "", fmt.Errorf("expected at most one sandbox name before --, got %d", len(before))
	}
	if len(before) == 1 {
		nameOrID = before[0]
	}
	if len(after) == 0 {
		return nameOrID, "", nil
	}
	return nameOrID, joinCommand(after), nil
}
