package commands_test

import (
	"strings"
	"testing"
)

const engineBackupsJS = `{"data":[{"id":"778439c0-0000-0000-0000-000000000001","state":"completed","backup_type":"full","size_bytes":135950,"created_at":"2026-10-19T16:47:15Z"},{"id":"778439c0-0000-0000-0000-000000000002","state":"failed","backup_type":"full","size_bytes":0,"created_at":"2026-10-18T16:47:15Z"}]}`

func TestEngineBackupResolvesTheNameAndStartsABackup(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
		"POST /databases/" + engineID + "/backups": resp{201, `{"data":{"id":"778439c0-0000-0000-0000-000000000001","state":"running","backup_type":"full","size_bytes":0,"created_at":"2026-10-20T10:00:00Z"}}`},
	})

	out, err := run(t, "engine", "backup", "qa-brain")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "backup 778439c0") || !strings.Contains(out, "running") {
		t.Fatalf("output = %s", out)
	}
	if posts := f.find("POST", "/databases/"+engineID+"/backups"); len(posts) != 1 {
		t.Fatalf("posts = %v", posts)
	}
}

func TestEngineBackupsListsStates(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
		"GET /databases/" + engineID + "/backups": engineBackupsJS,
	})

	out, err := run(t, "engine", "backups", "qa-brain")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"778439c0", "completed", "failed", "135950"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	if gets := f.find("GET", "/databases/"+engineID+"/backups"); len(gets) != 1 {
		t.Fatalf("gets = %v", gets)
	}
}

func TestEngineRmDeletesWithYes(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":                engineListJS,
		"DELETE /databases/" + engineID: `{"data":{"state":"destroyed"}}`,
	})

	out, err := run(t, "engine", "rm", "qa-brain", "--yes")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "qa-brain deleted") {
		t.Fatalf("output = %s", out)
	}
	if deletes := f.find("DELETE", "/databases/"+engineID); len(deletes) != 1 {
		t.Fatalf("deletes = %v", deletes)
	}
}

func TestEngineRmOffTerminalNeedsYes(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases": engineListJS,
	})

	out, err := run(t, "engine", "rm", "qa-brain")
	if err == nil {
		t.Fatalf("a non-interactive delete without --yes must refuse:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("error = %v", err)
	}
	if deletes := f.find("DELETE", "/databases/"+engineID); len(deletes) != 0 {
		t.Fatalf("nothing must be deleted: %v", deletes)
	}
}

func TestEngineRecoverIsVisibleAndTheRetiredSpellingStaysHidden(t *testing.T) {
	// "restore" is a retired command word; the visible command is "recover"
	// and the old spelling stays as a hidden deprecated command.
	out, err := run(t, "engine", "recover", "--help")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "recover <engine>") {
		t.Fatalf("engine recover help = %s", out)
	}

	old, err := run(t, "engine", "restore", "--help")
	if err != nil {
		t.Fatalf("%v\n%s", err, old)
	}
	if !strings.Contains(old, "deprecated") && !strings.Contains(old, "Deprecated") {
		t.Fatalf("engine restore must be a deprecated command:\n%s", old)
	}
}
