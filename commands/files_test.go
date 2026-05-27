package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	miosa "github.com/Miosa-osa/miosa-go"
)

func TestFilesLsCommand_UsesSandboxFilesAPI(t *testing.T) {
	var sawSandboxGet bool
	var sawSandboxFiles bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/sandboxes/abc123":
			sawSandboxGet = true
			w.Write(fakeSandbox("abc123", "my-box"))
		case r.Method == http.MethodGet && r.URL.Path == "/sandboxes/abc123/files":
			sawSandboxFiles = true
			if got := r.URL.Query().Get("path"); got != "/workspace" {
				t.Fatalf("expected path=/workspace, got %q", got)
			}
			data, _ := json.Marshal(miosa.FileListResult{
				Path: "/workspace",
				Entries: []miosa.FileEntry{
					{Name: "main.py", Path: "/workspace/main.py", Size: 12, IsDir: false, ModifiedAt: "2026-01-01T00:00:00Z"},
				},
			})
			w.Write(data)
		case strings.HasPrefix(r.URL.Path, "/computers/"):
			t.Fatalf("sandbox files command must not call computer API: %s", r.URL.Path)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "files", "ls", "abc123:/workspace")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sawSandboxGet || !sawSandboxFiles {
		t.Fatalf("expected sandbox get and sandbox files calls, got get=%v files=%v", sawSandboxGet, sawSandboxFiles)
	}
	if !strings.Contains(out, "main.py") {
		t.Errorf("expected file name in output, got: %s", out)
	}
}
