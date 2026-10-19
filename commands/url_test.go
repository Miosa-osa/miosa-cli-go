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
		if r.Method == http.MethodGet && (r.URL.Path == "/sandboxes/abc123" || r.URL.Path == "/sandboxes/by-name/abc123") {
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
		if r.Method == http.MethodGet && (r.URL.Path == "/sandboxes/abc123" || r.URL.Path == "/sandboxes/by-name/abc123") {
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
	if jsonErr := json.Unmarshal(dataJSON(out), &result); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jsonErr, out)
	}
	if _, ok := result["url"]; !ok {
		t.Errorf("expected 'url' key in JSON output, got: %v", result)
	}
}

func TestURLHasNoUpdateSubcommand(t *testing.T) {
	// 'url update' used to ship and answer "not available yet". Changing a
	// preview's visibility is 'preview share' / 'preview unshare'.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("no request may be made")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "url", "update", "abc123", "--auth", "public"); err == nil {
		t.Fatal("url update must not exist")
	}
}
