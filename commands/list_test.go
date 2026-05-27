package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	miosa "github.com/Miosa-osa/miosa-go"
)

func TestListCommand_EmptyTable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakeSandboxListResponse(nil))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "No sandboxes") {
		t.Errorf("expected empty state message, got: %s", out)
	}
}

func TestListCommand_WithData(t *testing.T) {
	sandboxes := []miosa.SandboxData{
		{ID: "abc", Name: "box-1", State: miosa.StatusRunning, TemplateID: "miosa-sandbox", CPUCount: 1, MemoryMB: 1024, CreatedAt: "2026-04-18T00:00:00Z"},
		{ID: "def", Name: "box-2", State: miosa.StatusStopped, TemplateID: "miosa-sandbox", CPUCount: 2, MemoryMB: 2048, CreatedAt: "2026-04-17T00:00:00Z"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakeSandboxListResponse(sandboxes))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "box-1") {
		t.Errorf("expected box-1 in output, got: %s", out)
	}
	if !strings.Contains(out, "box-2") {
		t.Errorf("expected box-2 in output, got: %s", out)
	}
}

func TestListCommand_JSONOutput(t *testing.T) {
	sandboxes := []miosa.SandboxData{
		{ID: "abc", Name: "box-1", State: miosa.StatusRunning, CreatedAt: "2026-04-18T00:00:00Z"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakeSandboxListResponse(sandboxes))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "list", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jsonErr, out)
	}

	data, ok := result["data"].([]interface{})
	if !ok {
		t.Fatalf("expected data array in response, got: %v", result)
	}
	if len(data) != 1 {
		t.Errorf("expected 1 sandbox, got %d", len(data))
	}
}

func TestListCommand_WorkspaceFilter(t *testing.T) {
	sandboxes := []miosa.SandboxData{
		{ID: "abc", Name: "box-ws1", State: miosa.StatusRunning, CreatedAt: "2026-04-18T00:00:00Z",
			Metadata: map[string]string{"workspace": "ws1"}},
		{ID: "def", Name: "box-ws2", State: miosa.StatusRunning, CreatedAt: "2026-04-18T00:00:00Z",
			Metadata: map[string]string{"workspace": "ws2"}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakeSandboxListResponse(sandboxes))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "list", "--workspace", "ws1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "box-ws1") {
		t.Errorf("expected box-ws1 in output, got: %s", out)
	}
	if strings.Contains(out, "box-ws2") {
		t.Errorf("box-ws2 should be filtered out, got: %s", out)
	}
}

func TestListCommand_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"unauthorized"}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "list")
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}
