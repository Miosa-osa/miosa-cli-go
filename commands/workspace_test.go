package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeWorkspace(id, name, slug string) []byte {
	b, _ := json.Marshal(map[string]interface{}{
		"data": map[string]string{
			"id":   id,
			"name": name,
			"slug": slug,
		},
	})
	return b
}

func TestWorkspaceCreate_Success(t *testing.T) {
	created := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/workspaces" {
			created = true
			w.WriteHeader(http.StatusCreated)
			w.Write(fakeWorkspace("ws_001", "myteam", "myteam"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "workspace", "create", "myteam")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Error("expected workspace create endpoint to be called")
	}
}

func TestWorkspaceCreate_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/workspaces" {
			w.WriteHeader(http.StatusCreated)
			w.Write(fakeWorkspace("ws_001", "myteam", "myteam"))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "workspace", "create", "myteam", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]interface{}
	if jsonErr := json.Unmarshal(dataJSON(out), &result); jsonErr != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", jsonErr, out)
	}
	if result["id"] != "ws_001" {
		t.Errorf("expected id=ws_001, got %v", result["id"])
	}
}

func TestWorkspaceList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/workspaces" {
			resp, _ := json.Marshal(map[string]interface{}{
				"data": []map[string]string{
					{"id": "ws_001", "name": "alpha", "slug": "alpha"},
					{"id": "ws_002", "name": "beta", "slug": "beta"},
				},
			})
			w.Write(resp)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "workspace", "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected 'alpha' in output, got: %s", out)
	}
}

func workspaceDeleteServer(t *testing.T, deleted *bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces":
			w.Write([]byte(`{"data":[{"id":"ws_001","name":"alpha","slug":"alpha"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces/ws_001":
			w.Write([]byte(`{"data":{"id":"ws_001","name":"alpha","slug":"alpha"}}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/workspaces/"):
			*deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
}

// Off a terminal a workspace delete needs --yes: it used to delete at once.
func TestWorkspaceDelete_RefusesWithoutYes(t *testing.T) {
	deleted := false
	srv := workspaceDeleteServer(t, &deleted)
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "workspace", "delete", "alpha")
	if err == nil || !strings.Contains(err.Error(), "--yes") || !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("must refuse without --yes and name the workspace: %v", err)
	}
	if deleted {
		t.Fatal("nothing may be deleted without confirmation")
	}
}

func TestWorkspaceDelete_WithYes(t *testing.T) {
	deleted := false
	srv := workspaceDeleteServer(t, &deleted)
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "workspace", "delete", "alpha", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted || !strings.Contains(out, "alpha") {
		t.Errorf("expected the delete and a line naming the workspace, got %q", out)
	}
}

func TestWorkspaceList_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/workspaces" {
			resp, _ := json.Marshal(map[string]interface{}{
				"data": []map[string]string{
					{"id": "ws_001", "name": "alpha", "slug": "alpha"},
				},
			})
			w.Write(resp)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "workspace", "list", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "ws_001") {
		t.Errorf("expected ws_001 in JSON output, got: %s", out)
	}
}
