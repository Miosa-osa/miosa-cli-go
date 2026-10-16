package commands_test

import (
	"strings"
	"testing"
)

// envServer answers the probe the way a server with (or without) environments does.
func envServer(t *testing.T, supportsEnvironments bool) *fakeAPI {
	t.Helper()
	routes := guardRoutes("")
	return newFakeAPI(t, func(c call) (int, string) {
		base := "/deployments/" + depID + "/env"
		switch {
		case c.is("GET", base) && strings.Contains(c.Query, "environment=__probe__"):
			if supportsEnvironments {
				return 422, `{"error":{"code":"INVALID_ENVIRONMENT","field":"environment","message":"environment must be one of production, preview, development"}}`
			}
			return 200, `{"data":[],"env_vars":[]}`
		case c.is("GET", base):
			return 200, `{"data":[{"name":"API","environment":"preview","updated_at":"2026-10-16T10:00:00Z"}],"env_vars":[{"name":"API","environment":"preview","updated_at":"2026-10-16T10:00:00Z"}]}`
		case c.is("POST", base):
			return 200, `{"data":[{"name":"API","environment":"preview"}],"env_vars":[{"name":"API","environment":"preview"}]}`
		case c.is("DELETE", base+"/API"):
			return 200, `{"data":{"deleted":["API"],"missing":[],"environment":"development"}}`
		}
		return routeReply(routes)(c)
	})
}

func TestEnvSetWithAnEnvironmentSendsIt(t *testing.T) {
	f := envServer(t, true)
	if out, err := run(t, "deploy", "env-set", "my-app", "--set", "API=v", "--environment", "preview"); err != nil {
		t.Fatalf("env-set: %v\n%s", err, out)
	}
	posts := f.find("POST", "/deployments/"+depID+"/env")
	if len(posts) != 1 || posts[0].Body["environment"] != "preview" {
		t.Fatalf("POST bodies = %+v", posts)
	}
}

func TestEnvSetRefusesAnEnvironmentAnOldServerWouldIgnore(t *testing.T) {
	f := envServer(t, false)
	_, err := run(t, "deploy", "env-set", "my-app", "--set", "API=v", "-e", "preview")
	if err == nil || !strings.Contains(err.Error(), "does not support --environment") {
		t.Fatalf("err = %v", err)
	}
	if n := len(f.find("POST", "/deployments/"+depID+"/env")); n != 0 {
		t.Fatalf("an old server would have written production; %d POST(s) sent", n)
	}
}

func TestEnvListAndRmTakeTheEnvironment(t *testing.T) {
	f := envServer(t, true)
	out, err := run(t, "deploy", "env", "my-app", "-e", "preview")
	if err != nil || !strings.Contains(out, "ENVIRONMENT") || !strings.Contains(out, "preview") {
		t.Fatalf("env list: %v\n%s", err, out)
	}
	gets := f.find("GET", "/deployments/"+depID+"/env")
	if last := gets[len(gets)-1]; !strings.Contains(last.Query, "environment=preview") {
		t.Fatalf("list query = %q", last.Query)
	}

	if out, err := run(t, "deploy", "env-rm", "my-app", "API", "-e", "development"); err != nil {
		t.Fatalf("env-rm: %v\n%s", err, out)
	}
	dels := f.find("DELETE", "/deployments/"+depID+"/env/API")
	if len(dels) != 1 || !strings.Contains(dels[0].Query, "environment=development") {
		t.Fatalf("DELETE = %+v", dels)
	}
}

func TestEnvSetWithoutAnEnvironmentNeedsNoProbe(t *testing.T) {
	f := envServer(t, false)
	if out, err := run(t, "deploy", "env-set", "my-app", "--set", "API=v"); err != nil {
		t.Fatalf("env-set: %v\n%s", err, out)
	}
	for _, c := range f.find("GET", "/deployments/"+depID+"/env") {
		if strings.Contains(c.Query, "__probe__") {
			t.Fatal("production needs no capability probe")
		}
	}
	if posts := f.find("POST", "/deployments/"+depID+"/env"); len(posts) != 1 || posts[0].Body["environment"] != nil {
		t.Fatalf("POST = %+v", posts)
	}
}
