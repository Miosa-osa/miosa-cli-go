package commands

import (
	"io"
	"strings"
	"testing"
)

// withPipedStdout makes every writer look like a pipe for one test.
func withPipedStdout(t *testing.T) {
	t.Helper()
	prevEnabled, prevTerm := autoJSONEnabled, outputIsTerminal
	autoJSONEnabled = true
	outputIsTerminal = func(io.Writer) bool { return false }
	t.Cleanup(func() { autoJSONEnabled, outputIsTerminal = prevEnabled, prevTerm })
}

// outputFor resolves the output format the way a run of args would.
func outputFor(t *testing.T, args ...string) string {
	t.Helper()
	ResetForTest()
	autoJSONEnabled = true
	finalize()
	cmd, rest, err := rootCmd.Find(args)
	if err != nil {
		t.Fatalf("find %v: %v", args, err)
	}
	// Parse only flags: parsing "--" would leave ArgsLenAtDash set on the
	// shared command tree for later tests.
	var flagArgs []string
	for _, a := range rest {
		if strings.HasPrefix(a, "-") && a != "--" {
			flagArgs = append(flagArgs, a)
		} else if len(flagArgs) > 0 && !strings.HasPrefix(flagArgs[len(flagArgs)-1], "--") {
			flagArgs = append(flagArgs, a) // the value of a short flag (-o text)
		}
	}
	if err := cmd.ParseFlags(flagArgs); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	applyGlobals(cmd)
	return globalFlags.Output
}

func TestAutoJSONWhenStdoutIsPiped(t *testing.T) {
	withPipedStdout(t)
	for _, args := range [][]string{
		{"list"},
		{"deploy", "list"},
		{"service", "list", "proj"},
		{"db", "get", "x"},
		{"whoami"},
		{"run", "list"},
	} {
		if got := outputFor(t, args...); got != "json" {
			t.Errorf("%v piped: output %q, want json", args, got)
		}
	}
}

func TestAutoJSONKeepsTextForRawOutput(t *testing.T) {
	withPipedStdout(t)
	for _, args := range [][]string{
		{"exec"},
		{"ssh", "box"},
		{"logs", "box"},
		{"deploy", "logs", "app"},
		{"db", "logs", "x"},
		{"files", "cat", "box:/etc/hosts"},
		{"api", "/me"},
		{"mcp", "serve"},
		{"run"},
		{"__docs"},
		{"version"},
	} {
		if got := outputFor(t, args...); got != "text" {
			t.Errorf("%v piped: output %q, want text", args, got)
		}
	}
}

func TestAutoJSONRespectsAnExplicitChoice(t *testing.T) {
	withPipedStdout(t)
	if got := outputFor(t, "list", "-o", "text"); got != "text" {
		t.Errorf("-o text piped: output %q, want text", got)
	}
	t.Setenv("MIOSA_OUTPUT", "text")
	if got := outputFor(t, "list"); got != "text" {
		t.Errorf("MIOSA_OUTPUT=text piped: output %q, want text", got)
	}
}

func TestAutoJSONOffOnATerminal(t *testing.T) {
	prevEnabled, prevTerm := autoJSONEnabled, outputIsTerminal
	t.Cleanup(func() { autoJSONEnabled, outputIsTerminal = prevEnabled, prevTerm })
	outputIsTerminal = func(io.Writer) bool { return true }
	if got := outputFor(t, "list"); got != "text" {
		t.Errorf("terminal: output %q, want text", got)
	}
}
