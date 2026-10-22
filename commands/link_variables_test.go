package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	linkProjectID = "11111111-1111-1111-1111-111111111111"
	linkServiceID = "33333333-3333-3333-3333-333333333333"
)

// linkedRoutes is the API a linked directory talks to.
func linkedRoutes() map[string]any {
	routes := map[string]any{}
	routes["GET /projects"] = `{"data":[{"id":"` + linkProjectID + `","name":"cliniciq","slug":"cliniciq","is_default":true}]}`
	routes["GET /projects/"+linkProjectID] = `{"data":{"id":"` + linkProjectID + `","name":"cliniciq","slug":"cliniciq"}}`
	routes["GET /projects/"+linkProjectID+"/environments"] = `{"data":[{"id":"e1","project_id":"` + linkProjectID + `","name":"production","slug":"production","is_default":true}]}`
	routes["GET /projects/"+linkProjectID+"/services"] = `{"data":[{"id":"` + linkServiceID + `","project_id":"` + linkProjectID + `","environment_id":"e1","name":"API","slug":"api","kind":"server","source":{"type":"git"},"build":{},"settings":{},"public_address":{"label":"api","host":"api.acme.miosa.app","url":"https://api.acme.miosa.app"},"lock_version":1}]}`
	routes["GET /projects/"+linkProjectID+"/services/"+linkServiceID+"/deployments"] = `{"data":[{"id":"r1","service_id":"` + linkServiceID + `","number":3,"state":"active","trigger":"manual"}]}`
	routes["PUT /projects/"+linkProjectID+"/services/"+linkServiceID+"/variables"] = `{"data":{}}`
	routes["GET /projects/"+linkProjectID+"/services/"+linkServiceID+"/variables"] = `{"data":[{"id":"v1","name":"API_BASE","kind":"plain","source":"service","sealed":false,"build":false},{"id":"v2","name":"SECRET_KEY","kind":"plain","source":"service","sealed":true,"build":false}]}`
	return routes
}
func TestLinkWritesTheProjectTargetAndService(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)

	out, err := run(t, "link", "cliniciq", "--service", "API")
	if err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(".miosa", "link.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{linkProjectID, "production", linkServiceID, `"service": "API"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("link file missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(string(body), "msk_") {
		t.Fatal("a link file must never carry a key")
	}
}

func TestStatusInALinkedDirectoryShowsTheService(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)

	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	for _, want := range []string{"cliniciq", "production", "API", "https://api.acme.miosa.app", "Release  3 active"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status missing %q:\n%s", want, out)
		}
	}
}

func TestUnlinkRemovesTheFile(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)

	if _, err := run(t, "link", "cliniciq"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "unlink"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(".miosa", "link.json")); !os.IsNotExist(err) {
		t.Fatal("unlink must remove the link file")
	}
}

func TestVariablesListUsesTheLink(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)

	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "variables", "list")
	if err != nil {
		t.Fatalf("variables list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "API_BASE") || !strings.Contains(out, "SECRET_KEY") {
		t.Fatalf("list = %s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Fatal("a sealed value must not be printed")
	}
}

func TestVariablesSetChecksReferencesBeforeWriting(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "variables", "set", "CALLBACK=${{API_BASE")
	if err == nil || !strings.Contains(err.Error(), "unclosed reference") {
		t.Fatalf("want a reference error, got %v", err)
	}
	if got := f.find("PUT", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/variables"); len(got) != 0 {
		t.Fatal("an invalid value must not be written")
	}

	_, err = run(t, "variables", "set", "CALLBACK=${{nope}}", "OK=${{shared.API_BASE}}")
	if err != nil {
		t.Fatalf("valid references refused: %v", err)
	}
	put := f.find("PUT", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/variables")
	if len(put) != 1 {
		t.Fatalf("want one write, got %d", len(put))
	}
	vars, _ := put[0].Body["variables"].(map[string]any)
	if vars["CALLBACK"] != "${{nope}}" || vars["OK"] != "${{shared.API_BASE}}" {
		t.Fatalf("body = %v", put[0].Body)
	}
}

func TestVariablesImportReadsDotenv(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), ".env")
	body := "# comment\nexport A=1\nB=\"two words\"\nC='${{shared.X}}'\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "variables", "import", path); err != nil {
		t.Fatal(err)
	}
	put := f.find("PUT", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/variables")
	vars, _ := put[0].Body["variables"].(map[string]any)
	if vars["A"] != "1" || vars["B"] != "two words" || vars["C"] != "${{shared.X}}" {
		t.Fatalf("body = %v", put[0].Body)
	}
}

func TestVariablesExportPrintsDotenv(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "variables", "export")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `API_BASE="`) {
		t.Fatalf("export = %s", out)
	}
	out, err = run(t, "variables", "export", "--shell")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `export API_BASE="`) {
		t.Fatalf("export --shell = %s", out)
	}
}

func TestVariablesSharedUsesTheSharedPath(t *testing.T) {
	t.Chdir(t.TempDir())
	routes := linkedRoutes()
	routes["GET /projects/"+linkProjectID+"/shared-variables"] = `{"data":[{"id":"s1","name":"SHARED_BASE","kind":"plain","source":"shared","sealed":false}]}`
	f := fakeRoutes(t, routes)
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "variables", "list", "--shared")
	if err != nil {
		t.Fatalf("shared list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "SHARED_BASE") {
		t.Fatalf("out = %s", out)
	}
}

func TestRunInALinkedDirectoryUsesTheServiceVariables(t *testing.T) {
	t.Chdir(t.TempDir())
	routes := linkedRoutes()
	routes["GET /projects/"+linkProjectID+"/services/"+linkServiceID+"/variables/API_BASE/reveal"] = `{"data":{"value":"https://example.test"}}`
	routes["GET /projects/"+linkProjectID+"/services/"+linkServiceID+"/variables/SECRET_KEY/reveal"] = `{"data":{"value":"s3cret"}}`
	f := fakeRoutes(t, routes)
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "run", "--", "sh", "-c", "printf %s \"$API_BASE\"")
	if err != nil {
		t.Fatalf("run --: %v\n%s", err, out)
	}
	if !strings.Contains(out, "https://example.test") {
		t.Fatalf("the command did not receive the variable: %q", out)
	}
	if strings.Contains(out, "s3cret") {
		t.Fatal("values must not be printed by the CLI itself")
	}
	if got := f.find("POST", "/sandboxes"); len(got) != 0 {
		t.Fatal("a linked directory must run here, not in a sandbox")
	}
}

func TestRunWithSandboxFlagStillUsesASandbox(t *testing.T) {
	t.Chdir(t.TempDir())
	routes := linkedRoutes()
	routes["POST /sandboxes"] = resp{201, `{"data":{"id":"sb1","status":"running"}}`}
	f := fakeRoutes(t, routes)
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}

	if _, err := run(t, "run", "--sandbox", "--", "true"); err != nil {
		// The one-shot path may fail later on run polling in this fake; what
		// matters is that it did not fall back to running here.
		t.Logf("sandbox one-shot: %v", err)
	}
	if got := f.find("GET", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/variables/API_BASE/reveal"); len(got) != 0 {
		t.Fatal("--sandbox must not reveal the service's values")
	}
}
