package commands_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// Multi-org CLI access: one `miosa login` covers several organizations,
// `org use` picks one, list commands fan out with --all-orgs.

const multiOrgToken = `{"api_key":"msk_u_multi","key":{"id":"k1","org_scope":"listed"},
  "tenant":{"id":"t-b","slug":"beta","name":"Beta"},
  "orgs":[{"id":"t-b","slug":"beta","name":"Beta","role":"admin"},{"id":"t-a","slug":"acme","name":"Acme Inc","role":"owner"}]}`

func deviceLoginAPI(t *testing.T, token string) *fakeAPI {
	t.Helper()
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Method + " " + c.Path {
		case "POST /auth/cli/start":
			return 200, `{"device_code":"d","user_code":"U","verification_uri":"https://miosa.ai/cli/auth","expires_in":600,"interval":1}`
		case "POST /auth/cli/token":
			return 200, token
		case "GET /platform/tenants/current":
			return 200, `{"id":"t-b","name":"Beta","slug":"beta"}`
		}
		return 404, `{}`
	})
	t.Cleanup(commands.SetSleepForTest(func(context.Context, time.Duration) error { return nil }))
	t.Cleanup(commands.SetOpenBrowserForTest(func(string) error { return nil }))
	return f
}

func TestLoginAllOrgsAsksForEveryOrgAndStoresTheGrant(t *testing.T) {
	f := deviceLoginAPI(t, multiOrgToken)
	// A previous profile's org must not ride along on the sign-in.
	t.Setenv("MIOSA_ORG", "old-org")

	out, err := runIn(t, "", "login", "--device", "--all-orgs")
	if err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	start := f.find("POST", "/auth/cli/start")[0]
	if start.Body["all_orgs"] != true || start.Body["os"] == nil || start.Body["cli_version"] == nil {
		t.Fatalf("start body = %+v", start.Body)
	}
	if _, ok := start.Body["orgs"]; ok {
		t.Fatalf("--all-orgs must not also send orgs: %+v", start.Body)
	}
	for _, c := range append(start.Headers.Values("X-Miosa-Org"), f.find("POST", "/auth/cli/token")[0].Headers.Values("X-Miosa-Org")...) {
		t.Fatalf("X-Miosa-Org sent while signing in: %q", c)
	}
	if !strings.Contains(out, "Signed in to 2 organizations, starting in beta") {
		t.Fatalf("output = %s", out)
	}
	cfg := readConfig(t)
	for _, want := range []string{`active_org = "beta"`, `orgs = ["beta", "acme"]`, `org_scope = "listed"`} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %s:\n%s", want, cfg)
		}
	}
}

func TestLoginOrgFlagsPreSelectThoseOrgs(t *testing.T) {
	f := deviceLoginAPI(t, `{"api_key":"msk_u_k","key":{"org_scope":"single"},"tenant":{"slug":"beta"},"orgs":[{"id":"t-b","slug":"beta"}]}`)
	if out, err := runIn(t, "", "login", "--device", "--org", "acme", "--org", "beta"); err != nil {
		t.Fatalf("login: %v\n%s", err, out)
	}
	start := f.find("POST", "/auth/cli/start")[0]
	orgs, _ := json.Marshal(start.Body["orgs"])
	if string(orgs) != `["acme","beta"]` || start.Body["all_orgs"] != nil || start.Body["tenant"] != nil {
		t.Fatalf("start body = %+v", start.Body)
	}
}

const billToViewingBeta = `{"data":{"viewing_id":"t-b","source":"credential","options":[
  {"id":"t-a","name":"Acme Inc","slug":"acme","role":"owner","viewing":false},
  {"id":"t-b","name":"Beta","slug":"beta","role":"admin","viewing":true}]}}`

func TestOrgUseSavesTheOrgAndLaterCommandsSendIt(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Path {
		case "/bill-to":
			return 200, billToViewingBeta
		case "/platform/tenants/current":
			return 200, `{"id":"t-b","name":"Beta","slug":"beta"}`
		}
		return 200, `{"data":[]}`
	})
	writeConfigFile(t, "api_key = \"msk_u_test\"\norgs = [\"acme\", \"beta\"]\norg_scope = \"listed\"\n")

	if out, err := run(t, "org", "use", "beta"); err != nil || !strings.Contains(out, "Now working in beta") {
		t.Fatalf("org use: %v\n%s", err, out)
	}
	if got := f.find("GET", "/bill-to")[0].Headers.Get("X-Miosa-Org"); got != "beta" {
		t.Fatalf("org use checked with X-Miosa-Org %q", got)
	}
	if !strings.Contains(readConfig(t), `active_org = "beta"`) {
		t.Fatalf("config = %s", readConfig(t))
	}

	if _, err := run(t, "api", "/sandboxes"); err != nil {
		t.Fatal(err)
	}
	if got := f.find("GET", "/sandboxes")[0].Headers.Get("X-Miosa-Org"); got != "beta" {
		t.Fatalf("a later command sent X-Miosa-Org %q, want the active org", got)
	}

	out, err := run(t, "whoami")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"beta (miosa org use)", "acme, beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami missing %q:\n%s", want, out)
		}
	}
}

func TestOrgUseRefusesAnOrgTheKeyDoesNotActIn(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /bill-to": billToViewingBeta})
	writeConfigFile(t, "api_key = \"msk_u_test\"\n")
	_, err := run(t, "org", "use", "acme")
	if err == nil || !strings.Contains(err.Error(), "acme") || commands.ExitCode(err) != 3 {
		t.Fatalf("org use for an org the key does not act in: err = %v, want exit 3 naming acme", err)
	}
	if strings.Contains(readConfig(t), "active_org") {
		t.Fatalf("a refused org was saved:\n%s", readConfig(t))
	}
}

func TestAllOrgsListsEveryCoveredOrgWithAnOrgColumn(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch c.Headers.Get("X-Miosa-Org") {
		case "acme":
			return 200, `{"data":[{"id":"d-1","name":"site-a","status":"live"}]}`
		case "beta":
			return 200, `{"data":[{"id":"d-2","name":"site-b","status":"live"}]}`
		}
		return 500, `{"error":{"code":"no_org","message":"no org header"}}`
	})
	writeConfigFile(t, "api_key = \"msk_u_test\"\norgs = [\"acme\", \"beta\"]\n")

	out, err := run(t, "deploy", "list", "--all-orgs")
	if err != nil {
		t.Fatalf("deploy list --all-orgs: %v\n%s", err, out)
	}
	if n := len(f.find("GET", "/deployments")); n != 2 {
		t.Fatalf("requests = %d, want one per org", n)
	}
	for _, want := range []string{"ORG", "acme", "site-a", "beta", "site-b"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	jout, err := run(t, "deploy", "list", "--all-orgs", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(dataJSON(jout), &rows); err != nil || len(rows) != 2 || rows[0]["org"] != "acme" {
		t.Fatalf("json rows = %+v (%v)\n%s", rows, err, jout)
	}
}

func TestAChangeSaysWhereItLandsOnceAndNotInJSON(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.Path == "/bill-to" {
			return 200, billToViewingBeta
		}
		return 200, `{"data":{}}`
	})
	writeConfigFile(t, "api_key = \"msk_u_test\"\nactive_org = \"beta\"\ndefault_workspace = \"ops\"\n")

	_, stderr, err := runSplit(t, "api", "-X", "POST", "/webhooks", "-d", `{"url":"https://x/h"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(stderr, "org: beta, workspace: ops") != 1 {
		t.Fatalf("stderr = %q", stderr)
	}

	_, stderr, err = runSplit(t, "api", "-X", "POST", "/webhooks", "-d", `{"url":"https://x/h"}`, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "org: beta") {
		t.Fatalf("--json must keep stderr clean: %q", stderr)
	}
}

func TestLoginAllOrgsOnAnOldServerSaysOnlyOneOrg(t *testing.T) {
	deviceLoginAPI(t, `{"api_key":"msk_u_old","tenant":{"id":"t-b","slug":"beta","name":"Beta"}}`)
	_, stderr, err := runSplitIn(t, "", "login", "--device", "--all-orgs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "does not support signing in to several organizations yet") {
		t.Fatalf("stderr = %q", stderr)
	}
}
