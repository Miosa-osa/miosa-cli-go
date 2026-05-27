package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExecCommand_Success(t *testing.T) {
	execCalled := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			w.Write(fakeSandbox("abc123", "my-box"))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/exec":
			execCalled = true
			result, _ := json.Marshal(map[string]interface{}{
				"data": map[string]interface{}{
					"stdout":    "hello\n",
					"stderr":    "",
					"exit_code": 0,
				},
			})
			w.Write(result)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "exec", "abc123", "--", "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !execCalled {
		t.Error("expected exec endpoint to be called")
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output, got: %s", out)
	}
}

func TestExecCommand_NonZeroExit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			w.Write(fakeSandbox("abc123", "my-box"))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/exec":
			result, _ := json.Marshal(map[string]interface{}{
				"data": map[string]interface{}{
					"stdout":    "bash: command not found\n",
					"stderr":    "",
					"exit_code": 127,
				},
			})
			w.Write(result)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "exec", "abc123", "--", "nonexistent-cmd")
	if err == nil {
		t.Fatal("expected non-zero exit code to return error")
	}
}

func TestExecCommand_NoSandboxSpecified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called when no sandbox is specified")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	// Empty HOME means no config current_sandbox, no flag, no arg → error.
	_, err := run(t, "exec", "--", "echo", "hello")
	if err == nil {
		t.Fatal("expected error when no sandbox is specified")
	}
}

func TestExecCommand_NoCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called when no command is specified")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "exec", "my-box")
	if err == nil {
		t.Fatal("expected error when no command is specified")
	}
}
