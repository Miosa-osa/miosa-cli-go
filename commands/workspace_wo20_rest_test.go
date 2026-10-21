package commands_test

import (
	"os"
	"strings"
	"testing"
)

// WO-20, the rest of the CLI surface: the hand-written creates, the exec/files
// banner, rename, and the per-organization default. The declarative creates
// (schedule, bucket, agent, webhook) are covered by workspace_creates_test.go.

const (
	wo20KeyCfg   = "api_key = \"msk_u_test\"\nactive_org = \"acme\"\n"
	wo20WSID     = "11111111-1111-4111-8111-111111111111"
	wo20OtherID  = "22222222-2222-4222-8222-222222222222"
	wo20OtherOrg = "beta"
)

func TestComputerCreateResolvesTheWorkspaceFlag(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /bill-to":    `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /workspaces": twoWorkspacesForCli,
		"POST /computers": `{"id":"c-1","name":"desk","status":"provisioning","size":"small","workspace_id":"` + wo20WSID + `"}`,
	})
	writeConfigFile(t, wo20KeyCfg)

	out, err := run(t, "computer", "create", "desk", "--workspace", "clips", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("POST", "/computers")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != wo20WSID {
		t.Fatalf("body = %+v", calls)
	}
	if !strings.Contains(out, wo20WSID) {
		t.Fatalf("--json must carry workspace_id:\n%s", out)
	}
}

func TestComputerCreateUsesTheProfileDefault(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /workspaces": twoWorkspacesForCli,
		"POST /computers": `{"id":"c-1","name":"desk","status":"provisioning","size":"small","workspace_id":"` + wo20WSID + `"}`,
	})
	writeConfigFile(t, "api_key = \"msk_u_test\"\ndefault_workspace = \"clips\"\n")

	if out, err := run(t, "computer", "create", "desk", "--json"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("POST", "/computers")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != wo20WSID {
		t.Fatalf("body = %+v", calls)
	}
}

func TestSandboxCreateResolvesTheWorkspaceAndReportsIt(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /bill-to":    `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /workspaces": twoWorkspacesForCli,
		"POST /sandboxes": `{"id":"sb-1","name":"box","state":"running","template_id":"miosa-sandbox","workspace_id":"` + wo20WSID + `"}`,
	})
	writeConfigFile(t, wo20KeyCfg)

	out, err := run(t, "create", "box", "--size", "micro", "--workspace", "clips")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("POST", "/sandboxes")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != wo20WSID || calls[0].Body["workspace_slug"] != nil {
		t.Fatalf("body = %+v", calls)
	}
	if !strings.Contains(out, "workspace: "+wo20WSID) {
		t.Fatalf("a create must print where it landed:\n%s", out)
	}
}

func TestDbCreateResolvesTheWorkspaceFlag(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /bill-to":    `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /workspaces": twoWorkspacesForCli,
		"POST /databases": `{"id":"d-1","name":"app-db","state":"provisioning","workspace_id":"` + wo20WSID + `"}`,
	})
	writeConfigFile(t, wo20KeyCfg)

	out, err := run(t, "db", "create", "app-db", "--workspace", "clips", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("POST", "/databases")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != wo20WSID {
		t.Fatalf("body = %+v", calls)
	}
}

func TestDeployCreateSendsWorkspaceOnGit(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /bill-to":                   `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /workspaces":                twoWorkspacesForCli,
		"GET /deployments/my-app":        resp{404, `{"error":{"code":"NOT_FOUND"}}`},
		"POST /deployments":              `{"data":{"id":"d-1","name":"my-app","state":"building","public_url":"https://my-app.example","workspace_id":"` + wo20WSID + `"}}`,
		"POST /deployments/d-1/redeploy": `{}`,
	})
	writeConfigFile(t, wo20KeyCfg)

	out, err := run(t, "deploy", "create", "my-app", "--git", "https://example.com/me/app.git", "--workspace", "clips", "--no-wait", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("POST", "/deployments")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != wo20WSID {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(out, wo20WSID) {
		t.Fatalf("--json must carry workspace_id:\n%s", out)
	}
}

func TestWorkspaceRenamePatchesNameAndSlug(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /bill-to":                  `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /workspaces":               twoWorkspacesForCli,
		"PATCH /workspaces/" + wo20WSID: `{"data":{"id":"` + wo20WSID + `","name":"Clips EU","slug":"clips-eu"}}`,
	})
	writeConfigFile(t, wo20KeyCfg)

	out, err := run(t, "workspace", "rename", "clips", "Clips EU", "--slug", "clips-eu")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	calls := f.find("PATCH", "/workspaces/"+wo20WSID)
	if len(calls) != 1 || calls[0].Body["name"] != "Clips EU" || calls[0].Body["slug"] != "clips-eu" {
		t.Fatalf("calls = %+v", calls)
	}
	if !strings.Contains(out, "slug changed") {
		t.Fatalf("a slug change must warn about the mapping:\n%s", out)
	}
}

func TestExecBannerNamesTheResourcesOrgAndWorkspace(t *testing.T) {
	routes := map[string]any{
		"GET /bill-to":                       `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /sandboxes/by-name/box-1":       `{"id":"` + depID + `"}`,
		"GET /sandboxes/" + depID:            `{"data":{"id":"` + depID + `","workspace_id":"` + wo20WSID + `"}}`,
		"GET /workspaces/" + wo20WSID:        `{"data":{"id":"` + wo20WSID + `","slug":"clips"}}`,
		"POST /sandboxes/" + depID + "/exec": `{"exit_code":0,"stdout":"","stderr":""}`,
	}
	fakeRoutes(t, routes)
	writeConfigFile(t, wo20KeyCfg)

	out, err := run(t, "exec", "box-1", "--", "true")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	first := strings.SplitN(out, "\n", 2)[0]
	if first != "miosa: org acme, workspace clips" {
		t.Fatalf("first line = %q, want the banner", first)
	}
}

func TestFilesBannerNamesTheResourcesOrgAndWorkspace(t *testing.T) {
	routes := map[string]any{
		"GET /bill-to":                    `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /sandboxes/by-name/my-box":   `{"id":"` + depID + `"}`,
		"GET /sandboxes/" + depID:         `{"data":{"id":"` + depID + `","workspace_id":"` + wo20WSID + `"}}`,
		"GET /workspaces/" + wo20WSID:     `{"data":{"id":"` + wo20WSID + `","slug":"clips"}}`,
		"GET /sandboxes/" + depID + "/fs": `{"path":"/workspace","files":[{"name":"main.py","type":"file","size_bytes":7}]}`,
	}
	fakeRoutes(t, routes)
	writeConfigFile(t, wo20KeyCfg)

	out, err := run(t, "files", "ls", "my-box:/workspace")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	first := strings.SplitN(out, "\n", 2)[0]
	if first != "miosa: org acme, workspace clips" {
		t.Fatalf("first line = %q, want the banner", first)
	}
}

// WO-20 criterion 4: `workspace use` changes only the current organization's
// default, and a create reads the default of the organization it acts in.

func TestCreateUsesTheDefaultOfTheActiveOrganization(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /bill-to":    `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /workspaces": twoWorkspacesForCli,
		"POST /sandboxes": `{"id":"abc123","name":"my-box","state":"running","template_id":"miosa-sandbox"}`,
	})
	writeConfigFile(t, `api_key = "msk_u_test"
active_org = "acme"
default_workspace = "other"
[default_workspaces]
acme = "clips"
`)

	if out, err := run(t, "create", "my-box"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	calls := f.find("POST", "/sandboxes")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != wo20WSID {
		t.Fatalf("body = %+v", calls)
	}
}

func TestCreateFallsBackToTheFlatDefaultForAnotherOrganization(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /bill-to":    `{"data":{"options":[{"id":"t-2","slug":"beta","name":"Beta","viewing":true}]}}`,
		"GET /workspaces": twoWorkspacesForCli,
		"POST /sandboxes": `{"id":"abc123","name":"my-box","state":"running","template_id":"miosa-sandbox"}`,
	})
	writeConfigFile(t, `api_key = "msk_u_test"
active_org = "beta"
default_workspace = "other"
[default_workspaces]
acme = "clips"
`)

	if out, err := run(t, "create", "my-box"); err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}
	calls := f.find("POST", "/sandboxes")
	if len(calls) != 1 || calls[0].Body["workspace_id"] != wo20OtherID {
		t.Fatalf("body = %+v", calls)
	}
}

func TestWorkspaceUseWritesOnlyTheActiveOrganizationsDefault(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /bill-to":                `{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`,
		"GET /workspaces":             twoWorkspacesForCli,
		"GET /workspaces/" + wo20WSID: `{"data":{"id":"` + wo20WSID + `","slug":"clips","name":"Clips"}}`,
	})
	path := writeConfigFile(t, `api_key = "msk_u_test"
active_org = "acme"
default_workspace = "other"
[default_workspaces]
beta = "kept"
`)

	if out, err := run(t, "workspace", "use", "clips"); err != nil {
		t.Fatalf("workspace use: %v\n%s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	if !strings.Contains(got, "[default_workspaces]") || !strings.Contains(got, `acme = "clips"`) || !strings.Contains(got, `beta = "kept"`) {
		t.Fatalf("the per-organization map must keep beta and gain acme:\n%s", got)
	}
	if !strings.Contains(got, `default_workspace = "other"`) {
		t.Fatalf("the flat default must not change:\n%s", got)
	}
}
