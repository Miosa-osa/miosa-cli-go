package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const dashboardLinkJSON = `{"data":{"url":"https://miosa.ai/auth/link#code=abc","expires_at":"2026-10-23T20:00:00Z","expires_in":900}}`

func TestDashboardOpensTheOneTimeLink(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /cli/session/dashboard-link": dashboardLinkJSON})
	var opened string
	defer commands.SetOpenBrowserForTest(func(u string) error { opened = u; return nil })()

	out, err := run(t, "dashboard")
	if err != nil {
		t.Fatalf("dashboard: %v\n%s", err, out)
	}
	if len(f.find("POST", "/cli/session/dashboard-link")) != 1 {
		t.Fatal("no link was asked for")
	}
	if opened != "https://miosa.ai/auth/link#code=abc" {
		t.Fatalf("opened %q", opened)
	}
	// The link signs someone in: it is opened, never echoed.
	if strings.Contains(out, "code=abc") {
		t.Fatalf("the link was printed:\n%s", out)
	}
}

func TestDashboardPrintWarnsTheLinkIsACredential(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /cli/session/dashboard-link": dashboardLinkJSON})
	defer commands.SetOpenBrowserForTest(func(string) error { t.Fatal("opened a browser with --print"); return nil })()

	out, err := run(t, "dashboard", "--print")
	if err != nil {
		t.Fatalf("dashboard --print: %v\n%s", err, out)
	}
	if !strings.Contains(out, "code=abc") || !strings.Contains(out, "Anyone with this link") {
		t.Fatalf("out = %s", out)
	}
}

func TestDashboardWithANonLoginKeyOpensTheSignInPage(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /cli/session/dashboard-link": resp{409, `{"error":{"code":"NOT_A_CLI_LOGIN_KEY","message":"no"}}`}})
	var opened string
	defer commands.SetOpenBrowserForTest(func(u string) error { opened = u; return nil })()

	out, err := run(t, "dashboard")
	if err != nil {
		t.Fatalf("dashboard: %v\n%s", err, out)
	}
	if !strings.HasSuffix(opened, "/login") || !strings.Contains(out, "miosa login") {
		t.Fatalf("opened %q\n%s", opened, out)
	}
}

func TestDashboardJSONSaysWhetherItSignsIn(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /cli/session/dashboard-link": dashboardLinkJSON})
	defer commands.SetOpenBrowserForTest(func(string) error { t.Fatal("opened a browser with --json"); return nil })()

	out, err := run(t, "dashboard", "--json")
	if err != nil {
		t.Fatalf("dashboard --json: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"signed_in": true`) || !strings.Contains(out, "code=abc") {
		t.Fatalf("out = %s", out)
	}
}

func TestDashboardSurfacesAServerFailure(t *testing.T) {
	fakeRoutes(t, map[string]any{"POST /cli/session/dashboard-link": resp{500, `{"error":{"code":"LINK_FAILED","message":"The link could not be created."}}`}})
	defer commands.SetOpenBrowserForTest(func(string) error { t.Fatal("opened a browser on failure"); return nil })()

	if out, err := run(t, "dashboard"); err == nil {
		t.Fatalf("expected an error\n%s", out)
	}
}
