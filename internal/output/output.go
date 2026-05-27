// Package output provides consistent TTY vs JSON rendering for CLI commands.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

// Format is the output format selected by the --output flag.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Printer writes command output to an io.Writer with format awareness.
type Printer struct {
	w      io.Writer
	format Format
	quiet  bool
}

// New returns a Printer that writes to w.
func New(w io.Writer, format Format, quiet bool) *Printer {
	return &Printer{w: w, format: format, quiet: quiet}
}

// Default returns a Printer targeting os.Stdout with text format.
func Default() *Printer {
	return New(os.Stdout, FormatText, false)
}

// JSON prints v as indented JSON unconditionally (used when --json flag is set).
func (p *Printer) JSON(v interface{}) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Table prints a table to stdout. headers and rows must have the same number of
// columns. Use this when format is text; callers should call JSON for json mode.
func (p *Printer) Table(headers []string, rows [][]string) {
	if p.quiet {
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	fmt.Fprintln(tw, strings.Join(repeat("─", len(headers)), "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	tw.Flush()
}

// Line prints a plain text line unless quiet is set.
func (p *Printer) Line(format string, args ...interface{}) {
	if p.quiet {
		return
	}
	fmt.Fprintf(p.w, format+"\n", args...)
}

// Success prints a success line prefixed with a checkmark (text mode only).
func (p *Printer) Success(format string, args ...interface{}) {
	if p.quiet || p.format == FormatJSON {
		return
	}
	fmt.Fprintf(p.w, "ok  "+format+"\n", args...)
}

// Warn prints a warning to stderr.
func Warn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

// Error prints an error message to stderr with the "miosa: " prefix.
func Error(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "miosa: "+format+"\n", args...)
}

// FriendlyError converts SDK errors to user-facing messages and prints them.
// Returns a non-nil error so callers can do: return output.FriendlyError(err).
func FriendlyError(err error) error {
	if err == nil {
		return nil
	}
	msg := friendlyMessage(err)
	fmt.Fprintln(os.Stderr, "miosa: "+msg)
	return fmt.Errorf("%s", msg)
}

// friendlyMessage maps SDK error types to human-readable messages.
func friendlyMessage(err error) string {
	if err == nil {
		return ""
	}
	errStr := err.Error()
	// Authentication errors.
	if strings.Contains(errStr, "status=401") || strings.Contains(errStr, "not authenticated") {
		return "not authenticated (run 'miosa login')"
	}
	// Not found.
	if strings.Contains(errStr, "status=404") {
		return "resource not found"
	}
	// Insufficient credits.
	if strings.Contains(errStr, "status=402") {
		return "insufficient credits (visit https://miosa.ai/billing to top up)"
	}
	// Permission denied.
	if strings.Contains(errStr, "status=403") {
		return "permission denied"
	}
	// Rate limited.
	if strings.Contains(errStr, "status=429") {
		return "rate limit exceeded — please wait and try again"
	}
	// Phase not ready.
	if strings.Contains(errStr, "requires control-plane server") {
		return errStr
	}
	// Connection errors.
	if strings.Contains(errStr, "connection error") {
		return "cannot reach the MIOSA API — check your network connection"
	}
	// Fall back to the raw error but strip the "miosa: " prefix to avoid doubling.
	msg := errStr
	if after, ok := strings.CutPrefix(msg, "miosa: "); ok {
		msg = after
	}
	return msg
}

// ParseFormat parses the --output flag value. Returns an error for invalid values.
func ParseFormat(s string) (Format, error) {
	switch Format(strings.ToLower(s)) {
	case FormatText, "":
		return FormatText, nil
	case FormatJSON:
		return FormatJSON, nil
	default:
		return FormatText, fmt.Errorf("invalid output format %q: must be text or json", s)
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}
