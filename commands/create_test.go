package commands_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
	miosa "github.com/Miosa-osa/miosa-go"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

// fakeComputer returns a minimal ComputerData JSON payload.
func fakeComputer(id, name string) []byte {
	data, _ := json.Marshal(miosa.ComputerData{
		ID:           id,
		Name:         name,
		Status:       miosa.StatusRunning,
		Size:         miosa.SizeSmall,
		TemplateType: "miosa-sandbox",
		CreatedAt:    "2026-01-01T00:00:00Z",
	})
	return data
}

// fakeSandbox returns a minimal SandboxData JSON payload.
func fakeSandbox(id, name string) []byte {
	data, _ := json.Marshal(miosa.SandboxData{
		ID:         id,
		Name:       name,
		State:      miosa.StatusRunning,
		TemplateID: "miosa-sandbox",
		CPUCount:   1,
		MemoryMB:   1024,
		DiskSizeMB: 3072,
		CreatedAt:  "2026-01-01T00:00:00Z",
	})
	return data
}

func fakeSandboxWithPreview(id, name, previewURL string) []byte {
	data, _ := json.Marshal(miosa.SandboxData{
		ID:         id,
		Name:       name,
		State:      miosa.StatusRunning,
		TemplateID: "miosa-sandbox",
		CPUCount:   1,
		MemoryMB:   1024,
		DiskSizeMB: 3072,
		PreviewURL: previewURL,
		Ready:      true,
		CreatedAt:  "2026-01-01T00:00:00Z",
	})
	return data
}

// fakeListResponse wraps computers in a list envelope.
func fakeListResponse(computers []miosa.ComputerData) []byte {
	type meta struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	}
	type resp struct {
		Data []miosa.ComputerData `json:"data"`
		Meta meta                 `json:"meta"`
	}
	data, _ := json.Marshal(resp{
		Data: computers,
		Meta: meta{Total: len(computers), Page: 1, PerPage: 25},
	})
	return data
}

// fakeSandboxListResponse wraps sandboxes in a list envelope.
func fakeSandboxListResponse(sandboxes []miosa.SandboxData) []byte {
	type meta struct {
		Total   int `json:"total"`
		Page    int `json:"page"`
		PerPage int `json:"per_page"`
	}
	type resp struct {
		Data []miosa.SandboxData `json:"data"`
		Meta meta                `json:"meta"`
	}
	data, _ := json.Marshal(resp{
		Data: sandboxes,
		Meta: meta{Total: len(sandboxes), Page: 1, PerPage: 25},
	})
	return data
}

// setupEnv points the CLI at a test server and a temp home dir.
// Returns a cleanup func.
func setupEnv(t *testing.T, srv *httptest.Server) func() {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)
	return func() {
		os.Unsetenv("MIOSA_API_KEY")
		os.Unsetenv("MIOSA_BASE_URL")
	}
}

// writeTestConfig writes a minimal config.toml to homeDir/.miosa/config.toml
// with the given current_sandbox and api_url so tests can pre-configure state.
func writeTestConfig(t *testing.T, homeDir, apiURL string) {
	t.Helper()
	dir := filepath.Join(homeDir, ".miosa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating config dir: %v", err)
	}
	content := fmt.Sprintf(`api_url = %q
api_key = "msk_u_test"
current_sandbox = "abc123"
`, apiURL)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}

// run executes the CLI with the given args and returns stdout output and error.
// It resets global flag state before each call to prevent leakage between tests.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	commands.ResetForTest()
	root := commands.Root()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

// ─── create tests ─────────────────────────────────────────────────────────────

func TestCreateCommand_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(fakeSandbox("abc123", "my-box"))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "create", "my-box", "--size", "small")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateCommand_InvalidSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called for invalid size")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "create", "my-box", "--size", "xlarge")
	if err == nil {
		t.Fatal("expected error for invalid size")
	}
}

func TestCreateCommand_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(fakeSandbox("abc123", "my-box"))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "create", "my-box", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jsonErr, out)
	}
	if result["id"] != "abc123" {
		t.Errorf("expected id=abc123, got %v", result["id"])
	}
}

func TestCreateCommand_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintln(w, `{"message":"unauthorized"}`)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "create", "my-box")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestCreateCommand_NoName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(fakeSandbox("gen001", ""))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "create")
	if err != nil {
		t.Fatalf("create without name failed: %v", err)
	}
}
