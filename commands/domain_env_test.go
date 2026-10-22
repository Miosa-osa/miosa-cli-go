package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func domainRoutes() map[string]any {
	routes := linkedRoutes()
	routes["GET /projects/"+linkProjectID+"/services/"+linkServiceID+"/domains"] = `{"data":[
		{"id":"d1","service_id":"` + linkServiceID + `","fqdn":"app.example.com","status":"pending_dns","serving":false,
		 "dns_records":[{"type":"CNAME","name":"app.example.com","value":"proxy.miosa.ai","purpose":"routing"}],"created_at":"2026-10-22T10:00:00Z"}],
		"preview":{"label":"api","host":"api.acme.miosa.app","url":"https://api.acme.miosa.app"}}`
	routes["POST /projects/"+linkProjectID+"/services/"+linkServiceID+"/domains"] = `{"data":{"id":"d2","service_id":"` + linkServiceID + `","fqdn":"new.example.com","status":"pending_dns","serving":false,
		"dns_records":[{"type":"CNAME","name":"new.example.com","value":"proxy.miosa.ai","purpose":"routing"}],"created_at":"2026-10-22T10:00:00Z"}}`
	routes["POST /projects/"+linkProjectID+"/services/"+linkServiceID+"/domains/d1/verify"] = `{"data":{"id":"d1","service_id":"` + linkServiceID + `","fqdn":"app.example.com","status":"active","serving":true,"verified_at":"2026-10-22T10:05:00Z"}}`
	routes["DELETE /projects/"+linkProjectID+"/services/"+linkServiceID+"/domains/d1"] = `{"data":{}}`
	return routes
}

func TestDomainListShowsRecordsAndTheGeneratedAddress(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, domainRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "domain", "list")
	if err != nil {
		t.Fatalf("domain list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "app.example.com") || !strings.Contains(out, "pending_dns") {
		t.Fatalf("out = %s", out)
	}
	if !strings.Contains(out, "https://api.acme.miosa.app") {
		t.Fatalf("the generated address is missing: %s", out)
	}
}

func TestDomainAddPrintsTheDNSRecords(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, domainRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "domain", "add", "new.example.com", "--no-wait")
	if err != nil {
		t.Fatalf("domain add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CNAME") || !strings.Contains(out, "proxy.miosa.ai") {
		t.Fatalf("records missing: %s", out)
	}
}

func TestDomainAddRefusesAMiosaAINameLocally(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, domainRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "domain", "add", "evil.miosa.ai")
	if err == nil || !strings.Contains(err.Error(), "cannot end in miosa.ai") {
		t.Fatalf("want a local refusal, got %v", err)
	}
	if got := f.find("POST", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/domains"); len(got) != 0 {
		t.Fatal("nothing may be sent for an invalid name")
	}
}

func TestDomainVerifyReportsActive(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, domainRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "domain", "verify", "app.example.com", "--no-wait")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	if !strings.Contains(out, "active") {
		t.Fatalf("out = %s", out)
	}
}

func TestDomainRemoveNeedsYes(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, domainRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "domain", "remove", "app.example.com", "--yes"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := f.find("DELETE", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/domains/d1"); len(got) != 1 {
		t.Fatalf("want one delete, got %d", len(got))
	}
}

func TestDomainSaysWhenTheRouteIsNotLive(t *testing.T) {
	t.Chdir(t.TempDir())
	routes := linkedRoutes()
	routes["GET /projects/"+linkProjectID+"/services/"+linkServiceID+"/domains"] = resp{404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`}
	f := fakeRoutes(t, routes)
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "domain", "list")
	if err == nil || !strings.Contains(err.Error(), "not live") {
		t.Fatalf("want a not-live message, got %v", err)
	}
}

func TestEnvironmentListThroughTheLink(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "environment", "list")
	if err != nil {
		t.Fatalf("environment list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "production") {
		t.Fatalf("out = %s", out)
	}
}

func TestEnvironmentNewAndFork(t *testing.T) {
	t.Chdir(t.TempDir())
	routes := linkedRoutes()
	routes["POST /projects/"+linkProjectID+"/environments"] = `{"data":{"id":"e2","name":"staging","slug":"staging"}}`
	routes["POST /projects/"+linkProjectID+"/environments/e1/duplicate"] = `{"data":{"id":"e3","name":"preview","slug":"preview"}}`
	f := fakeRoutes(t, routes)
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "environment", "new", "staging"); err != nil {
		t.Fatalf("new: %v\n%s", err, out)
	}
	if out, err := run(t, "environment", "fork", "production", "--name", "preview"); err != nil {
		t.Fatalf("fork: %v\n%s", err, out)
	}
}

func TestServiceSelectRewritesTheLink(t *testing.T) {
	t.Chdir(t.TempDir())
	f := fakeRoutes(t, linkedRoutes())
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq"); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, "service", "select", "API"); err != nil {
		t.Fatalf("select: %v\n%s", err, out)
	}
	body, err := os.ReadFile(filepath.Join(".miosa", "link.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), linkServiceID) {
		t.Fatalf("link was not rewritten:\n%s", body)
	}
}
