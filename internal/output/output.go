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

// Writer returns the underlying writer.
func (p *Printer) Writer() io.Writer { return p.w }

// JSON prints v as indented JSON unconditionally.
func (p *Printer) JSON(v interface{}) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// JSONLine prints v as one compact JSON line (for streams).
func (p *Printer) JSONLine(v interface{}) error {
	return json.NewEncoder(p.w).Encode(v)
}

// Table prints a compact table: a header row and aligned columns, no rules.
// Empty cells show "-" so columns stay aligned and awk-able.
func (p *Printer) Table(headers []string, rows [][]string) {
	if p.quiet {
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, c := range row {
			if c == "" {
				c = "-"
			}
			cells[i] = c
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	tw.Flush()
}

// Fields prints "Label:  value" pairs with aligned values. Rows with an empty
// value are skipped.
func (p *Printer) Fields(rows [][2]string) {
	if p.quiet {
		return
	}
	width := 0
	for _, r := range rows {
		if r[1] != "" && len(r[0]) > width {
			width = len(r[0])
		}
	}
	for _, r := range rows {
		if r[1] == "" {
			continue
		}
		fmt.Fprintf(p.w, "%-*s  %s\n", width+1, r[0]+":", r[1])
	}
}

// Line prints a plain text line unless quiet is set.
func (p *Printer) Line(format string, args ...interface{}) {
	if p.quiet {
		return
	}
	fmt.Fprintf(p.w, format+"\n", args...)
}

// Success prints a success line prefixed with "ok" (text mode only).
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
