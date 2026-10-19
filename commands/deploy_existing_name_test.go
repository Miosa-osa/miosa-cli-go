package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// existingNameAPI knows one deployment, "taken-site" (id d9), and serves the
// drop flow for it.
func existingNameAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.Method == "GET" && c.Path == "/deployments":
			return 200, `{"data":[{"id":"d9","name":"taken-site","slug":"taken-site"}]}`
		case c.Method == "GET" && c.Path == "/deployments/d9":
			return 200, `{"data":{"id":"d9","name":"taken-site","slug":"taken-site"}}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d9"}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{"session_id":"s1","chunk_index":0,"bytes_received":10}}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/drop/sessions/s1/complete"):
			return 200, `{"data":{"version_id":"v2","deployment_id":"d9","public_url":"https://taken-site.miosa.app","promoted":true}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func readLink(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".miosa", "deployment.json"))
	if err != nil {
		t.Fatalf("the folder must be linked: %v", err)
	}
	var link map[string]string
	if err := json.Unmarshal(raw, &link); err != nil {
		t.Fatal(err)
	}
	return link
}

// An unlinked folder deployed under a name that already exists pushes a new
// version to that deployment and links the folder; it never creates a second
// deployment with the same name.
func TestDeployUnlinkedFolderWithAnExistingNamePushesInsteadOfDuplicating(t *testing.T) {
	f := existingNameAPI(t)
	dir := t.TempDir()
	writeStaticSite(t, dir)

	out, err := run(t, "deploy", "create", "--dir", dir, "--name", "taken-site", "--no-wait")
	if err != nil {
		t.Fatalf("deploy: %v (out=%q)", err, out)
	}
	if len(f.find("POST", "/deployments/drop/sessions")) != 0 {
		t.Fatal("a new deployment was created next to the existing one")
	}
	if len(f.find("POST", "/deployments/d9/drop/sessions")) != 1 {
		t.Fatalf("the version must go to the existing deployment d9: %+v", f.calls)
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("the output must say it pushed to the existing deployment: %q", out)
	}
	if got := readLink(t, dir)["deployment_id"]; got != "d9" {
		t.Fatalf("link = %q, want d9", got)
	}
}

// A push (not only a first create) writes the link, so folders deployed
// before the link existed get one.
func TestDeployPushWritesTheFolderLink(t *testing.T) {
	pushAPI(t)
	dir := t.TempDir()
	writeStaticSite(t, dir)

	if out, err := run(t, "deploy", "push", "my-site", "--dir", dir, "--no-wait"); err != nil {
		t.Fatalf("deploy push: %v (out=%q)", err, out)
	}
	if got := readLink(t, dir)["deployment_id"]; got != "d3" {
		t.Fatalf("link = %q, want d3", got)
	}
}

// A repository deploy under an existing name is refused, not duplicated.
func TestDeployGitWithAnExistingNameRefuses(t *testing.T) {
	f := existingNameAPI(t)
	out, err := run(t, "deploy", "create", "--git", "https://github.com/acme/taken-site", "--name", "taken-site", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), "redeploy") {
		t.Fatalf("must refuse and point at redeploy: %v (out=%q)", err, out)
	}
	if len(f.find("POST", "/deployments")) != 0 {
		t.Fatal("nothing may be created")
	}
}
