package commands_test

import (
	"strings"
	"testing"
)

const (
	flagsProject = "/projects/11111111-1111-1111-1111-111111111111"
	flagsService = flagsProject + "/services/33333333-3333-3333-3333-333333333333"
)

// Once references resolve, the API answers each one as an object; the
// command read them as strings and crashed.
func TestServiceVarsEffectiveReadsReferenceObjects(t *testing.T) {
	servicesAPI(t)
	out, err := run(t, "service", "vars", "effective", "cliniciq", "API")
	if err != nil {
		t.Fatalf("vars effective: %v", err)
	}
	if !strings.Contains(out, "DATABASE_URL") {
		t.Fatalf("output:\n%s", out)
	}
	out, err = run(t, "service", "vars", "effective", "cliniciq", "API", "--json")
	if err != nil || !strings.Contains(out, `"service": "Postgres"`) {
		t.Fatalf("--json must keep the reference objects: %v\n%s", err, out)
	}
}

func TestServiceCreateCarriesBuildAndRunFlags(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "service", "create", "cliniciq", "API", "--repo", "https://github.com/acme/api",
		"--root-dir", "apps/api", "--dockerfile", "/Dockerfile", "--port", "8000",
		"--start-command", "uvicorn app.main:app", "--pre-deploy", "alembic upgrade head",
		"--healthcheck-path", "/readyz", "--replicas", "2")
	if err != nil {
		t.Fatalf("service create: %v", err)
	}
	body := f.find("POST", flagsProject+"/services")[0].Body
	src, _ := body["source"].(map[string]any)
	if src["root_dir"] != "apps/api" || src["dockerfile"] != "Dockerfile" || src["port"] != float64(8000) {
		t.Fatalf("the Dockerfile and port go on the source, where the builder reads them: %#v", src)
	}
	if _, wrong := body["build"]; wrong {
		t.Fatalf("build.dockerfile is ignored by the builder; it must not be sent: %#v", body)
	}
	settings, _ := body["settings"].(map[string]any)
	if settings["pre_deploy_command"] != "alembic upgrade head" || settings["healthcheck_path"] != "/readyz" ||
		settings["start_command"] != "uvicorn app.main:app" || settings["replicas"] != float64(2) {
		t.Fatalf("settings = %#v", settings)
	}
}

func TestServiceCreateRefusesAHealthCheckPathWithoutSlash(t *testing.T) {
	servicesAPI(t)
	_, err := run(t, "service", "create", "cliniciq", "API", "--repo", "https://github.com/acme/api", "--healthcheck-path", "readyz")
	if err == nil || !strings.Contains(err.Error(), "must start with /") {
		t.Fatalf("want a refusal, got %v", err)
	}
}

func TestServiceUpdateSetsAndMergesTheSource(t *testing.T) {
	f := servicesAPI(t)
	_, err := run(t, "service", "update", "cliniciq", "API", "--pre-deploy", "alembic upgrade head",
		"--healthcheck-path", "/readyz", "--root-dir", "/apps/api/", "--dockerfile", "Dockerfile.prod", "--port", "8000")
	if err != nil {
		t.Fatalf("service update: %v", err)
	}
	body := f.find("PATCH", flagsService)[0].Body
	settings, _ := body["settings"].(map[string]any)
	if settings["pre_deploy_command"] != "alembic upgrade head" || settings["healthcheck_path"] != "/readyz" {
		t.Fatalf("settings = %#v", settings)
	}
	src, _ := body["source"].(map[string]any)
	// The source is replaced whole, so the service's own type and ref stay.
	if src["type"] != "git" || src["ref"] != "https://github.com/acme/api" ||
		src["root_dir"] != "apps/api" || src["dockerfile"] != "Dockerfile.prod" || src["port"] != float64(8000) {
		t.Fatalf("source = %#v", src)
	}
}

func TestServiceUpdateEmptyPreDeployRemovesIt(t *testing.T) {
	f := servicesAPI(t)
	if _, err := run(t, "service", "update", "cliniciq", "API", "--pre-deploy", ""); err != nil {
		t.Fatalf("service update: %v", err)
	}
	settings, _ := f.find("PATCH", flagsService)[0].Body["settings"].(map[string]any)
	if v, ok := settings["pre_deploy_command"]; !ok || v != nil {
		t.Fatalf("an empty --pre-deploy must send null (remove), got %#v", settings)
	}
	if _, sent := f.find("PATCH", flagsService)[0].Body["source"]; sent {
		t.Fatal("no source flag, no source change")
	}
}
