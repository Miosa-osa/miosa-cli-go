package commands_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// noChangeAPI: deployment d3 whose latest version (v2) ships exactly the files
// writeStaticSite writes.
func noChangeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	manifest := `{"data":{"id":"91e9d02f-0000-4000-8000-000000000002","artifact_manifest":{"files":[` +
		`{"path":"index.html","sha256":"` + sha("<h1>hi</h1>") + `","size":11},` +
		`{"path":"assets/app.js","sha256":"` + sha("console.log(1)") + `","size":14}]}}}`
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.Method == "GET" && c.Path == "/deployments/my-site/versions":
			return 200, `{"data":[{"id":"91e9d02f-0000-4000-8000-000000000002","version_number":2},{"id":"11111111-0000-4000-8000-000000000001","version_number":1}]}`
		case c.Method == "GET" && c.Path == "/deployments/my-site/versions/91e9d02f-0000-4000-8000-000000000002":
			return 200, manifest
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d3"}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{}}`
		case c.Method == "POST" && strings.HasSuffix(c.Path, "/drop/sessions/s1/complete"):
			return 200, `{"data":{"version_id":"v3","deployment_id":"d3","promoted":true}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestPushWithNoChangesUploadsNothingAndForceUploads(t *testing.T) {
	f := noChangeAPI(t)
	dir := t.TempDir()
	writeStaticSite(t, dir)

	out, err := run(t, "deploy", "push", "my-site", "--dir", dir)
	if err != nil || !strings.Contains(out, "No changes since version 2") {
		t.Fatalf("want the no-change line: %v\n%s", err, out)
	}
	if len(f.find("POST", "/deployments/my-site/drop/sessions")) != 0 {
		t.Fatal("nothing may be uploaded")
	}
	if out, err := run(t, "deploy", "push", "my-site", "--dir", dir, "--force"); err != nil {
		t.Fatalf("--force: %v\n%s", err, out)
	}
	if len(f.find("POST", "/deployments/my-site/drop/sessions")) != 1 {
		t.Fatal("--force must upload")
	}
	// A changed file uploads.
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>changed</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "deploy", "push", "my-site", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	if len(f.find("POST", "/deployments/my-site/drop/sessions")) != 2 {
		t.Fatal("a changed folder must upload")
	}
}

func TestGetOnAVersionIDNamesItsDeployment(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /deployments":                        `{"data":[{"id":"` + depID + `","name":"lunivate-site","slug":"lunivate-site"}]}`,
		"GET /deployments/" + depID + "/versions": `{"data":[{"id":"90a9dee1-2222-4222-8222-222222222222","version_number":2}]}`,
	})
	_, err := run(t, "deploy", "get", "90a9dee1")
	if err == nil || !strings.Contains(err.Error(), "version 2 of deployment lunivate-site") || !strings.Contains(err.Error(), "deploy versions lunivate-site") {
		t.Fatalf("want the version owner named: %v", err)
	}
}

func TestVersionsRmRefusesTheLiveVersionAndDeletesAnOldOne(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /deployments":                        `{"data":[{"id":"` + depID + `","name":"My App","slug":"my-app"}]}`,
		"GET /deployments/" + depID:               `{"data":{"id":"` + depID + `","active_version_id":"67cf439d-1111-4111-8111-111111111111"}}`,
		"GET /deployments/" + depID + "/versions": `{"data":[{"id":"67cf439d-1111-4111-8111-111111111111","version_number":2},{"id":"90a9dee1-2222-4222-8222-222222222222","version_number":1}]}`,
		"DELETE /deployments/" + depID + "/versions/90a9dee1-2222-4222-8222-222222222222": `{"data":{}}`,
	})
	if _, err := run(t, "deploy", "versions", "rm", "67cf439d", "--deployment", "my-app", "-y"); err == nil || !strings.Contains(err.Error(), "is live") {
		t.Fatalf("the live version must be refused: %v", err)
	}
	if out, err := run(t, "deploy", "versions", "rm", "90a9dee1", "--deployment", "my-app", "-y"); err != nil || !strings.Contains(out, "Deleted version") {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	if len(f.find("DELETE", "/deployments/"+depID+"/versions/90a9dee1-2222-4222-8222-222222222222")) != 1 {
		t.Fatal("one DELETE of the old version")
	}
}

func TestDeployCreateForgeUpdateAndRedeployCommitSendTheRightBodies(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /deployments":                         `{"data":[{"id":"` + depID + `","name":"My App","slug":"my-app"}]}`,
		"POST /deployments":                        `{"data":{"id":"` + depID + `","name":"site","state":"building"}}`,
		"PATCH /deployments/" + depID:              `{"data":{"id":"` + depID + `","name":"My App"}}`,
		"POST /deployments/" + depID + "/redeploy": `{"data":{}}`,
	})
	if out, err := run(t, "deploy", "create", "--forge", "acme/site", "--branch", "main"); err != nil {
		t.Fatalf("create --forge: %v\n%s", err, out)
	}
	c := f.find("POST", "/deployments")[0]
	if c.Body["repo_provider"] != "forge" || c.Body["forge_repository"] != "acme/site" || c.Body["branch"] != "main" || c.Body["auto_deploy"] != true || c.Body["name"] != "site" {
		t.Fatalf("create body = %+v", c.Body)
	}
	if out, err := run(t, "deploy", "update", "my-app", "--forge", "acme/site", "--no-auto"); err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	u := f.find("PATCH", "/deployments/"+depID)[0]
	if u.Body["forge_repository"] != "acme/site" || u.Body["auto_deploy"] != false {
		t.Fatalf("update body = %+v", u.Body)
	}
	if _, ok := u.Body["branch"]; ok {
		t.Fatal("only flags given are sent")
	}
	if _, err := run(t, "deploy", "update", "my-app"); err == nil {
		t.Fatal("update with nothing to change must refuse")
	}
	if out, err := run(t, "deploy", "redeploy", "my-app", "--commit", "1a2b3c4d"); err != nil {
		t.Fatalf("redeploy: %v\n%s", err, out)
	}
	if r := f.find("POST", "/deployments/"+depID+"/redeploy")[0]; r.Body["commit_sha"] != "1a2b3c4d" {
		t.Fatalf("redeploy body = %+v", r.Body)
	}
}

func TestVersionsRmOnAVersionIDAloneFindsItsDeployment(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, map[string]any{
		"GET /deployments":                        `{"data":[{"id":"` + depID + `","name":"My App","slug":"my-app"}]}`,
		"GET /deployments/" + depID:               `{"data":{"id":"` + depID + `","active_version_id":"67cf439d-1111-4111-8111-111111111111"}}`,
		"GET /deployments/" + depID + "/versions": `{"data":[{"id":"67cf439d-1111-4111-8111-111111111111","version_number":2},{"id":"90a9dee1-2222-4222-8222-222222222222","version_number":1}]}`,
		"DELETE /deployments/" + depID + "/versions/90a9dee1-2222-4222-8222-222222222222": `{"data":{}}`,
	})
	if out, err := run(t, "deploy", "versions", "rm", "90a9dee1", "-y"); err != nil || !strings.Contains(out, "Deleted version") {
		t.Fatalf("delete by version id alone: %v\n%s", err, out)
	}
	if len(f.find("DELETE", "/deployments/"+depID+"/versions/90a9dee1-2222-4222-8222-222222222222")) != 1 {
		t.Fatal("one DELETE of the old version")
	}
	if _, err := run(t, "deploy", "versions", "rm", "3", "-y"); err == nil || !strings.Contains(err.Error(), "no deployment to work on") {
		t.Fatalf("a version number needs a deployment: %v", err)
	}
}
