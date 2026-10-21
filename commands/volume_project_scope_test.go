package commands_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Every workspace auto-creates its own Default project, so "the default
// project" is only meaningful inside a workspace. A tenant-wide /projects list
// is newest-first; picking the first is_default there returns the newest
// workspace's Default (the WO-20 bug this pins).

const (
	scopeWSA  = "aaaaaaaa-0000-4000-8000-00000000000a"
	scopeWSB  = "bbbbbbbb-0000-4000-8000-00000000000b"
	scopeWSOr = "cccccccc-0000-4000-8000-00000000000c"
	scopePjA  = "11111111-0000-4000-8000-0000000000a1"
	scopePjB  = "11111111-0000-4000-8000-0000000000b1"
	scopePjOr = "11111111-0000-4000-8000-0000000000c1"
	scopeSvc  = "22222222-0000-4000-8000-000000000001"
)

type projectScope struct {
	mu      sync.Mutex
	queries []string
	patched []string
	orgSlug string // the organization's Default workspace slug; "" means none
}

func (s *projectScope) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces":
			rows := `{"id":"` + scopeWSA + `","name":"A","slug":"ws-a"},` +
				`{"id":"` + scopeWSB + `","name":"B","slug":"ws-b"}`
			if s.orgSlug != "" {
				rows += `,{"id":"` + scopeWSOr + `","name":"` + s.orgSlug + `","slug":"` + s.orgSlug + `"}`
			}
			_, _ = w.Write([]byte(`{"data":[` + rows + `]}`))

		case r.Method == http.MethodGet && r.URL.Path == "/projects":
			s.mu.Lock()
			s.queries = append(s.queries, r.URL.Query().Get("workspace_id"))
			s.mu.Unlock()
			var body string
			switch r.URL.Query().Get("workspace_id") {
			case scopeWSA:
				body = `{"data":[{"id":"` + scopePjA + `","name":"Default","slug":"default","is_default":true}]}`
			case scopeWSB:
				body = `{"data":[{"id":"` + scopePjB + `","name":"Default","slug":"default","is_default":true}]}`
			case scopeWSOr:
				body = `{"data":[{"id":"` + scopePjOr + `","name":"Default","slug":"default","is_default":true}]}`
			default:
				// Newest first: the old code took the first is_default row.
				body = `{"data":[{"id":"` + scopePjB + `","name":"Default","slug":"default","is_default":true},` +
					`{"id":"` + scopePjA + `","name":"Default","slug":"default","is_default":true}]}`
			}
			_, _ = w.Write([]byte(body))

		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/services"):
			_, _ = w.Write([]byte(`{"data":[{"id":"` + scopeSvc + `","name":"web","slug":"web","lock_version":1,"settings":{}}]}`))

		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/services/"):
			s.mu.Lock()
			s.patched = append(s.patched, r.URL.Path)
			s.mu.Unlock()
			_, _ = w.Write([]byte(`{"data":{"id":"` + scopeSvc + `"}}`))

		default:
			http.Error(w, `{"error":{"code":"NOT_FOUND"}}`, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestVolumeMountUsesTheProfileWorkspacesDefaultProject(t *testing.T) {
	scope := &projectScope{orgSlug: "default"}
	srv := scope.server(t)
	setupEnv(t, srv)
	writeConfigFile(t, "api_key = \"msk_u_test\"\ndefault_workspace = \"ws-a\"\n")

	out, err := run(t, "volume", "mount", "data", "web", "--mount", "/data")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(scope.queries) != 1 || scope.queries[0] != scopeWSA {
		t.Fatalf("project lookup scoped to %v, want the profile workspace %s", scope.queries, scopeWSA)
	}
	if len(scope.patched) != 1 || !strings.Contains(scope.patched[0], scopePjA) {
		t.Fatalf("patched %v, want the default project of ws-a", scope.patched)
	}
}

func TestVolumeMountUsesTheNamedWorkspacesDefaultProject(t *testing.T) {
	scope := &projectScope{orgSlug: "default"}
	srv := scope.server(t)
	setupEnv(t, srv)
	writeConfigFile(t, "api_key = \"msk_u_test\"\ndefault_workspace = \"ws-a\"\n")

	out, err := run(t, "volume", "mount", "data", "web", "--mount", "/data", "--workspace", "ws-b")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(scope.queries) != 1 || scope.queries[0] != scopeWSB {
		t.Fatalf("project lookup scoped to %v, want the named workspace %s", scope.queries, scopeWSB)
	}
	if len(scope.patched) != 1 || !strings.Contains(scope.patched[0], scopePjB) {
		t.Fatalf("patched %v, want the default project of ws-b", scope.patched)
	}
}

func TestVolumeMountFallsBackToTheOrganizationDefaultWorkspace(t *testing.T) {
	// No profile default and no --workspace: the organization's Default
	// workspace decides, not the newest Default project in the tenant.
	scope := &projectScope{orgSlug: "default"}
	srv := scope.server(t)
	setupEnv(t, srv)
	writeConfigFile(t, "api_key = \"msk_u_test\"\n")
	t.Setenv("MIOSA_WORKSPACE", "")

	if out, err := run(t, "volume", "mount", "data", "web", "--mount", "/data"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(scope.queries) != 1 || scope.queries[0] != scopeWSOr {
		t.Fatalf("project lookup scoped to %v, want the organization Default workspace %s", scope.queries, scopeWSOr)
	}
	if len(scope.patched) != 1 || !strings.Contains(scope.patched[0], scopePjOr) {
		t.Fatalf("patched %v, want the organization Default workspace's project", scope.patched)
	}
}

func TestVolumeMountRefusesToGuessBetweenDefaultProjects(t *testing.T) {
	// No profile default, no --workspace, and no workspace named default: the
	// tenant-wide list has two Default projects. Refuse instead of picking the
	// newest one.
	scope := &projectScope{orgSlug: ""}
	srv := scope.server(t)
	setupEnv(t, srv)
	writeConfigFile(t, "api_key = \"msk_u_test\"\n")
	t.Setenv("MIOSA_WORKSPACE", "")

	out, err := run(t, "volume", "mount", "data", "web", "--mount", "/data")
	if err == nil {
		t.Fatalf("must refuse an ambiguous default project, got:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--workspace") && !strings.Contains(err.Error(), "--project") {
		t.Fatalf("the error must ask for a scope: %v", err)
	}
	if len(scope.patched) != 0 {
		t.Fatalf("nothing may be patched, got %v", scope.patched)
	}
}
