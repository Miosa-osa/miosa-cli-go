package commands

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
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

	target, isComputer := resolveExecTarget(cmd, c, nameOrID)

	var stdout, stderr string
	var exitCode int

	if isComputer {
		// A computer runs commands at its own route; the sandbox route answers
		// 404 for a computer id.
		var res struct {
			ExitCode int    `json:"exit_code"`
			Stdout   string `json:"stdout"`
			Stderr   string `json:"stderr"`
			Output   string `json:"output"`
		}
		body := map[string]any{"command": in.Command}
		if in.Cwd != "" {
			body["cwd"] = in.Cwd
		}
		if in.TimeoutSeconds > 0 {
			body["timeout"] = in.TimeoutSeconds
		}
		if err := c.API.Post(cmd.Context(), "/computers/"+url.PathEscape(target)+"/exec", body, &res); err != nil {
			return execFailure(cmd, err, timeout)
		}
		exitCode, stdout, stderr = res.ExitCode, res.Stdout, res.Stderr
		if stdout == "" {
			stdout = res.Output
		}
	} else {
		res, err := c.SDK.Commands.Run(cmd.Context(), lookupComputerID(target), in)
		if err != nil {
			return execFailure(cmd, err, timeout)
		}
		exitCode, stdout, stderr = res.ExitCode, res.Stdout, res.Stderr
		if stdout == "" {
			stdout = res.Output
		}
	}

	fmt.Fprint(cmd.OutOrStdout(), stdout)
	if stderr != "" {
		fmt.Fprint(cmd.ErrOrStderr(), stderr)
	}
	if exitCode != 0 {
		return &ExitError{Code: exitCode}
	}
	return nil
}

// execTimeoutCode is the exit status when the command did not finish in time
// (the same 124 as timeout(1)).
const execTimeoutCode = 124

// execFailure reports a failed exec. When the request ran out of time, the CLI
// stops waiting and exits 124 with a clear line, even if a background child
// still holds the command's output open on the machine.
func execFailure(cmd *cobra.Command, err error, timeout int) error {
	if timeout > 0 && isTimeoutErr(err) {
		fmt.Fprintf(cmd.ErrOrStderr(), "miosa: the command did not finish within %ds; stopped waiting (it may still be running on the machine)\n", timeout)
		return &ExitError{Code: execTimeoutCode}
	}
	return die(err)
}

func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// resolveExecTarget says which machine a reference names and so which route
// runs its commands: a sandbox name wins where both exist, because exec began
// as a sandbox command; then a computer name, a computer's short id, or a
// computer's full id. A reference neither knows is left to the sandbox route,
// which answers for it as it always did.
func resolveExecTarget(cmd *cobra.Command, c *client.Client, ref string) (string, bool) {
	if api.IsUUID(ref) {
		if c.API.Get(cmd.Context(), "/computers/"+url.PathEscape(ref), nil, nil) == nil {
			return ref, true
		}
		return ref, false
	}

	if id, err := c.API.ResolveSandbox(cmd.Context(), ref); err == nil && id != ref {
		return id, false
	}
	if id, err := c.API.ResolveComputer(cmd.Context(), ref); err == nil && id != ref {
		return id, true
	}
	return ref, false
}

// execRequestTimeout is the HTTP timeout for a command: the command's own
// timeout plus headroom, never below the default request timeout.
func execRequestTimeout(cmdTimeout int) time.Duration {
	d := time.Duration(globalFlags.Timeout) * time.Second
	if want := time.Duration(cmdTimeout+5) * time.Second; cmdTimeout > 0 && want > d {
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
