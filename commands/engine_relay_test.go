package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// localEngineStub is a laptop Engine, read and written by the command itself.
type localEngineStub struct {
	server         *httptest.Server
	applied        map[string]any
	ledgerHeader   string
	appliedBatches int
	mutations      []map[string]any
	cursor         int
	migration      string
	version        string
	replica        string
}

func newLocalEngineStub(t *testing.T) *localEngineStub {
	t.Helper()
	stub := &localEngineStub{
		mutations: []map[string]any{{"id": "local-1", "idempotency_key": "k-local-1"}},
		cursor:    6,
		migration: "20261019130000",
		version:   "0.4.1",
		replica:   "replica-local",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"version":            stub.version,
			"expected_migration": stub.migration,
		})
	})
	mux.HandleFunc("/api/sync/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled":         true,
			"ledger_sequence": 4,
			"open_conflicts":  1,
			"replica_id":      stub.replica,
		})
	})
	mux.HandleFunc("/api/sync/changes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			stub.appliedBatches++
			_ = json.NewDecoder(r.Body).Decode(&stub.applied)
			writeJSON(w, http.StatusOK, map[string]any{"applied": 2})
			return
		}
		stub.ledgerHeader = r.Header.Get("x-optimal-replica")
		writeJSON(w, http.StatusOK, map[string]any{
			"mutations": stub.mutations,
			"cursor":    stub.cursor,
			"engine": map[string]any{
				"replica_id": stub.replica,
				"version":    stub.version,
				"migration":  stub.migration,
			},
		})
	})

	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

// relayStub is the MIOSA control-plane relay in front of the cloud Engine.
type relayStub struct {
	server     *httptest.Server
	pushed     map[string]any
	pushStatus int
	pushBody   map[string]any
	pullQuery  string
	pushes     int
}

func newRelayStub(t *testing.T) *relayStub {
	t.Helper()
	stub := &relayStub{
		pushStatus: http.StatusOK,
		pushBody:   map[string]any{"data": map[string]any{"applied": 1}},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/engines/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sync/status"):
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
				"enabled":         true,
				"ledger_sequence": 9,
				"open_conflicts":  0,
				"replica_id":      "replica-cloud",
				"migration":       "20261019120000",
				"version":         "0.4.0",
			}})
		case strings.HasSuffix(r.URL.Path, "/sync/pull"):
			stub.pullQuery = r.URL.RawQuery
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
				"workspace_id": "ws-1",
				"mutations": []any{
					map[string]any{"id": "cloud-1", "idempotency_key": "k-cloud-1"},
					map[string]any{"id": "cloud-2", "idempotency_key": "k-cloud-2"},
				},
				"cursor": 11,
				"more":   false,
				"engine": map[string]any{
					"replica_id": "replica-cloud",
					"version":    "0.4.0",
					"migration":  "20261019120000",
				},
			}})
		case strings.HasSuffix(r.URL.Path, "/sync/push"):
			stub.pushes++
			_ = json.NewDecoder(r.Body).Decode(&stub.pushed)
			writeJSON(w, stub.pushStatus, stub.pushBody)
		default:
			http.NotFound(w, r)
		}
	})

	stub.server = httptest.NewServer(mux)
	t.Cleanup(stub.server.Close)
	return stub
}

func relayOptions(local *localEngineStub, relay *relayStub) engineSyncOptions {
	return engineSyncOptions{
		relayURL:     relay.server.URL,
		engineID:     "eng-1",
		miosaKey:     "mks_test",
		localURL:     local.server.URL,
		localKey:     "admin-local",
		workspace:    "ws-1",
		requestLimit: 5 * time.Second,
	}
}

// The Engine compares the sender's schema migration with its own, so each side
// of the relay must carry the matching envelope: the cloud's when its changes
// are applied locally, and the local Engine's when the local ledger is pushed.
func TestEngineRelaySyncCarriesTheSenderEnvelope(t *testing.T) {
	local := newLocalEngineStub(t)
	relay := newRelayStub(t)

	var buf bytes.Buffer
	p := output.New(&buf, output.FormatText, false)

	if err := runEngineRelaySync(context.Background(), p, relayOptions(local, relay)); err != nil {
		t.Fatalf("runEngineRelaySync: %v", err)
	}

	appliedEngine, _ := local.applied["engine"].(map[string]any)
	if got := appliedEngine["migration"]; got != "20261019120000" {
		t.Errorf("local apply envelope migration = %v, want the cloud's 20261019120000", got)
	}
	if got := appliedEngine["replica_id"]; got != "replica-cloud" {
		t.Errorf("local apply envelope replica = %v, want replica-cloud", got)
	}
	if got := len(local.applied["mutations"].([]any)); got != 2 {
		t.Errorf("applied %d cloud mutations, want 2", got)
	}

	if local.ledgerHeader != "replica-cloud" {
		t.Errorf("local ledger read sent x-optimal-replica %q, want replica-cloud", local.ledgerHeader)
	}

	pushedEngine, _ := relay.pushed["engine"].(map[string]any)
	if got := pushedEngine["migration"]; got != "20261019130000" {
		t.Errorf("push envelope migration = %v, want the local Engine's 20261019130000", got)
	}
	if got := pushedEngine["version"]; got != "0.4.1" {
		t.Errorf("push envelope version = %v, want the local Engine's 0.4.1", got)
	}
	if got := pushedEngine["replica_id"]; got != "replica-local" {
		t.Errorf("push envelope replica = %v, want replica-local", got)
	}
	if got := relay.pushed["replica_id"]; got != "replica-local" {
		t.Errorf("push body replica_id = %v, want replica-local", got)
	}

	if !strings.Contains(relay.pullQuery, "since=0") || !strings.Contains(relay.pullQuery, "workspace=ws-1") {
		t.Errorf("pull query = %q, want workspace and since", relay.pullQuery)
	}
	if !strings.Contains(buf.String(), "ws-1") || !strings.Contains(buf.String(), "2 / 1") {
		t.Errorf("output does not report the workspace and the moved counts:\n%s", buf.String())
	}
}

func TestEngineRelayStatusOnlyReads(t *testing.T) {
	local := newLocalEngineStub(t)
	relay := newRelayStub(t)

	var buf bytes.Buffer
	p := output.New(&buf, output.FormatText, false)

	if err := showEngineRelayStatus(context.Background(), p, relayOptions(local, relay)); err != nil {
		t.Fatalf("showEngineRelayStatus: %v", err)
	}

	if local.appliedBatches != 0 {
		t.Errorf("status applied %d batches, want none", local.appliedBatches)
	}
	if relay.pushes != 0 {
		t.Errorf("status pushed %d times, want none", relay.pushes)
	}
	if local.ledgerHeader != "" {
		t.Errorf("status read the ledger, want status only")
	}
	if !strings.Contains(buf.String(), "9") || !strings.Contains(buf.String(), "4") {
		t.Errorf("output does not show both ledger sequences:\n%s", buf.String())
	}
}

// A refused push is the caller's to fix. The relay now answers with the
// Engine's own status and reason, so the CLI must surface them rather than a
// generic relay failure.
func TestEngineRelaySyncSurfacesVersionMismatch(t *testing.T) {
	local := newLocalEngineStub(t)
	relay := newRelayStub(t)
	relay.pushStatus = http.StatusConflict
	relay.pushBody = map[string]any{
		"error":   "version_mismatch",
		"message": "sender migration 20261019130000 is ahead of 20261019120000",
	}

	err := runEngineRelaySync(context.Background(), nil, relayOptions(local, relay))
	if err == nil {
		t.Fatal("expected a version mismatch error")
	}

	message := err.Error()
	for _, want := range []string{"409", "version_mismatch", "is ahead of"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q does not carry %q", message, want)
		}
	}
}
