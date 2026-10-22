package railway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func fixtureSource() Source {
	return Source{
		Kind:         "railway",
		ProjectID:    "11111111-1111-1111-1111-111111111111",
		ProjectName:  "acme-api",
		Environment:  "production",
		Environments: []string{"production", "staging"},
		FetchedAt:    time.Date(2026, 10, 22, 11, 40, 0, 0, time.UTC),
		Services: []Service{
			{
				Name:            "api",
				Repo:            "acme/api",
				Branch:          "main",
				RootDirectory:   "/app",
				DockerfilePath:  "Dockerfile.prod",
				StartCommand:    "npm start",
				BuildCommand:    "npm run build",
				HealthcheckPath: "/health",
				RestartPolicy:   "ON_FAILURE",
				NumReplicas:     2,
				Region:          "us-east4",
				ServiceDomains:  []string{"api-production.up.railway.app"},
				CustomDomains:   []string{"api.acme.com"},
				Volumes:         []VolumeRef{{MountPath: "/data", SizeMB: 5000}},
				Variables: []Variable{
					{Name: "DATABASE_URL", Value: "${{Postgres.DATABASE_URL}}", HasValue: true},
					{Name: "LOG_LEVEL", Value: "info", HasValue: true},
					{Name: "STRIPE_KEY"},
				},
			},
			{
				Name:         "worker",
				Image:        "ghcr.io/acme/worker:1",
				CronSchedule: "0 3 * * *",
			},
		},
		Plugins: []PluginRef{
			{Name: "postgresql", FriendlyName: "Postgres"},
			{Name: "mongodb", FriendlyName: "Mongo"},
		},
	}
}

func actionsOf(plan Plan, op string) []Action {
	var out []Action
	for _, a := range plan.Actions {
		if a.Op == op {
			out = append(out, a)
		}
	}
	return out
}

func TestBuildMapsServicesDatabasesAndVariables(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Version != PlanVersion {
		t.Fatalf("version = %d", plan.Version)
	}

	services := actionsOf(plan, "service.create")
	if len(services) != 2 {
		t.Fatalf("want 2 services, got %d", len(services))
	}
	api := services[0]
	if api.Name != "api" || api.Kind != "server" {
		t.Fatalf("api = %+v", api)
	}
	if api.Source == nil || api.Source.Ref != "https://github.com/acme/api" || api.Source.Branch != "main" || api.Source.RootDir != "/app" {
		t.Fatalf("api source = %+v", api.Source)
	}
	if api.Build == nil || api.Build.Dockerfile != "Dockerfile.prod" {
		t.Fatalf("api build = %+v", api.Build)
	}
	if api.Settings.StartCommand != "npm start" || api.Settings.Replicas != 2 || api.Settings.Region != "us-east" {
		t.Fatalf("api settings = %+v", api.Settings)
	}
	if api.Address == nil || !*api.Address {
		t.Fatalf("api should get a public address")
	}
	worker := services[1]
	if worker.Kind != "cron" || worker.Settings.CronSchedule != "0 3 * * *" {
		t.Fatalf("worker = %+v", worker)
	}
	if worker.Address == nil || *worker.Address {
		t.Fatalf("worker should not get a public address")
	}
	if worker.Source == nil || worker.Source.Type != "image" {
		t.Fatalf("worker source = %+v", worker.Source)
	}

	databases := actionsOf(plan, "database.create")
	if len(databases) != 1 || databases[0].Engine != "postgres" || databases[0].Name != "Postgres" {
		t.Fatalf("databases = %+v", databases)
	}

	vars := actionsOf(plan, "variable.set")
	if len(vars) != 3 {
		t.Fatalf("want 3 variables, got %d", len(vars))
	}
	byName := map[string]*Variable{}
	for _, v := range vars {
		byName[v.Variable.Name] = v.Variable
	}
	if got := byName["DATABASE_URL"]; got == nil || got.Value != "${{Postgres.DATABASE_URL}}" || !got.HasValue {
		t.Fatalf("reference = %+v", got)
	}
	if got := byName["STRIPE_KEY"]; got == nil || got.HasValue {
		t.Fatalf("secret should need a value: %+v", got)
	}

	domains := actionsOf(plan, "domain.report")
	if len(domains) != 1 || domains[0].Domains[0] != "api-production.up.railway.app" {
		t.Fatalf("domains = %+v", domains)
	}
	custom := actionsOf(plan, "domain.custom")
	if len(custom) != 1 || custom[0].Domains[0] != "api.acme.com" {
		t.Fatalf("custom domains must be their own action (the apply seam): %+v", custom)
	}
}

func TestBuildReportsEverythingItCannotCarry(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]Action{}
	for _, a := range actionsOf(plan, "unmapped") {
		fields[a.Field] = a
	}
	for _, want := range []string{"buildCommand", "volume /data", "plugin mongodb"} {
		if _, ok := fields[want]; !ok {
			t.Fatalf("no unmapped action for %q; got %v", want, keysOf(fields))
		}
	}
	if got := fields["volume /data"].Next; !strings.Contains(got, "miosa volume create api-data --size-gb 5") {
		t.Fatalf("volume next = %q", got)
	}
}

func TestBuildTargetsAndNames(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{ProjectName: "acme", Into: "existing", IncludeTargets: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(actionsOf(plan, "project.create")) != 0 {
		t.Fatal("--into must not create a project")
	}
	targets := actionsOf(plan, "target.create")
	if len(targets) != 1 || targets[0].Name != "staging" {
		t.Fatalf("targets = %+v", targets)
	}
	if plan.Target.ProjectName != "acme" || plan.Target.Into != "existing" {
		t.Fatalf("target = %+v", plan.Target)
	}
}

func TestDigestIgnoresReadOrder(t *testing.T) {
	a := fixtureSource()
	b := fixtureSource()
	b.Services[0].Variables = []Variable{b.Services[0].Variables[2], b.Services[0].Variables[0], b.Services[0].Variables[1]}
	b.Plugins = []PluginRef{b.Plugins[1], b.Plugins[0]}

	pa, err := Build(a, Options{})
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Build(b, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if pa.Source.Digest != pb.Source.Digest {
		t.Fatalf("digest depends on read order: %s vs %s", pa.Source.Digest, pb.Source.Digest)
	}
}

func TestBuildClampsReplicasAndReportsThem(t *testing.T) {
	src := fixtureSource()
	src.Services[0].NumReplicas = 80
	plan, err := Build(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := actionsOf(plan, "service.create")[0].Settings.Replicas; got != 50 {
		t.Fatalf("replicas = %d", got)
	}
	found := false
	for _, a := range actionsOf(plan, "unmapped") {
		if a.Field == "numReplicas" {
			found = true
		}
	}
	if !found {
		t.Fatal("a clamped replica count must be reported")
	}
}

func TestBuildFileCarriesConfigAndReportsTheRest(t *testing.T) {
	cfg := FileConfig{}
	cfg.Build.Builder = "NIXPACKS"
	cfg.Build.DockerfilePath = "Dockerfile"
	cfg.Build.BuildCommand = "npm run build"
	cfg.Build.WatchPatterns = []string{"src/**"}
	cfg.Deploy.StartCommand = "npm start"
	cfg.Deploy.HealthcheckPath = "/health"
	cfg.Deploy.RestartPolicyType = "ON_FAILURE"
	cfg.Deploy.NumReplicas = 3
	cfg.Deploy.Region = "europe-west4"
	cfg.Deploy.SleepApplication = true
	cfg.Deploy.PreDeployCommand = []string{"npm run migrate"}

	plan := BuildFile(cfg, "api", Options{ProjectName: "acme"})
	services := actionsOf(plan, "service.create")
	if len(services) != 1 {
		t.Fatalf("services = %+v", services)
	}
	svc := services[0]
	if svc.Build == nil || svc.Build.Dockerfile != "Dockerfile" {
		t.Fatalf("build = %+v", svc.Build)
	}
	if svc.Settings.HealthcheckPath != "/health" || svc.Settings.RestartPolicy != "on_failure" || svc.Settings.PreDeployCommand != "npm run migrate" {
		t.Fatalf("settings must carry health check, restart policy and pre-deploy: %+v", svc.Settings)
	}
	if svc.Settings.StartCommand != "npm start" || svc.Settings.Replicas != 3 {
		t.Fatalf("settings = %+v", svc.Settings)
	}
	if svc.Settings.Region != "" {
		t.Fatalf("europe-west4 has no MIOSA region, got %q", svc.Settings.Region)
	}
	if svc.Source != nil {
		t.Fatalf("a config file invents no source, got %+v", svc.Source)
	}

	fields := map[string]bool{}
	for _, a := range actionsOf(plan, "unmapped") {
		fields[a.Field] = true
	}
	for _, want := range []string{"build.builder", "build.buildCommand", "build.watchPatterns", "deploy.sleepApplication", "deploy.region"} {
		if !fields[want] {
			t.Fatalf("no unmapped action for %q", want)
		}
	}
}

func TestPlanJSONIsStable(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Build(fixtureSource(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("the same source must produce the same plan")
	}
}

func TestMapRegion(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"us-east4":        "us-east",
		"us-east4-eqdc4a": "us-east",
		"us-west1":        "us-west",
		"us-west2":        "us-west",
		"us-south1":       "us-mia",
		"europe-west4":    "",
		"asia-southeast1": "",
	}
	for in, want := range cases {
		if got := mapRegion(in); got != want {
			t.Fatalf("mapRegion(%q) = %q, want %q", in, got, want)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestOnlyKeepsTheNamedServicesAndSaysWhatItLeftOut(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{Only: []string{"API"}})
	if err != nil {
		t.Fatal(err)
	}
	services := actionsOf(plan, "service.create")
	if len(services) != 1 || services[0].Name != "api" {
		t.Fatalf("services = %+v", services)
	}
	joined := strings.Join(plan.Warnings, " ")
	if !strings.Contains(joined, "not imported (--only): worker") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestOnlyNamesAServiceThatIsNotThere(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{Only: []string{"api", "ghost"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, " "), "ghost (not in this project)") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestARegionWithoutAnEquivalentNeedsOneAtApply(t *testing.T) {
	src := fixtureSource()
	src.Services[0].Region = "europe-west4"
	plan, err := Build(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	svc := actionsOf(plan, "service.create")[0]
	if !svc.NeedsRegion {
		t.Fatal("a service from an unmapped region must ask for --region")
	}
	if svc.Settings.Region != "" {
		t.Fatalf("the plan must not guess a region, got %q", svc.Settings.Region)
	}
	var next string
	for _, a := range actionsOf(plan, "unmapped") {
		if a.Field == "region" {
			next = a.Next
		}
	}
	if !strings.Contains(next, "closest MIOSA region is us-east") || !strings.Contains(next, "--region us-east") {
		t.Fatalf("next = %q", next)
	}
}

func TestDatabaseImagesBecomeDatabaseServices(t *testing.T) {
	src := fixtureSource()
	src.Services = append(src.Services, Service{
		Name:         "Postgres",
		Image:        "ghcr.io/railwayapp-templates/postgres-ssl:18",
		Variables:    []Variable{{Name: "PGDATABASE", Value: "${{PGUSER}}", HasValue: true}},
		Volumes:      []VolumeRef{{MountPath: "/var/lib/postgresql/data", SizeMB: 5120}},
		StartCommand: "docker-entrypoint.sh postgres",
	})
	src.Services = append(src.Services, Service{Name: "Redis", Image: "redis:8.2.9"})

	plan, err := Build(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	databases := actionsOf(plan, "database.service")
	if len(databases) != 2 {
		t.Fatalf("database services = %+v", databases)
	}
	engines := map[string]string{}
	for _, a := range databases {
		engines[a.Name] = a.Engine
	}
	if engines["Postgres"] != "postgres" || engines["Redis"] != "redis" {
		t.Fatalf("engines = %v", engines)
	}
	for _, a := range actionsOf(plan, "service.create") {
		if a.Name == "Postgres" || a.Name == "Redis" {
			t.Fatalf("%s must be a database service, not a server from an image", a.Name)
		}
	}
	// The database service's own provider variables are not recreated.
	for _, a := range actionsOf(plan, "variable.set") {
		if a.Service == "Postgres" {
			t.Fatalf("a database service's own variables must not be copied: %+v", a)
		}
	}
}

func TestWorkersAreInferredFromNameOrCommand(t *testing.T) {
	src := fixtureSource()
	src.Services = append(src.Services,
		Service{Name: "backend-worker-media", Repo: "acme/api"},
		Service{Name: "jobs", Repo: "acme/api", StartCommand: "arq app.worker.WorkerSettings"},
	)
	plan, err := Build(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, a := range actionsOf(plan, "service.create") {
		kinds[a.Name] = a.Kind
	}
	if kinds["backend-worker-media"] != "worker" || kinds["jobs"] != "worker" {
		t.Fatalf("kinds = %v", kinds)
	}
}

func TestHealthCheckAndRestartPolicyCarryOver(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	api := actionsOf(plan, "service.create")[0]
	if api.Settings.HealthcheckPath != "/health" {
		t.Fatalf("healthcheck_path = %q", api.Settings.HealthcheckPath)
	}
	if api.Settings.RestartPolicy != "on_failure" {
		t.Fatalf("restart_policy = %q", api.Settings.RestartPolicy)
	}
	for _, a := range actionsOf(plan, "unmapped") {
		if a.Field == "healthcheckPath" || a.Field == "restartPolicyType" {
			t.Fatalf("%s must be carried, not reported: %+v", a.Field, a)
		}
	}
}

func TestRestartPolicyMapping(t *testing.T) {
	cases := map[string]string{
		"ALWAYS":     "always",
		"ON_FAILURE": "on_failure",
		"NEVER":      "never",
		"":           "",
		"sometimes":  "",
	}
	for in, want := range cases {
		if got := mapRestartPolicy(in); got != want {
			t.Fatalf("mapRestartPolicy(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAnUnknownRestartPolicyIsReportedNotGuessed(t *testing.T) {
	src := fixtureSource()
	src.Services[0].RestartPolicy = "SOMETIMES"
	plan, err := Build(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := actionsOf(plan, "service.create")[0].Settings.RestartPolicy; got != "" {
		t.Fatalf("restart_policy = %q, want empty", got)
	}
	found := false
	for _, a := range actionsOf(plan, "unmapped") {
		if a.Field == "restartPolicyType" {
			found = true
		}
	}
	if !found {
		t.Fatal("an unknown policy must be reported")
	}
}

func TestDatabaseVolumeReportsTheSizeAgainstTheManagedSize(t *testing.T) {
	src := fixtureSource()
	src.Services = append(src.Services, Service{
		Name:    "Postgres",
		Image:   "postgres:18",
		Volumes: []VolumeRef{{MountPath: "/var/lib/postgresql/data", SizeMB: 50000}},
	})
	plan, err := Build(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var next string
	for _, a := range actionsOf(plan, "unmapped") {
		if a.Name == "Postgres" && a.Field == "volume /var/lib/postgresql/data" {
			next = a.Next
		}
	}
	if !strings.Contains(next, "50000 MB") || !strings.Contains(next, "size small") {
		t.Fatalf("next = %q", next)
	}
}

func TestRotationWarningNamesTheDisclosureDate(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range plan.Warnings {
		if strings.Contains(w, "2026-10-21") {
			found = true
		}
		if strings.Contains(w, "2025") {
			t.Fatalf("the warning must not name 2025: %q", w)
		}
	}
	if !found {
		t.Fatalf("the warning must name the disclosure date: %v", plan.Warnings)
	}
}

func TestExcludeDropsNamedServicesAndReportsThem(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{Exclude: []string{"worker", "ghost"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range actionsOf(plan, "service.create") {
		if a.Name == "worker" {
			t.Fatal("an excluded service must not be created")
		}
	}
	joined := strings.Join(plan.Warnings, " ")
	if !strings.Contains(joined, "not imported (--exclude): ghost (not in this project), worker") {
		t.Fatalf("warnings = %v", plan.Warnings)
	}
}

func TestExcludeAppliesAfterOnly(t *testing.T) {
	plan, err := Build(fixtureSource(), Options{Only: []string{"api", "worker"}, Exclude: []string{"worker"}})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, a := range actionsOf(plan, "service.create") {
		names = append(names, a.Name)
	}
	if len(names) != 1 || names[0] != "api" {
		t.Fatalf("names = %v", names)
	}
}
