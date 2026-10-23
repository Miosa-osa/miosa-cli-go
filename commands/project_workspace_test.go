package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// optimal-qa (10-16): 'miosa project create <name>' failed WORKSPACE_REQUIRED
// unless --workspace-id was given; the console uses the default workspace.

const twoWorkspaces = `{"data":[
 {"id":"11111111-1111-4111-8111-111111111111","slug":"staging","name":"Staging","is_default":false},
 {"id":"22222222-2222-4222-8222-222222222222","slug":"default","name":"Default","is_default":true}]}`

func projectAPI(t *testing.T, workspaces string) *fakeAPI {
	t.Helper()
	return fakeRoutes(t, map[string]any{
		"GET /workspaces": workspaces,
		"POST /projects":  resp{201, `{"data":{"id":"p1","name":"shop"}}`},
	})
}

func createdIn(t *testing.T, f *fakeAPI) any {
	t.Helper()
	calls := f.find("POST", "/projects")
	if len(calls) != 1 {
		t.Fatalf("POST /projects calls = %d", len(calls))
	}
	return calls[0].Body["workspace_id"]
}

func TestProjectCreateDefaultsToTheDefaultWorkspace(t *testing.T) {
	f := projectAPI(t, twoWorkspaces)
	writeConfigFile(t, "default_workspace = \"\"\n")
	out, err := run(t, "project", "create", "shop")
	if err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if got := createdIn(t, f); got != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("workspace_id = %v", got)
	}
	if !strings.Contains(out, "Using workspace default") {
		t.Fatalf("out = %s", out)
	}
}

func TestProjectCreateUsesTheProfileWorkspace(t *testing.T) {
	f := projectAPI(t, twoWorkspaces)
	writeConfigFile(t, "default_workspace = \"staging\"\n")
	if out, err := run(t, "project", "create", "shop", "--json"); err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if got := createdIn(t, f); got != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("workspace_id = %v", got)
	}
}

func TestProjectCreateResolvesAWorkspaceSlug(t *testing.T) {
	f := projectAPI(t, twoWorkspaces)
	if out, err := run(t, "project", "create", "shop", "--workspace-id", "staging"); err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if got := createdIn(t, f); got != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("workspace_id = %v", got)
	}
}

func TestProjectCreateUsesTheOnlyWorkspace(t *testing.T) {
	f := projectAPI(t, `{"data":[{"id":"33333333-3333-4333-8333-333333333333","slug":"main","is_default":false}]}`)
	writeConfigFile(t, "default_workspace = \"\"\n")
	if out, err := run(t, "project", "create", "shop"); err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if got := createdIn(t, f); got != "33333333-3333-4333-8333-333333333333" {
		t.Fatalf("workspace_id = %v", got)
	}
}

func TestProjectCreateWithNoDefaultAsksForOne(t *testing.T) {
	f := projectAPI(t, `{"data":[{"id":"1","slug":"b","is_default":false},{"id":"2","slug":"a","is_default":false}]}`)
	writeConfigFile(t, "default_workspace = \"\"\n")
	out, err := run(t, "project", "create", "shop")
	if err == nil || commands.ExitCode(err) != 2 {
		t.Fatalf("want a usage error, got %v\n%s", err, out)
	}
	if !strings.Contains(err.Error(), "one of: a, b") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", "/projects")) != 0 {
		t.Fatal("nothing may be created")
	}
}

// QA 10-22: 'miosa project delete <name> --yes' failed because the verb did
// not exist at all. It exists now, and it resolves a name or slug to the id the
// API needs, exactly like 'project get'.
func TestProjectDeleteResolvesANameAndIssuesTheDelete(t *testing.T) {
	const id = "33333333-3333-4333-8333-333333333333"

	f := fakeRoutes(t, map[string]any{
		"GET /projects":          `{"data":[{"id":"` + id + `","name":"shop","slug":"shop"}]}`,
		"DELETE /projects/" + id: resp{204, ``},
	})

	out, err := run(t, "project", "rm", "shop", "--yes")
	if err != nil {
		t.Fatalf("project rm: %v\n%s", err, out)
	}

	if len(f.find("DELETE", "/projects/"+id)) != 1 {
		t.Fatalf("DELETE /projects/%s was not called; calls = %v", id, f.find("DELETE", "/projects/"+id))
	}

	if !strings.Contains(out, "Deleted") {
		t.Fatalf("out = %s", out)
	}
}

func TestProjectDeleteIsAlsoAvailableAsDelete(t *testing.T) {
	const id = "44444444-4444-4444-8444-444444444444"

	f := fakeRoutes(t, map[string]any{
		"GET /projects":          `{"data":[{"id":"` + id + `","name":"shop","slug":"shop"}]}`,
		"DELETE /projects/" + id: resp{204, ``},
	})

	if out, err := run(t, "project", "delete", "shop", "--yes"); err != nil {
		t.Fatalf("project delete: %v\n%s", err, out)
	}

	if len(f.find("DELETE", "/projects/"+id)) != 1 {
		t.Fatalf("DELETE /projects/%s was not called", id)
	}
}

// The live /workspaces answer is `{"data":[{id, name, slug, is_default, ...}]}`
// and an organization can hold dozens: acme-qa had 40+ test workspaces with the
// real default far from the front. The rehearsal failed there, so these use the
// real shape and put the default where it actually was.
const manyWorkspaces = `{"data":[
 {"id":"a1","name":"engine-console-bound","slug":"engine-console-bound-470441","is_default":false,"tenant_id":"t"},
 {"id":"a2","name":"engine-console-final","slug":"engine-console-final-ws-886483","is_default":false,"tenant_id":"t"},
 {"id":"a3","name":"probe","slug":"engine-console-probe-291942","is_default":false,"tenant_id":"t"},
 {"id":"b89beea8-9094-43ef-9e00-477520c0932e","name":"Default","slug":"default","is_default":true,"tenant_id":"t"},
 {"id":"a4","name":"zzz","slug":"zzz","is_default":false,"tenant_id":"t"}]}`

func TestProjectCreateFindsTheDefaultWorkspaceDeepInTheList(t *testing.T) {
	f := projectAPI(t, manyWorkspaces)
	writeConfigFile(t, "default_workspace = \"\"\n")

	if out, err := run(t, "project", "create", "shop"); err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if got := createdIn(t, f); got != "b89beea8-9094-43ef-9e00-477520c0932e" {
		t.Fatalf("workspace_id = %v", got)
	}
}

func TestProjectCreatePrefersTheSlugDefaultWhenNoneIsMarked(t *testing.T) {
	f := projectAPI(t, `{"data":[
	 {"id":"x1","name":"one","slug":"one","is_default":false},
	 {"id":"x2","name":"Default","slug":"default","is_default":false}]}`)
	writeConfigFile(t, "default_workspace = \"\"\n")

	if out, err := run(t, "project", "create", "shop"); err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if got := createdIn(t, f); got != "x2" {
		t.Fatalf("workspace_id = %v", got)
	}
}

func TestProjectCreateReadsTheWrappedWorkspaceShape(t *testing.T) {
	f := projectAPI(t, `{"data":{"workspaces":[{"id":"w9","name":"Default","slug":"default","is_default":true}]}}`)
	writeConfigFile(t, "default_workspace = \"\"\n")

	if out, err := run(t, "project", "create", "shop"); err != nil {
		t.Fatalf("project create: %v\n%s", err, out)
	}
	if got := createdIn(t, f); got != "w9" {
		t.Fatalf("workspace_id = %v", got)
	}
}

func TestProjectCreateRefusesAnUnknownWorkspace(t *testing.T) {
	f := projectAPI(t, manyWorkspaces)
	writeConfigFile(t, "default_workspace = \"\"\n")

	_, err := run(t, "project", "create", "shop", "--workspace", "nope")
	if err == nil || commands.ExitCode(err) != 2 || !strings.Contains(err.Error(), "default") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", "/projects")) != 0 {
		t.Fatal("nothing may be created")
	}
}

func TestProjectCreateSaysWhatToDoWhenTheListCannotBeRead(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /workspaces": resp{500, `{"error":{"code":"INTERNAL","message":"boom"}}`},
		"POST /projects":  resp{201, `{"data":{"id":"p1","name":"shop"}}`},
	})
	writeConfigFile(t, "default_workspace = \"\"\n")

	_, err := run(t, "project", "create", "shop")
	if err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", "/projects")) != 0 {
		t.Fatal("nothing may be created")
	}
}
