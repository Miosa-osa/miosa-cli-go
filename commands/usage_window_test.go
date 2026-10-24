package commands_test

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestUsageWindowSendsSinceUntilAndShowsRunTimeAndCredits(t *testing.T) {
	f := boxRoutes(t, map[string]any{
		"GET " + boxPath + "/usage": `{"data":{"state":"running","runtime_sec":500,"window":{"since":"2026-10-20T10:00:00Z","until":"2026-10-20T12:00:00Z","credits":7,
			"meters":[{"product":"sandboxes","meter":"runtime","unit":"second","quantity":90,"credits":7},{"product":"storage","meter":"paused_disk","unit":"byte_second","quantity":1000,"credits":0}]}}}`,
	})
	out, err := run(t, "usage", "my-box", "--since", "2026-10-20T10:00:00Z", "--until", "2026-10-20T12:00:00Z")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	q, _ := url.ParseQuery(f.find("GET", boxPath+"/usage")[0].Query)
	if q.Get("since") != "2026-10-20T10:00:00Z" || q.Get("until") != "2026-10-20T12:00:00Z" {
		t.Fatalf("query = %v", q)
	}
	for _, want := range []string{"2026-10-20T10:00:00Z to 2026-10-20T12:00:00Z", "Window run time (s)", "90", "Window credits"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in\n%s", want, out)
		}
	}

	before := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if out, err := run(t, "usage", "my-box", "--since", "7d"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	q, _ = url.ParseQuery(f.find("GET", boxPath+"/usage")[1].Query)
	since, err := time.Parse(time.RFC3339, q.Get("since"))
	if err != nil || since.Before(before.Add(-time.Minute)) || since.After(before.Add(time.Minute)) {
		t.Fatalf("--since 7d sent %q", q.Get("since"))
	}

	if _, err := run(t, "usage", "my-box", "--until", "1h"); err == nil {
		t.Fatal("--until without --since must be refused")
	}
	if _, err := run(t, "usage", "my-box", "--since", "last tuesday"); err == nil {
		t.Fatal("an unreadable --since must be refused")
	}
}
