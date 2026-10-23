package commands_test

import (
	"strings"
	"testing"
)

const drainsProject = "11111111-1111-4111-8111-111111111111"
const drainsEnv = "22222222-2222-4222-8222-222222222222"

func drainsRoutes(extra map[string]any) map[string]any {
	routes := map[string]any{
		"GET /projects": `{"data":[{"id":"` + drainsProject + `","name":"shop","slug":"shop"}]}`,
		"GET /projects/" + drainsProject + "/environments": `{"data":[
			{"id":"` + drainsEnv + `","name":"production","slug":"production","is_default":true}]}`,
	}
	for key, value := range extra {
		routes[key] = value
	}
	return routes
}

func TestDrainsListShowsWhatEachDrainIsDoing(t *testing.T) {
	fakeRoutes(t, drainsRoutes(map[string]any{
		"GET /projects/" + drainsProject + "/environments/" + drainsEnv + "/log-drains": `{"data":[
			{"id":"d1","name":"ops","kind":"http","url":"https://logs.example.test/ingest","sources":["runtime","build"],
			 "state":"active","delivered_lines":1200,"dropped_lines":0,"failed_batches":1,"last_error":"the endpoint answered HTTP 500","last_delivered_at":"2026-10-22T10:00:00Z"}]}`,
	}))

	out, err := run(t, "drains", "list", "--project", "shop")
	if err != nil {
		t.Fatalf("drains list: %v\n%s", err, out)
	}
	for _, want := range []string{"ops", "runtime,build", "active", "1200", "HTTP 500"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestDrainsAddSendsTheSourcesAndKind(t *testing.T) {
	f := fakeRoutes(t, drainsRoutes(map[string]any{
		"POST /projects/" + drainsProject + "/environments/" + drainsEnv + "/log-drains": `{"data":{"id":"d2","name":"archive","kind":"syslog","url":"syslog+tls://logs.example.org:6514","sources":["runtime"],"state":"active"}}`,
	}))

	if out, err := run(t, "drains", "add", "--project", "shop", "--name", "archive", "--kind", "syslog", "--url", "syslog+tls://logs.example.org:6514", "--source", "runtime"); err != nil {
		t.Fatalf("drains add: %v\n%s", err, out)
	}

	calls := f.find("POST", "/projects/"+drainsProject+"/environments/"+drainsEnv+"/log-drains")
	if len(calls) != 1 {
		t.Fatalf("POST calls = %d", len(calls))
	}
	body := calls[0].Body
	if body["kind"] != "syslog" || body["name"] != "archive" {
		t.Fatalf("body = %v", body)
	}
	sources, _ := body["sources"].([]any)
	if len(sources) != 1 || sources[0] != "runtime" {
		t.Fatalf("sources = %v", body["sources"])
	}
}

func TestDrainsPauseLooksTheNameUpAndSendsOnlyTheState(t *testing.T) {
	base := "GET /projects/" + drainsProject + "/environments/" + drainsEnv + "/log-drains"
	f := fakeRoutes(t, drainsRoutes(map[string]any{
		base: `{"data":[{"id":"d1","name":"ops","kind":"http","url":"https://x","sources":["runtime"],"state":"active"}]}`,
		"PATCH /projects/" + drainsProject + "/environments/" + drainsEnv + "/log-drains/d1": `{"data":{"id":"d1","name":"ops","kind":"http","url":"https://x","sources":["runtime"],"state":"paused"}}`,
	}))

	if out, err := run(t, "drains", "pause", "ops", "--project", "shop"); err != nil {
		t.Fatalf("drains pause: %v\n%s", err, out)
	}

	calls := f.find("PATCH", "/projects/"+drainsProject+"/environments/"+drainsEnv+"/log-drains/d1")
	if len(calls) != 1 {
		t.Fatalf("PATCH calls = %d", len(calls))
	}
	if calls[0].Body["state"] != "paused" {
		t.Fatalf("body = %v", calls[0].Body)
	}
}

func TestDrainsTestReportsWhatTheEndpointAnswered(t *testing.T) {
	base := "GET /projects/" + drainsProject + "/environments/" + drainsEnv + "/log-drains"
	fakeRoutes(t, drainsRoutes(map[string]any{
		base: `{"data":[{"id":"d1","name":"ops","kind":"http","url":"https://x","sources":["runtime"],"state":"active"}]}`,
		"POST /projects/" + drainsProject + "/environments/" + drainsEnv + "/log-drains/d1/test": `{"data":{"delivered":false,"error":"the endpoint answered HTTP 500"}}`,
	}))

	out, err := run(t, "drains", "test", "ops", "--project", "shop")
	if err != nil {
		t.Fatalf("drains test: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Not delivered: the endpoint answered HTTP 500") {
		t.Fatalf("out = %s", out)
	}
}

func TestDrainsUnknownNameSaysWhereToLook(t *testing.T) {
	fakeRoutes(t, drainsRoutes(map[string]any{
		"GET /projects/" + drainsProject + "/environments/" + drainsEnv + "/log-drains": `{"data":[]}`,
	}))

	_, err := run(t, "drains", "rm", "nope", "--project", "shop")
	if err == nil || !strings.Contains(err.Error(), "miosa drains list") {
		t.Fatalf("err = %v", err)
	}
}
