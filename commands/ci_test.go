package commands_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const ciIdle = `{"data":{"available":true,"label":"miosa","installations":[],"steps":{"app_installed":false,"repositories_granted":false,"label_used":false,"job_passed":false},"last_job":null,"isolation":{"inherits_environment":false,"public_repositories":false}}}`

const ciConnected = `{"data":{"available":true,"label":"miosa","installations":[{"installation_id":123,"account_login":"acme","account_type":"Organization","repository_selection":"selected","status":"active","connected_at":"2026-10-29T10:00:00Z"}],"steps":{"app_installed":true,"repositories_granted":true,"label_used":false,"job_passed":false},"last_job":null}}`

const ciUnavailable = `{"error":{"code":"CI_NOT_AVAILABLE","message":"CI is not configured"}}`

func TestCIConnectPollsUntilAnInstallationAppears(t *testing.T) {
	defer commands.SetCIWaitForTest(5*time.Millisecond, 5*time.Second)()
	var opened string
	defer commands.SetOpenBrowserForTest(func(u string) error { opened = u; return nil })()
	polls := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/ci/connect-intents"):
			return 201, `{"data":{"install_url":"https://github.com/apps/miosa-ci/installations/new?state=abc","expires_at":"2026-10-29T10:30:00Z"}}`
		case c.is("GET", "/ci"):
			polls++
			if polls < 3 {
				return 200, ciIdle
			}
			return 200, ciConnected
		}
		return 404, `{}`
	})
	out, err := run(t, "ci", "connect")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if opened != "https://github.com/apps/miosa-ci/installations/new?state=abc" {
		t.Errorf("opened %q", opened)
	}
	for _, want := range []string{"installations/new?state=abc", "Connected GitHub account acme", "runs-on: miosa"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if n := len(f.find("GET", "/ci")); n != 3 {
		t.Errorf("polls = %d, want 3", n)
	}
}

func TestCIConnectNoWaitPrintsTheLinkAndStops(t *testing.T) {
	defer commands.SetOpenBrowserForTest(func(string) error { t.Error("opened a browser with --no-wait"); return nil })()
	f := newFakeAPI(t, func(_ call) (int, string) {
		return 201, `{"data":{"install_url":"https://github.com/apps/miosa-ci/installations/new?state=abc","expires_at":"x"}}`
	})
	out, err := run(t, "ci", "connect", "--no-wait", "--json")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if !strings.Contains(out, `"install_url": "https://github.com/apps/miosa-ci/installations/new?state=abc"`) || !strings.Contains(out, `"connected": false`) {
		t.Errorf("output = %s", out)
	}
	if n := len(f.find("GET", "/ci")); n != 0 {
		t.Errorf("polled %d times with --no-wait", n)
	}
}

func TestCIConnectNotAvailable(t *testing.T) {
	newFakeAPI(t, func(_ call) (int, string) { return 503, ciUnavailable })
	out, err := run(t, "ci", "connect")
	if err == nil || !strings.Contains(commands.ErrorText(err, false), "not available on this platform yet") {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

func TestCIStatusShowsStepsAccountsAndIsolation(t *testing.T) {
	newFakeAPI(t, func(_ call) (int, string) { return 200, ciConnected })
	out, err := run(t, "ci", "status")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	for _, want := range []string{"GitHub App installed", "yes", "no", "acme", "123", "GitHub Actions secrets"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestCIStatusNotAvailable(t *testing.T) {
	newFakeAPI(t, func(_ call) (int, string) {
		return 200, `{"data":{"available":false,"installations":[],"steps":{}}}`
	})
	out, err := run(t, "ci", "status")
	if err != nil || !strings.Contains(out, "not available on this platform yet") {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

func TestCIStatusJSON(t *testing.T) {
	newFakeAPI(t, func(_ call) (int, string) { return 200, ciConnected })
	out, err := run(t, "ci", "status", "--json")
	if err != nil || !strings.Contains(out, `"account_login": "acme"`) || !strings.Contains(out, `"data"`) {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

const ciJobs = `{"data":[{"id":"j1","repository":"acme/app","workflow_name":"CI","job_name":"test","status":"completed","conclusion":"success","started_at":"2026-10-29T10:00:00Z","duration_seconds":95},{"id":"j2","repository":"acme/app","workflow_name":"CI","job_name":"lint","status":"queued","conclusion":null,"started_at":null,"duration_seconds":null}]}`

func TestCIJobsTableAndLimit(t *testing.T) {
	f := newFakeAPI(t, func(_ call) (int, string) { return 200, ciJobs })
	out, err := run(t, "ci", "jobs", "--limit", "5")
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out)
	}
	for _, want := range []string{"acme/app", "CI / test", "success", "1m35s", "queued"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := f.find("GET", "/ci/jobs"); len(got) != 1 || got[0].Query != "limit=5" {
		t.Errorf("calls = %+v", got)
	}
}

func TestCIJobsJSONAndNotAvailable(t *testing.T) {
	newFakeAPI(t, func(_ call) (int, string) { return 200, ciJobs })
	out, err := run(t, "ci", "jobs", "--json")
	if err != nil || !strings.Contains(out, `"repository": "acme/app"`) {
		t.Fatalf("err = %v\n%s", err, out)
	}
	newFakeAPI(t, func(_ call) (int, string) { return 503, ciUnavailable })
	_, err = run(t, "ci", "jobs")
	if err == nil || !strings.Contains(commands.ErrorText(err, false), "not available on this platform yet") {
		t.Fatalf("err = %v", err)
	}
}

func TestCIDisconnect(t *testing.T) {
	f := newFakeAPI(t, func(_ call) (int, string) { return 200, `{"data":{}}` })
	if _, err := run(t, "ci", "disconnect", "123"); err == nil {
		t.Fatal("disconnect without --yes must not proceed off a terminal")
	}
	if n := len(f.find("DELETE", "/ci/installations/123")); n != 0 {
		t.Fatalf("deleted without confirmation")
	}
	out, err := run(t, "ci", "disconnect", "123", "--yes", "--json")
	if err != nil || !strings.Contains(out, `"status": "disconnected"`) {
		t.Fatalf("err = %v\n%s", err, out)
	}
	if n := len(f.find("DELETE", "/ci/installations/123")); n != 1 {
		t.Fatalf("deletes = %d", n)
	}
}

func TestCIDisconnectUnknownInstallation(t *testing.T) {
	newFakeAPI(t, func(_ call) (int, string) {
		return 404, `{"error":{"code":"CI_INSTALLATION_NOT_FOUND","message":"nope"}}`
	})
	_, err := run(t, "ci", "disconnect", "9", "--yes")
	if err == nil || !strings.Contains(commands.ErrorText(err, false), "no such installation") {
		t.Fatalf("err = %v", err)
	}
}
