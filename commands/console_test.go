package commands_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleCommand_NonTTY(t *testing.T) {
	// In test environments stdin is not a TTY.
	// The console command should return an error when stdin is not a TTY.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("console command must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "console", "abc123")
	// In a non-TTY test environment, console must fail cleanly.
	if err == nil {
		t.Fatal("expected console to fail when stdin is not a TTY")
	}
}

func TestConsoleCommand_NoSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called when no sandbox specified")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "console")
	if err == nil {
		t.Fatal("expected error when no sandbox specified")
	}
}
