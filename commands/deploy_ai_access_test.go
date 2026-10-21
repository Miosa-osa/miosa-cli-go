package commands_test

import (
	"strings"
	"testing"
)

// --ai-access says whether agents may read a published site (allow is the default, block asks agents to
// stay away). It is a deployment field the server stores; the CLI sets it with a PATCH once the deployment exists,
// whatever the source, because an upload creates its deployment through a drop session with no such field.

func aiAccessAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments"):
			return 201, `{"data":{"id":"d2","name":"app","slug":"app","state":"running","public_url":"https://d2.miosa.app"},"webhook_secret":"whsec_abc"}`
		case c.is("PATCH", "/deployments/d2"), c.is("PATCH", "/deployments/my-site"):
			return 200, `{"data":{"id":"d2","name":"my-site","ai_access":"block"}}`
		case c.is("GET", "/deployments/d2"):
			return 200, `{"data":{"id":"d2","name":"app","state":"running","public_url":"https://d2.miosa.app","ai_access":"block"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestDeployCreateSetsAIAccess(t *testing.T) {
	f := aiAccessAPI(t)
	out, err := run(t, "deploy", "create", "--git", "https://github.com/me/app", "--ai-access", "block", "--no-wait")
	if err != nil {
		t.Fatalf("deploy create --ai-access: %v (out=%q)", err, out)
	}
	patch := f.find("PATCH", "/deployments/d2")
	if len(patch) != 1 || patch[0].Body["ai_access"] != "block" {
		t.Fatalf("expected one PATCH with ai_access=block, got %+v", patch)
	}
	if !strings.Contains(out, "AI access") || !strings.Contains(out, "block") {
		t.Fatalf("the choice should be printed: %q", out)
	}
}

func TestDeployCreateWithoutAIAccessSendsNothingAboutIt(t *testing.T) {
	f := aiAccessAPI(t)
	if out, err := run(t, "deploy", "create", "--git", "https://github.com/me/app", "--no-wait"); err != nil {
		t.Fatalf("deploy create: %v (out=%q)", err, out)
	}
	if n := len(f.find("PATCH", "/deployments/d2")); n != 0 {
		t.Fatalf("no --ai-access must leave the server default alone, got %d PATCH calls", n)
	}
}

func TestDeployCreateRefusesABadAIAccessBeforeCreatingAnything(t *testing.T) {
	f := aiAccessAPI(t)
	_, err := run(t, "deploy", "create", "--git", "https://github.com/me/app", "--ai-access", "maybe", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), "allow or block") {
		t.Fatalf("expected a usage error naming allow or block, got %v", err)
	}
	if n := len(f.find("POST", "/deployments")); n != 0 {
		t.Fatalf("nothing may be created for a bad value, got %d creates", n)
	}
}

func TestDeployUpdateSetsAIAccess(t *testing.T) {
	f := aiAccessAPI(t)
	out, err := run(t, "deploy", "update", "my-site", "--ai-access", "block")
	if err != nil {
		t.Fatalf("deploy update --ai-access: %v (out=%q)", err, out)
	}
	patch := f.find("PATCH", "/deployments/my-site")
	if len(patch) != 1 || patch[0].Body["ai_access"] != "block" || len(patch[0].Body) != 1 {
		t.Fatalf("expected only ai_access in the PATCH, got %+v", patch)
	}
}

func TestDeploySettingsIsAnAliasOfUpdate(t *testing.T) {
	f := aiAccessAPI(t)
	if out, err := run(t, "deploy", "settings", "my-site", "--ai-access", "allow"); err != nil {
		t.Fatalf("deploy settings --ai-access: %v (out=%q)", err, out)
	}
	patch := f.find("PATCH", "/deployments/my-site")
	if len(patch) != 1 || patch[0].Body["ai_access"] != "allow" {
		t.Fatalf("expected a PATCH with ai_access=allow, got %+v", patch)
	}
}

func TestDeployUpdateRefusesABadAIAccess(t *testing.T) {
	f := aiAccessAPI(t)
	_, err := run(t, "deploy", "update", "my-site", "--ai-access", "nope")
	if err == nil || !strings.Contains(err.Error(), "allow or block") {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if n := len(f.find("PATCH", "/deployments/my-site")); n != 0 {
		t.Fatalf("nothing may be sent for a bad value, got %d PATCH calls", n)
	}
}

func TestDeployGetShowsAIAccess(t *testing.T) {
	aiAccessAPI(t)
	out, err := run(t, "deploy", "get", "d2")
	if err != nil {
		t.Fatalf("deploy get: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "AI access") || !strings.Contains(out, "block") {
		t.Fatalf("the AI access setting should be shown: %q", out)
	}
}
