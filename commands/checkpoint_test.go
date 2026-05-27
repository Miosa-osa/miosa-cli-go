package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSnapshot returns a minimal snapshot JSON payload.
func fakeSnapshot(id, computerID, comment string) []byte {
	type data struct {
		ID        string `json:"id"`
		SandboxID string `json:"sandbox_id"`
		Comment   string `json:"comment"`
		CreatedAt string `json:"created_at"`
	}
	b, _ := json.Marshal(map[string]interface{}{
		"data": data{
			ID:        id,
			SandboxID: computerID,
			Comment:   comment,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
	})
	return b
}

func TestCheckpointCreate_Success(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/snapshots":
			created = true
			w.WriteHeader(http.StatusCreated)
			w.Write(fakeSnapshot("snap_001", "abc123", "test snapshot"))
		case strings.HasPrefix(r.URL.Path, "/computers/"):
			t.Fatalf("checkpoint create must not call computer API: %s", r.URL.Path)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "checkpoint", "create", "abc123", "--comment", "test snapshot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected snapshot endpoint to be called")
	}
}

func TestCheckpointCreate_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/snapshots":
			w.WriteHeader(http.StatusCreated)
			w.Write(fakeSnapshot("snap_001", "abc123", ""))
		case strings.HasPrefix(r.URL.Path, "/computers/"):
			t.Fatalf("checkpoint create must not call computer API: %s", r.URL.Path)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "checkpoint", "create", "abc123", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jsonErr, out)
	}
	if result["id"] != "snap_001" {
		t.Errorf("expected id=snap_001, got %v", result["id"])
	}
}

func TestCheckpointList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sandboxes/abc123/snapshots":
			type data struct {
				ID        string `json:"id"`
				SandboxID string `json:"sandbox_id"`
				Comment   string `json:"comment"`
				CreatedAt string `json:"created_at"`
			}
			resp, _ := json.Marshal(map[string]interface{}{
				"data": []data{
					{ID: "snap_001", SandboxID: "abc123", Comment: "first", CreatedAt: "2026-01-01T00:00:00Z"},
					{ID: "snap_002", SandboxID: "abc123", Comment: "second", CreatedAt: "2026-01-02T00:00:00Z"},
				},
			})
			w.Write(resp)
		case strings.HasPrefix(r.URL.Path, "/computers/"):
			t.Fatalf("checkpoint list must not call computer API: %s", r.URL.Path)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "checkpoint", "list", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "snap_001") {
		t.Errorf("expected snap_001 in output, got: %s", out)
	}
}

func TestCheckpointDelete_Success(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/snapshots/") {
			deleted = true
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "checkpoint", "delete", "snap_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected delete endpoint to be called")
	}
}

func TestCheckpointInfo_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/snapshots/") {
			w.Write(fakeSnapshot("snap_001", "abc123", "info test"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "checkpoint", "info", "snap_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "snap_001") {
		t.Errorf("expected snap_001 in output, got: %s", out)
	}
}

func TestCheckpointCreate_NoSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "checkpoint", "create")
	if err == nil {
		t.Fatal("expected error when no sandbox specified")
	}
}
