package commands_test

import (
	"strings"
	"testing"
)

func envReleaseServer(t *testing.T, release string) *fakeAPI {
	t.Helper()
	routes := guardRoutes("")
	return newFakeAPI(t, func(c call) (int, string) {
		base := "/deployments/" + depID + "/env"
		switch {
		case c.is("POST", base):
			return 200, `{"data":[{"name":"API"}],"release":` + release + `}`
		case c.is("DELETE", base+"/API"):
			return 200, `{"data":{"deleted":["API"],"missing":[],"environment":"production"},"release":` + release + `}`
		}
		return routeReply(routes)(c)
	})
}

func TestEnvSetPrintsTheReleaseTheServerCut(t *testing.T) {
	f := envReleaseServer(t, `{"id":"3917d8f9-0000-0000-0000-000000000000","state":"deploying"}`)
	out, err := run(t, "deploy", "env-set", "my-app", "--set", "API=v")
	if err != nil {
		t.Fatalf("env-set: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Release 3917d8f9") || !strings.Contains(out, "deploying") {
		t.Fatalf("output does not name the release:\n%s", out)
	}
	posts := f.find("POST", "/deployments/"+depID+"/env")
	if len(posts) != 1 {
		t.Fatalf("POST = %+v", posts)
	}
	if _, sent := posts[0].Body["release"]; sent {
		t.Fatalf("release must be left to the server default, body = %+v", posts[0].Body)
	}
}

func TestEnvSetNoReleaseSendsReleaseFalseAndSaysNoneWasCut(t *testing.T) {
	f := envReleaseServer(t, `null`)
	out, err := run(t, "deploy", "env-set", "my-app", "--set", "API=v", "--no-release")
	if err != nil {
		t.Fatalf("env-set: %v\n%s", err, out)
	}
	posts := f.find("POST", "/deployments/"+depID+"/env")
	if len(posts) != 1 || posts[0].Body["release"] != false {
		t.Fatalf("POST = %+v", posts)
	}
	if _, leaked := posts[0].Body["no_release"]; leaked {
		t.Fatalf("no_release must not reach the server: %+v", posts[0].Body)
	}
	if !strings.Contains(out, "No release was cut") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestEnvRmNoReleaseSendsReleaseFalse(t *testing.T) {
	f := envReleaseServer(t, `null`)
	out, err := run(t, "deploy", "env-rm", "my-app", "API", "--no-release")
	if err != nil {
		t.Fatalf("env-rm: %v\n%s", err, out)
	}
	dels := f.find("DELETE", "/deployments/"+depID+"/env/API")
	if len(dels) != 1 || !strings.Contains(dels[0].Query, "release=false") || strings.Contains(dels[0].Query, "no_release") {
		t.Fatalf("DELETE = %+v", dels)
	}
	if !strings.Contains(out, "No release was cut") {
		t.Fatalf("output:\n%s", out)
	}
}
