package commands

import (
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Agents and scripts read the CLI through a pipe, so a command whose standard
// output is not a terminal prints JSON unless the format was chosen (--json,
// -o, MIOSA_OUTPUT). Commands that print raw content keep text: their output is
// the payload itself (a remote command's stdout, a log, a file, an env file, a
// generated document), and wrapping it would break `> file` and `| grep`.

// autoJSONEnabled is off in tests, whose standard output is always a pipe.
var autoJSONEnabled = true

// outputIsTerminal reports whether w is a terminal. Anything that is not a
// file (a test buffer) counts as one, so it never switches the format.
var outputIsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return !ok || term.IsTerminal(int(f.Fd()))
}

// rawOutputNames are command names whose output is raw content wherever they
// sit in the tree (deploy logs, db logs, files cat, vars reveal, ...).
var rawOutputNames = map[string]bool{
	"logs": true, "cat": true, "reveal": true, "connection": true, "export": true,
	"pull": true, "env-pull": true, "url": true, "urls": true, "events": true,
	"completion": true, "help": true, "__docs": true, "__api-map": true,
}

// rawOutputTrees are top-level commands whose whole subtree prints raw
// content or speaks a protocol on standard output (mcp serve).
var rawOutputTrees = map[string]bool{
	"exec": true, "ssh": true, "console": true, "api": true, "mcp": true,
	"proxy": true, "browser": true, "version": true, "update": true,
	"login": true, "logout": true, "open": true,
}

// autoJSON reports whether cmd, with no format chosen, should print JSON
// because its standard output is a pipe or a file.
func autoJSON(cmd *cobra.Command) bool {
	if !autoJSONEnabled || outputIsTerminal(cmd.OutOrStdout()) {
		return false
	}
	return !rawOutputCommand(cmd)
}

// rawOutputCommand reports whether cmd prints raw content rather than a
// result: by its own name, by its top-level command, or `miosa run -- cmd`
// (the one-shot, whose output is the command's).
func rawOutputCommand(cmd *cobra.Command) bool {
	if rawOutputNames[cmd.Name()] {
		return true
	}
	path := strings.Fields(cmd.CommandPath())
	if len(path) < 2 {
		return true // the bare root prints help
	}
	if rawOutputTrees[path[1]] {
		return true
	}
	return len(path) == 2 && path[1] == "run"
}
