package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	miosa "github.com/Miosa-osa/miosa-go"
)

// twoWorkspacesForCli is the workspace list the resolver reads.
const twoWorkspacesForCli = `{"data":[
 {"id":"11111111-1111-4111-8111-111111111111","slug":"clips","name":"Clips"},
 {"id":"22222222-2222-4222-8222-222222222222","slug":"other","name":"Other"}]}`

func sandboxCreateBody(t *testing.T) (*httptest.Server, *string, func()) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body miosa.CreateSandboxInput
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.WorkspaceSlug != "" {
			got = body.WorkspaceSlug
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(fakeSandbox("abc123", "my-box"))
	}))
	return srv, &got, func() { srv.Close() }
}

// A profile with default_workspace must scope a sandbox create even when
// --workspace is absent.
func TestCreateUsesTheProfileDefaultWorkspace(t *testing.T) {
	srv, got, done := sandboxCreateBody(t)
	defer done()
	cleanup := setupEnv(t, srv)
	defer cleanup()
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if _, err := run(t, "create", "my-box"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if *got != "clips" {
		t.Fatalf("workspace_slug = %q, want clips", *got)
	}
}

func TestCreateFlagBeatsTheProfileDefaultWorkspace(t *testing.T) {
	srv, got, done := sandboxCreateBody(t)
	defer done()
	cleanup := setupEnv(t, srv)
	defer cleanup()
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if _, err := run(t, "create", "my-box", "--workspace", "other"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if *got != "other" {
		t.Fatalf("workspace_slug = %q, want other", *got)
	}
}

func TestCreateUsesMiosaWorkspaceOverTheProfile(t *testing.T) {
	srv, got, done := sandboxCreateBody(t)
	defer done()
	cleanup := setupEnv(t, srv)
	defer cleanup()
	writeConfigFile(t, "default_workspace = \"clips\"\n")
	t.Setenv("MIOSA_WORKSPACE", "env-ws")

	if _, err := run(t, "create", "my-box"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if *got != "env-ws" {
		t.Fatalf("workspace_slug = %q, want env-ws", *got)
	}
}

func TestCreateIgnoresTheBuiltInDefaultWorkspace(t *testing.T) {
	srv, got, done := sandboxCreateBody(t)
	defer done()
	cleanup := setupEnv(t, srv)
	defer cleanup()
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"default\"\n")

	if _, err := run(t, "create", "my-box"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if *got != "" {
		t.Fatalf("workspace_slug = %q, want none", *got)
	}
}

func TestDbCreateUsesTheProfileDefaultWorkspace(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /databases": resp{201, `{"data":{"id":"db1","name":"app-db"}}`},
	})
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if out, err := run(t, "db", "create", "app-db"); err != nil {
		t.Fatalf("db create: %v\n%s", err, out)
	}
	calls := f.find("POST", "/databases")
	if len(calls) != 1 || calls[0].Body["workspace_slug"] != "clips" {
		t.Fatalf("body = %+v", calls)
	}
}

func TestStorageCreateResolvesTheProfileDefaultWorkspace(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /workspaces":       twoWorkspacesForCli,
		"POST /storage/buckets": resp{201, `{"data":{"id":"b1","name":"b1"}}`},
	})
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if out, err := run(t, "storage", "create", "b1"); err != nil {
		t.Fatalf("storage create: %v\n%s", err, out)
	}
	calls := f.find("POST", "/storage/buckets")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("body = %+v", calls)
	}
}

func TestVolumeCreateResolvesTheProfileDefaultWorkspace(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /workspaces": twoWorkspacesForCli,
		"POST /volumes":   resp{201, `{"data":{"id":"v1","name":"data"}}`},
	})
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if out, err := run(t, "volume", "create", "data", "--size-gb", "5"); err != nil {
		t.Fatalf("volume create: %v\n%s", err, out)
	}
	calls := f.find("POST", "/volumes")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("body = %+v", calls)
	}
}

func TestBatchNewUsesTheProfileDefaultWorkspace(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /sandboxes/batch": resp{201, `{"data":{"id":"batch1"}}`},
	})
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if out, err := run(t, "batch", "new", "--count", "1"); err != nil {
		t.Fatalf("batch new: %v\n%s", err, out)
	}
	calls := f.find("POST", "/sandboxes/batch")
	if len(calls) != 1 || calls[0].Body["workspace_slug"] != "clips" {
		t.Fatalf("body = %+v", calls)
	}
}

func TestEngineCreateUsesTheProfileDefaultWorkspace(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /databases":  `{"data":[]}`,
		"POST /databases": resp{201, `{"id":"db1","name":"qa-brain","engine":"optimal","state":"provisioning"}`},
	})
	t.Setenv("MIOSA_WORKSPACE", "")
	writeConfigFile(t, "default_workspace = \"clips\"\n")

	if out, err := run(t, "engine", "create", "qa-brain"); err != nil {
		t.Fatalf("engine create: %v\n%s", err, out)
	}
	calls := f.find("POST", "/databases")
	if len(calls) != 1 || calls[0].Body["workspace_slug"] != "clips" {
		t.Fatalf("body = %+v", calls)
	}
}
