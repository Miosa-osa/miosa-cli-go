package commands_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
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
			_, _ = w.Write(fakeSandbox("abc123", "my-box"))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/exec":
			execCalled = true
			result, _ := json.Marshal(map[string]interface{}{
				"data": map[string]interface{}{
					"stdout":    "hello\n",
					"stderr":    "",
					"exit_code": 0,
				},
			})
			_, _ = w.Write(result)
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
			_, _ = w.Write(fakeSandbox("abc123", "my-box"))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/exec":
			result, _ := json.Marshal(map[string]interface{}{
				"data": map[string]interface{}{
					"stdout":    "bash: command not found\n",
					"stderr":    "",
					"exit_code": 127,
				},
			})
			_, _ = w.Write(result)
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
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
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

// A command over the argv limit of the machine's shell never rides in the exec
// request: it is written through the files API and the shell runs the file.
func TestExecCommand_LargeCommandIsStagedThroughTheFilesAPI(t *testing.T) {
	payload := "echo " + strings.Repeat("A", 300*1024) // over 256 KiB
	var written, execCommand, writePath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sandboxes/"):
			_, _ = w.Write(fakeSandbox("abc123", "my-box"))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/files/write":
			var body struct {
				Path          string `json:"path"`
				ContentBase64 string `json:"content_base64"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			dec, _ := base64.StdEncoding.DecodeString(body.ContentBase64)
			written, writePath = string(dec), body.Path
			_, _ = w.Write([]byte(`{"data":{}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/abc123/exec":
			var body struct {
				Command string `json:"command"`
			}
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			execCommand = body.Command
			_, _ = w.Write([]byte(`{"data":{"stdout":"ok\n","stderr":"","exit_code":0}}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := run(t, "exec", "abc123", "--", "sh", "-c", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ok") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(written, payload) {
		t.Errorf("the staged file holds %d bytes, want the %d byte command", len(written), len(payload))
	}
	if len(execCommand) > 1024 || !strings.Contains(execCommand, writePath) {
		t.Errorf("exec command must be short and run %q, got %d bytes: %.120q", writePath, len(execCommand), execCommand)
	}
	if !strings.Contains(execCommand, "rm -f") {
		t.Errorf("the staged file must be removed afterwards: %.200q", execCommand)
	}
}

func TestExecCommand_LargeCommandOnAComputerUsesTheComputerFilesRoute(t *testing.T) {
	const id = "78aa09c5-d7a4-450f-b765-7b4b4ce96c8a"
	var wrote, execd bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/computers":
			_, _ = w.Write([]byte(`{"computers":[{"id":"` + id + `","name":"my-desktop"}]}`))
		case r.URL.Path == "/computers/"+id+"/files/write":
			wrote = true
			_, _ = w.Write([]byte(`{"data":{}}`))
		case r.URL.Path == "/computers/"+id+"/exec":
			execd = true
			_, _ = w.Write([]byte(`{"stdout":"ok\n","exit_code":0}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND"}}`))
		}
	}))
	defer srv.Close()
	defer setupEnv(t, srv)()

	if _, err := run(t, "exec", "my-desktop", "--", "sh", "-c", strings.Repeat("x", 300*1024)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wrote || !execd {
		t.Errorf("wrote=%v exec=%v", wrote, execd)
	}
}
