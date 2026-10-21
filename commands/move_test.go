package commands_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// otherWS is the workspace the move fixtures start in.
const otherWS = "9c0f5a52-3b1d-4d7e-9a55-0a1b2c3d4e5f"

const (
	moveDB      = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	moveBucket  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	moveWebhook = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
)

type moveServer struct {
	mu     sync.Mutex
	posted []map[string]any
}

// handler knows one database and one bucket, both called "shared"; a database
// "only-db"; and the target workspace "staging".
func (m *moveServer) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		key := r.Method + " " + r.URL.Path
		switch key {
		case "GET /workspaces":
			w.Write([]byte(`{"data":[{"id":"` + wsID + `","name":"Staging","slug":"staging"}]}`))
		case "GET /databases":
			w.Write([]byte(`{"data":[{"id":"` + moveDB + `","name":"shared"},{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","name":"only-db"}]}`))
		case "GET /storage/buckets":
			w.Write([]byte(`{"data":[{"id":"` + moveBucket + `","name":"shared"}]}`))
		case "GET /webhooks":
			w.Write([]byte(`{"data":[{"id":"` + moveWebhook + `","name":"hook-1"}]}`))
		case "GET /databases/" + moveDB, "GET /databases/cccccccc-cccc-4ccc-8ccc-cccccccccccc", "GET /storage/buckets/" + moveBucket, "GET /webhooks/" + moveWebhook:
			w.Write([]byte(`{"data":{"id":"x","workspace_id":"` + otherWS + `"}}`))
		case "POST /workspaces/" + otherWS + "/move":
			raw, _ := io.ReadAll(r.Body)
			var b map[string]any
			_ = json.Unmarshal(raw, &b)
			m.mu.Lock()
			m.posted = append(m.posted, b)
			m.mu.Unlock()
			w.Write([]byte(`{"data":{"moved":[{"type":"x"}]}}`))
		default:
			// Every other collection is empty or unknown.
			if r.Method == http.MethodGet {
				if strings.Count(r.URL.Path, "/") == 1 {
					w.Write([]byte(`{"data":[]}`))
					return
				}
				http.Error(w, `{"error":"not found"}`, 404)
				return
			}
			t.Errorf("unexpected %s", key)
			http.Error(w, "unexpected", 404)
		}
	})
}

func TestMoveFindsTheResourceByNameAndSendsTheTargetWorkspace(t *testing.T) {
	fake := &moveServer{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	out, err := run(t, "move", "only-db", "--to-workspace", "staging")
	if err != nil {
		t.Fatalf("move failed: %v\n%s", err, out)
	}
	if len(fake.posted) != 1 {
		t.Fatalf("want one move request, got %v", fake.posted)
	}
	body := fake.posted[0]
	if body["target_workspace_id"] != wsID {
		t.Errorf("want the resolved workspace id, got %v", body)
	}
	res, _ := body["resources"].([]any)
	if len(res) != 1 || res[0].(map[string]any)["type"] != "database" || res[0].(map[string]any)["id"] != "cccccccc-cccc-4ccc-8ccc-cccccccccccc" {
		t.Errorf("want the database named, got %v", body["resources"])
	}
	if !strings.Contains(out, "Moved database only-db to workspace staging") {
		t.Errorf("want a success line, got %q", out)
	}
}

func TestMoveNeedsTheKindWhenANameIsAmbiguous(t *testing.T) {
	fake := &moveServer{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	_, err := run(t, "move", "shared", "--to-workspace", "staging")
	if err == nil || !strings.Contains(err.Error(), "add --type") {
		t.Fatalf("want an ambiguity error, got %v", err)
	}
	if len(fake.posted) != 0 {
		t.Errorf("nothing may be moved on an ambiguous name, got %v", fake.posted)
	}

	if out, err := run(t, "move", "shared", "--type", "bucket", "--to-workspace", "staging"); err != nil {
		t.Fatalf("typed move failed: %v\n%s", err, out)
	}
	if len(fake.posted) != 1 {
		t.Fatalf("want one move request, got %v", fake.posted)
	}
	res, _ := fake.posted[0]["resources"].([]any)
	if len(res) != 1 || res[0].(map[string]any)["type"] != "bucket" || res[0].(map[string]any)["id"] != moveBucket {
		t.Errorf("the bucket must be moved and not the database of the same name, got %v", fake.posted[0]["resources"])
	}
}

func TestMoveRefusesWithoutADestinationOrForAnUnknownResource(t *testing.T) {
	fake := &moveServer{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	if _, err := run(t, "move", "only-db"); err == nil {
		t.Error("a move without --to-workspace must be refused")
	}
	if _, err := run(t, "move", "nothing-here", "--to-workspace", "staging"); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Errorf("want a not-found error, got %v", err)
	}
	if _, err := run(t, "move", "only-db", "--type", "sandwich", "--to-workspace", "staging"); err == nil {
		t.Error("an unknown --type must be refused")
	}
	if len(fake.posted) != 0 {
		t.Errorf("nothing may be sent, got %v", fake.posted)
	}
}

func TestMovePassesTheProjectAndDryRunOptionsToTheWorkspaceRoute(t *testing.T) {
	fake := &moveServer{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	setupEnv(t, srv)

	out, err := run(t, "move", "only-db", "--to-workspace", "staging", "--detach-from-project", "--dry-run")
	if err != nil {
		t.Fatalf("move failed: %v\n%s", err, out)
	}
	if len(fake.posted) != 1 || fake.posted[0]["detach_from_project"] != true || fake.posted[0]["dry_run"] != true {
		t.Errorf("want detach and dry_run sent, got %v", fake.posted)
	}
	if !strings.Contains(out, "Dry run") {
		t.Errorf("a dry run must say it moved nothing, got %q", out)
	}
}

// A volume, a schedule and an agent each move through their own route, with the
// target workspace as target_workspace_id. Not proven live for the schedule: a QA key
// without cron_jobs:write cannot create one.
func TestMoveVolumeScheduleAndAgentUseTheirOwnRoutes(t *testing.T) {
	const (
		volID  = "d1111111-1111-4111-8111-111111111111"
		cronID = "d2222222-2222-4222-8222-222222222222"
		agtID  = "d3333333-3333-4333-8333-333333333333"
	)
	var mu sync.Mutex
	posted := map[string]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /workspaces":
			w.Write([]byte(`{"data":[{"id":"` + wsID + `","name":"Staging","slug":"staging"}]}`))
		case "GET /volumes", "GET /volumes/" + volID:
			w.Write([]byte(`{"data":[{"id":"` + volID + `","name":"cache"}]}`))
		case "GET /cron-jobs", "GET /cron-jobs/" + cronID:
			w.Write([]byte(`{"data":[{"id":"` + cronID + `","name":"nightly"}]}`))
		case "GET /agents", "GET /agents/" + agtID:
			w.Write([]byte(`{"data":[{"id":"` + agtID + `","name":"reviewer"}]}`))
		case "POST /volumes/" + volID + "/move", "POST /cron-jobs/" + cronID + "/move", "POST /agents/" + agtID + "/move":
			raw, _ := io.ReadAll(r.Body)
			var b map[string]any
			_ = json.Unmarshal(raw, &b)
			mu.Lock()
			posted[r.URL.Path] = b
			mu.Unlock()
			w.Write([]byte(`{"data":{"id":"x","workspace_id":"` + wsID + `"}}`))
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()
	setupEnv(t, srv)

	for _, c := range []struct{ name, kind, path string }{
		{"cache", "volume", "/volumes/" + volID + "/move"},
		{"nightly", "schedule", "/cron-jobs/" + cronID + "/move"},
		{"reviewer", "agent", "/agents/" + agtID + "/move"},
	} {
		if out, err := run(t, "move", c.name, "--type", c.kind, "--to-workspace", "staging"); err != nil {
			t.Fatalf("move %s failed: %v\n%s", c.kind, err, out)
		}
		if posted[c.path]["target_workspace_id"] != wsID {
			t.Errorf("%s: want target_workspace_id %s on %s, got %v", c.kind, wsID, c.path, posted[c.path])
		}
	}
}

func TestMoveMovesAWebhookThroughItsOwnRoute(t *testing.T) {
	fake := &moveServer{}
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/webhooks/") {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":{"type":"webhook","id":"` + moveWebhook + `"}}`))
			return
		}
		fake.handler(t).ServeHTTP(w, r)
	}))
	defer srv.Close()
	setupEnv(t, srv)

	out, err := run(t, "move", "hook-1", "--type", "webhook", "--to-workspace", "staging")
	if err != nil {
		t.Fatalf("move failed: %v\n%s", err, out)
	}
	if gotPath != "/webhooks/"+moveWebhook+"/move" {
		t.Fatalf("posted to %q, want the webhook move route", gotPath)
	}
	if !strings.Contains(out, "Moved webhook hook-1") {
		t.Fatalf("output = %q", out)
	}
}

const moveDep = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"

// transferServer knows one deployment "my-app" in otherWS and the workspace
// "staging" in the same organization, and records the transfer request.
func transferServer(t *testing.T, posted *map[string]any, gotPath *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/bill-to":
			w.Write([]byte(`{"data":{"options":[{"id":"t-1","slug":"acme","name":"Acme","viewing":true}]}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/workspaces":
			w.Write([]byte(`{"data":[{"id":"` + wsID + `","name":"Staging","slug":"staging"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/deployments":
			w.Write([]byte(`{"data":[{"id":"` + moveDep + `","name":"my-app","slug":"my-app"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/deployments/my-app":
			w.Write([]byte(`{"data":{"id":"` + moveDep + `","name":"my-app"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/deployments/"+moveDep:
			w.Write([]byte(`{"data":{"id":"` + moveDep + `","name":"my-app","workspace_id":"` + otherWS + `"}}`))
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "cross-org-transfer"):
			raw, _ := io.ReadAll(r.Body)
			var b map[string]any
			_ = json.Unmarshal(raw, &b)
			*posted = b
			*gotPath = r.URL.Path
			w.Write([]byte(`{"data":{"billing_switched_at":"2026-10-20T12:00:00Z","transferred":[{"type":"deployment","id":"` + moveDep + `"}]}}`))
		default:
			http.Error(w, `{"error":"not found"}`, 404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMoveTransfersADeploymentToAnotherOrganization(t *testing.T) {
	var posted map[string]any
	var gotPath string
	srv := transferServer(t, &posted, &gotPath)
	setupEnv(t, srv)
	writeConfigFile(t, "api_key = \"msk_u_test\"\nactive_org = \"acme\"\n")

	out, err := run(t, "move", "my-app", "--type", "deployment", "--to-org", "acme-eu",
		"--to-workspace", "staging", "--confirm", "my-app")
	if err != nil {
		t.Fatalf("transfer failed: %v\n%s", err, out)
	}
	if gotPath != "/workspaces/"+otherWS+"/cross-org-transfer" {
		t.Fatalf("posted to %q, want the source workspace transfer route", gotPath)
	}
	if posted["target_org_id"] != "acme-eu" || posted["target_workspace_id"] != wsID || posted["confirm"] != "my-app" {
		t.Fatalf("body = %v", posted)
	}
	resources, _ := posted["resources"].([]any)
	if len(resources) != 1 || resources[0].(map[string]any)["type"] != "deployment" {
		t.Fatalf("resources = %v", posted["resources"])
	}
	if !strings.Contains(out, "billing switched at 2026-10-20T12:00:00Z") {
		t.Fatalf("the billing switch must be reported: %q", out)
	}
}

func TestMoveTransferNeedsTheNameAndADifferentOrganization(t *testing.T) {
	var posted map[string]any
	var gotPath string
	srv := transferServer(t, &posted, &gotPath)
	setupEnv(t, srv)
	writeConfigFile(t, "api_key = \"msk_u_test\"\nactive_org = \"acme\"\n")

	if out, err := run(t, "move", "my-app", "--type", "deployment", "--to-org", "acme-eu", "--to-workspace", "staging"); err == nil {
		t.Fatalf("must ask for --confirm:\n%s", out)
	}
	if out, err := run(t, "move", "my-app", "--type", "deployment", "--to-org", "acme-eu", "--to-workspace", "staging", "--confirm", "other-app"); err == nil {
		t.Fatalf("a mismatched --confirm must be refused:\n%s", out)
	}
	if out, err := run(t, "move", "my-app", "--type", "deployment", "--to-org", "acme", "--to-workspace", "staging", "--confirm", "my-app"); err == nil {
		t.Fatalf("the current organization must be refused as a transfer:\n%s", out)
	}
	if gotPath != "" {
		t.Fatalf("nothing may be sent, posted %q", gotPath)
	}
}
