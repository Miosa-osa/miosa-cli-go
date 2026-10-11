package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// browserTestID is a UUID shaped sandbox id so ResolveSandbox passes it through
// without a by-name lookup.
const browserTestID = "11111111-1111-1111-1111-111111111111"

// browserAction records the browser action payload and answers with a small
// daemon result.
type browserActionBody struct {
	Actions []struct {
		Type string  `json:"type"`
		URL  string  `json:"url"`
		Text string  `json:"text"`
		Key  string  `json:"key"`
		X    float64 `json:"x"`
		Y    float64 `json:"y"`
	} `json:"actions"`
}

func TestBrowserGotoCommand(t *testing.T) {
	var got browserActionBody
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "browser", "goto", "https://example.com", "--sandbox", browserTestID); err != nil {
		t.Fatalf("browser goto: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sandboxes/"+browserTestID+"/browser/actions" {
		t.Fatalf("request %s %s, want POST /sandboxes/%s/browser/actions", gotMethod, gotPath, browserTestID)
	}
	if len(got.Actions) != 1 {
		t.Fatalf("actions = %#v, want one action", got.Actions)
	}
	if a := got.Actions[0]; a.Type != "navigate" || a.URL != "https://example.com" {
		t.Fatalf("action = %#v, want navigate https://example.com", a)
	}
}

func TestBrowserClickCommand(t *testing.T) {
	var got browserActionBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "browser", "click", "40", "80", "--sandbox", browserTestID); err != nil {
		t.Fatalf("browser click: %v", err)
	}
	if len(got.Actions) != 1 {
		t.Fatalf("actions = %#v, want one action", got.Actions)
	}
	if a := got.Actions[0]; a.Type != "click" || a.X != 40 || a.Y != 80 {
		t.Fatalf("action = %#v, want click x=40 y=80", a)
	}
}

func TestBrowserTypeCommand(t *testing.T) {
	var got browserActionBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "browser", "type", "hello world", "--sandbox", browserTestID); err != nil {
		t.Fatalf("browser type: %v", err)
	}
	if a := got.Actions[0]; a.Type != "type" || a.Text != "hello world" {
		t.Fatalf("action = %#v, want type hello world", a)
	}
}

func TestBrowserKeyCommand(t *testing.T) {
	var got browserActionBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "browser", "key", "ctrl+a", "--sandbox", browserTestID); err != nil {
		t.Fatalf("browser key: %v", err)
	}
	if a := got.Actions[0]; a.Type != "key" || a.Key != "ctrl+a" {
		t.Fatalf("action = %#v, want key ctrl+a", a)
	}
}

func TestBrowserRevokeUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("server must not be called on a usage error: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	// Neither flag.
	_, err := run(t, "browser", "revoke", "--sandbox", browserTestID)
	if err == nil {
		t.Fatal("expected a usage error when neither --all nor --share-id is given")
	}
	if code := commands.ExitCode(err); code != commands.ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage)", code, commands.ExitUsage)
	}

	// Both flags.
	_, err = run(t, "browser", "revoke", "--all", "--share-id", "shr_1", "--sandbox", browserTestID)
	if err == nil {
		t.Fatal("expected a usage error when both --all and --share-id are given")
	}
	if code := commands.ExitCode(err); code != commands.ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage)", code, commands.ExitUsage)
	}
}

func TestBrowserRevokeAllCommand(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"revoked":true,"epoch":4}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	if _, err := run(t, "browser", "revoke", "--all", "--sandbox", browserTestID); err != nil {
		t.Fatalf("browser revoke --all: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/sandboxes/"+browserTestID+"/browser/share" {
		t.Fatalf("request %s %s, want DELETE /sandboxes/%s/browser/share", gotMethod, gotPath, browserTestID)
	}
}

func TestBrowserStatusCommand(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"state":"sandbox_not_running","epoch":0,"viewport":{"width":0,"height":0}}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "browser", "status", "--sandbox", browserTestID)
	if err != nil {
		t.Fatalf("browser status: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/sandboxes/"+browserTestID+"/browser" {
		t.Fatalf("request %s %s, want GET /sandboxes/%s/browser", gotMethod, gotPath, browserTestID)
	}
	if !strings.Contains(out, "sandbox_not_running") {
		t.Fatalf("output = %q, want the state printed", out)
	}
}
