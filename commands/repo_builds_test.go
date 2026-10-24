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
// make the repository a deployment in the project instead.

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

func TestImportRailwayRepoServiceBecomesAProjectDeployment(t *testing.T) {
	planPath := writeRepoPlan(t, []railway.Action{
		{Op: "project.create", Name: "acme-api"},
		{Op: "database.create", Name: "Postgres", Engine: "postgres"},
		{Op: "service.create", Name: "api", Kind: "server",
			Source:   &railway.ServiceSource{Type: "git", Ref: "https://github.com/acme/api", Branch: "main"},
			Settings: &railway.ServiceSetting{StartCommand: "npm start", Replicas: 2}},
		{Op: "service.create", Name: "jobs", Kind: "worker",
			Source: &railway.ServiceSource{Type: "git", Ref: "https://github.com/acme/api"}},
		{Op: "service.create", Name: "cache", Kind: "server",
			Source: &railway.ServiceSource{Type: "image", Ref: "redis:7"}},
		{Op: "variable.set", Service: "api", Variable: &railway.Variable{Name: "LOG_LEVEL", Value: "info", HasValue: true}},
		{Op: "variable.set", Service: "api", Variable: &railway.Variable{Name: "DATABASE_URL", Value: "${{Postgres.DATABASE_URL}}", HasValue: true}},
		{Op: "variable.set", Service: "api", Variable: &railway.Variable{Name: "CACHE_HOST", Value: "${{cache.RAILWAY_PRIVATE_DOMAIN}}", HasValue: true}},
		{Op: "domain.custom", Service: "api", Domains: []string{"api.acme.com"}},
	})

	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/project-capabilities"):
			return 200, `{"data":{"services_api":true}}`
		case c.is("GET", "/projects"):
			return 200, `{"data":[]}`
		case c.is("POST", "/projects"):
			return 201, `{"data":{"id":"p1","name":"acme-api"}}`
		case c.is("GET", "/databases"):
			return 200, `{"data":[]}`
		case c.is("POST", "/databases"):
			return 201, `{"data":{"id":"db1"}}`
		case c.is("GET", "/projects/p1/services"):
			return 200, `{"data":[]}`
		case c.is("POST", "/projects/p1/services"):
			return 201, `{"data":{"id":"s1","name":"cache"}}`
		case c.is("POST", "/deployments"):
			return 201, `{"data":{"id":"d1","name":"api","state":"building"}}`
		case c.Method == "POST" && strings.HasPrefix(c.Path, "/deployments/d1/"):
			return 200, `{"data":{}}`
		case c.Method == "PUT", c.Method == "DELETE":
			return 200, `{"data":{}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})

	state := filepath.Join(t.TempDir(), "state.json")
	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", state)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}

	deploys := f.find("POST", "/deployments")
	if len(deploys) != 1 {
		t.Fatalf("want one deployment (api), got %d", len(deploys))
	}
	body := deploys[0].Body
	env, _ := body["env"].(map[string]interface{})
	if body["project_id"] != "p1" || body["repo_url"] != "https://github.com/acme/api" || body["branch"] != "main" ||
		body["run_command"] != "npm start" || env["LOG_LEVEL"] != "info" || len(env) != 1 {
		t.Fatalf("deployment body = %+v", body)
	}
	attach := f.find("POST", "/deployments/d1/database")
	if len(attach) != 1 || attach[0].Body["database_id"] != "db1" || attach[0].Body["env"] != "DATABASE_URL" {
		t.Fatalf("want DATABASE_URL attached to db1, got %+v", attach)
	}
	if domains := f.find("POST", "/deployments/d1/domains"); len(domains) != 1 || domains[0].Body["hostname"] != "api.acme.com" {
		t.Fatalf("want the custom domain on the deployment, got %+v", domains)
	}
	services := f.find("POST", "/projects/p1/services")
	if len(services) != 1 || services[0].Body["name"] != "cache" {
		t.Fatalf("want only the image service created through Services, got %+v", services)
	}
	for _, want := range []string{
		"created deployment api from https://github.com/acme/api",
		"DATABASE_URL connects to Postgres",
		"not carried to the deployment: 2 replicas",
		"service jobs not created: a worker runs as a service only",
		"deployments (repositories, building now): api",
		"miosa deploy env-set api --set CACHE_HOST=<value>",
		"miosa deploy get api",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}

	saved, _ := os.ReadFile(state)
	if !strings.Contains(string(saved), `"op": "deployment.create"`) && !strings.Contains(string(saved), `"op":"deployment.create"`) {
		t.Fatalf("want the deployment recorded for --resume and --cleanup, got %s", saved)
	}

	// --cleanup deletes the deployment it recorded.
	if _, err := run(t, "import", "railway", "--cleanup", "--state", state, "--yes"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if n := len(f.find("DELETE", "/deployments/d1")); n != 1 {
		t.Fatalf("want the deployment deleted once, got %d", n)
	}
}

func TestImportRailwayRepoServiceStaysAServiceWhereServicesBuild(t *testing.T) {
	planPath := writeRepoPlan(t, []railway.Action{
		{Op: "project.create", Name: "acme-api"},
		{Op: "service.create", Name: "api", Kind: "server",
			Source: &railway.ServiceSource{Type: "git", Ref: "https://github.com/acme/api"}},
	})
	f := newFakeAPI(t, func(c call) (int, string) {
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
}
