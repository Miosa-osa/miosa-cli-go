package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// relayGrantServer answers the relay-key route and records what the CLI sent.
func relayGrantServer(t *testing.T, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls = *calls + 1
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/cli/relay-keys") {
			t.Errorf("mint relay key hit %s %s, want POST .../cli/relay-keys", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.Contains(got, "msk_u_test") {
			t.Errorf("mint relay key sent Authorization %q, want the profile key", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the mint body: %v", err)
		}
		if body["confirm"] != "engine:sync" {
			t.Errorf("confirm = %v, want engine:sync", body["confirm"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Connection", "close")
		fmt.Fprint(w, `{"key":"msk_relay_abc","name":"MIOSA CLI relay","scopes":["engine:sync"],"expires_at":"2026-10-21T06:30:00Z"}`)
	}))
}

func TestMintRelayKeyReturnsTheGrantedKeyAndExpiry(t *testing.T) {
	calls := 0
	srv := relayGrantServer(t, &calls)
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)

	key, expiresAt, err := mintRelayKey(context.Background())
	if err != nil {
		t.Fatalf("mintRelayKey: %v", err)
	}
	if key != "msk_relay_abc" {
		t.Errorf("key = %q, want the granted key", key)
	}
	if expiresAt.IsZero() || expiresAt.UTC().Format("2006-01-02T15:04:05Z") != "2026-10-21T06:30:00Z" {
		t.Errorf("expiresAt = %v, want 2026-10-21T06:30:00Z", expiresAt)
	}
	if calls != 1 {
		t.Errorf("the mint route was called %d times, want 1", calls)
	}
}

func TestRelayModeNeedsAKeyOrGrant(t *testing.T) {
	opts := engineSyncOptions{
		relayURL:  "http://127.0.0.1:1",
		engineID:  "db-1",
		workspace: "ws-1",
		localURL:  "http://127.0.0.1:2",
		localKey:  "local-admin",
	}

	err := opts.validate()
	if err == nil || !strings.Contains(err.Error(), "--grant") {
		t.Fatalf("validate() without a key = %v, want an error that names --grant", err)
	}

	opts.grant = true
	if err := opts.validate(); err != nil {
		t.Fatalf("validate() with --grant = %v, want nil", err)
	}
}

func TestGrantRelayKeyKeepsAnExplicitKey(t *testing.T) {
	calls := 0
	srv := relayGrantServer(t, &calls)
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)

	cmd := &cobra.Command{}
	opts := engineSyncOptions{grant: true, miosaKey: "msk_explicit", relayURL: "http://127.0.0.1:1", engineID: "db-1", workspace: "ws-1", localURL: "http://127.0.0.1:2", localKey: "local-admin"}
	if err := grantRelayKey(cmd, &opts); err != nil {
		t.Fatalf("grantRelayKey: %v", err)
	}
	if opts.miosaKey != "msk_explicit" {
		t.Errorf("miosaKey = %q, want the explicit key untouched", opts.miosaKey)
	}
	if calls != 0 {
		t.Errorf("the mint route was called %d times, want 0 when a key is given", calls)
	}
}

func TestMintRelayKeyOnAServerWithoutTheRouteSaysWhatToDo(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":{"code":"NOT_FOUND","message":"no route for POST /api/v1/cli/relay-keys"}}`)
	}))
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	t.Setenv("MIOSA_API_KEY", "msk_u_test")
	t.Setenv("MIOSA_BASE_URL", srv.URL)

	_, _, err := mintRelayKey(context.Background())
	if err == nil {
		t.Fatal("mintRelayKey on a server without the route = nil, want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "does not support relay key grants yet") || !strings.Contains(msg, "--api-key") {
		t.Errorf("error = %q, want it to name the missing route support and --api-key", msg)
	}
	if strings.Contains(msg, "404") || strings.Contains(msg, "NOT_FOUND") {
		t.Errorf("error = %q, want no raw status or server code", msg)
	}
	if calls != 1 {
		t.Errorf("the mint route was called %d times, want 1", calls)
	}
}
