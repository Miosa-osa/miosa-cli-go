package commands_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func chdir(t *testing.T, dir string) {
	t.Helper()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

// The owner's folder is a git checkout named like the live upload deployment:
// bare 'miosa deploy' must link and push to it, never create or refuse.
func TestBareDeployInAGitFolderNamedLikeAnUploadDeploymentPushesToIt(t *testing.T) {
	f := existingNameAPI(t)
	dir := filepath.Join(t.TempDir(), "taken-site")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStaticSite(t, dir)
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://github.com/acme/taken-site.git"}} {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	chdir(t, dir)

	if _, err := run(t, "deploy", "--no-wait"); err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("off a terminal it must name the existing deployment and ask for --yes: %v", err)
	}
	out, err := run(t, "deploy", "--yes", "--no-wait")
	if err != nil {
		t.Fatalf("deploy --yes: %v (out=%q)", err, out)
	}
	if len(f.find("POST", "/deployments")) != 0 || len(f.find("POST", "/deployments/drop/sessions")) != 0 {
		t.Fatal("nothing may be created")
	}
	if len(f.find("POST", "/deployments/d9/drop/sessions")) != 1 {
		t.Fatalf("the folder must be pushed to d9: %+v", f.calls)
	}
	if got := readLink(t, dir)["deployment_id"]; got != "d9" {
		t.Fatalf("link = %q, want d9", got)
	}
}

// push --dir ./dist from the project folder links the project folder too: that
// is where the next bare 'miosa deploy' looks.
func TestPushWithADirInsideTheProjectLinksTheProjectFolder(t *testing.T) {
	pushAPI(t)
	project := t.TempDir()
	dist := filepath.Join(project, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStaticSite(t, dist)
	chdir(t, project)

	if out, err := run(t, "deploy", "push", "my-site", "--dir", "dist", "--no-wait"); err != nil {
		t.Fatalf("push: %v (out=%q)", err, out)
	}
	if got := readLink(t, project)["deployment_id"]; got != "d3" {
		t.Fatalf("project link = %q, want d3", got)
	}
}

func TestReleasesShowTheVersionAndWhichIsLive(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /deployments":          `{"data":[{"id":"` + depID + `","name":"My App","slug":"my-app"}]}`,
		"GET /deployments/" + depID: `{"data":{"id":"` + depID + `","active_release_id":"r2222222-0000-4000-8000-000000000000"}}`,
		"GET /deployments/" + depID + "/releases": `{"data":[` +
			`{"id":"r2222222-0000-4000-8000-000000000000","deployment_version_id":"67cf439d-1111-4111-8111-111111111111","state":"ready"},` +
			`{"id":"r1111111-0000-4000-8000-000000000000","deployment_version_id":"90a9dee1-2222-4222-8222-222222222222","state":"ready"}]}`,
	})
	out, err := run(t, "deploy", "releases", "my-app")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"VERSION", "67cf439d", "90a9dee1", "LIVE", "true", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("releases must show %q:\n%s", want, out)
		}
	}
}

func TestDomainUpdateSetsAndClearsTheRedirectInPlace(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /domains/www.example.com":         `{"data":{"hostname":"www.example.com","deployment_id":"` + depID + `"}}`,
		"POST /domains/www.example.com/assign": `{"data":{"hostname":"www.example.com","redirect_policy":"www_to_apex"}}`,
	})
	out, err := run(t, "deploy", "domain-update", "www.example.com", "--redirect-to", "example.com")
	if err != nil || !strings.Contains(out, "redirects to example.com") {
		t.Fatalf("set: %v\n%s", err, out)
	}
	calls := f.find("POST", "/domains/www.example.com/assign")
	if len(calls) != 1 || calls[0].Body["deployment_id"] != depID || calls[0].Body["redirect_policy"] != "www_to_apex" {
		t.Fatalf("assign must keep the deployment and set www_to_apex: %+v", calls)
	}
	if _, err := run(t, "deploy", "domain-update", "www.example.com", "--no-redirect"); err != nil {
		t.Fatal(err)
	}
	calls = f.find("POST", "/domains/www.example.com/assign")
	if len(calls) != 2 || calls[1].Body["redirect_policy"] != "none" {
		t.Fatalf("--no-redirect must send none: %+v", calls)
	}
	if _, err := run(t, "deploy", "domain-update", "www.example.com"); err == nil {
		t.Fatal("one of --redirect-to or --no-redirect is required")
	}
}

// A static deployment reports only its live version; the newest release of
// that version is the live one.
func TestReleasesMarkLiveByVersionForAStaticDeployment(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /deployments":          `{"data":[{"id":"` + depID + `","name":"My App","slug":"my-app"}]}`,
		"GET /deployments/" + depID: `{"data":{"id":"` + depID + `","active_version_id":"67cf439d-1111-4111-8111-111111111111"}}`,
		"GET /deployments/" + depID + "/releases": `{"data":[` +
			`{"id":"r3","deployment_version_id":"90a9dee1-2222-4222-8222-222222222222"},` +
			`{"id":"r2","deployment_version_id":"67cf439d-1111-4111-8111-111111111111"},` +
			`{"id":"r1","deployment_version_id":"67cf439d-1111-4111-8111-111111111111"}]}`,
	})
	out, err := run(t, "deploy", "releases", "my-app", "--json")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	compact := strings.Join(strings.Fields(out), "")
	if strings.Count(compact, `"live":true`) != 1 {
		t.Fatalf("exactly one release (the newest of the live version) is live:\n%s", out)
	}
}

// Linking an unlinked folder to an existing deployment names it, not its id.
func TestPushToAnExistingDeploymentPrintsItsName(t *testing.T) {
	existingNameAPI(t)
	dir := filepath.Join(t.TempDir(), "taken-site")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStaticSite(t, dir)
	chdir(t, dir)
	out, err := run(t, "deploy", "--yes", "--no-wait")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(successLine(out), `Deployed "taken-site"`) {
		t.Fatalf("the success line must name taken-site: %q", successLine(out))
	}
	if got := readLink(t, dir)["name"]; got != "taken-site" {
		t.Fatalf("link name = %q", got)
	}
}
