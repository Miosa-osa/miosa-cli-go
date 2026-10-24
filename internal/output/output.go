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

// JSONMode reports whether the caller asked for machine-readable output, so a
// custom command can choose JSON over a table without re-reading the flag.
func (p *Printer) JSONMode() bool { return p.format == FormatJSON }

// JSON prints v as indented JSON inside one {"data": ...} envelope (see
// Envelope), unconditionally.
func (p *Printer) JSON(v interface{}) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	// URLs keep their "&" (not "\u0026"): people read this output too.
	enc.SetEscapeHTML(false)
	return enc.Encode(Envelope(v))
}

// Envelope gives every --json result one shape: an object with "data" (tester
// item 14, 2026-10-16: deploy list, releases, builds and env repeated the rows
// under a second key, observe and requests had no data, org list was a bare
// array). Anything without "data" becomes the value of "data". Beside an
// existing "data", "ok", copies of the data (deployments, builds, ...) and
// counts equal to its length are dropped; other keys move into "data" when it
// is an object, and stay beside it (pagination, for example) when it is a
// list. An error body (an object with "error") is printed as it is.
func Envelope(v interface{}) interface{} {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var generic interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return v
	}
	m, ok := generic.(map[string]interface{})
	if !ok {
		return map[string]interface{}{"data": generic}
	}
	if _, isError := m["error"]; isError {
		return m
	}
	data, has := m["data"]
	if !has {
		return map[string]interface{}{"data": m}
	}
	dataJSON, _ := json.Marshal(data)
	list, isList := data.([]interface{})
	obj, isObj := data.(map[string]interface{})
	out := map[string]interface{}{"data": data}
	for k, val := range m {
		if k == "data" || k == "ok" {
			continue
		}
		if b, _ := json.Marshal(val); string(b) == string(dataJSON) {
			continue // the same rows under a second key
		}
		// The same rows under a second key, serialized slightly differently
		// (deploy releases sends data and releases): one list is enough.
		if other, isOtherList := val.([]interface{}); isOtherList && isList && len(other) == len(list) {
			continue
		}
		if n, isNum := val.(float64); isNum && isList && (k == "total" || k == "count") && int(n) == len(list) {
			continue
		}
		if isObj {
			if _, taken := obj[k]; !taken {
				obj[k] = val
			}
			continue
		}
		out[k] = val
	}
	return out
}

// JSONLine prints v as one compact JSON line (for streams).
func (p *Printer) JSONLine(v interface{}) error {
	enc := json.NewEncoder(p.w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
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
