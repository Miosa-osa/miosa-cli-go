package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const srcID = "11111111-1111-4111-8111-111111111111"

// dbServer is a fake API for the restore command: it records every request.
type dbServer struct {
	mu       sync.Mutex
	calls    []string
	bodies   map[string]map[string]any
	newState string
	backups  string
}

func (d *dbServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		key := r.Method + " " + r.URL.Path
		d.calls = append(d.calls, key)
		raw, _ := io.ReadAll(r.Body)
		if len(raw) > 0 {
			var b map[string]any
			_ = json.Unmarshal(raw, &b)
			if d.bodies == nil {
				d.bodies = map[string]map[string]any{}
			}
			d.bodies[key] = b
		}
		w.Header().Set("Content-Type", "application/json")
		switch key {
		case "GET /databases/" + srcID:
			w.Write([]byte(`{"id":"` + srcID + `","name":"app-db","engine":"postgresql","engine_version":"16","cpu_count":1,"memory_mb":2048,"storage_mb":10240,"workspace_id":"ws1","state":"running"}`))
		case "GET /databases/" + srcID + "/backups":
			if d.backups != "" {
				w.Write([]byte(d.backups))
				break
			}
			w.Write([]byte(`{"data":[{"id":"bk-failed","state":"failed"},{"id":"bk-newest","state":"completed"},{"id":"bk-old","state":"completed"}]}`))
		case "POST /databases":
			w.WriteHeader(201)
			w.Write([]byte(`{"id":"22222222-2222-4222-8222-222222222222","name":"app-db-r","state":"provisioning"}`))
		case "GET /databases/22222222-2222-4222-8222-222222222222":
			w.Write([]byte(`{"id":"22222222-2222-4222-8222-222222222222","state":"` + d.newState + `"}`))
		case "POST /databases/22222222-2222-4222-8222-222222222222/restore", "POST /databases/" + srcID + "/restore":
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"r1","state":"pending"}`))
		case "GET /databases/22222222-2222-4222-8222-222222222222/restores", "GET /databases/" + srcID + "/restores":
			w.Write([]byte(`{"data":[{"id":"r1","state":"completed","verification":{"checksum_verified":true}}]}`))
		default:
			t.Errorf("unexpected request %s", key)
			http.Error(w, "unexpected", 404)
		}
	})
}

func (d *dbServer) has(call string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, c := range d.calls {
		if c == call {
			return true
		}
	}
	return false
}

func TestDBRestoreToNewCreatesTheTargetAndLeavesTheSourceAlone(t *testing.T) {
	fake := &dbServer{newState: "running"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	out, err := run(t, "db", "restore", srcID, "--to-new", "app-db-r")
	if err != nil {
		t.Fatalf("restore failed: %v\n%s", err, out)
	}

	create := fake.bodies["POST /databases"]
	if create["name"] != "app-db-r" || create["engine"] != "postgresql" || create["engine_version"] != "16" || create["workspace_id"] != "ws1" {
		t.Errorf("the new database must copy the source's engine, version and workspace, got %v", create)
	}
	body := fake.bodies["POST /databases/22222222-2222-4222-8222-222222222222/restore"]
	if body["backup_id"] != "bk-newest" {
		t.Errorf("default backup must be the newest completed one, got %v", body["backup_id"])
	}
	if body["allow_overwrite"] != true || body["allow_in_place"] != nil {
		t.Errorf("a restore into a new database allows overwrite of the empty target only, got %v", body)
	}
	if fake.has("POST /databases/" + srcID + "/restore") {
		t.Error("the source must not be restored into")
	}
	if !strings.Contains(out, "Restored backup") {
		t.Errorf("want a success line, got %q", out)
	}
}

func TestDBRestoreToNewUsesTheBackupItIsGiven(t *testing.T) {
	fake := &dbServer{newState: "running"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	const full = "33333333-3333-4333-8333-333333333333"
	if out, err := run(t, "db", "restore", srcID, "--backup", full, "--to-new", "app-db-r"); err != nil {
		t.Fatalf("restore failed: %v\n%s", err, out)
	}
	if got := fake.bodies["POST /databases/22222222-2222-4222-8222-222222222222/restore"]["backup_id"]; got != full {
		t.Errorf("want %s, got %v", full, got)
	}
	if fake.has("GET /databases/" + srcID + "/backups") {
		t.Error("a full id needs no backup list")
	}
}

func TestDBRestoreOverTheSourceNeedsConfirmation(t *testing.T) {
	fake := &dbServer{newState: "running"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	if _, err := run(t, "db", "restore", srcID); err == nil {
		t.Fatal("restoring over the source without --yes must be refused off a terminal")
	}
	if fake.has("POST /databases/" + srcID + "/restore") {
		t.Fatal("nothing may be sent before the confirmation")
	}

	if out, err := run(t, "db", "restore", srcID, "--yes"); err != nil {
		t.Fatalf("restore failed: %v\n%s", err, out)
	}
	body := fake.bodies["POST /databases/"+srcID+"/restore"]
	if body["allow_in_place"] != true || body["backup_id"] != "bk-newest" {
		t.Errorf("want an in-place restore of the newest backup, got %v", body)
	}
	if fake.has("POST /databases") {
		t.Error("no new database for an in-place restore")
	}
}

func TestDBRestoreStopsWhenTheNewDatabaseFails(t *testing.T) {
	fake := &dbServer{newState: "error"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	_, err := run(t, "db", "restore", srcID, "--to-new", "app-db-r")
	if err == nil || !strings.Contains(err.Error(), "ended in state error") {
		t.Fatalf("want the new database's failure, got %v", err)
	}
	if fake.has("POST /databases/22222222-2222-4222-8222-222222222222/restore") {
		t.Error("no restore into a database that never ran")
	}
}

const backupList = `{"data":[{"id":"778439c0-1111-4222-8333-444455556666","state":"completed"},{"id":"778439c0-9999-4222-8333-444455556666","state":"completed"},{"id":"aaaaaaaa-1111-4222-8333-444455556666","state":"completed"}]}`

func TestDBRecoverResolvesAShortBackupID(t *testing.T) {
	fake := &dbServer{newState: "running", backups: `{"data":[{"id":"778439c0-1111-4222-8333-444455556666","state":"completed"}]}`}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	out, err := run(t, "db", "recover", srcID, "--backup", "778439c0", "--to-new", "app-db-r")
	if err != nil {
		t.Fatalf("recover: %v\n%s", err, out)
	}
	body := fake.bodies["POST /databases/22222222-2222-4222-8222-222222222222/restore"]
	if body["backup_id"] != "778439c0-1111-4222-8333-444455556666" {
		t.Fatalf("backup_id = %v, want the full id", body["backup_id"])
	}
}

func TestDBRecoverRefusesAnUnknownShortBackupID(t *testing.T) {
	fake := &dbServer{newState: "running", backups: backupList}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	_, err := run(t, "db", "recover", srcID, "--backup", "deadbeef", "--to-new", "app-db-r")
	if err == nil || !strings.Contains(err.Error(), "no backup") {
		t.Fatalf("err = %v", err)
	}
	if fake.has("POST /databases/22222222-2222-4222-8222-222222222222/restore") {
		t.Fatal("nothing may be restored")
	}
}

func TestDBRecoverRefusesAnAmbiguousShortBackupID(t *testing.T) {
	fake := &dbServer{newState: "running", backups: backupList}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	_, err := run(t, "db", "recover", srcID, "--backup", "778439c0", "--to-new", "app-db-r")
	if err == nil || !strings.Contains(err.Error(), "matches 2 backups") {
		t.Fatalf("err = %v", err)
	}
	if fake.has("POST /databases/22222222-2222-4222-8222-222222222222/restore") {
		t.Fatal("nothing may be restored")
	}
}
