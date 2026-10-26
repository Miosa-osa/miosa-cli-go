package commands_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
)

// planFor runs a dry run against the Railway fixture and returns the plan and
// the review text.
func planFor(t *testing.T, project rwProject, args ...string) (railway.Plan, string, string) {
	t.Helper()
	rail := railwayServer(t, project)
	t.Setenv("RAILWAY_API_URL", rail.URL)
	t.Setenv("RAILWAY_TOKEN", "rail_test_token")
	out := filepath.Join(t.TempDir(), "plan.json")
	stdout, err := run(t, append([]string{"import", "railway", "--project", project.ID, "--out", out}, args...)...)
	if err != nil {
		t.Fatalf("import railway: %v\n%s", err, stdout)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var plan railway.Plan
	if err := json.Unmarshal(body, &plan); err != nil {
		t.Fatal(err)
	}
	return plan, stdout, out
}

func serviceAction(t *testing.T, plan railway.Plan, env, name string) railway.Action {
	t.Helper()
	for _, a := range plan.Actions {
		if a.Op == "service.create" && a.Env == env && a.Name == name {
			return a
		}
	}
	t.Fatalf("no service.create %s in %s", name, env)
	return railway.Action{}
}

func variableOf(t *testing.T, plan railway.Plan, env, service, name string) railway.Variable {
	t.Helper()
	for _, a := range plan.Actions {
		if a.Op == "variable.set" && a.Env == env && a.Service == service && a.Variable != nil && a.Variable.Name == name {
			return *a.Variable
		}
	}
	t.Fatalf("no variable %s.%s in %s", service, name, env)
	return railway.Variable{}
}

func TestImportRailwayClinicIQSystemPlansEveryEnvironment(t *testing.T) {
	plan, stdout, _ := planFor(t, clinicIQSystem(), "--environment", "all")
	t.Logf("dry run:\n%s", stdout)

	if plan.Version != railway.PlanVersion || plan.Source.Environment != "development,production" {
		t.Fatalf("source = %+v", plan.Source)
	}
	targets := map[string]bool{}
	perEnv := map[string]int{}
	for _, a := range plan.Actions {
		switch a.Op {
		case "target.create":
			targets[a.Name] = true
		case "service.create", "database.service":
			perEnv[a.Env]++
		}
	}
	if !targets["production"] || !targets["development"] {
		t.Fatalf("both environments must become Deploy targets: %v", targets)
	}
	// 13 instances in each environment: every one lands in its own
	// target, nothing collapses across them.
	if perEnv["production"] != 13 || perEnv["development"] != 13 {
		t.Fatalf("instances per environment = %v", perEnv)
	}

	api := serviceAction(t, plan, "production", "backend-cliniciq")
	if api.Source == nil || api.Source.Ref != "https://github.com/platform-cliniciq/backend-cliniciq" || api.Source.Branch != "main" {
		t.Fatalf("api source = %+v", api.Source)
	}
	if api.Build == nil || api.Build.Dockerfile != "Dockerfile" {
		t.Fatalf("the Dockerfile Railway built with (/Dockerfile) must carry, relative: %+v", api.Build)
	}
	if api.Settings.PreDeployCommand != "alembic upgrade head" || api.Settings.HealthcheckPath != "/readyz" {
		t.Fatalf("api settings = %+v", api.Settings)
	}
	if api.Provenance["dockerfile"] != "latest deployment" || api.Provenance["start_command"] != "service settings" {
		t.Fatalf("provenance = %v", api.Provenance)
	}
	if !api.NeedsRegion || api.RailwayRegion != "asia-southeast1-eqsg3a" {
		t.Fatalf("Singapore has no MIOSA region; the plan must say so: %+v", api)
	}
	devAPI := serviceAction(t, plan, "development", "backend-cliniciq")
	if devAPI.Source.Branch != "dev" {
		t.Fatalf("development builds its own branch: %+v", devAPI.Source)
	}

	// Railway's own names are not imported; MIOSA injects its own.
	for _, a := range plan.Actions {
		if a.Op == "variable.set" && strings.HasPrefix(a.Variable.Name, "RAILWAY_") {
			t.Fatalf("Railway built-in imported: %+v", a)
		}
	}
	if got := variableOf(t, plan, "production", "backend-cliniciq-workers", "REDIS_URL").Value; got !=
		"redis://default:${{Redis.REDIS_PASSWORD}}@${{Redis.REDIS_HOST}}:${{Redis.REDIS_PORT}}" {
		t.Fatalf("Railway's Redis names must become MIOSA's: %q", got)
	}
	if got := variableOf(t, plan, "development", "backend-cliniciq", "SMTP_HOST").Value; got != "${{mailhog.MIOSA_PRIVATE_DOMAIN}}" {
		t.Fatalf("RAILWAY_PRIVATE_DOMAIN must become MIOSA_PRIVATE_DOMAIN: %q", got)
	}
	if got := variableOf(t, plan, "production", "backend-cliniciq-workers", "DATABASE_URL"); !got.HasValue || !strings.Contains(got.Value, "${{Postgres.PGHOST}}") {
		t.Fatalf("Postgres references resolve as they are: %+v", got)
	}

	for _, want := range []string{
		"in Deploy target production:",
		"in Deploy target development:",
		"create server backend-cliniciq from https://github.com/platform-cliniciq/backend-cliniciq (main) with Dockerfile",
		"pre_deploy_command: alembic upgrade head  [service settings]",
		"create worker backend-cliniciq-worker-engines",
		"create database service Postgres (postgres",
		"ran outside the US on Railway",
		"Apply creates every service stopped",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("review is missing %q:\n%s", want, stdout)
		}
	}
}

func TestImportRailwayMonorepoReadsTheConfigFile(t *testing.T) {
	plan, stdout, _ := planFor(t, clinicIQBetaStaging(), "--environment", "staging")
	t.Logf("dry run:\n%s", stdout)

	worker := serviceAction(t, plan, "staging", "clinic-iq-worker")
	if worker.Build == nil || worker.Build.Dockerfile != "apps/worker/Dockerfile" {
		t.Fatalf("the worker's Dockerfile comes from apps/worker/railway.toml: %+v", worker.Build)
	}
	if worker.Settings.Replicas != 2 || worker.Settings.StartCommand != "pnpm --filter @clinic-iq/worker start" {
		t.Fatalf("worker settings = %+v", worker.Settings)
	}
	if worker.Provenance["replicas"] != "/apps/worker/railway.toml" || worker.Provenance["start_command"] != "/apps/worker/railway.toml" {
		t.Fatalf("worker provenance = %v", worker.Provenance)
	}

	web := serviceAction(t, plan, "staging", "clinic-iq-web")
	if web.Settings.StartCommand != "pnpm --filter @clinic-iq/web start" || web.Settings.PreDeployCommand != "pnpm --filter @clinic-iq/db db:migrate" {
		t.Fatalf("web settings come from /railway.json: %+v", web.Settings)
	}
	if web.Provenance["pre_deploy_command"] != "/railway.json" {
		t.Fatalf("web provenance = %v", web.Provenance)
	}

	programs := serviceAction(t, plan, "staging", "clinic-iq-programs")
	if programs.Build == nil || programs.Build.BuildCommand != "pnpm --filter @clinic-iq/programs-app build" {
		t.Fatalf("the build command the last deployment ran must carry: %+v", programs.Build)
	}
	if programs.Settings.PreDeployCommand != "pnpm --filter @clinic-iq/programs db:migrate" {
		t.Fatalf("programs pre-deploy = %q", programs.Settings.PreDeployCommand)
	}

	// A reference to Railway's public domain has no MIOSA equivalent: it is
	// listed, and the variable needs a value.
	if got := variableOf(t, plan, "staging", "clinic-iq-web", "NEXT_PUBLIC_APP_URL"); got.HasValue {
		t.Fatalf("an unmappable reference must not be carried as is: %+v", got)
	}
	for _, want := range []string{
		"staging/clinic-iq-web variable NEXT_PUBLIC_APP_URL: it references ${{RAILWAY_PUBLIC_DOMAIN}}",
		"start_command: pnpm --filter @clinic-iq/worker start  [/apps/worker/railway.toml]",
		"Nixpacks",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("review is missing %q:\n%s", want, stdout)
		}
	}
}

func TestImportRailwayApplyCreatesEachEnvironmentStoppedAndRerunsIdempotently(t *testing.T) {
	_, _, planPath := planFor(t, clinicIQSystem(), "--environment", "all", "--name", "clinic-iq-system")
	miosa := newFakeMiosa()
	f := newFakeAPI(t, miosa.reply)

	state := filepath.Join(t.TempDir(), "state.json")
	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", state, "--allow-missing-values", "--region", "us-west")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}

	// Every service lands in its own Deploy target.
	envByName := map[string]string{}
	for _, projectEnvs := range miosa.envs {
		for _, e := range projectEnvs {
			envByName[e["name"].(string)] = e["id"].(string)
		}
	}
	if envByName["production"] == "" || envByName["development"] == "" {
		t.Fatalf("targets = %v", envByName)
	}
	count := map[string]int{}
	for _, s := range miosa.services {
		for name, id := range envByName {
			if s["environment_id"] == id {
				count[name]++
			}
		}
	}
	if count["production"] != 13 || count["development"] != 13 {
		t.Fatalf("services per target = %v", count)
	}

	// Private repositories are linked through the GitHub connection, by id,
	// and the Dockerfile is written back onto the linked source.
	links := 0
	for _, c := range f.calls {
		if c.Method == "PUT" && strings.HasSuffix(c.Path, "/source/github") {
			links++
			if c.Body["github_repository_id"] != "1237052916" && c.Body["github_repository_id"] != "1237052917" {
				t.Fatalf("link body = %+v", c.Body)
			}
		}
		if c.Method == "POST" && strings.HasSuffix(c.Path, "/services") {
			if src, ok := c.Body["source"].(map[string]any); ok && src["type"] == "git" {
				t.Fatalf("a GitHub service must not be created on a credential-less git URL: %+v", c.Body)
			}
		}
		// Nothing may start on a plain apply.
		if c.Method == "POST" && (strings.HasSuffix(c.Path, "/deployments") || strings.HasSuffix(c.Path, "/redeploy") || strings.HasSuffix(c.Path, "/start")) {
			t.Fatalf("apply without --deploy started something: %s %s", c.Method, c.Path)
		}
		if strings.HasSuffix(c.Path, "/changes/apply") && c.Body["deploy"] != false {
			t.Fatalf("database change set must apply with deploy false: %+v", c.Body)
		}
	}
	if links != 20 {
		t.Fatalf("want every repository service linked (20), got %d", links)
	}
	api := findService(miosa, envByName["production"], "backend-cliniciq")
	if src := api["source"].(map[string]any); src["type"] != "forge" || src["dockerfile"] != "Dockerfile" {
		t.Fatalf("api source = %+v", src)
	}
	settings, _ := api["settings"].(map[string]any)
	if settings["region"] != "us-west" || settings["pre_deploy_command"] != "alembic upgrade head" {
		t.Fatalf("api settings = %+v", settings)
	}
	for _, want := range []string{
		"nothing was started: every service is created and stopped",
		"linked backend-cliniciq (production) to github.com/platform-cliniciq/backend-cliniciq (private;",
		"region backend-cliniciq (production): Railway asia-southeast1-eqsg3a -> MIOSA us-west",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply output is missing %q:\n%s", want, out)
		}
	}

	// A second apply, even from a fresh state file, creates nothing new.
	before := len(f.calls)
	out2, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "fresh.json"),
		"--allow-missing-values", "--region", "us-west")
	if err != nil {
		t.Fatalf("second apply: %v\n%s", err, out2)
	}
	for _, c := range f.calls[before:] {
		creates := c.Method == "POST" && (c.Path == "/projects" || strings.HasSuffix(c.Path, "/services") ||
			strings.HasSuffix(c.Path, "/environments") || strings.HasSuffix(c.Path, "/changes"))
		if creates || strings.HasSuffix(c.Path, "/source/github") {
			t.Fatalf("a re-run must not create or relink: %s %s", c.Method, c.Path)
		}
	}
	if len(miosa.services) != 26 {
		t.Fatalf("services after the re-run = %d", len(miosa.services))
	}
	if !strings.Contains(out2, "service backend-cliniciq (development) already exists") {
		t.Fatalf("re-run output:\n%s", out2)
	}
}

func findService(m *fakeMiosa, envID, name string) map[string]any {
	for _, s := range m.services {
		if s["environment_id"] == envID && s["name"] == name {
			return s
		}
	}
	return nil
}

func TestImportRailwayApplyStopsWithoutAGitHubConnection(t *testing.T) {
	_, _, planPath := planFor(t, clinicIQSystem(), "--environment", "production")
	miosa := newFakeMiosa()
	miosa.github = nil
	f := newFakeAPI(t, miosa.reply)

	_, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "s.json"),
		"--allow-missing-values", "--region", "us-west")
	if err == nil || !strings.Contains(err.Error(), "Connect GitHub first: miosa connections add apps github") {
		t.Fatalf("want the connect-GitHub refusal, got %v", err)
	}
	for _, c := range f.calls {
		if c.Method != "GET" {
			t.Fatalf("nothing may be written before the refusal: %s %s", c.Method, c.Path)
		}
	}
}

func TestImportRailwayApplyStopsOnARepositoryTheConnectionCannotRead(t *testing.T) {
	_, _, planPath := planFor(t, clinicIQSystem(), "--environment", "production")
	miosa := newFakeMiosa()
	miosa.github = miosa.github[:1] // only backend-cliniciq
	f := newFakeAPI(t, miosa.reply)

	_, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "s.json"),
		"--allow-missing-values", "--region", "us-west")
	if err == nil || !strings.Contains(err.Error(), "cannot read platform-cliniciq/clinic-iq-frontend") {
		t.Fatalf("want a refusal naming the unreadable repository, got %v", err)
	}
	for _, c := range f.calls {
		if c.Method != "GET" {
			t.Fatalf("nothing may be written before the refusal: %s %s", c.Method, c.Path)
		}
	}
}

func TestImportRailwayApplyRefusesWhereServicesCannotBuild(t *testing.T) {
	_, _, planPath := planFor(t, clinicIQSystem(), "--environment", "production")
	miosa := newFakeMiosa()
	miosa.sourceBuilds = false
	f := newFakeAPI(t, miosa.reply)

	_, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "s.json"),
		"--allow-missing-values", "--region", "us-west")
	if err == nil || !strings.Contains(err.Error(), "cannot build services from a repository yet") ||
		!strings.Contains(err.Error(), "nothing was created") {
		t.Fatalf("want the capabilities refusal, got %v", err)
	}
	for _, c := range f.calls {
		if c.Method != "GET" {
			t.Fatalf("no legacy deployment, build or create may happen: %s %s", c.Method, c.Path)
		}
	}
}

func TestImportRailwayApplyCreatesACLIUploadedServiceAndGoesOn(t *testing.T) {
	plan, stdout, planPath := planFor(t, clinicIQFreeAccount())
	if web := serviceAction(t, plan, "production", "web"); web.Source != nil {
		t.Fatalf("a CLI-uploaded service has no source: %+v", web.Source)
	}
	if !strings.Contains(stdout, "create server web, no source (needs a source upload)") {
		t.Fatalf("review:\n%s", stdout)
	}

	miosa := newFakeMiosa()
	f := newFakeAPI(t, miosa.reply)
	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "s.json"), "--allow-missing-values")
	if err != nil {
		t.Fatalf("a service without a source must not stop the apply: %v\n%s", err, out)
	}
	if len(miosa.services) != 2 {
		t.Fatalf("want the database and the web service, got %d", len(miosa.services))
	}
	if len(f.find("PUT", "/projects/proj-001/shared-variables")) != 1 {
		t.Fatal("the environment's shared variables must be written")
	}
	for _, want := range []string{
		"need a source upload: web (production)",
		"miosa service deploy cliniciq-free-account web --dir <path to its code> --deploy-target production",
		"custom domains not attached (pass --attach-domains when DNS is ready to move): claim.cliniciq.com",
		"miosa service domain enable cliniciq-free-account web --domain claim.cliniciq.com --deploy-target production",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("apply output is missing %q:\n%s", want, out)
		}
	}
}

func TestImportRailwayAttachesCustomDomainsOnlyWhenAsked(t *testing.T) {
	_, stdout, planPath := planFor(t, clinicIQFreeAccount())
	if !strings.Contains(stdout, "custom domain claim.cliniciq.com: skipped (pass --attach-domains)") {
		t.Fatalf("the review must say the domain is skipped:\n%s", stdout)
	}

	miosa := newFakeMiosa()
	f := newFakeAPI(t, miosa.reply)
	if out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "a.json"), "--allow-missing-values"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	for _, c := range f.calls {
		if c.Method == "POST" && strings.HasSuffix(c.Path, "/domains") {
			t.Fatalf("a domain was attached without --attach-domains: %s", c.Path)
		}
	}

	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "b.json"),
		"--allow-missing-values", "--attach-domains")
	if err != nil {
		t.Fatalf("apply --attach-domains: %v\n%s", err, out)
	}
	attached := 0
	for _, c := range f.calls {
		if c.Method == "POST" && strings.HasSuffix(c.Path, "/domains") && c.Body["domain"] == "claim.cliniciq.com" {
			attached++
		}
	}
	if attached != 1 || !strings.Contains(out, "added custom domain claim.cliniciq.com to web (production)") {
		t.Fatalf("want the domain attached once with --attach-domains, got %d:\n%s", attached, out)
	}
}

func TestImportRailwayPackageNamesLongNamesAndPorts(t *testing.T) {
	plan, stdout, planPath := planFor(t, clinicIQProd())

	web := serviceAction(t, plan, "production", "clinic-iq-web")
	if web.RailwayName != "@clinic-iq/web" || web.Slug != "clinic-iq-web" || web.Source.Port != 8080 {
		t.Fatalf("web = %+v source %+v", web, web.Source)
	}
	if got := variableOf(t, plan, "production", "clinic-iq-worker", "WEB_URL").Value; got != "${{clinic-iq-web.APP_URL}}" {
		t.Fatalf("a reference must follow the renamed service: %q", got)
	}
	long := serviceAction(t, plan, "production", "backend-cliniciq-worker-notifications-dev")
	if len(long.Slug) > 40 || !strings.HasPrefix(long.Slug, "backend-cliniciq-worker-notificat-") {
		t.Fatalf("a 41-character name needs a shortened slug: %q", long.Slug)
	}
	mailhog := serviceAction(t, plan, "production", "mailhog")
	if mailhog.Source == nil || mailhog.Source.Port != 8025 {
		t.Fatalf("mailhog must listen on its web port 8025, not SMTP: %+v", mailhog.Source)
	}
	if !strings.Contains(stdout, "create server clinic-iq-web (Railway: @clinic-iq/web)") || !strings.Contains(stdout, "port 8025") {
		t.Fatalf("review:\n%s", stdout)
	}

	miosa := newFakeMiosa()
	f := newFakeAPI(t, miosa.reply)
	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "s.json"), "--allow-missing-values")
	if err != nil {
		t.Fatalf("every service must pass MIOSA's slug rule: %v\n%s", err, out)
	}
	for _, c := range f.calls {
		if c.Method == "POST" && strings.HasSuffix(c.Path, "/services") && c.Body["name"] == "mailhog" {
			if src, _ := c.Body["source"].(map[string]any); src["port"] != float64(8025) {
				t.Fatalf("mailhog source = %+v", src)
			}
		}
	}
	web2 := findService(miosa, firstEnv(miosa), "clinic-iq-web")
	if src := web2["source"].(map[string]any); src["type"] != "forge" || fmt.Sprint(src["port"]) != "8080" {
		t.Fatalf("the linked web service keeps its port: %+v", src)
	}
}

func TestImportRailwayRecordsTheDatabaseServiceIDNotTheChangeID(t *testing.T) {
	_, _, planPath := planFor(t, clinicIQFreeAccount())
	miosa := newFakeMiosa()
	newFakeAPI(t, miosa.reply)
	state := filepath.Join(t.TempDir(), "s.json")
	if out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", state, "--allow-missing-values"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	body, _ := os.ReadFile(state)
	var st struct {
		Created []struct{ Op, Name, ID string } `json:"created"`
	}
	_ = json.Unmarshal(body, &st)
	for _, ref := range st.Created {
		if ref.Op == "database.service" {
			if strings.HasPrefix(ref.ID, "chg-") || miosa.services[ref.ID] == nil {
				t.Fatalf("the state must record the database service's id, got %s", ref.ID)
			}
			return
		}
	}
	t.Fatalf("no database service recorded: %s", body)
}

func TestImportRailwayNextStepsNameTheIntoProject(t *testing.T) {
	_, _, planPath := planFor(t, clinicIQFreeAccount(), "--into", "claim-live")
	miosa := newFakeMiosa()
	miosa.projects = []map[string]any{{"id": "proj-into", "name": "claim-live", "slug": "claim-live"}}
	miosa.envs["proj-into"] = []map[string]any{{"id": "env-into", "name": "production", "is_default": true}}
	newFakeAPI(t, miosa.reply)
	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "s.json"), "--allow-missing-values")
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if !strings.Contains(out, "miosa service vars set claim-live web GHL_API_KEY=<new value>") || strings.Contains(out, "vars set cliniciq-free-account") {
		t.Fatalf("the next steps must name the --into project:\n%s", out)
	}
}

func firstEnv(m *fakeMiosa) string {
	for _, envs := range m.envs {
		return envs[0]["id"].(string)
	}
	return ""
}

func TestImportRailwayDeployFlagStartsOnlyWhenAsked(t *testing.T) {
	plan := railway.Plan{
		Version: railway.PlanVersion,
		Source:  railway.PlanSource{Kind: "railway", ProjectName: "acme", Digest: "sha256:deploy"},
		Target:  railway.PlanTarget{ProjectName: "acme"},
		Actions: []railway.Action{
			{Op: "project.create", Name: "acme"},
			{Op: "target.create", Name: "production"},
			{Op: "service.create", Env: "production", Name: "api", Kind: "server",
				Source:   &railway.ServiceSource{Type: "git", Ref: "https://github.com/platform-cliniciq/backend-cliniciq", Branch: "main"},
				Settings: &railway.ServiceSetting{StartCommand: "uvicorn app.main:app"}},
			{Op: "variable.set", Env: "production", Service: "api", Variable: &railway.Variable{Name: "SECRET_KEY"}},
		},
	}
	body, _ := json.Marshal(plan)
	planPath := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(planPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	miosa := newFakeMiosa()
	f := newFakeAPI(t, miosa.reply)
	_, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "a.json"),
		"--allow-missing-values", "--deploy")
	if err == nil || !strings.Contains(err.Error(), "--deploy would start services with 1 empty variable") {
		t.Fatalf("--deploy with empty secrets must be refused, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("the refusal comes before any call: %d calls", len(f.calls))
	}

	plan.Actions[3].Variable = &railway.Variable{Name: "SECRET_KEY", Value: "rotated", HasValue: true}
	body, _ = json.Marshal(plan)
	if err := os.WriteFile(planPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "import", "railway", "--apply", "--plan", planPath, "--state", filepath.Join(t.TempDir(), "b.json"), "--deploy")
	if err != nil {
		t.Fatalf("apply --deploy: %v\n%s", err, out)
	}
	var deployed int
	for _, c := range f.calls {
		if c.Method == "POST" && strings.HasSuffix(c.Path, "/deployments") {
			deployed++
		}
	}
	if deployed != 1 || len(f.find("GET", "/forge/github/imports/imp-svc-003")) == 0 {
		t.Fatalf("want the GitHub copy awaited and one release, got %d releases:\n%s", deployed, out)
	}
	if !strings.Contains(out, "deploying (--deploy): api (production)") {
		t.Fatalf("apply output:\n%s", out)
	}
}
