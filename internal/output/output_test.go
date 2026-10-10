package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestTableIsCompactAndFillsEmptyCells(t *testing.T) {
	var b bytes.Buffer
	New(&b, FormatText, false).Table([]string{"NAME", "ID"}, [][]string{{"web", "1"}, {"", "22"}})
	got := b.String()
	if strings.Contains(got, "─") {
		t.Fatalf("no rule line expected:\n%s", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "-") {
		t.Fatalf("lines = %q", lines)
	}
}

func TestFieldsAlignAndSkipEmpty(t *testing.T) {
	var b bytes.Buffer
	New(&b, FormatText, false).Fields([][2]string{{"Name", "web"}, {"Skipped", ""}, {"Longer label", "x"}})
	got := b.String()
	if strings.Contains(got, "Skipped") {
		t.Fatalf("empty rows must be skipped:\n%s", got)
	}
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if strings.Index(lines[0], "web") != strings.Index(lines[1], "x") {
		t.Fatalf("values not aligned:\n%s", got)
	}
}

func TestQuietSuppressesChatterButNotJSON(t *testing.T) {
	var b bytes.Buffer
	p := New(&b, FormatText, true)
	p.Line("hello")
	p.Success("ok")
	p.Table([]string{"A"}, [][]string{{"1"}})
	if b.Len() != 0 {
		t.Fatalf("quiet printed %q", b.String())
	}
	if err := p.JSON(map[string]int{"a": 1}); err != nil || !strings.Contains(b.String(), `"a": 1`) {
		t.Fatalf("JSON must print even when quiet: %q %v", b.String(), err)
	}
	b.Reset()
	if err := p.JSONLine(map[string]int{"a": 1}); err != nil || b.String() != "{\"a\":1}\n" {
		t.Fatalf("JSONLine = %q", b.String())
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"": FormatText, "text": FormatText, "JSON": FormatJSON, "json": FormatJSON} {
		if got, err := ParseFormat(in); err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseFormat("yaml"); err == nil {
		t.Error("yaml must be rejected")
	}
}
