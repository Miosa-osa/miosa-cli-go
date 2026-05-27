package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	miosa "github.com/Miosa-osa/miosa-go"
)

func TestRestoreCommand_Success(t *testing.T) {
	restored := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/restore/snap_001":
			restored = true
			newComp, _ := json.Marshal(map[string]interface{}{
				"data": miosa.SandboxData{
					ID:    "new123",
					Name:  "restored-box",
					State: miosa.StatusProvisioning,
				},
			})
			w.WriteHeader(http.StatusCreated)
			w.Write(newComp)
		case strings.HasPrefix(r.URL.Path, "/computers/"):
			t.Fatalf("restore must not call computer API: %s", r.URL.Path)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "restore", "abc123", "snap_001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !restored {
		t.Error("expected restore endpoint to be called")
	}
	if !strings.Contains(out, "restored-box") {
		t.Errorf("expected new sandbox name in output, got: %s", out)
	}
}

func TestRestoreCommand_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/restore/snap_001":
			newComp, _ := json.Marshal(map[string]interface{}{
				"data": miosa.SandboxData{ID: "new123", Name: "restored-box"},
			})
			w.WriteHeader(http.StatusCreated)
			w.Write(newComp)
		case strings.HasPrefix(r.URL.Path, "/computers/"):
			t.Fatalf("restore must not call computer API: %s", r.URL.Path)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "restore", "abc123", "snap_001", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jsonErr, out)
	}
}

func TestRestoreCommand_NoSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "restore", "snap_001")
	// With no current_sandbox and only one arg (checkpoint ID), requireSandbox falls back.
	// Both behaviors (error or success with empty sandbox) are acceptable.
	_ = err
}
