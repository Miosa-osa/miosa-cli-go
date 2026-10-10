package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/terminal"
)

func newConsoleCmd() *cobra.Command {
	var (
		shell string
		cwd   string
	)
	cmd := &cobra.Command{
		Use:     "console [name|id] [-- <cmd> [args...]]",
		Aliases: []string{"shell"},
		Short:   "Open an interactive shell in a sandbox",
		Long: `Open a full-duplex interactive terminal (a PTY) in a sandbox over the
miosa-terminal-v1 WebSocket. Your terminal goes into raw mode for the session;
resizing the window resizes the remote PTY and Ctrl-C reaches the remote process.

With a command after -- the terminal runs that program instead of a login shell.
The exit status is the program's. Use 'miosa exec' or 'miosa ssh -- cmd' for
non-interactive commands.

  miosa console my-box
  miosa console              # the current sandbox
  miosa console my-box --cwd /workspace
  miosa console my-box -- htop`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConsole(cmd, args, shell, cwd)
		},
	}
	cmd.Flags().StringVar(&shell, "shell", "", "Shell to start (a single executable, default: the sandbox's login shell)")
	cmd.Flags().StringVar(&cwd, "cwd", "", "Absolute directory to start in")
	return cmd
}

// terminalRequest is the body of POST /sandboxes/:id/terminal.
func terminalRequest(size terminal.Size, shell, cwd string, command []string) map[string]any {
	body := map[string]any{"cols": size.Cols, "rows": size.Rows}
	if cwd != "" {
		body["cwd"] = cwd
	}
	if len(command) > 0 {
		body["command"] = command[0]
		if len(command) > 1 {
			body["args"] = command[1:]
		}
	} else if shell != "" {
		body["shell"] = shell
	}
	return body
}

func runConsole(cmd *cobra.Command, args []string, shell, cwd string) error {
	dash := cmd.ArgsLenAtDash()
	var ref string
	var command []string
	if dash >= 0 {
		if dash > 0 {
			ref = args[0]
		}
		command = args[dash:]
	} else if len(args) > 0 {
		ref = args[0]
		if len(args) > 1 {
			return usagef("a command goes after --: miosa console %s -- %s", ref, args[1])
		}
	}
	if shell != "" && len(command) > 0 {
		return usagef("--shell and a command after -- cannot be combined")
	}
	return openConsole(cmd, ref, shell, cwd, command)
}

// openConsole runs an interactive terminal session in the sandbox ref (empty
// means the current sandbox).
func openConsole(cmd *cobra.Command, ref, shell, cwd string, command []string) error {
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}
	inFile, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(inFile.Fd())) {
		return usagef("console needs an interactive terminal; run a command with 'miosa exec %s -- <cmd>' or pipe a script to 'miosa ssh'", ref)
	}
	fd := int(inFile.Fd())

	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return die(err)
	}
	cols, rows, err := term.GetSize(fd)
	if err != nil || cols <= 0 || rows <= 0 {
		cols, rows = 80, 24
	}
	var sess terminal.Session
	body := terminalRequest(terminal.Size{Cols: cols, Rows: rows}, shell, cwd, command)
	if err := c.API.Post(cmd.Context(), "/sandboxes/"+id+"/terminal", body, &sess); err != nil {
		return die(err)
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	conn, err := terminal.Dial(ctx, sess, c.Key)
	if err != nil {
		return die(err)
	}

	old, err := term.MakeRaw(fd)
	if err != nil {
		conn.Close()
		return die(fmt.Errorf("putting the terminal in raw mode: %w", err))
	}
	restore := func() { _ = term.Restore(fd, old) }
	defer restore()

	resizes := make(chan terminal.Size, 4)
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			if w, h, err := term.GetSize(fd); err == nil {
				select {
				case resizes <- terminal.Size{Cols: w, Rows: h}:
				default:
				}
			}
		}
	}()

	res, err := terminal.Bridge(ctx, conn, inFile, cmd.OutOrStdout(), resizes)
	restore()
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil && !errors.Is(err, context.Canceled) {
		return die(err)
	}
	if res.ExitCode != 0 {
		return &ExitError{Code: res.ExitCode}
	}
	return nil
}
