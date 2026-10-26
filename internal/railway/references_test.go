package railway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestRewriteReferencesMapsRailwayNamesOntoMIOSA(t *testing.T) {
	ctx := refContext{engines: map[string]string{"redis": "redis", "postgres": "postgres", "clinic-iq-web": ""},
		renames: map[string]string{"@clinic-iq/web": "clinic-iq-web"}}
	cases := []struct {
		in, want   string
		unmappable []string
	}{
		{"redis://${{Redis.REDISUSER}}:${{Redis.REDISPASSWORD}}@${{Redis.REDISHOST}}:${{Redis.REDISPORT}}",
			"redis://default:${{Redis.REDIS_PASSWORD}}@${{Redis.REDIS_HOST}}:${{Redis.REDIS_PORT}}", nil},
		{"${{Redis.REDIS_URL}}", "${{Redis.REDIS_URL}}", nil},
		{"${{ Redis.REDISHOST }}", "${{Redis.REDIS_HOST}}", nil},
		{"${{Redis.REDIS_PUBLIC_URL}}", "${{Redis.REDIS_PUBLIC_URL}}", []string{"${{Redis.REDIS_PUBLIC_URL}}"}},
		{"${{Postgres.POSTGRES_PASSWORD}}", "${{Postgres.PGPASSWORD}}", nil},
		{"${{Postgres.DATABASE_URL}}", "${{Postgres.DATABASE_URL}}", nil},
		{"${{Postgres.RAILWAY_PRIVATE_DOMAIN}}", "${{Postgres.PGHOST}}", nil},
		{"${{mailhog.RAILWAY_PRIVATE_DOMAIN}}", "${{mailhog.MIOSA_PRIVATE_DOMAIN}}", nil},
		{"${{RAILWAY_ENVIRONMENT_NAME}}", "${{MIOSA_ENVIRONMENT_NAME}}", nil},
		{"https://${{RAILWAY_PUBLIC_DOMAIN}}", "https://${{RAILWAY_PUBLIC_DOMAIN}}", []string{"${{RAILWAY_PUBLIC_DOMAIN}}"}},
		{"${{shared.RAILWAY_PUBLIC_DOMAIN}}", "${{shared.RAILWAY_PUBLIC_DOMAIN}}", nil},
		{"${{backend-cliniciq.SECRET_KEY}}", "${{backend-cliniciq.SECRET_KEY}}", nil},
		{"no reference", "no reference", nil},
		{"${{@clinic-iq/web.APP_URL}}", "${{clinic-iq-web.APP_URL}}", nil},
	}
	for _, c := range cases {
		got, unmappable, _ := rewriteReferences(c.in, ctx)
		if got != c.want || !reflect.DeepEqual(unmappable, c.unmappable) {
			t.Errorf("rewrite(%q) = %q, %v; want %q, %v", c.in, got, unmappable, c.want, c.unmappable)
		}
	}
}

func TestAnUnmappableReferenceNeedsAValueAndIsReported(t *testing.T) {
	v, reports := carryVariable(Variable{Name: "NEXT_PUBLIC_APP_URL", Value: "https://${{RAILWAY_PUBLIC_DOMAIN}}", HasValue: true}, "web", "staging", refContext{})
	if v.HasValue || v.Value != "" {
		t.Fatalf("an unmappable reference must not be carried as is: %+v", v)
	}
	if len(reports) != 1 || reports[0].Field != "variable NEXT_PUBLIC_APP_URL" || reports[0].Env != "staging" {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestAPrivateDomainReferenceIsCarriedAndReported(t *testing.T) {
	v, reports := carryVariable(Variable{Name: "SMTP_HOST", Value: "${{mailhog.RAILWAY_PRIVATE_DOMAIN}}", HasValue: true}, "backend", "development", refContext{})
	if !v.HasValue || v.Value != "${{mailhog.MIOSA_PRIVATE_DOMAIN}}" {
		t.Fatalf("carried = %+v", v)
	}
	if len(reports) != 1 || !strings.Contains(reports[0].Why, "mailhog is unreachable by name on MIOSA today") ||
		reports[0].Next != "point SMTP at your provider" {
		t.Fatalf("reports = %+v", reports)
	}
}

func TestRailwayBuiltinsAreNotImported(t *testing.T) {
	for name, want := range map[string]bool{"RAILWAY_PUBLIC_DOMAIN": true, "railway_service_id": true, "REDIS_URL": false, "MY_RAILWAY_X": false} {
		if got := IsRailwayBuiltin(name); got != want {
			t.Errorf("IsRailwayBuiltin(%q) = %v", name, got)
		}
	}
}

func TestSettingsComeFromTheConfigFileThenTheServiceThenTheLastDeployment(t *testing.T) {
	inst := wireInstance{
		StartCommand:     "npm run serve",
		HealthcheckPath:  "/health",
		PreDeployCommand: json.RawMessage(`["npm run migrate","npm run seed"]`),
		ResolvedFileConfig: &wireFileConfig{
			ConfigFile:   "/apps/worker/railway.toml",
			FileManifest: json.RawMessage(`{"build":{"dockerfilePath":"apps/worker/Dockerfile"},"deploy":{"startCommand":"pnpm start","numReplicas":2}}`),
		},
		LatestDeployment: &wireDeployment{Meta: json.RawMessage(`{"plan":{"variables":{"SECRET":"x"}},
			"serviceManifest":{"build":{"buildCommand":"pnpm build","dockerfilePath":"/Dockerfile"},
			"deploy":{"multiRegionConfig":{"europe-west4-drams3a":{"numReplicas":2}},"healthcheckPath":"/old"}}}`)},
	}
	var svc Service
	resolveSettings(&svc, inst)

	if svc.StartCommand != "pnpm start" || svc.Provenance["start_command"] != "/apps/worker/railway.toml" {
		t.Fatalf("the config file wins, as on Railway: %q from %q", svc.StartCommand, svc.Provenance["start_command"])
	}
	if svc.DockerfilePath != "apps/worker/Dockerfile" || svc.NumReplicas != 2 {
		t.Fatalf("file settings = %+v", svc)
	}
	if svc.HealthcheckPath != "/health" || svc.Provenance["healthcheck_path"] != "service settings" {
		t.Fatalf("a service setting beats the last deployment: %+v", svc)
	}
	if svc.PreDeployCommand != "npm run migrate && npm run seed" {
		t.Fatalf("pre-deploy = %q", svc.PreDeployCommand)
	}
	if svc.BuildCommand != "pnpm build" || svc.Provenance["build_command"] != "latest deployment" {
		t.Fatalf("the last deployment fills what nothing else has: %+v", svc)
	}
	if svc.Region != "europe-west4-drams3a" {
		t.Fatalf("region from the multi-region config = %q", svc.Region)
	}
}

func TestPreDeployCommandIsAStringOrAList(t *testing.T) {
	for raw, want := range map[string]string{
		`"alembic upgrade head"`:   "alembic upgrade head",
		`["alembic upgrade head"]`: "alembic upgrade head",
		`["a", " ", "b"]`:          "a && b",
		`null`:                     "",
		`{"not":"a command"}`:      "",
	} {
		if got := commandText(json.RawMessage(raw)); got != want {
			t.Errorf("commandText(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestBuildSettingIsRelativeToTheRootDirectory(t *testing.T) {
	cases := []struct {
		svc  Service
		want *BuildSetting
	}{
		{Service{DockerfilePath: "/Dockerfile"}, &BuildSetting{Dockerfile: "Dockerfile"}},
		{Service{DockerfilePath: "apps/api/Dockerfile", RootDirectory: "/apps/api"}, &BuildSetting{Dockerfile: "Dockerfile"}},
		{Service{BuildCommand: "pnpm build"}, &BuildSetting{BuildCommand: "pnpm build"}},
		{Service{}, nil},
	}
	for _, c := range cases {
		if got := buildSetting(c.svc); !reflect.DeepEqual(got, c.want) {
			t.Errorf("buildSetting(%+v) = %+v, want %+v", c.svc, got, c.want)
		}
	}
}

func TestEnvironmentsAreReadByNameOrAll(t *testing.T) {
	project := wireProject{Name: "clinic-iq-system"}
	project.Environments.Edges = []struct {
		Node wireRef `json:"node"`
	}{{Node: wireRef{ID: "p", Name: "production"}}, {Node: wireRef{ID: "d", Name: "development"}}}

	all, err := pickEnvironments(project, []string{"all"})
	if err != nil || len(all) != 2 || all[0].Name != "development" {
		t.Fatalf("all = %+v, %v", all, err)
	}
	both, err := pickEnvironments(project, []string{"production", "development", "production"})
	if err != nil || len(both) != 2 {
		t.Fatalf("named = %+v, %v", both, err)
	}
	if _, err := pickEnvironments(project, []string{"staging"}); err == nil {
		t.Fatal("an unknown environment must be refused")
	}
}

func TestServiceNamesAndSlugsFollowMIOSARules(t *testing.T) {
	long := "backend-cliniciq-worker-notifications-development" // 50 characters
	cases := []struct{ in, name, slug string }{
		{"@clinic-iq/web", "clinic-iq-web", "clinic-iq-web"},
		{"@clinic-iq/worker", "clinic-iq-worker", "clinic-iq-worker"},
		{"Postgres", "Postgres", "postgres"},
		{"backend-cliniciq-worker-notifications-dev", "backend-cliniciq-worker-notifications-dev", "backend-cliniciq-worker-notificat-" + hashOf("backend-cliniciq-worker-notifications-dev")},
		{long, long, "backend-cliniciq-worker-notificat-" + hashOf(long)},
	}
	for _, c := range cases {
		name := ServiceName(c.in)
		slug := ServiceSlug(name)
		if name != c.name || slug != c.slug {
			t.Errorf("%q -> name %q slug %q; want %q %q", c.in, name, slug, c.name, c.slug)
		}
		if len(slug) > 40 || !regexpSlug.MatchString(slug) {
			t.Errorf("slug %q breaks the MIOSA slug rule", slug)
		}
	}
	if ServiceSlug(long) != ServiceSlug(long) {
		t.Fatal("a slug must be the same on every run")
	}
	if got := ServiceName(strings.Repeat("a", 70)); len(got) != 60 {
		t.Fatalf("a long name must be cut to 60 characters, got %d", len(got))
	}
}

var regexpSlug = regexp.MustCompile(`\A[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?\z`)

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:6]
}
