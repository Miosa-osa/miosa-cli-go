package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	miosa "github.com/Miosa-osa/miosa-go"
)

func newSSHCmd() *cobra.Command {
	var cwd string
	var viaAPI bool
	cmd := &cobra.Command{
		Use:   "ssh [name|id] [-- <cmd> [args...]]",
		Short: "Open an SSH session in a sandbox, or run a command over it",
		Long: `Connect to a sandbox with the system ssh client. miosa makes a one-time key,
asks the API for a short-lived certificate for it (15 minutes) that only this
sandbox accepts, and runs ssh through MIOSA's authenticated tunnel. Where the
server has no certificate authority it authorizes the one-time key in the
sandbox instead. The key, certificate and host file live in a private temporary
directory that is removed when ssh ends. Nothing needs setting up in ~/.ssh.

With no command and a terminal you get a login shell. Standard input is piped
through, so a script that is not on the sandbox yet runs without copying it:

  miosa ssh my-box -- bash -s < ./setup.sh
  cat ./setup.sh | miosa ssh my-box -- bash -s
  miosa ssh my-box < ./setup.sh          # no command: runs 'bash -s'
  miosa ssh my-box -- bash -lc "cd my-repo && npm test"

With several arguments after -- each one is quoted, so 'bash -lc "a && b"'
reaches the sandbox as written. miosa exits with the remote command's exit code.

When ssh or ssh-keygen is not installed, or with --api, the command runs over
the MIOSA API instead of an SSH connection: standard input is then read to the
end and sent with the command, so it is limited to 80KB, and the global
--timeout bounds the command (1-300 seconds, default 30). Without a command and
with a terminal on stdin that mode opens 'miosa console'.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSSH(cmd, args, cwd, commandTimeout(cmd))
		},
	}
	cmd.Flags().StringVar(&cwd, "cwd", "", "Working directory, relative to the sandbox home directory")
	cmd.Flags().BoolVar(&viaAPI, "api", false, "Run over the MIOSA API instead of an SSH connection")
	return cmd
}

func runSSH(cmd *cobra.Command, args []string, cwd string, timeout int) error {
	nameOrID, command, err := parseSSHArgs(args, cmd.ArgsLenAtDash())
	if err != nil {
		return die(err)
	}
	if viaAPI, _ := cmd.Flags().GetBool("api"); !viaAPI {
		if sshBin, keygenBin, ok := lookSSH(); ok {
			tty := false
			if command == "" && !stdinIsTerminal(cmd) {
				command = "bash -s"
			}
			if cwd != "" {
				if command == "" {
					command, tty = `cd `+miosa.ShellQuote(cwd)+` && exec "$SHELL" -l`, true // a login shell there
				} else {
					command = "cd " + miosa.ShellQuote(cwd) + " && " + command
				}
			}
			return runSSHClient(cmd, nameOrID, command, tty, sshBin, keygenBin)
		}
	}
	stdin, piped, err := readPipedStdin(cmd.InOrStdin())
	if err != nil {
		return die(err)
	}
	if command == "" {
		if !piped {
			// No command and a terminal on stdin: this is a login shell.
			cwdAbs := cwd
			if cwdAbs != "" && !strings.HasPrefix(cwdAbs, "/") {
				cwdAbs = "" // the terminal API takes an absolute path; start in the default directory
			}
			return openConsole(cmd, nameOrID, "", cwdAbs, nil)
		}
		command = "bash -s"
	}
	in := miosa.CommandInput{Command: command, Cwd: cwd, TimeoutSeconds: timeout}
	if piped {
		in.Stdin = stdin
	}
	if err := in.Validate(); err != nil {
		return die(err)
	}
	return runInSandbox(cmd, nameOrID, in, timeout)
}

// parseSSHArgs is parseExecArgs, except that without "--" the first argument
// is the sandbox and the rest is the command: miosa ssh my-box "cd app && ls".
func parseSSHArgs(args []string, dashAt int) (nameOrID, command string, err error) {
	if dashAt >= 0 || len(args) == 0 {
		return parseExecArgs(args, dashAt)
	}
	return args[0], joinCommand(args[1:]), nil
}

// readPipedStdin reads stdin to the end unless it is an interactive terminal
// or the null device. piped reports whether any stdin was taken. It reads at
// most miosa.MaxStdinBytes+1 so an oversized input is refused, not buffered.
func readPipedStdin(r io.Reader) (data []byte, piped bool, err error) {
	if f, ok := r.(*os.File); ok {
		if term.IsTerminal(int(f.Fd())) {
			return nil, false, nil
		}
		if fi, serr := f.Stat(); serr == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return nil, false, nil
		}
	}
	data, err = io.ReadAll(io.LimitReader(r, int64(miosa.MaxStdinBytes)+1))
	if err != nil {
		return nil, false, fmt.Errorf("reading stdin: %w", err)
	}
	if len(data) > miosa.MaxStdinBytes {
		return nil, false, errors.New("stdin is larger than 80KB; copy the file with 'miosa files cp' and run it instead")
	}
	return data, true, nil
}

var safeWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// joinCommand builds the shell command from the words after "--". A single
// word is a shell string and passes through as written. Several words are an
// argv: each is quoted unless it is already shell-safe.
func joinCommand(words []string) string {
	if len(words) == 0 {
		return ""
	}
	if len(words) == 1 {
		return words[0]
	}
	out := make([]string, len(words))
	for i, w := range words {
		if safeWord.MatchString(w) {
			out[i] = w
		} else {
			out[i] = miosa.ShellQuote(w)
		}
	}
	return strings.Join(out, " ")
}
