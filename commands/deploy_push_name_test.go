package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pushAPI serves the drop flow for a push to an existing deployment.
func pushAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d3"}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{"session_id":"s1","chunk_index":0,"bytes_received":10}}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/drop/sessions/s1/complete"):
			return 200, `{"data":{"version_id":"v2","deployment_id":"d3","public_url":"https://d3.miosa.app","promoted":true}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

// successLine is the "ok ..." line the command prints when it is done; the
// upload progress line legitimately names the directory, so only this one is
// checked for the deployment name.
func successLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "ok ") {
			return line
		}
	}
	return ""
}

func writeLink(t *testing.T, dir, id, name string) {
	t.Helper()
	link, err := json.Marshal(map[string]string{"deployment_id": id, "name": name})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".miosa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".miosa", "deployment.json"), append(link, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A push names the deployment it pushed to, not the folder it uploaded.
func TestDeployPushNamesTheDeploymentNotTheFolder(t *testing.T) {
	f := pushAPI(t)
	dir := t.TempDir()
	writeStaticSite(t, dir)

	out, err := run(t, "deploy", "push", "my-site", "--dir", dir, "--no-wait")
	if err != nil {
		t.Fatalf("deploy push: %v (out=%q)", err, out)
	}
	if got := successLine(out); !strings.Contains(got, `Deployed "my-site"`) {
		t.Fatalf("the line should name the deployment pushed to: %q", got)
	}
	if strings.Contains(successLine(out), filepath.Base(dir)) {
		t.Fatalf("the folder name must not be shown as the deployment: %q", out)
	}
	if len(f.find("POST", "/deployments/my-site/drop/sessions")) != 1 {
		t.Fatalf("the push should have gone to the named deployment")
	}
}

// The bare command in a linked folder names the linked deployment, in the
// prompt and in the result.
func TestDeployCurrentDirInALinkedFolderNamesTheLinkedDeployment(t *testing.T) {
	f := pushAPI(t)
	dir := t.TempDir()
	writeStaticSite(t, dir)
	writeLink(t, dir, "d3", "my-linked-site")
	t.Chdir(dir)

	out, err := run(t, "deploy", "--yes", "--no-wait")
	if err != nil {
		t.Fatalf("deploy --yes: %v (out=%q)", err, out)
	}
	if got := successLine(out); !strings.Contains(got, `Deployed "my-linked-site"`) {
		t.Fatalf("the line should name the linked deployment: %q", got)
	}
	if strings.Contains(successLine(out), filepath.Base(dir)) {
		t.Fatalf("the folder name must not be shown as the deployment: %q", out)
	}
	if len(f.find("POST", "/deployments/d3/drop/sessions")) != 1 {
		t.Fatalf("the push should have gone to the linked deployment")
	}
}
