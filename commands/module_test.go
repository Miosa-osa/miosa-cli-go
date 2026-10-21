package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const moduleYAML = `module: wo28
resources:
  - {kind: bucket, name: wo28-assets, spec: {visibility: private}}
  - {kind: volume, name: wo28-data, spec: {size_gb: 1}}
  - {kind: schedule, name: wo28-nightly, spec: {schedule: "0 2 * * *", command: "true"}}
`

type fakeModuleAPI struct {
	mu      sync.Mutex
	items   map[string][]map[string]any // list path -> items
	posts   []string
	patches []string
	deletes []string
}

func (f *fakeModuleAPI) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces":
			w.Write([]byte(`{"data":[{"id":"11111111-1111-1111-1111-111111111111","name":"acme","slug":"acme"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces/11111111-1111-1111-1111-111111111111":
			w.Write([]byte(`{"data":{"id":"11111111-1111-1111-1111-111111111111","name":"acme","slug":"acme"}}`))
		case r.Method == http.MethodGet:
			json.NewEncoder(w).Encode(map[string]any{"data": f.items[r.URL.Path]})
		case r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(b, &body)
			f.posts = append(f.posts, r.URL.Path+" "+body["name"].(string))
			body["id"] = "id-" + body["name"].(string)
			f.items[r.URL.Path] = append(f.items[r.URL.Path], body)
			if r.URL.Path == "/agents" {
				// The real create response nests the new definition, and ids are UUIDs, which
				// the client's path resolver takes as ids rather than names.
				body["id"] = moduleTestAgentID
				json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"definition": body}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": body})
		case r.Method == http.MethodPatch:
			f.patches = append(f.patches, r.URL.Path)
			w.Write([]byte(`{"data":{}}`))
		case r.Method == http.MethodDelete:
			f.deletes = append(f.deletes, r.URL.Path)
			w.Write([]byte(`{}`))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})
}

func writeManifest(t *testing.T, content string) string {
	p := filepath.Join(t.TempDir(), "wo28.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestModule_PlanApplyIdempotentDiffRemove(t *testing.T) {
	f := &fakeModuleAPI{items: map[string][]map[string]any{}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	defer setupEnv(t, srv)()
	file := writeManifest(t, moduleYAML)

	out, err := run(t, "module", "plan", file, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "plan: 3 create") {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if len(f.posts) != 0 {
		t.Fatalf("plan must change nothing: %v", f.posts)
	}

	out, err = run(t, "module", "apply", file, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "apply: 3 created") || len(f.posts) != 3 {
		t.Fatalf("apply: %v\n%s\nposts=%v", err, out, f.posts)
	}
	// apply prints the id of every resource it creates: users need them to
	// clean up exactly what they made.
	for _, id := range []string{"id-wo28-assets", "id-wo28-data", "id-wo28-nightly"} {
		if !strings.Contains(out, id) {
			t.Fatalf("apply output must name %s:\n%s", id, out)
		}
	}

	// Second apply: nothing created, nothing updated.
	out, err = run(t, "module", "apply", file, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "apply: 3 unchanged") || len(f.posts) != 3 || len(f.patches) != 0 {
		t.Fatalf("second apply: %v\n%s", err, out)
	}

	// A changed cron is shown by diff and updated in place by apply.
	file2 := writeManifest(t, strings.Replace(moduleYAML, "0 2 * * *", "30 3 * * *", 1))
	out, err = run(t, "module", "diff", file2, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "schedule: 0 2 * * * -> 30 3 * * *") {
		t.Fatalf("diff: %v\n%s", err, out)
	}
	out, err = run(t, "module", "apply", file2, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "1 updated") || len(f.patches) != 1 {
		t.Fatalf("apply drift: %v\n%s", err, out)
	}

	// remove --dry-run lists the three and deletes nothing; remove --yes deletes exactly those.
	out, err = run(t, "module", "remove", file, "--workspace", "acme", "--dry-run")
	if err != nil || !strings.Contains(out, "remove: 3 remove") || len(f.deletes) != 0 {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	for _, id := range []string{"id-wo28-assets", "id-wo28-data", "id-wo28-nightly"} {
		if !strings.Contains(out, id) {
			t.Fatalf("remove --dry-run must name %s:\n%s", id, out)
		}
	}
	if _, err = run(t, "module", "remove", file, "--workspace", "acme", "--yes"); err != nil || len(f.deletes) != 3 {
		t.Fatalf("remove: %v deletes=%v", err, f.deletes)
	}
}

func TestModule_RefusesALiteralSecretNamingTheKey(t *testing.T) {
	f := &fakeModuleAPI{items: map[string][]map[string]any{}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	defer setupEnv(t, srv)()

	bad := writeManifest(t, "module: m\nresources:\n  - {kind: schedule, name: s, spec: {schedule: \"0 * * * *\", command: x, env: {API_TOKEN: abc123}}}\n")
	_, err := run(t, "module", "apply", bad, "--workspace", "acme")
	if err == nil || !strings.Contains(err.Error(), "resources[0].spec.env.API_TOKEN") {
		t.Fatalf("want the offending key named, got %v", err)
	}
	if len(f.posts) != 0 {
		t.Fatal("nothing may be created from a manifest with a secret")
	}

	ok := writeManifest(t, "module: m\nresources:\n  - {kind: schedule, name: s, spec: {schedule: \"0 * * * *\", command: x, env: {API_TOKEN: \"${API_TOKEN}\"}}}\n")
	if _, err := run(t, "module", "plan", ok, "--workspace", "acme"); err != nil {
		t.Fatalf("a reference is allowed: %v", err)
	}

	byValue := writeManifest(t, "module: m\nresources:\n  - {kind: bucket, name: b, spec: {note: sk_live_abcdef123456}}\n")
	if _, err := run(t, "module", "plan", byValue, "--workspace", "acme"); err == nil || !strings.Contains(err.Error(), "spec.note") {
		t.Fatalf("a secret-looking value is refused: %v", err)
	}
}

// WO-28's acceptance manifest: one database (with vector), bucket, volume, agent, schedule and
// static interface.
const (
	moduleTestAgentID   = "33333333-3333-4333-8333-333333333333"
	moduleTestProjectID = "22222222-2222-4222-8222-222222222222"
)

const moduleSixYAML = `module: wo28
resources:
  - {kind: database, name: wo28-db, spec: {engine: postgresql, vector: true}}
  - {kind: bucket, name: wo28-assets, spec: {visibility: private}}
  - {kind: volume, name: wo28-data, spec: {size_gb: 1}}
  - {kind: agent, name: wo28-reviewer, spec: {configuration: {instructions: Review the queue.}}}
  - {kind: schedule, name: wo28-nightly, spec: {schedule: "0 2 * * *", command: "true"}}
  - {kind: interface, name: wo28-web, spec: {project_id: 22222222-2222-4222-8222-222222222222, kind: static}}
`

func TestModule_SixKindsAgentAndInterface(t *testing.T) {
	f := &fakeModuleAPI{items: map[string][]map[string]any{}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	defer setupEnv(t, srv)()
	file := writeManifest(t, moduleSixYAML)

	out, err := run(t, "module", "plan", file, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "plan: 6 create") {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if len(f.posts) != 0 {
		t.Fatalf("plan must change nothing: %v", f.posts)
	}

	out, err = run(t, "module", "apply", file, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "apply: 6 created") {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	// The agent's id comes from data.definition.id; the interface is created under its project.
	for _, want := range []string{
		"/databases wo28-db",
		"/storage/buckets wo28-assets",
		"/volumes wo28-data",
		"/agents wo28-reviewer",
		"/cron-jobs wo28-nightly",
		"/projects/" + moduleTestProjectID + "/services wo28-web",
	} {
		found := false
		for _, post := range f.posts {
			if post == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing create %q in %v", want, f.posts)
		}
	}
	if !strings.Contains(out, moduleTestAgentID) {
		t.Fatalf("apply must name the agent id it read from data.definition.id:\n%s", out)
	}

	out, err = run(t, "module", "apply", file, "--workspace", "acme")
	if err != nil || !strings.Contains(out, "apply: 6 unchanged") || len(f.posts) != 6 || len(f.patches) != 0 {
		t.Fatalf("second apply: %v\n%s\nposts=%v patches=%v", err, out, f.posts, f.patches)
	}

	out, err = run(t, "module", "remove", file, "--workspace", "acme", "--dry-run")
	if err != nil || !strings.Contains(out, "remove: 6 remove") || len(f.deletes) != 0 {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "id-wo28-web") {
		t.Fatalf("dry run must name the interface it would remove:\n%s", out)
	}

	if _, err = run(t, "module", "remove", file, "--workspace", "acme", "--yes"); err != nil || len(f.deletes) != 6 {
		t.Fatalf("remove: %v deletes=%v", err, f.deletes)
	}
	deletes := strings.Join(f.deletes, "\n")
	for _, want := range []string{
		"/agents/" + moduleTestAgentID,
		"/projects/" + moduleTestProjectID + "/services/id-wo28-web",
		"/cron-jobs/id-wo28-nightly",
	} {
		if !strings.Contains(deletes, want) {
			t.Fatalf("missing delete %q in:\n%s", want, deletes)
		}
	}
}

func TestModule_InterfaceNeedsAProject(t *testing.T) {
	f := &fakeModuleAPI{items: map[string][]map[string]any{}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	defer setupEnv(t, srv)()

	bad := writeManifest(t, "module: m\nresources:\n  - {kind: interface, name: web, spec: {kind: static}}\n")
	_, err := run(t, "module", "plan", bad, "--workspace", "acme")
	if err == nil || !strings.Contains(err.Error(), "spec.project_id") {
		t.Fatalf("want spec.project_id named, got %v", err)
	}
}

func TestModule_ANameWithoutAnExtensionReadsTheYAML(t *testing.T) {
	f := &fakeModuleAPI{items: map[string][]map[string]any{}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	defer setupEnv(t, srv)()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wo28.yaml"), []byte(moduleYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "module", "plan", filepath.Join(dir, "wo28"), "--workspace", "acme")
	if err != nil || !strings.Contains(out, "plan: 3 create") {
		t.Fatalf("a bare module name must read wo28.yaml: %v\n%s", err, out)
	}
}
