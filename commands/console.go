package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newConsoleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "console [name|id]",
		Short: "Open an interactive shell in a sandbox",
		Long: `Open a full-duplex interactive TTY shell inside a sandbox.

The local terminal is set to raw mode for the duration of the session.
Window resize (SIGWINCH) is forwarded to the remote process.
Ctrl-C sends SIGINT to the remote process.

Example:
  miosa console my-box
  miosa console          # uses current sandbox`,
		Args: cobra.MaximumNArgs(1),
		RunE: runConsole,
	}
}

func runConsole(cmd *cobra.Command, args []string) error {
	_, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID := ""
	if len(args) > 0 {
		nameOrID = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return die(fmt.Errorf("console requires a TTY"))
	}
	_ = nameOrID

	return die(sandboxNativeFeatureUnavailable(
		"sandbox interactive console",
		"use the web terminal or POST /api/v1/sandboxes/{id}/terminal until the CLI native terminal relay is implemented",
	))
}
