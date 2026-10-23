package commands_test

import (
	"strings"
	"testing"
)

const (
	protProject = "11111111-1111-4111-8111-111111111111"
	protService = "33333333-3333-4333-8333-333333333333"
)

func protectionRoutes(extra map[string]any) map[string]any {
	routes := map[string]any{
		"GET /projects": `{"data":[{"id":"` + protProject + `","name":"shop","slug":"shop"}]}`,
		"GET /projects/" + protProject + "/services": `{"data":[
			{"id":"` + protService + `","name":"API","slug":"api","kind":"server","lock_version":3,"settings":{}}]}`,
	}
	for key, value := range extra {
		routes[key] = value
	}
	base := "GET /projects/" + protProject + "/services/" + protService
	routes[base+"/protection"] = `{"data":{"mode":"password","scope":"service","password_set":true,"cookie_ttl_s":86400}}`
	routes[base+"/waf"] = `{"data":{"mode":"enforce","ip_allow":["198.51.100.0/24"],"ip_deny":["203.0.113.0/24"],"country_allow":[],"country_deny":[],"rate_limit_per_minute":120,"attack_mode":false,"country_rules_active":false}}`
	return routes
}

func TestProtectionShowsAndSetsAPassword(t *testing.T) {
	f := fakeRoutes(t, protectionRoutes(map[string]any{
		"PUT /projects/" + protProject + "/services/" + protService + "/protection": `{"data":{"protection":true}}`,
	}))

	out, err := run(t, "protection", "shop", "API")
	if err != nil {
		t.Fatalf("protection: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Password set") || !strings.Contains(out, "yes") {
		t.Fatalf("out = %s", out)
	}

	if out, err := run(t, "protection", "shop", "API", "--password", "open-sesame"); err != nil {
		t.Fatalf("protection --password: %v\n%s", err, out)
	}
	calls := f.find("PUT", "/projects/"+protProject+"/services/"+protService+"/protection")
	if len(calls) != 1 || calls[0].Body["password"] != "open-sesame" || calls[0].Body["mode"] != "password" {
		t.Fatalf("PUT body = %v", calls)
	}
	if strings.Contains(out, "open-sesame") {
		t.Fatal("the password must never be printed back")
	}
}

func TestProtectionShareLinkPrintsTheTokenOnce(t *testing.T) {
	fakeRoutes(t, protectionRoutes(map[string]any{
		"POST /projects/" + protProject + "/services/" + protService + "/share-links": `{"data":{"id":"l1","token":"msl_abc","expires_at":"2026-10-23T00:00:00Z"}}`,
	}))

	out, err := run(t, "protection", "shop", "API", "--share-link", "--expires", "3600")
	if err != nil {
		t.Fatalf("protection --share-link: %v\n%s", err, out)
	}
	if !strings.Contains(out, "msl_abc") || !strings.Contains(out, "Shown once") {
		t.Fatalf("out = %s", out)
	}
}

func TestWAFShowsAndSendsTheRules(t *testing.T) {
	f := fakeRoutes(t, protectionRoutes(map[string]any{
		"PUT /projects/" + protProject + "/services/" + protService + "/waf": `{"data":{"mode":"enforce"}}`,
	}))

	out, err := run(t, "waf", "shop", "API")
	if err != nil {
		t.Fatalf("waf: %v\n%s", err, out)
	}
	for _, want := range []string{"enforce", "203.0.113.0/24", "120", "x-miosa-country"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not contain %q:\n%s", want, out)
		}
	}

	if out, err := run(t, "waf", "shop", "API", "--mode", "observe", "--deny", "203.0.113.0/24", "--rate", "60"); err != nil {
		t.Fatalf("waf put: %v\n%s", err, out)
	}
	body := f.find("PUT", "/projects/"+protProject+"/services/"+protService+"/waf")[0].Body
	if body["mode"] != "observe" || body["rate_limit_per_minute"] != float64(60) {
	}

	if _, ok := body["ip_deny"]; !ok {
		t.Fatalf("body = %v", body)
	}
}

func TestWAFClearAndAttackMode(t *testing.T) {
	f := fakeRoutes(t, protectionRoutes(map[string]any{
		"DELETE /projects/" + protProject + "/services/" + protService + "/waf": `{"data":{"deleted":true}}`,
		"PUT /projects/" + protProject + "/services/" + protService + "/waf":    `{"data":{"mode":"off","attack_mode":true}}`,
	}))

	if out, err := run(t, "waf", "shop", "API", "--off"); err != nil {
		t.Fatalf("waf off: %v\n%s", err, out)
	}
	if len(f.find("DELETE", "/projects/"+protProject+"/services/"+protService+"/waf")) != 1 {
		t.Fatal("DELETE was not sent")
	}

	if out, err := run(t, "waf", "shop", "API", "--attack-mode", "on"); err != nil {
		t.Fatalf("waf attack-mode: %v\n%s", err, out)
	}
	calls := f.find("PUT", "/projects/"+protProject+"/services/"+protService+"/waf")
	if len(calls) != 1 || calls[0].Body["attack_mode"] != true {
		t.Fatalf("attack-mode body = %v", calls)
	}
}
