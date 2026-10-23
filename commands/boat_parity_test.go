package commands_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func sandboxByNameAPI(t *testing.T, more func(c call) (int, string, bool)) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/sandboxes/by-name/my-box") {
			return 200, `{"id":"` + boxID + `"}`
		}
		if status, body, ok := more(c); ok {
			return status, body
		}
		return 404, `{"error":{"code":"NOT_FOUND"}}`
	})
}

func TestRenameSendsTheNewName(t *testing.T) {
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if c.is("PATCH", boxPath) {
			return 200, `{"id":"` + boxID + `","name":"` + c.Body["name"].(string) + `","state":"running"}`, true
		}
		return 0, "", false
	})
	out, err := run(t, "rename", "my-box", "api-box")
	if err != nil || !strings.Contains(out, "api-box") {
		t.Fatalf("%v %s", err, out)
	}
	if f.find("PATCH", boxPath)[0].Body["name"] != "api-box" {
		t.Fatal("the new name was not sent")
	}
}

func TestExtendHoursAndNoAutoStop(t *testing.T) {
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("POST", boxPath+"/extend"):
			return 200, `{"id":"` + boxID + `","state":"running","timeout_sec":21600}`, true
		case c.is("PATCH", boxPath):
			return 200, `{"id":"` + boxID + `","always_on":true}`, true
		}
		return 0, "", false
	})
	if _, err := run(t, "extend", "my-box", "--hours", "6"); err != nil {
		t.Fatal(err)
	}
	if b := f.find("POST", boxPath+"/extend")[0].Body; b["timeout_sec"] != float64(21600) || b["hours"] != nil {
		t.Fatalf("body = %v", b)
	}
	if _, err := run(t, "extend", "my-box", "--no-auto-stop"); err != nil {
		t.Fatal(err)
	}
	if p := f.find("PATCH", boxPath); len(p) != 1 || p[0].Body["always_on"] != true {
		t.Fatalf("patch = %+v", p)
	}
	if b := f.find("POST", boxPath+"/extend")[1].Body; b["always_on"] != nil {
		t.Fatalf("always_on is not a field of the extend route: %v", b)
	}
	for _, args := range [][]string{{"extend", "my-box"}, {"extend", "my-box", "--for", "2h", "--hours", "2"}} {
		if _, err := run(t, args...); commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("%v: %v", args, err)
		}
	}
}

func TestForkWaitsAndNames(t *testing.T) {
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	const forkID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	polls := 0
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("POST", boxPath+"/fork"):
			return 201, `{"id":"` + forkID + `","state":"provisioning","name":"` + c.Body["name"].(string) + `"}`, true
		case c.is("GET", "/sandboxes/"+forkID):
			polls++
			if polls < 2 {
				return 200, `{"state":"provisioning"}`, true
			}
			return 200, `{"state":"running","ready":true}`, true
		}
		return 0, "", false
	})
	out, err := run(t, "fork", "my-box", "--name", "try", "--wait")
	if err != nil || !strings.Contains(out, "running") {
		t.Fatalf("%v %s", err, out)
	}
	if f.find("POST", boxPath+"/fork")[0].Body["wait"] != nil || polls < 2 {
		t.Fatalf("--wait must poll, not be sent: %+v", f.calls)
	}
}

func TestWhoamiCheck(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/whoami") {
			return 200, `{"organization":{"slug":"acme"}}`
		}
		return 404, `{}`
	})
	out, err := run(t, "whoami", "--check")
	if err != nil || strings.TrimSpace(out) != "signed in (acme)" {
		t.Fatalf("%v %q", err, out)
	}
	newFakeAPI(t, func(call) (int, string) { return 401, `{"error":{"code":"UNAUTHORIZED","message":"bad key"}}` })
	if _, err := run(t, "whoami", "--check"); commands.ExitCode(err) != commands.ExitAuth {
		t.Fatalf("a refused key must exit 3: %v (%d)", err, commands.ExitCode(err))
	}
}

func TestSnapshotShareShowsTheLinkOnce(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/snapshots/s1/shares") {
			return 201, `{"data":{"id":"sh1","path":"/app","expires_at":"2026-10-24T00:00:00Z"},"share_url":"https://api.miosa.ai/api/v1/shared/snapshots/ssl_x","token":"ssl_x"}`
		}
		return 404, `{}`
	})
	out, err := run(t, "snapshot", "share", "s1", "--hours", "24", "--path", "/app")
	if err != nil || !strings.Contains(out, "https://api.miosa.ai/api/v1/shared/snapshots/ssl_x") {
		t.Fatalf("%v %s", err, out)
	}
	if b := f.find("POST", "/snapshots/s1/shares")[0].Body; b["expires_in_hours"] != float64(24) || b["path"] != "/app" {
		t.Fatalf("body = %v", b)
	}
}

func TestCompletionOffersNamesIdsCurrentAndSelf(t *testing.T) {
	calls := 0
	newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/sandboxes") {
			calls++
			return 200, `{"data":[{"id":"` + boxID + `","name":"my-box"}]}`
		}
		return 404, `{}`
	})
	for i := 0; i < 2; i++ {
		out, _ := run(t, "__complete", "info", "")
		for _, want := range []string{"my-box", boxID, "current", "self"} {
			if !strings.Contains(out, want) {
				t.Fatalf("completion %q lacks %s", out, want)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("the second TAB within 15 s must come from the cache: %d API calls", calls)
	}
}

func TestCreateFromSnapshotNameOrID(t *testing.T) {
	const snapID = "11111111-2222-4333-8444-555555555555"
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/named-snapshots/web-stack/deploy"), c.is("POST", "/snapshots/"+snapID+"/fork"):
			return 201, `{"data":{"id":"` + boxID + `","name":"web-copy","state":"provisioning"}}`
		}
		return 404, `{}`
	})
	for _, from := range []string{"web-stack", snapID} {
		if out, err := run(t, "create", "--from", from); err != nil || !strings.Contains(out, "web-copy") {
			t.Fatalf("--from %s: %v %s", from, err, out)
		}
	}
	if len(f.find("POST", "/named-snapshots/web-stack/deploy")) != 1 || len(f.find("POST", "/snapshots/"+snapID+"/fork")) != 1 {
		t.Fatalf("calls: %+v", f.calls)
	}
	if _, err := run(t, "create", "--from", "web-stack", "--size", "large"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("--size with --from: %v", err)
	}
}

func TestResumeWaitsWhenAsked(t *testing.T) {
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	polls := 0
	sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("POST", boxPath+"/resume"):
			return 202, `{"id":"` + boxID + `","state":"starting"}`, true
		case c.is("GET", boxPath):
			polls++
			if polls < 2 {
				return 200, `{"state":"starting"}`, true
			}
			return 200, `{"state":"running","ready":true}`, true
		}
		return 0, "", false
	})
	if out, err := run(t, "resume", "my-box", "--wait"); err != nil || polls < 2 || !strings.Contains(out, "running") {
		t.Fatalf("%v polls=%d %s", err, polls, out)
	}
}

func TestAPIKeyUsageShowsRequestsAndLastUse(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/api-keys/k1") {
			return 200, `{"data":{"id":"k1","name":"ci","request_count":4211,"last_used_at":"2026-10-23T10:00:00Z","last_used_ip":"203.0.113.9","status":"active"}}`
		}
		return 404, `{}`
	})
	out, err := run(t, "api-key", "usage", "k1")
	if err != nil || !strings.Contains(out, "4211") || !strings.Contains(out, "203.0.113.9") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestScpIsFilesCp(t *testing.T) {
	cmd, _, err := commands.Root().Find([]string{"scp"})
	if err != nil || cmd.Name() != "scp" || cmd.Flags().Lookup("recursive") == nil {
		t.Fatalf("scp: %v", err)
	}
}

func TestAPIKeyUsageDaysListsRequestsPerDay(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/api-keys/k1"):
			return 200, `{"data":{"id":"k1","name":"ci","request_count":21,"status":"active"}}`
		case c.is("GET", "/api-keys/k1/usage"):
			return 200, `{"data":{"api_key_id":"k1","days":2,"total":21,"series":[{"date":"2026-10-22","requests":9},{"date":"2026-10-23","requests":12}]}}`
		}
		return 404, `{}`
	})
	out, err := run(t, "api-key", "usage", "k1", "--days", "2")
	if err != nil || !strings.Contains(out, "2026-10-22") || !strings.Contains(out, "12") {
		t.Fatalf("%v %s", err, out)
	}
	if q := f.find("GET", "/api-keys/k1/usage")[0].Query; !strings.Contains(q, "days=2") {
		t.Fatalf("query = %q", q)
	}
	out, err = run(t, "api-key", "usage", "k1", "--days", "2", "--json")
	if err != nil || !strings.Contains(out, `"daily"`) {
		t.Fatalf("%v %s", err, out)
	}
}

func TestAPIKeyUsageDaysOnAnOlderServerStillShowsTheKey(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/api-keys/k1") {
			return 200, `{"data":{"id":"k1","name":"ci","request_count":21,"status":"active"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND"}}`
	})
	stdout, stderr, err := runSplit(t, "api-key", "usage", "k1", "--days", "7")
	if err != nil || !strings.Contains(stdout, "21") || !strings.Contains(stderr, "does not keep requests per day") {
		t.Fatalf("%v %q %q", err, stdout, stderr)
	}
}
