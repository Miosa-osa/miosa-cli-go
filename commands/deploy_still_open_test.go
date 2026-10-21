package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The tester's "still open" deploy items (2026-10-16): deploy prove came back,
// purge-cache and env-rm exist, versions archive is rm's name for what it does.
func deployStillOpenServer(t *testing.T, proofOK bool, seen map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		seen[r.Method+" "+r.URL.Path] = "1"
		var body any
		switch {
		case r.URL.Path == "/deployments/app/proof":
			body = map[string]any{"data": map[string]any{"ok": proofOK, "checks": []any{
				map[string]any{"id": "deployment_row", "ok": true, "message": "Deployment exists."},
				map[string]any{"id": "deployment_running", "ok": proofOK, "message": "state"},
			}, "next_actions": []any{"Redeploy the deployment."}}}
		case r.URL.Path == "/deployments/app/purge-cache":
			body = map[string]any{"data": map[string]any{"versions": 3, "local_purged": 12, "nodes": 4, "remote_purged": 40}}
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/deployments/app/env/"):
			body = map[string]any{"data": map[string]any{"deleted": []any{"DEBUG"}, "missing": []any{"NOPE"}}}
		default:
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestDeployProveExitsNonZeroWhenACheckFails(t *testing.T) {
	seen := map[string]string{}
	srv := deployStillOpenServer(t, true, seen)
	defer srv.Close()
	defer setupEnv(t, srv)()
	out, err := run(t, "deploy", "prove", "app")
	if err != nil || !strings.Contains(out, "app is live: 2 of 2 checks passed") {
		t.Fatalf("passing proof: err=%v\n%s", err, out)
	}

	failing := deployStillOpenServer(t, false, seen)
	defer failing.Close()
	defer setupEnv(t, failing)()
	out, err = run(t, "deploy", "prove", "app")
	if err == nil || !strings.Contains(err.Error(), "1 of 2 checks failed") || !strings.Contains(out, "FAIL") {
		t.Fatalf("failing proof must exit non-zero: err=%v\n%s", err, out)
	}
	if out, err = run(t, "deploy", "prove", "app", "--json"); err == nil || !strings.Contains(out, `"ok": false`) {
		t.Fatalf("failing proof --json must print and exit non-zero: err=%v\n%s", err, out)
	}
}

func TestDeployPurgeCacheAndEnvRm(t *testing.T) {
	seen := map[string]string{}
	srv := deployStillOpenServer(t, true, seen)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := run(t, "deploy", "purge-cache", "app")
	if err != nil || seen["POST /deployments/app/purge-cache"] == "" || !strings.Contains(out, "40") {
		t.Fatalf("purge-cache: err=%v seen=%v\n%s", err, seen, out)
	}
	out, err = run(t, "deploy", "env-rm", "app", "DEBUG,NOPE")
	if err != nil || seen["DELETE /deployments/app/env/DEBUG,NOPE"] == "" {
		t.Fatalf("env-rm: err=%v seen=%v\n%s", err, seen, out)
	}
	if !strings.Contains(out, "Removed DEBUG from app") || !strings.Contains(out, "Not set, nothing removed: NOPE") {
		t.Fatalf("env-rm output:\n%s", out)
	}
	if _, err := run(t, "deploy", "versions", "archive", "--help"); err != nil {
		t.Fatalf("versions archive: %v", err)
	}
}

func proveServer(t *testing.T, publicURL string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/deployments/app/proof" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"ok": true, "public_url": publicURL,
			"checks": []any{map[string]any{"id": "deployment_row", "ok": true, "message": "exists"}},
		}})
	}))
}

func TestDeployProveChecksThePublicURL(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer site.Close()
	srv := proveServer(t, site.URL)
	defer srv.Close()
	defer setupEnv(t, srv)()
	out, err := run(t, "deploy", "prove", "app")
	if err != nil || !strings.Contains(out, "public_url_http") || !strings.Contains(out, "answered 200") || !strings.Contains(out, "2 of 2 checks passed") {
		t.Fatalf("serving site: err=%v\n%s", err, out)
	}
}

func TestDeployProveFailsWhenThePublicURLIsDownOrBroken(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer down.Close()
	srv := proveServer(t, down.URL)
	defer srv.Close()
	defer setupEnv(t, srv)()
	out, err := run(t, "deploy", "prove", "app")
	if err == nil || !strings.Contains(err.Error(), "1 of 2 checks failed") || !strings.Contains(out, "answered 502") {
		t.Fatalf("502 must fail the proof: err=%v\n%s", err, out)
	}

	// A self-signed certificate is what a visitor's browser would refuse.
	broken := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer broken.Close()
	tlsSrv := proveServer(t, broken.URL)
	defer tlsSrv.Close()
	defer setupEnv(t, tlsSrv)()
	out, err = run(t, "deploy", "prove", "app", "--json")
	if err == nil || !strings.Contains(out, `"ok": false`) || !strings.Contains(out, "public_url_http") {
		t.Fatalf("bad certificate must fail the proof: err=%v\n%s", err, out)
	}
}
