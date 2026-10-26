package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
)

// The harness (run, newFakeAPI, call, setupEnv) lives in the other _test files
// of package commands_test.

// railwayFixture is a small Railway project: one git service with a reference
// and a literal variable, and a Postgres plugin.
//
// Synthetic: built from Railway's public schema and docs. It is never recorded
// from a Railway account.
func railwayFixture(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("content-type", "application/json")
		switch {
		case strings.Contains(body.Query, "query Projects"):
			_, _ = w.Write([]byte(`{"data":{"projects":{"edges":[{"node":{"id":"11111111-1111-1111-1111-111111111111","name":"acme-api"}}]}}}`))
		case strings.Contains(body.Query, "query Project("):
			_, _ = w.Write([]byte(`{"data":{"project":{"id":"11111111-1111-1111-1111-111111111111","name":"acme-api","environments":{"edges":[{"node":{"id":"e1","name":"production"}}]}}}}`))
		case strings.Contains(body.Query, "query Environment"):
			_, _ = w.Write([]byte(`{"data":{"environment":{"id":"e1","name":"production",
				"serviceInstances":{"edges":[{"node":{"serviceId":"s1","serviceName":"api","source":{"repo":"acme/api"},
				"buildCommand":"npm run build","startCommand":"npm start","numReplicas":2,"region":"us-east4"}}]},
				"volumeInstances":{"edges":[]}}}}`))
		case strings.Contains(body.Query, "query Variables") && body.Variables["serviceId"] == nil:
			_, _ = w.Write([]byte(`{"data":{"variables":{}}}`))
		case strings.Contains(body.Query, "query Variables"):
			_, _ = w.Write([]byte(`{"data":{"variables":{"DATABASE_URL":"${{Postgres.DATABASE_URL}}","LOG_LEVEL":"info"}}}`))
		case strings.Contains(body.Query, "query Domains"):
			_, _ = w.Write([]byte(`{"data":{"domains":{"serviceDomains":[{"id":"d1","domain":"api-production.up.railway.app"}],"customDomains":[]}}}`))
		case strings.Contains(body.Query, "query Triggers"):
			_, _ = w.Write([]byte(`{"data":{"deploymentTriggers":{"edges":[{"node":{"id":"t1","branch":"main"}}]}}}`))
		case strings.Contains(body.Query, "query Plugins"):
			_, _ = w.Write([]byte(`{"data":{"project":{"plugins":{"edges":[{"node":{"id":"pl1","name":"postgresql","friendlyName":"Postgres"}}]}}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportRailwayDryRunWritesAReviewablePlan(t *testing.T) {
	rail := railwayFixture(t)
	t.Setenv("RAILWAY_API_URL", rail.URL)
	t.Setenv("RAILWAY_TOKEN", "rail_secret_value")

	out := filepath.Join(t.TempDir(), "plan.json")
	stdout, err := run(t, "import", "railway", "--project", "acme-api", "--out", out)
	if err != nil {
		t.Fatalf("import railway: %v", err)
	}
	for _, want := range []string{
		"create project acme-api",
		"create server api from https://github.com/acme/api (main)",
		"create database Postgres (postgres)",
		"production/api.DATABASE_URL (reference ${{Postgres.DATABASE_URL}})",
		"production/api.LOG_LEVEL",
		"Variables that need a value (1)",
		"Finish by hand:",
		"build_command: npm run build",
		"Rotate every secret",
		"Nothing was created",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("review is missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "rail_secret_value") {
		t.Fatal("the token must never be printed")
	}

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var plan railway.Plan
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Version != railway.PlanVersion || plan.Source.ProjectName != "acme-api" || plan.Source.Digest == "" {
		t.Fatalf("plan = %+v", plan)
	}
	if strings.Contains(string(body), "rail_secret_value") {
		t.Fatal("the token must never reach the plan")
	}
}

func TestImportRailwayFromFileNeedsNoToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "railway.toml")
	if err := os.WriteFile(path, []byte(`
[build]
dockerfilePath = "Dockerfile.prod"

[deploy]
startCommand = "npm start"
numReplicas = 3
`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RAILWAY_TOKEN", "")
	stdout, err := run(t, "import", "railway", "--from-file", path, "--service-name", "api")
	if err != nil {
		t.Fatalf("import railway --from-file: %v", err)
	}
	if !strings.Contains(stdout, "create server api") || !strings.Contains(stdout, "Dockerfile.prod") {
		t.Fatalf("review:\n%s", stdout)
	}
}

func TestImportRailwayCleanupRefusesWithoutYes(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	body := `{"version":1,"source_digest":"sha256:x","project_id":"p1","created":[{"op":"service.create","name":"api","id":"s1"}]}`
	if err := os.WriteFile(state, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, err := run(t, "import", "railway", "--cleanup", "--state", state)
	if err == nil {
		t.Fatal("cleanup without --yes must not delete anything")
	}
	if !strings.Contains(stdout, "service.create api") {
		t.Fatalf("cleanup must print what it would delete first:\n%s", stdout)
	}
}

func TestImportRailwayApplyCreatesInOrderAndRecordsState(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	plan := railway.Plan{
		Version: railway.PlanVersion,
		Source:  railway.PlanSource{Kind: "railway", ProjectName: "acme-api", Digest: "sha256:abc"},
		Target:  railway.PlanTarget{ProjectName: "acme-api"},
		Actions: []railway.Action{
			{Op: "project.create", Name: "acme-api"},
			{Op: "database.create", Name: "Postgres", Engine: "postgres"},
			{Op: "service.create", Name: "api", Kind: "server",
				Source:   &railway.ServiceSource{Type: "git", Ref: "https://github.com/acme/api", Branch: "main"},
				Settings: &railway.ServiceSetting{StartCommand: "npm start", Replicas: 2, Region: "us-east"},
				Address:  boolPtr(true)},
			{Op: "variable.set", Service: "api", Variable: &railway.Variable{Name: "LOG_LEVEL", Value: "info", HasValue: true}},
		},
	}
	body, _ := json.Marshal(plan)
	if err := os.WriteFile(planPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	var created []string
	f := newFakeAPI(t, func(c call) (int, string) {
		created = append(created, c.Method+" "+c.Path)
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
		case c.is("GET", "/databases"):
			return 200, `{"data":[]}`
		case c.is("POST", "/databases"):
			return 201, `{"data":{"id":"db1"}}`
		case c.is("GET", "/projects/p1/services"):
			return 200, `{"data":[]}`
		case c.is("POST", "/projects/p1/services"):
			return 201, `{"data":{"id":"s1","name":"api"}}`
		case c.Method == "PUT" && strings.HasSuffix(c.Path, "/variables"):
			return 200, `{"data":{}}`
		case c.Method == "PUT" && strings.HasSuffix(c.Path, "/public-address"):
			return 200, `{"data":{}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	setupEnv(t, f.srv)

	state := filepath.Join(dir, "state.json")
	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", state)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "created project acme-api") || !strings.Contains(out, "created server api") {
		t.Fatalf("apply output:\n%s", out)
	}

	// A database must exist before the first service so a reference resolves.
	dbAt, svcAt := indexOf(created, "POST /databases"), indexOf(created, "POST /projects/p1/services")
	if dbAt == -1 || svcAt == -1 || dbAt > svcAt {
		t.Fatalf("create order wrong: %v", created)
	}

	saved, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		ProjectID string `json:"project_id"`
		Created   []struct {
			Op, Name, ID string
		} `json:"created"`
	}
	if err := json.Unmarshal(saved, &st); err != nil {
		t.Fatal(err)
	}
	if st.ProjectID != "p1" || len(st.Created) < 3 {
		t.Fatalf("state = %s", saved)
	}
}

func TestImportRailwayApplyRefusesMissingValues(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	plan := railway.Plan{
		Version: railway.PlanVersion,
		Source:  railway.PlanSource{Kind: "railway", ProjectName: "acme-api", Digest: "sha256:abc"},
		Target:  railway.PlanTarget{ProjectName: "acme-api"},
		Actions: []railway.Action{
			{Op: "service.create", Name: "api", Kind: "server",
				Source: &railway.ServiceSource{Type: "git", Ref: "https://github.com/acme/api"}},
			{Op: "variable.set", Service: "api", Variable: &railway.Variable{Name: "STRIPE_KEY"}},
		},
	}
	body, _ := json.Marshal(plan)
	if err := os.WriteFile(planPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(dir, "s.json"))
	if err == nil || !strings.Contains(err.Error(), "have no value") {
		t.Fatalf("want a refusal to apply missing values, got %v", err)
	}
}

// githubRoutes answers the GitHub connection and link calls of an apply whose
// plan builds from github.com/acme/api.
func githubRoutes(c call) (int, string, bool) {
	switch {
	case c.is("GET", "/integrations/github/repos"):
		return 200, `{"data":[{"id":42,"full_name":"acme/api","private":true}]}`, true
	case c.Method == "GET" && strings.HasSuffix(c.Path, "/environments"):
		return 200, `{"data":[{"id":"env-default","name":"production","is_default":true}]}`, true
	case c.Method == "PUT" && strings.HasSuffix(c.Path, "/source/github"):
		return 202, `{"data":{"service":{"id":"s1","source":{"type":"forge","repository_id":"r1","branch":"main"}},"import":{"id":"i1","status":"queued"}}}`, true
	case c.is("GET", "/projects/p1/services/s1"):
		return 200, `{"data":{"id":"s1","name":"api","source":{}}}`, true
	}
	return 0, "", false
}

func indexOf(values []string, want string) int {
	for i, v := range values {
		if v == want {
			return i
		}
	}
	return -1
}

func boolPtr(v bool) *bool { return &v }

func TestImportRailwayApplyNeedsARegionWhenThePlanDoes(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	plan := railway.Plan{
		Version: railway.PlanVersion,
		Source:  railway.PlanSource{Kind: "railway", ProjectName: "acme-api", Digest: "sha256:abc"},
		Target:  railway.PlanTarget{ProjectName: "acme-api"},
		Actions: []railway.Action{
			{Op: "service.create", Name: "api", Kind: "server", NeedsRegion: true,
				Settings: &railway.ServiceSetting{StartCommand: "npm start"}},
		},
	}
	body, _ := json.Marshal(plan)
	if err := os.WriteFile(planPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(dir, "s.json"))
	if err == nil || !strings.Contains(err.Error(), "--region") {
		t.Fatalf("want a refusal that names --region, got %v", err)
	}
}

func TestImportRailwayApplyEndsWithTheNextCommands(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.json")
	plan := railway.Plan{
		Version: railway.PlanVersion,
		Source:  railway.PlanSource{Kind: "railway", ProjectName: "acme-api", Digest: "sha256:abc"},
		Target:  railway.PlanTarget{ProjectName: "acme-api"},
		Actions: []railway.Action{
			{Op: "project.create", Name: "acme-api"},
			{Op: "service.create", Name: "api", Kind: "server",
				Source: &railway.ServiceSource{Type: "git", Ref: "https://github.com/acme/api"}},
			{Op: "variable.set", Service: "api", Variable: &railway.Variable{Name: "STRIPE_KEY"}},
			{Op: "domain.custom", Service: "api", Domains: []string{"api.acme.com"}},
		},
	}
	body, _ := json.Marshal(plan)
	if err := os.WriteFile(planPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

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
		case c.Method == "PUT" && strings.HasSuffix(c.Path, "/variables"):
			return 200, `{"data":{}}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/domains"):
			return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	setupEnv(t, f.srv)

	out, err := run(t, "import", "railway", "--apply", "--plan", planPath,
		"--state", filepath.Join(dir, "state.json"), "--allow-missing-values")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	for _, want := range []string{
		"Summary",
		"services:  api",
		"need a value (1): api.STRIPE_KEY",
		"miosa service vars set acme-api api STRIPE_KEY=<new value>",
		"miosa service deploy acme-api api",
		"miosa service domain enable acme-api api --domain api.acme.com",
		"nothing was started",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary is missing %q:\n%s", want, out)
		}
	}
}
