package commands_test

import (
	"strings"
	"testing"
)

const changesPath = "/projects/11111111-1111-1111-1111-111111111111/changes"

func changesAPI(t *testing.T) *fakeAPI {
	t.Helper()
	routes := map[string]any{}
	routes["GET /projects"] = serviceProjectJSON
	routes["GET /projects/11111111-1111-1111-1111-111111111111/environments"] = `{"data":[{"id":"env-prod","name":"production","slug":"production","is_default":true},{"id":"env-stg","name":"Staging","slug":"staging"},{"id":"env-mz","name":"Maazin","slug":"maazin"}]}`
	routes["GET "+changesPath] = `{"data":{"id":"set1","state":"open","stale":false,"changes":[{"id":"c1","seq":1,"op":"variable.set","summary":"Set API_BASE"},{"id":"c2","seq":2,"op":"service.create","summary":"Create worker"}]}}`
	routes["POST "+changesPath+"/apply"] = `{"data":{"id":"set1","state":"applied","changes":[{"id":"c1"},{"id":"c2"}],"releases":[{"service_id":"s1"}]}}`
	routes["POST "+changesPath+"/discard"] = resp{204, ``}
	routes["DELETE "+changesPath+"/c1"] = resp{204, ``}
	return fakeRoutes(t, routes)
}

func TestProjectChangesListShowsTheStagedSet(t *testing.T) {
	f := changesAPI(t)
	out, err := run(t, "project", "changes", "list", "cliniciq", "--deploy-target", "staging")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Set API_BASE") || !strings.Contains(out, "Create worker") {
		t.Fatalf("out = %s", out)
	}
	// The target is resolved to its id, whatever case it was typed in.
	if q := f.find("GET", changesPath)[0].Query; q != "env=env-stg" {
		t.Fatalf("query = %q", q)
	}
}

func TestProjectChangesApplyDeploysByDefault(t *testing.T) {
	f := changesAPI(t)
	out, err := run(t, "project", "changes", "apply", "cliniciq")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Applied 2 changes") || !strings.Contains(out, "Deploying 1 service") {
		t.Fatalf("out = %s", out)
	}
	if q := f.find("POST", changesPath+"/apply")[0].Query; q != "" {
		t.Fatalf("query = %q", q)
	}
	if _, err := run(t, "project", "changes", "apply", "cliniciq", "--no-deploy"); err != nil {
		t.Fatal(err)
	}
	if q := f.find("POST", changesPath+"/apply")[1].Query; q != "deploy=false" {
		t.Fatalf("--no-deploy query = %q", q)
	}
}

func TestProjectChangesDiscardNeedsYesOffATerminal(t *testing.T) {
	f := changesAPI(t)
	if _, err := run(t, "project", "changes", "discard", "cliniciq"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", changesPath+"/discard")) != 0 {
		t.Fatal("discarded without --yes")
	}
	if _, err := run(t, "project", "changes", "discard", "cliniciq", "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "project", "changes", "rm", "cliniciq", "c1"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("POST", changesPath+"/discard")) != 1 || len(f.find("DELETE", changesPath+"/c1")) != 1 {
		t.Fatal("want one discard and one unstage")
	}
}

func TestDBCreateIntoAProjectUsesItsWorkspace(t *testing.T) {
	routes := map[string]any{}
	routes["GET /projects"] = `{"data":[{"id":"11111111-1111-1111-1111-111111111111","name":"Shop","slug":"shop","workspace_id":"22222222-2222-2222-2222-222222222222"}]}`
	routes["GET /workspaces/22222222-2222-2222-2222-222222222222"] = `{"data":{"id":"22222222-2222-2222-2222-222222222222","slug":"apps","name":"Apps"}}`
	routes["GET /workspaces"] = `{"data":[{"id":"22222222-2222-2222-2222-222222222222","slug":"apps","name":"Apps"}]}`
	routes["POST /databases"] = resp{201, `{"id":"d1","name":"app-db","engine":"postgres","state":"creating","workspace_id":"22222222-2222-2222-2222-222222222222","project_id":"11111111-1111-1111-1111-111111111111"}`}
	f := fakeRoutes(t, routes)
	if _, err := run(t, "db", "create", "app-db", "--project", "shop"); err != nil {
		t.Fatal(err)
	}
	body := f.find("POST", "/databases")[0].Body
	if body["project_id"] != "11111111-1111-1111-1111-111111111111" || body["workspace_id"] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("body = %v", body)
	}
}

func TestDBCreateIntoAnUnknownProjectIsRefused(t *testing.T) {
	routes := map[string]any{}
	routes["GET /projects"] = `{"data":[]}`
	f := fakeRoutes(t, routes)
	_, err := run(t, "db", "create", "app-db", "--project", "nope")
	if err == nil || !strings.Contains(err.Error(), `no project named "nope"`) {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", "/databases")) != 0 {
		t.Fatal("created without a project")
	}
}

func TestDeployTargetMatchesAnyCaseAndListsTargetsOnAMiss(t *testing.T) {
	for _, typed := range []string{"Maazin", "maazin", "MAAZIN", "env-mz"} {
		f := changesAPI(t)
		if _, err := run(t, "project", "changes", "list", "cliniciq", "--deploy-target", typed); err != nil {
			t.Fatalf("--deploy-target %s: %v", typed, err)
		}
		if q := f.find("GET", changesPath)[0].Query; q != "env=env-mz" {
			t.Fatalf("--deploy-target %s sent %q", typed, q)
		}
	}
	changesAPI(t)
	_, err := run(t, "project", "changes", "list", "cliniciq", "--deploy-target", "qa")
	if err == nil || !strings.Contains(err.Error(), `no Deploy target "qa"; this project has: Maazin, Staging, production`) {
		t.Fatalf("a miss must list the targets, got %v", err)
	}
}
