package commands_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestAPIGetPrintsBody(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"GET /credits/balance": `{"balance_credits":42}`})
	out, err := run(t, "api", "/credits/balance")
	if err != nil || strings.TrimSpace(out) != `{"balance_credits":42}` {
		t.Fatalf("%v %q", err, out)
	}
	if len(f.find("GET", "/credits/balance")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestAPIPostDataDefaultsToPOST(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /webhooks": resp{201, `{"id":"w1"}`}})
	out, err := run(t, "api", "/webhooks", "-d", `{"url":"https://x/h"}`)
	if err != nil || !strings.Contains(out, `"w1"`) {
		t.Fatalf("%v %q", err, out)
	}
	c := f.find("POST", "/webhooks")
	if len(c) != 1 || c[0].Body["url"] != "https://x/h" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestAPIExplicitMethodAndFileBody(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"PATCH /sandboxes/" + boxID + "/tags": `{}`})
	file := filepath.Join(t.TempDir(), "tags.json")
	os.WriteFile(file, []byte(`{"tags":{"a":"b"}}`), 0o600)
	// The sandbox name in the path is resolved too.
	if _, err := run(t, "api", "PATCH", "/sandboxes/"+boxID+"/tags", "-d", "@"+file); err != nil {
		t.Fatal(err)
	}
	if c := f.find("PATCH", "/sandboxes/"+boxID+"/tags"); len(c) != 1 || c[0].Body["tags"] == nil {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestAPIBodyFromStdin(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /workspaces": `{}`})
	if _, err := runIn(t, `{"name":"x"}`, "api", "POST", "/workspaces", "-d", "-"); err != nil {
		t.Fatal(err)
	}
	if c := f.find("POST", "/workspaces"); len(c) != 1 || c[0].Body["name"] != "x" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestAPIQueryStringAndHeaders(t *testing.T) {
	var gotQuery, gotHeader string
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotHeader = r.URL.RawQuery, r.Header.Get("X-Test")
		w.Write([]byte(`{}`))
	})
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)
	t.Setenv("HOME", t.TempDir())
	if _, err := run(t, "api", "/runs?limit=2&status=running", "-H", "X-Test: yes"); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "limit=2&status=running" || gotHeader != "yes" {
		t.Fatalf("query=%q header=%q", gotQuery, gotHeader)
	}
}

func TestAPIFailureStillPrintsBodyAndExitsByStatus(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /nope": resp{404, `{"error":{"code":"NOT_FOUND"}}`}})
	out, err := run(t, "api", "/nope")
	if err == nil || commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(out, "NOT_FOUND") {
		t.Fatalf("body must still be printed: %q", out)
	}
}

func TestAPIRejectsBadMethodAndHeader(t *testing.T) {
	fakeRoutes(t, nil)
	if _, err := run(t, "api", "FETCH", "/x"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("method: %v", err)
	}
	if _, err := run(t, "api", "/x", "-H", "nocolon"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("header: %v", err)
	}
}
