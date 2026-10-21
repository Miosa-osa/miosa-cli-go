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

const topologyYAML = `schema_version: 1
miosa_org: osa
keep_workspaces: [default, commas-partnership]
entities:
  miosa:
    workspaces:
      miosa:
        projects: [billing, compute]
      terrawatt: {bridge: x, projects: [rollout]}
`

func TestOrgApply_DryRunCreatesNothingApplyIsIdempotentAndExtrasStay(t *testing.T) {
	var mu sync.Mutex
	workspaces := []map[string]any{
		{"id": "w-default", "slug": "default", "name": "default"},
		{"id": "w-old", "slug": "tz-probe", "name": "tz-probe"},
		{"id": "w-miosa", "slug": "miosa", "name": "miosa"},
	}
	projects := map[string][]map[string]any{"w-miosa": {{"name": "billing", "slug": "billing"}}}
	var posts, deletes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces":
			json.NewEncoder(w).Encode(map[string]any{"data": workspaces})
		case r.Method == http.MethodGet && r.URL.Path == "/projects":
			json.NewEncoder(w).Encode(map[string]any{"data": projects[r.URL.Query().Get("workspace_id")]})
		case r.Method == http.MethodPost && r.URL.Path == "/workspaces":
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(b, &body)
			id := "w-" + body["slug"].(string)
			workspaces = append(workspaces, map[string]any{"id": id, "slug": body["slug"], "name": body["name"]})
			posts = append(posts, "workspace "+body["slug"].(string))
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"id": id}})
		case r.Method == http.MethodPost && r.URL.Path == "/projects":
			b, _ := io.ReadAll(r.Body)
			var body map[string]any
			json.Unmarshal(b, &body)
			posts = append(posts, "project "+body["workspace_id"].(string)+"/"+body["name"].(string))
			projects[body["workspace_id"].(string)] = append(projects[body["workspace_id"].(string)], map[string]any{"name": body["name"], "slug": body["name"]})
			w.Write([]byte(`{"data":{}}`))
		case r.Method == http.MethodDelete:
			deletes = append(deletes, r.URL.Path)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	defer setupEnv(t, srv)()
	file := writeManifest(t, topologyYAML)

	out, err := run(t, "org", "apply", file, "--dry-run")
	if err != nil || len(posts) != 0 {
		t.Fatalf("dry run: %v posts=%v", err, posts)
	}
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{"create workspace terrawatt", "create project miosa/compute", "create project terrawatt/rollout", "extra workspace tz-probe", "org apply:"} {
		if !strings.Contains(flat, want) {
			t.Errorf("dry run missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "default") || strings.Contains(out, "commas-partnership") {
		t.Errorf("keep_workspaces must not be listed as extras:\n%s", out)
	}

	if out, err = run(t, "org", "apply", file); err != nil || len(posts) != 3 {
		t.Fatalf("apply: %v posts=%v\n%s", err, posts, out)
	}
	// Second apply: nothing created; the extra is still only reported.
	before := len(posts)
	out, err = run(t, "org", "apply", file)
	if err != nil || len(posts) != before || !strings.Contains(out, "extra") {
		t.Fatalf("second apply: %v posts=%v\n%s", err, posts, out)
	}
	if len(deletes) != 0 {
		t.Fatalf("org apply must never delete: %v", deletes)
	}
}

func TestOrgApply_RefusesAnUnknownSchema(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	defer setupEnv(t, srv)()
	if _, err := run(t, "org", "apply", writeManifest(t, "schema_version: 9\nentities: {a: {}}\n"), "--dry-run"); err == nil || !strings.Contains(err.Error(), "schema_version") {
		t.Fatalf("want a schema error, got %v", err)
	}
}
