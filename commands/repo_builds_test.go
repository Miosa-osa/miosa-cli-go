package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
)

// Where services cannot build a repository (source_builds false, or a server
// that does not say), a git service deployment sat queued until its deadline.
// The CLI refuses it up front with the command that works, and the importers
// refuse before creating anything (import_railway_clinic_test.go).

const projectID = "11111111-1111-1111-1111-111111111111"

func noRepoBuilds(extra map[string]any) map[string]any {
	routes := map[string]any{
		"GET /project-capabilities":                `{"data":{"services_api":true,"source_builds":false}}`,
		"GET /projects":                            serviceProjectJSON,
		"GET /projects/" + projectID + "/services": webServiceJSON,
		"GET /projects/" + projectID + "/services/33333333-3333-3333-3333-333333333333":              oneWebServiceJSON,
		"POST /projects/" + projectID + "/services":                                                  oneWebServiceJSON,
		"POST /projects/" + projectID + "/services/33333333-3333-3333-3333-333333333333/deployments": resp{201, `{"data":{"id":"r1","state":"queued"}}`},
	}
	for k, v := range extra {
		routes[k] = v
	}
	return routes
}

func TestServiceCreateRepoRefusedWithTheWorkingCommand(t *testing.T) {
	f := fakeRoutes(t, noRepoBuilds(nil))
	_, err := run(t, "service", "create", "cliniciq", "API", "--repo", "https://github.com/acme/api", "--branch", "main")
	if err == nil {
		t.Fatal("want a refusal")
	}
	for _, want := range []string{
		"services cannot build from a repository yet",
		"miosa deploy create --git https://github.com/acme/api --project cliniciq --name API --branch main",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal is missing %q:\n%v", want, err)
		}
	}
	if n := len(f.find("POST", "/projects/"+projectID+"/services")); n != 0 {
		t.Fatalf("want nothing created, got %d service create(s)", n)
	}
}

func TestServiceCreateRepoRefusedWhenTheServerSaysSo(t *testing.T) {
	refusal := `{"error":{"code":"SOURCE_BUILD_UNAVAILABLE","message":"Services cannot build from a repository yet."}}`
	fakeRoutes(t, noRepoBuilds(map[string]any{
		"GET /project-capabilities":                 `{"data":{"source_builds":true}}`,
		"POST /projects/" + projectID + "/services": resp{422, refusal},
	}))
	_, err := run(t, "service", "create", "cliniciq", "API", "--repo", "https://github.com/acme/api")
	if err == nil || !strings.Contains(err.Error(), "miosa deploy create --git https://github.com/acme/api --project cliniciq --name API") {
		t.Fatalf("want the working command, got %v", err)
	}
}

func TestServiceCreateImageIsNotRefused(t *testing.T) {
	f := fakeRoutes(t, noRepoBuilds(map[string]any{
		"PUT /projects/" + projectID + "/services/33333333-3333-3333-3333-333333333333/public-address": `{"data":{}}`,
	}))
	if _, err := run(t, "service", "create", "cliniciq", "API", "--image", "nginx:1.27"); err != nil {
		t.Fatalf("service create --image: %v", err)
	}
	if n := len(f.find("POST", "/projects/"+projectID+"/services")); n != 1 {
		t.Fatalf("want the service created, got %d", n)
	}
}

func TestServiceDeployOfARepoServiceIsRefused(t *testing.T) {
	f := fakeRoutes(t, noRepoBuilds(nil))
	_, err := run(t, "service", "deploy", "cliniciq", "API")
	if err == nil || !strings.Contains(err.Error(), "miosa deploy create --git https://github.com/acme/api --project cliniciq --name API") {
		t.Fatalf("want the working command, got %v", err)
	}
	if n := len(f.find("POST", "/projects/"+projectID+"/services/33333333-3333-3333-3333-333333333333/deployments")); n != 0 {
		t.Fatalf("want nothing queued, got %d", n)
	}
}

func TestDeployCreateGitPutsTheDeploymentInTheProject(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /projects":     serviceProjectJSON,
		"POST /deployments": resp{201, `{"data":{"id":"d1","name":"api","state":"building"}}`},
	})
	if _, err := run(t, "deploy", "create", "--git", "https://github.com/acme/api", "--project", "cliniciq", "--name", "api", "--no-wait"); err != nil {
		t.Fatalf("deploy create: %v", err)
	}
	created := f.find("POST", "/deployments")
	if len(created) != 1 || created[0].Body["project_id"] != projectID || created[0].Body["repo_url"] != "https://github.com/acme/api" {
		t.Fatalf("want the deployment created in the project, got %+v", created)
	}
}

func writeRepoPlan(t *testing.T, actions []railway.Action) string {
	t.Helper()
	plan := railway.Plan{
		Version: railway.PlanVersion,
		Source:  railway.PlanSource{Kind: "railway", ProjectName: "acme-api", Digest: "sha256:abc"},
		Target:  railway.PlanTarget{ProjectName: "acme-api"},
		Actions: actions,
	}
	body, _ := json.Marshal(plan)
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportRailwayRepoServiceStaysAServiceWhereServicesBuild(t *testing.T) {
	planPath := writeRepoPlan(t, []railway.Action{
		{Op: "project.create", Name: "acme-api"},
		{Op: "service.create", Name: "api", Kind: "server",
			Source: &railway.ServiceSource{Type: "git", Ref: "https://github.com/acme/api"}},
	})
	f := newFakeAPI(t, func(c call) (int, string) {
		if status, body, ok := githubRoutes(c); ok {
			return status, body
		}
		switch {
		case c.is("GET", "/project-capabilities"):
			return 200, `{"data":{"source_builds":true}}`
		case c.is("GET", "/projects"):
			return 200, `{"data":[]}`
		case c.is("POST", "/projects"):
			return 201, `{"data":{"id":"p1","name":"acme-api"}}`
		case c.is("GET", "/projects/p1/services"):
			return 200, `{"data":[]}`
		case c.is("POST", "/projects/p1/services"):
			return 201, `{"data":{"id":"s1","name":"api"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	if out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "s.json")); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if len(f.find("POST", "/deployments")) != 0 || len(f.find("POST", "/projects/p1/services")) != 1 {
		t.Fatalf("want a Services service and no deployment")
	}
	if len(f.find("PUT", "/projects/p1/services/s1/source/github")) != 1 {
		t.Fatalf("want the repository linked through the GitHub connection")
	}
}
