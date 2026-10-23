package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mwProject = "11111111-1111-4111-8111-111111111111"
	mwService = "33333333-3333-4333-8333-333333333333"
)

func mwRoutes(settings string) map[string]any {
	return map[string]any{
		"GET /projects": `{"data":[{"id":"` + mwProject + `","name":"shop","slug":"shop"}]}`,
		"GET /projects/" + mwProject + "/services": `{"data":[
			{"id":"` + mwService + `","name":"API","slug":"api","kind":"server","lock_version":3,"settings":` + settings + `}]}`,
		"PATCH /projects/" + mwProject + "/services/" + mwService: `{"data":{"id":"` + mwService + `","name":"API","slug":"api","kind":"server","lock_version":4,"settings":{}}}`,
	}
}

func TestMiddlewareShowsWhatIsConfigured(t *testing.T) {
	fakeRoutes(t, mwRoutes(`{"edge_middleware":{"rules":[{"path":"/old/*","redirect":{"to":"/new"}}]}}`))

	out, err := run(t, "middleware", "shop", "API")
	if err != nil {
		t.Fatalf("middleware: %v\n%s", err, out)
	}
	if !strings.Contains(out, "/old/*") || !strings.Contains(out, "redirect") {
		t.Fatalf("out = %s", out)
	}
}

func TestMiddlewareSetsFromAFile(t *testing.T) {
	f := fakeRoutes(t, mwRoutes(`{}`))

	path := filepath.Join(t.TempDir(), "middleware.json")
	body := `{"rules":[{"path":"/api/*","request_headers":{"set":{"x-plan":"pro"}}}],"function":{"slug":"gate","paths":["/app/*"],"timeout_ms":250,"on_error":"closed"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := run(t, "middleware", "shop", "API", "--from", path); err != nil {
		t.Fatalf("middleware --from: %v\n%s", err, out)
	}

	calls := f.find("PATCH", "/projects/"+mwProject+"/services/"+mwService)
	if len(calls) != 1 {
		t.Fatalf("PATCH calls = %d", len(calls))
	}
	settings, _ := calls[0].Body["settings"].(map[string]any)
	rules, _ := settings["edge_middleware"].(map[string]any)
	if rules["function"] == nil {
		t.Fatalf("settings = %v", settings)
	}
}

func TestMiddlewareRefusesBadJSON(t *testing.T) {
	fakeRoutes(t, mwRoutes(`{}`))

	path := filepath.Join(t.TempDir(), "middleware.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := run(t, "middleware", "shop", "API", "--from", path)
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v", err)
	}
}

func TestMiddlewareOffClearsIt(t *testing.T) {
	f := fakeRoutes(t, mwRoutes(`{"edge_middleware":{"rules":[]}}`))

	if out, err := run(t, "middleware", "shop", "API", "--off"); err != nil {
		t.Fatalf("middleware --off: %v\n%s", err, out)
	}

	settings, _ := f.find("PATCH", "/projects/"+mwProject+"/services/"+mwService)[0].Body["settings"].(map[string]any)
	value, present := settings["edge_middleware"]
	if !present || value != nil {
		t.Fatalf("settings = %v", settings)
	}
}
