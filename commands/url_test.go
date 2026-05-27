package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURLCommand_ShowsURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("url command must not call computer API: %s", r.URL.Path)
		}
		if r.Method == http.MethodGet && r.URL.Path == "/sandboxes/abc123" {
			w.Write(fakeSandboxWithPreview("abc123", "my-box", "https://abc123.sandbox.miosa.app"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "url", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "abc123") {
		t.Errorf("expected sandbox id in URL, got: %s", out)
	}
	if !strings.Contains(out, "miosa.app") {
		t.Errorf("expected miosa.app domain in URL, got: %s", out)
	}
}

func TestURLCommand_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("url command must not call computer API: %s", r.URL.Path)
		}
		if r.Method == http.MethodGet && r.URL.Path == "/sandboxes/abc123" {
			w.Write(fakeSandboxWithPreview("abc123", "my-box", "https://abc123.sandbox.miosa.app"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "url", "abc123", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jsonErr, out)
	}
	if _, ok := result["url"]; !ok {
		t.Errorf("expected 'url' key in JSON output, got: %v", result)
	}
}

func TestURLUpdate_ValidAuthDoesNotFallbackToComputers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("url update must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "url", "update", "abc123", "--auth", "public")
	if err == nil {
		t.Fatal("expected unsupported native sandbox visibility error")
	}
}

func TestURLUpdate_InvalidAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called for invalid auth policy")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "url", "update", "abc123", "--auth", "badpolicy")
	if err == nil {
		t.Fatal("expected error for invalid auth policy")
	}
}

func TestURLCommand_NoSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called when no sandbox specified")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "url")
	if err == nil {
		t.Fatal("expected error when no sandbox specified")
	}
}
