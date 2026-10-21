package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const clipsID = "11111111-1111-4111-8111-111111111111"

// workspaceCreateServer answers the workspace list and any create with an
// object carrying the workspace it was sent, and records the create bodies by
// path suffix.
func workspaceCreateServer(t *testing.T) (*httptest.Server, func(suffix string) map[string]any) {
	t.Helper()
	var mu sync.Mutex
	bodies := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/workspaces") {
			io.WriteString(w, twoWorkspacesForCli)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		bodies[r.URL.Path] = body
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		ws, _ := body["workspace_id"].(string)
		io.WriteString(w, `{"id":"abc","name":"x","workspace_id":"`+ws+`","definition":{"id":"abc","workspace_id":"`+ws+`"}}`)
	}))
	return srv, func(suffix string) map[string]any {
		mu.Lock()
		defer mu.Unlock()
		for path, b := range bodies {
			if strings.HasSuffix(path, suffix) {
				return b
			}
		}
		return nil
	}
}

func TestCreatesResolveTheWorkspaceFlagToAnID(t *testing.T) {
	cases := []struct {
		name string
		path string
		args []string
	}{
		{"schedule", "/cron-jobs", []string{"schedule", "create", "--name", "nightly", "--cron", "0 2 * * *", "--command", "true", "--workspace", "clips"}},
		{"bucket", "/storage/buckets", []string{"storage", "create", "assets", "--workspace", "clips"}},
		{"agent", "/agents", []string{"agent", "new", "helper", "--harness", "claude-code", "--model", "sonnet", "--instructions", "x", "--workspace", "clips"}},
		{"webhook", "/webhooks", []string{"webhook", "create", "https://example.com/h", "--workspace", "clips"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, body := workspaceCreateServer(t)
			defer srv.Close()
			defer setupEnv(t, srv)()
			t.Setenv("MIOSA_WORKSPACE", "")
			writeConfigFile(t, "")

			if _, err := run(t, tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			got := body(tc.path)
			if got["workspace_id"] != clipsID {
				t.Fatalf("workspace_id = %v, want the id of clips; body %v", got["workspace_id"], got)
			}
		})
	}
}

func TestCreatesFallBackToTheShellThenProfileWorkspace(t *testing.T) {
	cases := []struct {
		name string
		path string
		args []string
	}{
		{"schedule", "/cron-jobs", []string{"schedule", "create", "--name", "nightly", "--cron", "0 2 * * *", "--command", "true"}},
		{"bucket", "/storage/buckets", []string{"storage", "create", "assets"}},
		{"agent", "/agents", []string{"agent", "new", "helper", "--harness", "claude-code", "--model", "sonnet", "--instructions", "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, body := workspaceCreateServer(t)
			defer srv.Close()
			defer setupEnv(t, srv)()
			t.Setenv("MIOSA_WORKSPACE", "")
			writeConfigFile(t, "default_workspace = \"clips\"\n")

			if _, err := run(t, tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			if got := body(tc.path)["workspace_id"]; got != clipsID {
				t.Fatalf("profile default: workspace_id = %v, want %s", got, clipsID)
			}
		})
	}
}

func TestAWebhookWithoutAWorkspaceStaysOrganizationWide(t *testing.T) {
	srv, body := workspaceCreateServer(t)
	defer srv.Close()
	defer setupEnv(t, srv)()
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if _, err := run(t, "webhook", "create", "https://example.com/h"); err != nil {
		t.Fatal(err)
	}
	if _, has := body("/webhooks")["workspace_id"]; has {
		t.Fatalf("a webhook must not inherit the default workspace as a filter: %v", body("/webhooks"))
	}
}

func TestACreateReportsTheWorkspaceItLandedIn(t *testing.T) {
	srv, _ := workspaceCreateServer(t)
	defer srv.Close()
	defer setupEnv(t, srv)()
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "")

	out, err := run(t, "storage", "create", "assets", "--workspace", "clips")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "workspace: "+clipsID) {
		t.Fatalf("output does not name the landing workspace:\n%s", out)
	}
}
