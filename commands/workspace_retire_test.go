package commands_test

import (
	"strings"
	"testing"
)

const retireWorkspaceID = "11111111-1111-4111-8111-111111111111"

func retireRoutes() map[string]any {
	return map[string]any{
		"GET /workspaces/" + retireWorkspaceID:                `{"data":{"id":"` + retireWorkspaceID + `","name":"clips","slug":"clips"}}`,
		"GET /workspaces/" + retireWorkspaceID + "/inventory": `{"data":{"counts":{"sandboxes":3,"databases":1}}}`,
		"POST /workspaces/" + retireWorkspaceID + "/cleanup":  `{"data":{"deletion_order":["sandboxes","databases"],"resources":{"sandboxes":[{"name":"a","state":"running"},{"name":"b","state":"running"},{"name":"c","state":"running"}],"databases":[{"name":"db","state":"running"}]},"deleted":[{},{},{},{}]}}`,
		"DELETE /workspaces/" + retireWorkspaceID:             `{"data":{"released":{"volumes":2}}}`,
	}
}

func TestWorkspaceRetireRefusesWithoutConfirmation(t *testing.T) {
	f := fakeRoutes(t, retireRoutes())

	_, err := run(t, "workspace", "retire", retireWorkspaceID)
	if err == nil {
		t.Fatal("retire without --yes must refuse off a terminal")
	}
	if len(f.find("POST", "/workspaces/"+retireWorkspaceID+"/cleanup")) != 0 {
		t.Fatal("nothing may be cleaned up before confirmation")
	}
	if len(f.find("DELETE", "/workspaces/"+retireWorkspaceID)) != 0 {
		t.Fatal("nothing may be deleted before confirmation")
	}
}

func TestWorkspaceRetireDryRunChangesNothing(t *testing.T) {
	f := fakeRoutes(t, retireRoutes())

	out, err := run(t, "workspace", "retire", retireWorkspaceID, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Would delete, in order:") {
		t.Fatalf("output = %q, want the deletion order", out)
	}
	// The engine's order is sandboxes before databases; the listing must keep it.
	if strings.Index(out, "sandboxes:") > strings.Index(out, "databases:") {
		t.Fatalf("deletion order not preserved:\n%s", out)
	}

	calls := f.find("POST", "/workspaces/"+retireWorkspaceID+"/cleanup")
	if len(calls) != 1 {
		t.Fatalf("cleanup calls = %d, want 1", len(calls))
	}
	if calls[0].Body["dry_run"] != true {
		t.Fatalf("dry_run = %v, want true", calls[0].Body["dry_run"])
	}
	if len(f.find("DELETE", "/workspaces/"+retireWorkspaceID)) != 0 {
		t.Fatal("a dry run must never delete the workspace")
	}
}

func TestWorkspaceRetireForcesCleanupThenDeletes(t *testing.T) {
	f := fakeRoutes(t, retireRoutes())

	out, err := run(t, "workspace", "retire", retireWorkspaceID, "--yes")
	if err != nil {
		t.Fatalf("retire: %v\n%s", err, out)
	}
	if !strings.Contains(out, "deleted 4 resource(s)") {
		t.Fatalf("output = %q, want the deleted count", out)
	}
	if !strings.Contains(out, "Retired workspace") {
		t.Fatalf("output = %q, want the retired line", out)
	}

	cleanup := f.find("POST", "/workspaces/"+retireWorkspaceID+"/cleanup")
	if len(cleanup) != 1 || cleanup[0].Body["force"] != true {
		t.Fatalf("cleanup must be forced: %+v", cleanup)
	}
	if len(f.find("DELETE", "/workspaces/"+retireWorkspaceID)) != 1 {
		t.Fatal("the workspace must be deleted after its resources")
	}
}

// A workspace that holds production: one sandbox tagged purpose=production and
// one deployment that is running.
func retireRoutesWithProduction() map[string]any {
	routes := retireRoutes()
	routes["GET /workspaces/"+retireWorkspaceID+"/inventory"] = `{"data":{"counts":{"sandboxes":2,"deployments":1},"resources":{` +
		`"sandboxes":[{"name":"build-1","state":"running","tags":{"purpose":"test"}},` +
		`{"name":"prod-api","state":"running","tags":{"purpose":"production"}}],` +
		`"deployments":[{"name":"site","state":"running"}]}}}`
	routes["POST /workspaces/"+retireWorkspaceID+"/cleanup"] = `{"data":{"deletion_order":["sandboxes","deployments"],"resources":{` +
		`"sandboxes":[{"name":"build-1","state":"running"},{"name":"prod-api","state":"running"}],` +
		`"deployments":[{"name":"site","state":"running"}]},"deleted":[]}}`
	return routes
}

func TestWorkspaceRetireRefusesProduction(t *testing.T) {
	f := fakeRoutes(t, retireRoutesWithProduction())

	out, err := run(t, "workspace", "retire", retireWorkspaceID, "--yes")
	if err == nil {
		t.Fatalf("a production-tagged resource must refuse the retire:\n%s", out)
	}
	if !strings.Contains(out, "prod-api") || !strings.Contains(out, "purpose=production") {
		t.Fatalf("output = %q, want the production resource listed", out)
	}
	if !strings.Contains(out, "site") || !strings.Contains(out, "serving traffic") {
		t.Fatalf("output = %q, want the running deployment listed", out)
	}
	if !strings.Contains(out, "--include-production") {
		t.Fatalf("output = %q, want the way forward", out)
	}
	if len(f.find("POST", "/workspaces/"+retireWorkspaceID+"/cleanup")) != 0 {
		t.Fatal("nothing may be cleaned up when production blocks the retire")
	}
	if len(f.find("DELETE", "/workspaces/"+retireWorkspaceID)) != 0 {
		t.Fatal("nothing may be deleted when production blocks the retire")
	}
}

func TestWorkspaceRetireIncludesProductionOnRequest(t *testing.T) {
	f := fakeRoutes(t, retireRoutesWithProduction())

	out, err := run(t, "workspace", "retire", retireWorkspaceID, "--yes", "--include-production")
	if err != nil {
		t.Fatalf("retire --include-production: %v\n%s", err, out)
	}
	if len(f.find("POST", "/workspaces/"+retireWorkspaceID+"/cleanup")) != 1 {
		t.Fatal("cleanup must run once production is explicitly included")
	}
	if len(f.find("DELETE", "/workspaces/"+retireWorkspaceID)) != 1 {
		t.Fatal("the workspace must be deleted")
	}
}

func TestWorkspaceRetireDryRunShowsProductionBlocker(t *testing.T) {
	f := fakeRoutes(t, retireRoutesWithProduction())

	out, err := run(t, "workspace", "retire", retireWorkspaceID, "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Production would block this") {
		t.Fatalf("output = %q, want the production warning", out)
	}
	if !strings.Contains(out, "Would delete, in order:") {
		t.Fatalf("output = %q, want the deletion order", out)
	}
	if len(f.find("DELETE", "/workspaces/"+retireWorkspaceID)) != 0 {
		t.Fatal("a dry run must never delete")
	}
}
