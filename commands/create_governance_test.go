package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// createSandboxBody captures the create body for the governance flag tests.
func createSandboxBody(t *testing.T) (*httptest.Server, *map[string]any, func()) {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A fresh map per request: decoding into the same map merges keys, so a
		// second create would still carry the first one's fields.
		var next map[string]any
		_ = json.NewDecoder(r.Body).Decode(&next)
		body = next
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(fakeSandbox("abc123", "my-box"))
	}))
	return srv, &body, func() { srv.Close() }
}

func TestCreateSendsGovernanceTags(t *testing.T) {
	srv, body, done := createSandboxBody(t)
	defer done()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "create", "my-box", "--tag", "purpose=test", "--tag", "owner=ana"); err != nil {
		t.Fatalf("create: %v", err)
	}

	tags, _ := (*body)["tags"].(map[string]any)
	if tags["purpose"] != "test" || tags["owner"] != "ana" {
		t.Fatalf("tags = %+v, want purpose=test owner=ana", tags)
	}
}

func TestCreateSendsADurationAsExpiresAndATimestampAsExpiresAt(t *testing.T) {
	srv, body, done := createSandboxBody(t)
	defer done()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "create", "my-box", "--expires", "24h"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if (*body)["expires"] != "24h" {
		t.Fatalf("expires = %v, want 24h", (*body)["expires"])
	}
	if _, ok := (*body)["expires_at"]; ok {
		t.Fatalf("a duration must not be sent as expires_at: %v", (*body)["expires_at"])
	}

	if _, err := run(t, "create", "my-box", "--expires", "2026-11-01T00:00:00Z"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if (*body)["expires_at"] != "2026-11-01T00:00:00Z" {
		t.Fatalf("expires_at = %v", (*body)["expires_at"])
	}
	if _, ok := (*body)["expires"]; ok {
		t.Fatalf("a timestamp must not be sent as expires: %v", (*body)["expires"])
	}
}

func TestCreateRefusesATagWithoutAValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the server must not be called for an invalid tag")
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "create", "my-box", "--tag", "purpose")
	if err == nil {
		t.Fatalf("want a usage error for a bare tag:\n%s", out)
	}
	if !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Fatalf("error = %v", err)
	}
}

func TestDbAndBucketCreateSendTheClassification(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /databases":       resp{201, `{"data":{"id":"db1","name":"app-db"}}`},
		"POST /storage/buckets": resp{201, `{"data":{"id":"b1","name":"b1"}}`},
	})

	if out, err := run(t, "db", "create", "app-db", "--class", "restricted"); err != nil {
		t.Fatalf("db create: %v\n%s", err, out)
	}
	if calls := f.find("POST", "/databases"); len(calls) != 1 || calls[0].Body["classification"] != "restricted" {
		t.Fatalf("db body = %+v", calls)
	}

	if out, err := run(t, "storage", "create", "b1", "--class", "confidential"); err != nil {
		t.Fatalf("storage create: %v\n%s", err, out)
	}
	if calls := f.find("POST", "/storage/buckets"); len(calls) != 1 || calls[0].Body["classification"] != "confidential" {
		t.Fatalf("bucket body = %+v", calls)
	}
}
