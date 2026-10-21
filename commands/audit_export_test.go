package commands_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// auditPage answers /audit-log from two canned pages, keyed by the cursor.
func auditPage(recent, old time.Time, calls *[]string) func(c call) (int, string) {
	return func(c call) (int, string) {
		if c.Method != http.MethodGet || c.Path != "/audit-log" {
			return http.StatusNotFound, `{"error":"no route in test"}`
		}
		*calls = append(*calls, c.Query)

		if strings.Contains(c.Query, "after=page-2") {
			return http.StatusOK, fmt.Sprintf(
				`{"data":[{"id":"old","ts":%q,"type":"api_key.created"}],"next_cursor":null}`,
				old.Format(time.RFC3339Nano),
			)
		}

		return http.StatusOK, fmt.Sprintf(
			`{"data":[
			 {"id":"a","ts":%q,"type":"sandbox.created"},
			 {"id":"b","ts":%q,"type":"sandbox.destroyed"}],
			 "next_cursor":"page-2"}`,
			recent.Format(time.RFC3339Nano),
			recent.Add(-time.Minute).Format(time.RFC3339Nano),
		)
	}
}

func exportedLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return lines
}

func TestAuditExportWritesJSONLAndStopsAtTheWindow(t *testing.T) {
	now := time.Now().UTC()
	var calls []string
	newFakeAPI(t, auditPage(now.Add(-time.Hour), now.Add(-120*24*time.Hour), &calls))

	out, err := run(t, "audit", "export")
	if err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}

	lines := exportedLines(out)
	if len(lines) != 2 {
		t.Fatalf("exported %d lines, want 2 (the third event is outside 90d):\n%s", len(lines), out)
	}

	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line is not JSON: %v", err)
	}
	if first["id"] != "a" {
		t.Fatalf("first event = %v, want a", first["id"])
	}

	// The old page must have been requested with the cursor from page 1.
	if len(calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(calls))
	}
	if !strings.Contains(calls[1], "after=page-2") {
		t.Fatalf("second call = %q, want the cursor", calls[1])
	}
}

func TestAuditExportHonoursAShortWindow(t *testing.T) {
	now := time.Now().UTC()
	var calls []string
	newFakeAPI(t, auditPage(now.Add(-2*time.Hour), now.Add(-3*time.Hour), &calls))

	out, err := run(t, "audit", "export", "--since", "1h")
	if err != nil {
		t.Fatalf("export: %v\n%s", err, out)
	}

	if lines := exportedLines(out); len(lines) != 0 {
		t.Fatalf("exported %d lines, want 0 for a one hour window:\n%s", len(lines), out)
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1: the first event already ends the window", len(calls))
	}
}

func TestAuditExportRejectsABadWindow(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) { return http.StatusOK, `{"data":[]}` })

	out, err := run(t, "audit", "export", "--since", "soon")
	if err == nil {
		t.Fatalf("want a usage error for --since soon:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Fatalf("error = %v", err)
	}
}
