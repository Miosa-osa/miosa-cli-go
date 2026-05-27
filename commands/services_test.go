package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeService(id, name, command, status string) []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"data": map[string]string{
			"id":             id,
			"name":           name,
			"command":        command,
			"status":         status,
			"restart_policy": "on-failure",
		},
	})
	return b
}

func fakeServicesList(services []map[string]string) []byte {
	b, _ := json.Marshal(map[string]interface{}{"data": services})
	return b
}

func TestServicesList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("services command must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	// Set current sandbox in env.
	t.Setenv("MIOSA_CURRENT_SANDBOX", "abc123")

	_, err := run(t, "services", "list", "abc123")
	if err == nil {
		t.Fatal("expected unsupported native sandbox services error")
	}
}

func TestServicesCreate_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("services create must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "services", "create", "abc123", "--name", "worker", "--command", "python worker.py")
	if err == nil {
		t.Fatal("expected unsupported native sandbox services error")
	}
}

func TestServicesCreate_InvalidRestart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called for invalid restart policy")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	// First set a current sandbox so it doesn't fail on that.
	// We must use current_sandbox via flag but there's none — so provide sandbox as arg.
	_, err := run(t, "services", "create", "abc123", "--name", "bad", "--command", "cmd", "--restart", "invalid")
	if err == nil {
		t.Fatal("expected error for invalid restart policy")
	}
}

func TestServicesLogs_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("services logs must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	// Write a config with current_sandbox set so requireSandbox("", ...) succeeds.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)
	writeTestConfig(t, home, srv.URL)

	_, err := run(t, "services", "logs", "web")
	if err == nil {
		t.Fatal("expected unsupported native sandbox service logs error")
	}
}

func TestServicesDelete_NoSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "services", "delete", "web")
	if err == nil {
		t.Fatal("expected error when no sandbox is set")
	}
}
