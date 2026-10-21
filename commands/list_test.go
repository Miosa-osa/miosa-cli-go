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

func TestListCommand_AcceptsObjectMetadataValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"data": [{
				"id": "abc",
				"name": "box-object-metadata",
				"state": "running",
				"metadata": {"workspace": "default", "data": {"source": "api", "version": 2}},
				"tags": {"team": "core", "labels": ["a", "b"]},
				"created_at": "2026-04-18T00:00:00Z"
			}, {
				"id": "def",
				"name": "box-object-workspace",
				"state": "running",
				"metadata": {"workspace": {"id": "default"}},
				"created_at": "2026-04-18T00:00:00Z"
			}],
			"meta": {"total": 2, "page": 1, "per_page": 20}
		}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "list")
	if err != nil {
		t.Fatalf("structured sandbox metadata or tags must not break list: %v", err)
	}
	if !strings.Contains(out, "box-object-metadata") {
		t.Errorf("expected sandbox in output, got: %s", out)
	}

	filtered, err := run(t, "list", "--workspace", "default")
	if err != nil {
		t.Fatalf("object-shaped metadata must not break workspace filtering: %v", err)
	}
	if !strings.Contains(filtered, "box-object-metadata") {
		t.Errorf("expected string workspace metadata to match the filter, got: %s", filtered)
	}
	if strings.Contains(filtered, "box-object-workspace") {
		t.Errorf("non-string workspace metadata must not match the filter, got: %s", filtered)
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
			Metadata: map[string]any{"workspace": "ws1"}},
		{ID: "def", Name: "box-ws2", State: miosa.StatusRunning, CreatedAt: "2026-04-18T00:00:00Z",
			Metadata: map[string]any{"workspace": "ws2"}},
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

func TestListCommand_ShowsWhyASandboxIsLost(t *testing.T) {
	sandboxes := []miosa.SandboxData{
		{ID: "lost1", Name: "gone", State: "lost", LostReason: "checkpoint unreadable", TemplateID: "miosa-sandbox", CreatedAt: "2026-04-18T00:00:00Z"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if !strings.Contains(out, "lost: checkpoint unreadable") {
		t.Errorf("list should show the lost reason, got: %s", out)
	}
}

// A list that holds machines back on purpose must say so (WO-04, QA
// 2026-10-19: two errored machines were absent from `list` with no hint).
func TestListCommand_ErroredHiddenFooterAndCount(t *testing.T) {
	body := `{"data":[{"id":"abc","name":"box-1","state":"running","template_id":"miosa-sandbox","created_at":"2026-04-18T00:00:00Z"}],
	  "meta":{"total":1,"page":1,"limit":50,"state_counts":{"running":1,"error":2}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "2 errored machines hidden, use --errored") {
		t.Errorf("expected the hidden-errored footer, got: %s", out)
	}

	jsonOut, err := run(t, "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var env struct {
		Meta struct {
			ErroredHidden int `json:"errored_hidden"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &env); err != nil {
		t.Fatalf("bad json: %v\n%s", err, jsonOut)
	}
	if env.Meta.ErroredHidden != 2 {
		t.Errorf("meta.errored_hidden = %d, want 2", env.Meta.ErroredHidden)
	}

	shown, err := run(t, "list", "--errored")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(shown, "hidden") {
		t.Errorf("--errored shows them, so nothing is hidden: %s", shown)
	}
}

func TestListCommand_NoFooterWhenNothingIsHidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"abc","name":"box-1","state":"running"}],"meta":{"total":1,"state_counts":{"running":1}}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "list")
	if err != nil || strings.Contains(out, "hidden") {
		t.Fatalf("%v\n%s", err, out)
	}
}
