package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// engineStub is one Engine endpoint that records what the command sends it.
type engineStub struct {
	server       *httptest.Server
	replica      string
	peer         map[string]any
	enabled      bool
	ran          bool
	statusHit    bool
	mintAuth     string
	existingKeys []map[string]any
	revoked      []string
}

func newEngineStub(t *testing.T, replica string) *engineStub {
	t.Helper()
	stub := &engineStub{replica: replica}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/keys", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			keys := stub.existingKeys
			if keys == nil {
				keys = []map[string]any{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
			return
		}
		stub.mintAuth = r.Header.Get("Authorization")
		writeJSON(w, http.StatusCreated, map[string]any{"id": "key_1", "key": stub.replica})
	})
	mux.HandleFunc("/api/auth/keys/", func(w http.ResponseWriter, r *http.Request) {
		stub.revoked = append(stub.revoked, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/sync/enable", func(w http.ResponseWriter, r *http.Request) {
		stub.enabled = true
		writeJSON(w, http.StatusOK, map[string]any{})
	})
	mux.HandleFunc("/api/sync/peers", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&stub.peer)
		writeJSON(w, http.StatusCreated, map[string]any{})
	})
	mux.HandleFunc("/api/sync/run", func(w http.ResponseWriter, r *http.Request) {
		stub.ran = true
		writeJSON(w, http.StatusOK, map[string]any{})
	})
	mux.HandleFunc("/api/sync/status", func(w http.ResponseWriter, r *http.Request) {
		stub.statusHit = true
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":         stub.enabled,
			"ledger_sequence": 7,
			"open_conflicts":  0,
			"replica_id":      "replica",
			"peers":           []any{map[string]any{"url": "peer"}},
			"workspace_id":    r.URL.Query().Get("workspace"),
		})
	})

	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func syncOptions(local, cloud *engineStub) engineSyncOptions {
	return engineSyncOptions{
		localURL:     local.server.URL,
		cloudURL:     cloud.server.URL,
		workspace:    "ws-1",
		localKey:     "admin-local",
		cloudKey:     "admin-cloud",
		policy:       "both",
		runOneCycle:  true,
		requestLimit: 5 * time.Second,
	}
}

func TestEngineSyncWiresBothPeersWithWorkspaceScopedReplicaKeys(t *testing.T) {
	local := newEngineStub(t, "oe_local_replica")
	cloud := newEngineStub(t, "oe_cloud_replica")

	var buf bytes.Buffer
	p := output.New(&buf, output.FormatText, false)

	if err := runEngineSync(context.Background(), p, syncOptions(local, cloud)); err != nil {
		t.Fatalf("runEngineSync: %v", err)
	}

	if local.mintAuth != "Bearer admin-local" {
		t.Errorf("local mint used %q, want the local admin key", local.mintAuth)
	}
	if cloud.mintAuth != "Bearer admin-cloud" {
		t.Errorf("cloud mint used %q, want the cloud admin key", cloud.mintAuth)
	}

	// The local Engine stores the cloud as its peer and uses the cloud's replica key.
	if got := local.peer["url"]; got != cloud.server.URL {
		t.Errorf("local peer url = %v, want %v", got, cloud.server.URL)
	}
	if got := local.peer["api_key"]; got != "oe_cloud_replica" {
		t.Errorf("local peer key = %v, want the cloud replica key", got)
	}

	if got := cloud.peer["url"]; got != local.server.URL {
		t.Errorf("cloud peer url = %v, want %v", got, local.server.URL)
	}
	if got := cloud.peer["api_key"]; got != "oe_local_replica" {
		t.Errorf("cloud peer key = %v, want the local replica key", got)
	}

	for name, stub := range map[string]*engineStub{"local": local, "cloud": cloud} {
		if !stub.enabled {
			t.Errorf("%s Engine was not enabled for the workspace", name)
		}
		if !stub.ran {
			t.Errorf("%s Engine did not run a cycle", name)
		}
		if !stub.statusHit {
			t.Errorf("%s Engine status was not read", name)
		}
		if got := stub.peer["policy"]; got != "both" {
			t.Errorf("%s peer policy = %v, want both", name, got)
		}
		if got := stub.peer["workspace_id"]; got != "ws-1" {
			t.Errorf("%s peer workspace = %v, want ws-1", name, got)
		}
	}

	if !strings.Contains(buf.String(), "ws-1") || !strings.Contains(buf.String(), "peers") {
		t.Errorf("status output does not mention the workspace and peers:\n%s", buf.String())
	}
}

func TestEngineSyncStatusOnlyReads(t *testing.T) {
	local := newEngineStub(t, "oe_local_replica")
	cloud := newEngineStub(t, "oe_cloud_replica")

	opts := syncOptions(local, cloud)
	var buf bytes.Buffer
	p := output.New(&buf, output.FormatText, true)

	if err := showEngineSyncStatus(context.Background(), p, opts); err != nil {
		t.Fatalf("showEngineSyncStatus: %v", err)
	}

	for name, stub := range map[string]*engineStub{"local": local, "cloud": cloud} {
		if stub.enabled || stub.ran || stub.peer != nil {
			t.Errorf("%s Engine was mutated by status", name)
		}
		if !stub.statusHit {
			t.Errorf("%s Engine status was not read", name)
		}
	}
}

func TestEngineSyncJSONOutput(t *testing.T) {
	local := newEngineStub(t, "oe_local_replica")
	cloud := newEngineStub(t, "oe_cloud_replica")

	previous := globalFlags.JSON
	globalFlags.JSON = true
	t.Cleanup(func() { globalFlags.JSON = previous })

	var buf bytes.Buffer
	p := output.New(&buf, output.FormatJSON, true)

	if err := runEngineSync(context.Background(), p, syncOptions(local, cloud)); err != nil {
		t.Fatalf("runEngineSync: %v", err)
	}

	var envelope map[string]any
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	decoded, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("output has no data envelope: %s", buf.String())
	}
	if decoded["workspace"] != "ws-1" {
		t.Errorf("workspace = %v, want ws-1", decoded["workspace"])
	}
	if _, ok := decoded["local"]; !ok {
		t.Error("JSON output has no local status")
	}
	if _, ok := decoded["cloud"]; !ok {
		t.Error("JSON output has no cloud status")
	}
}

func TestEngineSyncValidation(t *testing.T) {
	base := engineSyncOptions{
		localURL:  "http://127.0.0.1:1",
		cloudURL:  "http://127.0.0.1:2",
		workspace: "ws-1",
		localKey:  "a",
		cloudKey:  "b",
	}

	cases := []struct {
		name string
		mut  func(*engineSyncOptions)
		want string
	}{
		{"workspace", func(o *engineSyncOptions) { o.workspace = "" }, "workspace"},
		{"local key", func(o *engineSyncOptions) { o.localKey = "" }, "admin key"},
		{"bad url", func(o *engineSyncOptions) { o.cloudURL = "not-a-url" }, "scheme"},
		{"policy", func(o *engineSyncOptions) { o.policy = "sideways" }, "policy"},
	}

	for _, tc := range cases {
		opts := base
		tc.mut(&opts)
		err := opts.validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: validate() = %v, want an error mentioning %q", tc.name, err, tc.want)
		}
	}
}

func TestEngineSyncMissingKeyIsNotPrinted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"forbidden"}`)
	}))
	defer server.Close()

	opts := engineSyncOptions{
		localURL:     server.URL,
		cloudURL:     server.URL,
		workspace:    "ws-1",
		localKey:     "super-secret-local",
		cloudKey:     "super-secret-cloud",
		policy:       "both",
		requestLimit: 2 * time.Second,
	}

	var buf bytes.Buffer
	p := output.New(&buf, output.FormatText, true)
	err := runEngineSync(context.Background(), p, opts)
	if err == nil {
		t.Fatal("expected an error from a 401 Engine")
	}
	if strings.Contains(err.Error(), "super-secret") || strings.Contains(buf.String(), "super-secret") {
		t.Fatalf("an admin key leaked into output: %v / %s", err, buf.String())
	}
}

func TestEngineSyncRotatesTheReplicaKey(t *testing.T) {
	local := newEngineStub(t, "oe_local_replica")
	cloud := newEngineStub(t, "oe_cloud_replica")
	local.existingKeys = []map[string]any{{"id": "key_old", "name": "miosa-sync-ws-1"}}

	var buf bytes.Buffer
	p := output.New(&buf, output.FormatText, true)
	if err := runEngineSync(context.Background(), p, syncOptions(local, cloud)); err != nil {
		t.Fatalf("runEngineSync: %v", err)
	}

	if len(local.revoked) != 1 || local.revoked[0] != "/api/auth/keys/key_old/revoke" {
		t.Errorf("local revoked %v, want the previous replica key", local.revoked)
	}
	if len(cloud.revoked) != 0 {
		t.Errorf("cloud revoked %v, want nothing", cloud.revoked)
	}
}
