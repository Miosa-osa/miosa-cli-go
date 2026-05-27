package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newExecCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec [name|id] -- <cmd> [args...]",
		Short: "Execute a command in a sandbox",
		Long: `Execute a shell command inside a sandbox and stream the output.

The -- separator is required before the command and its arguments.

When Phase 1 (streaming WebSocket exec) is merged, this command will stream
output in real time. Currently it uses the blocking REST exec endpoint.

Examples:
  miosa exec my-box -- echo hello
  miosa exec -- ls -la /home      # uses current sandbox
  miosa exec my-box -- bash -c 'for i in 1 2 3; do echo $i; done'`,
		// Use ArbitraryArgs so Cobra passes everything (including post-"--") to RunE.
		// We parse the "--" separator ourselves.
		Args:               cobra.ArbitraryArgs,
		DisableFlagParsing: true,
		RunE:               runExec,
	}
	return cmd
}

func runExec(cmd *cobra.Command, args []string) error {
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	// With DisableFlagParsing=true, all tokens come through in args.
	// Strip leading global flags that cobra didn't parse.
	// Parse: miosa exec [name|id] -- <cmd> [args...]
	nameOrID, command, err := parseExecArgs(args)
	if err != nil {
		return die(err)
	}
	if nameOrID == "" {
		nameOrID = cfg.CurrentSandbox
	}
	if nameOrID == "" {
		return die(fmt.Errorf("no sandbox specified and no current sandbox set (run 'miosa use <name>')"))
	}
	if command == "" {
		return die(fmt.Errorf("no command specified — use: miosa exec [name|id] -- <cmd> [args...]"))
	}

	stdout := make(chan string, 64)
	done := make(chan struct{})

	// Write exec output to cmd's out writer so tests can capture it.
	var w io.Writer = os.Stdout
	if cmd != nil {
		w = cmd.OutOrStdout()
	}

	go func() {
		defer close(done)
		for chunk := range stdout {
			fmt.Fprint(w, chunk)
		}
	}()

	exitCode, err := c.Exec.Run(cmd.Context(), lookupComputerID(nameOrID), command, stdout, nil)
	close(stdout)
	<-done

	if err != nil {
		return die(err)
	}

	if exitCode != 0 {
		return fmt.Errorf("process exited with code %d", exitCode)
	}
	return nil
}

// parseExecArgs splits args into (nameOrID, command).
// Handles: exec [name] -- cmd args, exec -- cmd args, exec cmd (no "--", no name).
func parseExecArgs(args []string) (nameOrID, command string, err error) {
	// Find "--" separator.
	sepIdx := -1
	for i, a := range args {
		if a == "--" {
			sepIdx = i
			break
		}
	}

	if sepIdx == -1 {
		// No "--": all args are the command (no sandbox name disambiguation).
		if len(args) == 0 {
			return "", "", nil
		}
		return "", strings.Join(args, " "), nil
	}

	before := args[:sepIdx]
	after := args[sepIdx+1:]

	if len(before) > 0 {
		nameOrID = before[0]
	}
	if len(after) > 0 {
		command = strings.Join(after, " ")
	}
	return nameOrID, command, nil
}
