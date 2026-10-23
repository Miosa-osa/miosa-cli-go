package commands_test

import (
	"strings"
	"testing"
)

const dbID = "55555555-5555-4555-8555-555555555555"

func dbRoutes() map[string]any {
	return map[string]any{
		"GET /databases": `{"data":[{"id":"` + dbID + `","name":"app-db","engine":"postgres"}]}`,
	}
}

func TestDBPublicShowsTheState(t *testing.T) {
	routes := dbRoutes()
	routes["GET /databases/"+dbID+"/public-networking"] = `{"data":{"enabled":true,"supported":true,"host":"203.0.113.10","port":31432,"allowlist":["203.0.113.0/24"]}}`
	fakeRoutes(t, routes)

	out, err := run(t, "db", "public", "app-db")
	if err != nil {
		t.Fatalf("db public: %v\n%s", err, out)
	}
	for _, want := range []string{"203.0.113.10", "31432", "203.0.113.0/24", "sslmode=require"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}
}

func TestDBPublicOnSendsTheAllowlist(t *testing.T) {
	routes := dbRoutes()
	routes["POST /databases/"+dbID+"/public-networking"] = `{"data":{"enabled":true,"supported":true,"host":"203.0.113.10","port":31432,"allowlist":["203.0.113.0/24","198.51.100.7"]}}`
	f := fakeRoutes(t, routes)

	if out, err := run(t, "db", "public", "app-db", "on", "--allow", "203.0.113.0/24", "--allow", "198.51.100.7"); err != nil {
		t.Fatalf("db public on: %v\n%s", err, out)
	}

	calls := f.find("POST", "/databases/"+dbID+"/public-networking")
	if len(calls) != 1 {
		t.Fatalf("POST calls = %d", len(calls))
	}
	allow, _ := calls[0].Body["allowlist"].([]any)
	if len(allow) != 2 || allow[1] != "198.51.100.7" {
		t.Fatalf("allowlist = %v", calls[0].Body["allowlist"])
	}
}

func TestDBPublicOffAndRefusedEngines(t *testing.T) {
	routes := dbRoutes()
	routes["DELETE /databases/"+dbID+"/public-networking"] = `{"data":{"enabled":false,"supported":true}}`
	routes["POST /databases/"+dbID+"/public-networking"] = resp{422, `{"error":{"code":"ENGINE_NOT_SUPPORTED","message":"public networking supports postgresql, mysql, redis and mongodb"}}`}
	f := fakeRoutes(t, routes)

	out, err := run(t, "db", "public", "app-db", "off")
	if err != nil {
		t.Fatalf("db public off: %v\n%s", err, out)
	}
	if !strings.Contains(out, "is off") {
		t.Fatalf("out = %s", out)
	}
	if len(f.find("DELETE", "/databases/"+dbID+"/public-networking")) != 1 {
		t.Fatal("DELETE was not sent")
	}

	// The error is surfaced, not swallowed.
	if _, err := run(t, "db", "public", "app-db", "on"); err == nil {
		t.Fatal("a refused engine must be an error")
	}
}
