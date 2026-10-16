package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A folder whose .miosa/deployment.json names a deleted deployment answered a
// bare "Not Found [NOT_FOUND]" to 'deploy create <new-name>' (optimal-qa,
// studio-redesign).
func staleLinkAPI(t *testing.T) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d3","source_type":"upload","chunk_count":0}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{"session_id":"s1","chunk_index":0,"bytes_received":10}}`
		case c.is("POST", "/deployments/d3/drop/sessions/s1/complete"):
			return 200, `{"data":{"version_id":"v1","deployment_id":"d3","name":"fresh","public_url":"https://d3.miosa.app","promoted":true}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"Not Found"}}`
	})
}

func linkedToDeleted(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeStaticSite(t, dir)
	if err := os.MkdirAll(filepath.Join(dir, ".miosa"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := `{"deployment_id":"0d1e2a3b-0000-4000-8000-000000000001","name":"old-site"}`
	if err := os.WriteFile(filepath.Join(dir, ".miosa", "deployment.json"), []byte(link), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDeployCreateNewNameOverAStaleLinkCreatesAndRelinks(t *testing.T) {
	f := staleLinkAPI(t)
	dir := linkedToDeleted(t)

	out, err := run(t, "deploy", "create", "--dir", dir, "--name", "fresh", "--yes")
	if err != nil {
		t.Fatalf("deploy create: %v\n%s", err, out)
	}
	if !strings.Contains(out, "old-site, which no longer exists") {
		t.Fatalf("out = %s", out)
	}
	if len(f.find("POST", "/deployments/drop/sessions")) != 1 {
		t.Fatal("a new deployment must be created")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".miosa", "deployment.json"))
	var link map[string]string
	_ = json.Unmarshal(raw, &link)
	if link["deployment_id"] != "d3" || link["name"] != "fresh" {
		t.Fatalf("link not rewritten: %s", raw)
	}
	if !strings.Contains(successLine(out), `"fresh"`) || strings.Contains(successLine(out), "0d1e2a3b") {
		t.Fatalf("the success line must name the new deployment: %q", successLine(out))
	}
}

func TestDeployCreateOverAStaleLinkAsksBeforeCreating(t *testing.T) {
	f := staleLinkAPI(t)
	dir := linkedToDeleted(t)

	_, err := run(t, "deploy", "create", "--dir", dir, "--name", "fresh")
	if err == nil || !strings.Contains(err.Error(), "no longer exists") || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "[NOT_FOUND]") && !strings.Contains(err.Error(), "linked to") {
		t.Fatal("still the bare not found")
	}
	if len(f.find("POST", "/deployments/drop/sessions")) != 0 {
		t.Fatal("nothing may be created without confirmation")
	}
}
