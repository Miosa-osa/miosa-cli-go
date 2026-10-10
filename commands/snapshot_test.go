package commands_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const snapUUID = "9a2e5b1c-0d4f-4a77-8b3e-1c2d3e4f5a6b"

func jsonReply(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func apiError(w http.ResponseWriter, status int, code, message string, details interface{}) {
	e := map[string]interface{}{"code": code, "message": message}
	if details != nil {
		e["details"] = details
	}
	jsonReply(w, status, map[string]interface{}{"ok": false, "error": e})
}

// sandboxLookup answers GET /sandboxes/<ref> for any ref.
func sandboxLookup(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/") {
		jsonReply(w, 200, map[string]interface{}{"data": map[string]interface{}{"id": snapUUID, "name": "my-box"}})
		return true
	}
	return false
}

func snapshotServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sandboxLookup(w, r) {
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	cleanup := setupEnv(t, srv)
	t.Cleanup(cleanup)
	t.Cleanup(commands.SetSnapshotPollInterval(5 * time.Millisecond))
}

func TestSnapshotList_AcrossMachinesAndSearch(t *testing.T) {
	var gotQuery string
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/snapshots" {
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.RawQuery
		jsonReply(w, 200, map[string]interface{}{"data": []map[string]interface{}{{
			"id": "snap-1", "status": "ready", "size_bytes": 1536, "created_at": "2026-10-09T18:00:00Z",
			"resource": map[string]interface{}{"type": "sandbox", "id": snapUUID, "name": "builder", "state": "paused"},
		}}})
	})

	out, err := run(t, "snapshot", "list", "--search", "build")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	for _, want := range []string{"snap-1", "builder (sandbox, paused)", "ready", "1.5 KiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if gotQuery != "q=build" {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestSnapshotList_OneMachineResolvesTheName(t *testing.T) {
	var gotQuery string
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		jsonReply(w, 200, map[string]interface{}{"data": []interface{}{}})
	})
	out, err := run(t, "snapshot", "ls", "my-box")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "resource_id="+snapUUID {
		t.Errorf("query = %q", gotQuery)
	}
	if !strings.Contains(out, "No snapshots.") {
		t.Errorf("expected the empty state, got %q", out)
	}
}

func TestSnapshotNamed_ShowsAllowanceAndEmptyState(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"data":      []interface{}{},
			"allowance": map[string]interface{}{"included": 10, "used": 0, "billable": 0, "monthly_price_cents": 170},
		})
	})
	out, err := run(t, "snapshot", "named")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No named snapshots.") || !strings.Contains(out, "0 named snapshots on your account - 10 free") {
		t.Errorf("got %q", out)
	}
}

func TestSnapshotNamed_BillableLine(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"data": []map[string]interface{}{{"name": "web-stack", "status": "ready", "snapshot_id": "s1", "size_bytes": 2048,
				"source": map[string]interface{}{"name": "my-box"}}},
			"allowance": map[string]interface{}{"included": 10, "used": 11, "billable": 1, "monthly_price_cents": 170},
		})
	})
	out, _ := run(t, "snapshot", "named")
	if !strings.Contains(out, "web-stack") || !strings.Contains(out, "1 above the free allowance at $1.70 a month each") {
		t.Errorf("got %q", out)
	}
}

func TestSnapshotSave_WaitsUntilReady(t *testing.T) {
	var posted map[string]string
	var polls int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/named-snapshots":
			_ = json.NewDecoder(r.Body).Decode(&posted)
			jsonReply(w, 201, map[string]interface{}{"data": map[string]interface{}{"name": "web-stack", "status": "saving"}})
		case r.Method == http.MethodGet && r.URL.Path == "/named-snapshots/web-stack":
			status := "saving"
			if atomic.AddInt32(&polls, 1) >= 3 {
				status = "ready"
			}
			jsonReply(w, 200, map[string]interface{}{"data": map[string]interface{}{"name": "web-stack", "status": status}})
		default:
			http.NotFound(w, r)
		}
	})
	out, err := run(t, "snapshot", "save", "my-box", "web-stack")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if posted["sandbox_id"] != snapUUID || posted["name"] != "web-stack" {
		t.Errorf("posted = %v", posted)
	}
	if polls < 3 || !strings.Contains(out, `Saved "web-stack" (ready)`) {
		t.Errorf("polls=%d out=%q", polls, out)
	}
}

func TestSnapshotSave_ExistingSnapshotNeedsOnlyAName(t *testing.T) {
	var posted map[string]string
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&posted)
		jsonReply(w, 201, map[string]interface{}{"data": map[string]interface{}{"name": "pin", "status": "ready"}})
	})
	if _, err := run(t, "snapshot", "save", "--snapshot", "snap-9", "pin"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if posted["snapshot_id"] != "snap-9" || posted["sandbox_id"] != "" {
		t.Errorf("posted = %v", posted)
	}
	if _, err := run(t, "snapshot", "save", "pin"); err == nil {
		t.Error("expected an error for a missing machine argument")
	}
}

func TestSnapshotSave_ExplainsRefusals(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		apiError(w, 402, "CREDIT_REQUIRED", "Named snapshots beyond the free allowance need credit.", nil)
	})
	_, err := run(t, "snapshot", "save", "my-box", "eleventh")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "$1.70 a month") || !strings.Contains(err.Error(), "miosa billing") {
		t.Errorf("a refusal must say what a name costs and where to add credit: %v", err)
	}
}

func TestSnapshotSave_WarnsBeforeAPaidName(t *testing.T) {
	var posted bool
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			jsonReply(w, 200, map[string]interface{}{"data": []interface{}{}, "allowance": map[string]interface{}{"included": 10, "used": 10, "remaining_free": 0, "billable": 0, "monthly_price_cents": 170}})
		case http.MethodPost:
			posted = true
			jsonReply(w, 201, map[string]interface{}{"data": map[string]interface{}{"name": "eleventh", "status": "ready"}})
		}
	})
	_, errOut, err := runSplit(t, "snapshot", "save", "--snapshot", "snap-9", "eleventh")
	if err != nil || !posted {
		t.Fatalf("posted=%v err=%v", posted, err)
	}
	if !strings.Contains(errOut, "$1.70 a month") {
		t.Errorf("the charge must be announced before saving: %q", errOut)
	}
}

func TestSnapshotRm_NeedsYesWithoutATerminal(t *testing.T) {
	called := false
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	if _, err := run(t, "snapshot", "rm", "web-stack"); err == nil {
		t.Fatal("expected a refusal without --yes")
	}
	if called {
		t.Error("nothing may be sent without confirmation")
	}
}

func TestSnapshotRm_ReportsWhatHappenedToTheSnapshot(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/named-snapshots/web-stack" {
			http.NotFound(w, r)
			return
		}
		jsonReply(w, 200, map[string]interface{}{"data": map[string]interface{}{"name": "web-stack", "outcome": "released"}})
	})
	out, err := run(t, "snapshot", "rm", "web-stack", "--yes")
	if err != nil || !strings.Contains(out, "its snapshot is back in history") {
		t.Errorf("err=%v out=%q", err, out)
	}
}

func TestSnapshotInfo_ListsDependents(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{
			"data": map[string]interface{}{"id": "snap-1", "status": "ready", "browsable": true,
				"resource": map[string]interface{}{"type": "computer", "name": "desk"}},
			"dependents": []map[string]interface{}{{"type": "named_snapshot", "label": "pin"}, {"type": "suspended_computer", "label": "desk"}},
		})
	})
	out, err := run(t, "snapshot", "info", "snap-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`named snapshot "pin"`, "computer desk, which wakes from it", "desk (computer)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSnapshotTree_WaitsWhileWarming(t *testing.T) {
	var calls int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/snapshots/snap-1/tree" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("path") != "/home/user" {
			t.Errorf("path = %q", r.URL.Query().Get("path"))
		}
		if atomic.AddInt32(&calls, 1) < 3 {
			jsonReply(w, 202, map[string]interface{}{"status": "warming"})
			return
		}
		jsonReply(w, 200, map[string]interface{}{"data": map[string]interface{}{"path": "/home/user", "entries": []map[string]interface{}{
			{"name": "src", "type": "directory", "size": 4096, "mode": "0755"},
			{"name": "a.txt", "type": "file", "size": 6, "mode": "0644"},
		}}})
	})
	out, err := run(t, "snapshot", "tree", "snap-1", "/home/user")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if calls != 3 || !strings.Contains(out, "src/") || !strings.Contains(out, "a.txt") {
		t.Errorf("calls=%d out=%q", calls, out)
	}
}

func tarOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range entries {
		if strings.HasSuffix(name, "/") {
			_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755})
			continue
		}
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	return buf.Bytes()
}

func TestSnapshotPull_FileAndFolder(t *testing.T) {
	archive := tarOf(t, map[string]string{"app/": "", "app/main.go": "package main\n"})
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("path") {
		case "/home/user/.env":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("KEY=1\n"))
		case "/home/user/app":
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	})
	dir := t.TempDir()

	if _, err := run(t, "snapshot", "pull", "snap-1", "/home/user/.env", "-d", dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".env")); string(b) != "KEY=1\n" {
		t.Errorf("file = %q", b)
	}
	if _, err := run(t, "snapshot", "pull", "snap-1", "/home/user/app", "-d", dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "app", "main.go")); string(b) != "package main\n" {
		t.Errorf("folder file = %q", b)
	}
	// a second pull must not overwrite anything
	if _, err := run(t, "snapshot", "pull", "snap-1", "/home/user/.env", "-d", dir); err == nil {
		t.Error("expected a refusal to overwrite")
	}
}

func TestSnapshotPull_RefusesAnArchiveThatEscapes(t *testing.T) {
	evil := tarOf(t, map[string]string{"../escape.txt": "x"})
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write(evil)
	})
	parent := t.TempDir()
	dest := filepath.Join(parent, "out")
	if _, err := run(t, "snapshot", "pull", "snap-1", "/x", "-d", dest); err == nil {
		t.Fatal("expected a refusal")
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.txt")); err == nil {
		t.Fatal("the archive wrote outside the output directory")
	}
}

func TestSnapshotForkAndDeploy(t *testing.T) {
	var routes []string
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		routes = append(routes, r.Method+" "+r.URL.Path)
		jsonReply(w, 201, map[string]interface{}{"data": map[string]interface{}{"machine_type": "sandbox", "id": "new-1", "state": "provisioning"}})
	})
	out, err := run(t, "snapshot", "fork", "snap-1")
	if err != nil || !strings.Contains(out, "Creating sandbox new-1 (provisioning)") {
		t.Errorf("fork err=%v out=%q", err, out)
	}
	if _, err := run(t, "snapshot", "fork", "--name", "web-stack"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /snapshots/snap-1/fork", "POST /named-snapshots/web-stack/deploy"}
	if strings.Join(routes, ",") != strings.Join(want, ",") {
		t.Errorf("routes = %v", routes)
	}
}

func TestSnapshotDelete_ReportsBlockedAndNeverForcesIt(t *testing.T) {
	var body map[string]interface{}
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		jsonReply(w, 200, map[string]interface{}{"data": map[string]interface{}{
			"deleted":   []string{"s1"},
			"blocked":   []map[string]interface{}{{"snapshot_id": "s2", "dependents": []map[string]interface{}{{"type": "named_snapshot", "label": "pin"}}}},
			"not_found": []string{"s3"},
		}})
	})
	out, err := run(t, "snapshot", "delete", "s1", "s2", "s3", "--yes")
	if err != nil {
		t.Fatalf("a partial success is not an error: %v", err)
	}
	for _, want := range []string{"Deleted s1", "Skipped s2, still in use", `named snapshot "pin"`, "Not found: s3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if ids, _ := body["ids"].([]interface{}); len(ids) != 3 {
		t.Errorf("body = %v", body)
	}
}

func TestSnapshotDelete_FailsWhenEverythingIsBlocked(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 409, map[string]interface{}{"data": map[string]interface{}{
			"deleted": []string{},
			"blocked": []map[string]interface{}{{"snapshot_id": "s2", "dependents": []map[string]interface{}{{"type": "child_snapshot", "id": "c1"}}}},
		}})
	})
	if _, err := run(t, "snapshot", "delete", "s2", "--yes"); err == nil {
		t.Fatal("expected a non-zero exit when nothing could be deleted")
	}
}

func TestSnapshotDelete_AllSpellsOutTheMachineTwice(t *testing.T) {
	var body map[string]interface{}
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		jsonReply(w, 200, map[string]interface{}{"data": map[string]interface{}{"deleted": []string{"s1"}}})
	})
	if _, err := run(t, "snapshot", "delete", "--all", "my-box", "--yes"); err != nil {
		t.Fatal(err)
	}
	if body["resource_id"] != snapUUID || body["confirm"] != snapUUID || body["all"] != true {
		t.Errorf("body = %v", body)
	}
}

func TestSnapshotDelete_RequiresWhatToDelete(t *testing.T) {
	called := false
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	if _, err := run(t, "snapshot", "delete", "--yes"); err == nil {
		t.Fatal("expected an error with nothing to delete")
	}
	if _, err := run(t, "snapshot", "delete", "s1", "--all", "my-box", "--yes"); err == nil {
		t.Fatal("expected an error when mixing ids and --all")
	}
	if _, err := run(t, "snapshot", "delete", "s1"); err == nil {
		t.Fatal("expected a refusal without --yes when there is no terminal")
	}
	if called {
		t.Error("nothing may be sent for invalid or unconfirmed requests")
	}
}

func TestSnapshotLatest_NoSnapshotsIsAnError(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{"data": []interface{}{}})
	})
	if _, err := run(t, "snapshot", "latest", "my-box"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSnapshotList_JSONOutput(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]interface{}{"data": []map[string]interface{}{{"id": "snap-1", "status": "ready"}}})
	})
	out, err := run(t, "snapshot", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal([]byte(out), &rows); err != nil || len(rows) != 1 || rows[0]["id"] != "snap-1" {
		t.Errorf("json output = %q (%v)", out, err)
	}
}

// ─── shared client: exit codes, retries, idempotency ─────────────────────────

func TestSnapshotExitCodesFollowTheAPIStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
	}{
		{401, `{"error":{"code":"UNAUTHORIZED","message":"authentication required"}}`, commands.ExitAuth},
		{403, `{"error":{"code":"FORBIDDEN","message":"requires sandboxes:read"}}`, commands.ExitAuth},
		{404, `{"error":{"code":"NOT_FOUND","message":"snapshot not found"}}`, commands.ExitNotFound},
		{409, `{"error":{"code":"SNAPSHOT_IN_USE","message":"still in use"}}`, commands.ExitConflict},
		{422, `{"error":{"code":"INVALID_NAME","message":"bad name"}}`, commands.ExitConflict},
		{402, `{"error":{"code":"CREDIT_REQUIRED","message":"add credits"}}`, commands.ExitConflict},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			_, err := run(t, "snapshot", "named")
			if err == nil {
				t.Fatal("want an error")
			}
			if got := commands.ExitCode(err); got != tc.code {
				t.Errorf("exit = %d, want %d (%v)", got, tc.code, err)
			}
		})
	}
}

func TestSnapshotUnknownMachineExitsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiError(w, 404, "NOT_FOUND", "not found", nil)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(setupEnv(t, srv))
	_, err := run(t, "snapshot", "list", "ghost")
	if err == nil || commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("want exit %d, got %v (%d)", commands.ExitNotFound, err, commands.ExitCode(err))
	}
	if !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("the message should name what was not found: %v", err)
	}
}

func TestSnapshotDeleteWithoutYesIsAUsageError(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("nothing may be sent") })
	_, err := run(t, "snapshot", "delete", "s1")
	if err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("want exit %d, got %v (%d)", commands.ExitUsage, err, commands.ExitCode(err))
	}
}

func TestSnapshotDeleteEverythingBlockedExitsConflict(t *testing.T) {
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 409, map[string]interface{}{"data": map[string]interface{}{
			"deleted": []string{},
			"blocked": []map[string]interface{}{{"snapshot_id": "s2", "dependents": []map[string]interface{}{{"type": "child_snapshot", "id": "c1"}}}},
		}})
	})
	_, err := run(t, "snapshot", "delete", "s2", "--yes")
	if err == nil || commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("want exit %d, got %v (%d)", commands.ExitConflict, err, commands.ExitCode(err))
	}
}

func TestSnapshotReadsRetryOnUnavailable(t *testing.T) {
	var calls int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			apiError(w, 503, "UNAVAILABLE", "try again", nil)
			return
		}
		jsonReply(w, 200, map[string]interface{}{"data": []interface{}{}})
	})
	if _, err := run(t, "snapshot", "list", "--retries", "2"); err != nil {
		t.Fatalf("a transient 503 should be retried by the shared client: %v", err)
	}
	if calls < 2 {
		t.Errorf("calls = %d, want at least 2", calls)
	}
}

func TestSnapshotForkSendsAnIdempotencyKey(t *testing.T) {
	var keys []string
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		jsonReply(w, 201, map[string]interface{}{"data": map[string]interface{}{"machine_type": "sandbox", "id": "new-1"}})
	})
	if _, err := run(t, "snapshot", "fork", "snap-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "snapshot", "fork", "--name", "web-stack"); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[1] == "" || keys[0] == keys[1] {
		t.Errorf("each fork needs its own idempotency key, got %v", keys)
	}
}

func TestSnapshotSaveIsNotRetriedBlindly(t *testing.T) {
	var calls int32
	snapshotServer(t, func(w http.ResponseWriter, r *http.Request) {
		// The allowance read before the save is a GET and may be retried; only
		// the POST that creates the name must never be replayed.
		if r.Method == http.MethodPost {
			atomic.AddInt32(&calls, 1)
		}
		apiError(w, 502, "BAD_GATEWAY", "upstream", nil)
	})
	if _, err := run(t, "snapshot", "save", "--snapshot", "snap-9", "pin", "--retries", "3"); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Errorf("a save creates a name, so a 502 must not be replayed (calls = %d)", calls)
	}
}
