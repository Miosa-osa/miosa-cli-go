package commands_test

import (
	"strings"
	"testing"
)

func TestOrgCreateSendsNameAndCompany(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /tenants": resp{201, `{"id":"t1","name":"Acme Labs","slug":"acme-labs"}`},
	})
	out, err := run(t, "org", "create", "Acme Labs")
	if err != nil || !strings.Contains(out, "acme-labs") {
		t.Fatalf("%v\n%s", err, out)
	}
	body := f.find("POST", "/tenants")[0].Body
	if body["name"] != "Acme Labs" || body["slug"] != "Acme Labs" || body["organization_type"] != "company" {
		t.Fatalf("body = %v", body)
	}
	if _, leaked := body["personal"]; leaked {
		t.Fatalf("the flag must not reach the API: %v", body)
	}
}

func TestOrgCreatePersonal(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /tenants": resp{201, `{"id":"t1","name":"Me","slug":"me"}`},
	})
	if out, err := run(t, "org", "create", "Me", "--personal"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if body := f.find("POST", "/tenants")[0].Body; body["organization_type"] != "personal" {
		t.Fatalf("body = %v", body)
	}
}

func TestOrgCreateRefusalIsAnError(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"POST /tenants": resp{409, `{"error":"COMPANY_ORGANIZATION_EXISTS"}`},
	})
	if out, err := run(t, "org", "create", "Second"); err == nil {
		t.Fatalf("a refused create must fail\n%s", out)
	}
}

func TestOrgDeleteNeverCallsTheAPI(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"DELETE /tenants/acme": `{}`})
	out, err := run(t, "org", "delete", "acme")
	if err == nil || len(f.find("DELETE", "/tenants/acme")) != 0 {
		t.Fatalf("org delete must refuse without a request: %v\n%s", err, out)
	}
}

func TestOrgCreateCapShowsTheServerMessage(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"POST /tenants": resp{429, `{"error":"ORG_CREATE_LIMIT","message":"You can create up to 3 organizations in 24 hours."}`},
	})
	out, err := run(t, "org", "create", "Fourth")
	if err == nil || !strings.Contains(err.Error()+out, "up to 3 organizations") {
		t.Fatalf("the cap must fail with the server's sentence: %v\n%s", err, out)
	}
}
