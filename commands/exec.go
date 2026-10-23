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
	var ef execFlags
	cmd := &cobra.Command{
		Use:   "exec [name|id] -- <cmd> [args...]",
		Short: "Execute a command in a sandbox, in the foreground or the background",
		Long: `Run a command inside a sandbox and print its output. miosa exits with the
remote command's exit code, so it drops straight into scripts and CI.

The -- separator is required before the command and its arguments.

--cwd sets the working directory, relative to the sandbox home directory (or
absolute). The global --timeout bounds the command in seconds: up to 300 the
command runs as one request (default 30); 0 (no limit, up to a day) or more than
300 streams the output live as the command writes it, and so does --stream.
A command that runs out of time exits 124. Ctrl-C stops following.

--detach starts the command in the background and prints its id; it keeps
running after the CLI exits and its output is kept. Then:
  --attach <id>   print its output as it grows, exit with its code when it ends
  --status <id>   show whether it is running and its exit code
  --kill <id>     stop it

A sandbox that is still starting refuses commands; exec retries with backoff
until it is ready (up to 90 seconds).

Examples:
  miosa exec my-box -- echo hello
  miosa exec -- ls -la /home      # uses current sandbox
  miosa exec my-box --cwd my-repo --timeout 120 -- npm run build
  miosa exec my-box --timeout 0 -- ./long-test-suite.sh
  miosa exec my-box --detach -- npm run train
  miosa exec my-box --attach <id>
  miosa exec my-box -- bash -c 'for i in 1 2 3; do echo $i; done'`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if ef.attach != "" || ef.status != "" || ef.kill != "" {
				return runExecProcessOp(cmd, args, ef)
			}
			if ef.detach || ef.wantsStream(globalFlags.Timeout, timeoutGiven(cmd)) {
				return runExecLong(cmd, args, cwd, ef)
			}
			return runExec(cmd, args, cwd, commandTimeout(cmd))
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "Working directory, relative to the sandbox home directory")
	cmd.Flags().BoolVar(&ef.stream, "stream", false, "Print the output live as the command writes it")
	cmd.Flags().BoolVar(&ef.detach, "detach", false, "Run in the background and print the command's id")
	cmd.Flags().StringVar(&ef.attach, "attach", "", "Follow a background command's output and exit with its code")
	cmd.Flags().StringVar(&ef.status, "status", "", "Show a background command's state")
	cmd.Flags().StringVar(&ef.kill, "kill", "", "Stop a background command")
	cmd.MarkFlagsMutuallyExclusive("detach", "stream", "attach", "status", "kill")
	return cmd
}

func timeoutGiven(cmd *cobra.Command) bool {
	f := cmd.Flags().Lookup("timeout")
	return f != nil && f.Changed
}

// runExecLong runs a command on the streaming route or in the background.
func runExecLong(cmd *cobra.Command, args []string, cwd string, ef execFlags) error {
	nameOrID, command, err := parseExecArgs(args, cmd.ArgsLenAtDash())
	if err != nil {
		return die(err)
	}
	if command == "" {
		return usagef("no command specified - use: miosa exec [name|id] -- <cmd> [args...]")
	}
	if timeoutGiven(cmd) && (globalFlags.Timeout < 0 || globalFlags.Timeout > execStreamMaxSeconds) {
		return usagef("--timeout must be 0 (no limit) to %d seconds", execStreamMaxSeconds)
	}
	c, cfg, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ref, err := requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}
	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return die(err)
	}
	if ef.detach {
		return detachExec(cmd, c.API, ref, id, command, cwd)
	}
	timeout := 0
	if timeoutGiven(cmd) {
		timeout = globalFlags.Timeout
	}
	return streamExec(cmd, c.API, id, command, cwd, timeout)
}

// runExecProcessOp is --attach, --status or --kill on a background command.
func runExecProcessOp(cmd *cobra.Command, args []string, ef execFlags) error {
	if len(args) > 1 || (cmd.ArgsLenAtDash() >= 0 && len(args) > cmd.ArgsLenAtDash()) {
		return usagef("--attach, --status and --kill take a command id, not a command")
	}
	ref := ""
	if len(args) == 1 {
		ref = args[0]
	}
	c, cfg, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}
	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return die(err)
	}
	switch {
	case ef.attach != "":
		return attachExec(cmd, c.API, id, ef.attach)
	case ef.status != "":
		return statusExec(cmd, c.API, id, ef.status)
	}
	return killExec(cmd, c.API, id, ef.kill)
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
	// Say where the command runs before its output: the resource's own org and
	// workspace, not the profile's default.
	kind := "sandbox"
	if isComputer {
		kind = "computer"
	}
	printTargetScope(cmd, c.API, kind, target)

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
