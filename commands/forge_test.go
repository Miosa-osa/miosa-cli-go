package commands_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func forgeRepoJSON() map[string]interface{} {
	return map[string]interface{}{
		"id": "repo-1", "name": "Platform", "slug": "platform", "default_branch": "main",
		"visibility": "private", "state": "active", "clone_ready": true,
		"clone_url": "https://forge.miosa.ai/acme/platform.git", "project_ids": []interface{}{},
		"created_at": "2026-08-14T12:00:00Z", "updated_at": "2026-08-14T12:00:00Z",
	}
}

func TestForgeRepoPublicCommands(t *testing.T) {
	var idempotencyHeaders []string
	var createVisibility string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/forge/repositories":
			idempotencyHeaders = append(idempotencyHeaders, r.Header.Get("Idempotency-Key"))
			var payload struct {
				Visibility string `json:"visibility"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode create payload: %v", err)
			}
			createVisibility = payload.Visibility
			w.WriteHeader(http.StatusCreated)
			publicRepository := forgeRepoJSON()
			publicRepository["visibility"] = "public"
			json.NewEncoder(w).Encode(map[string]interface{}{"data": publicRepository})
		case r.Method == http.MethodGet && r.URL.Path == "/forge/repositories":
			json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{forgeRepoJSON()}})
		case r.Method == http.MethodGet && r.URL.Path == "/forge/repositories/repo-1":
			json.NewEncoder(w).Encode(map[string]interface{}{"data": forgeRepoJSON()})
		case r.Method == http.MethodDelete && r.URL.Path == "/forge/repositories/repo-1":
			w.Header().Set("X-Forge-Operation-Id", "repo-1")
			idempotencyHeaders = append(idempotencyHeaders, r.Header.Get("Idempotency-Key"))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cleanup := setupEnv(t, server)
	defer cleanup()

	created, err := run(t, "forge", "repo", "create", "Platform", "--visibility", "public", "--json", "--idempotency-key", "create-1")
	if err != nil || !strings.Contains(created, `"slug": "platform"`) {
		t.Fatalf("create: %v %s", err, created)
	}
	listed, err := run(t, "forge", "repo", "list")
	if err != nil || !strings.Contains(listed, "VISIBILITY") || !strings.Contains(listed, "platform") {
		t.Fatalf("list: %v %s", err, listed)
	}
	shown, err := run(t, "forge", "repo", "show", "repo-1", "--json")
	if err != nil || !strings.Contains(shown, `"id": "repo-1"`) {
		t.Fatalf("show: %v %s", err, shown)
	}
	deleted, err := run(t, "forge", "repo", "delete", "repo-1", "--yes", "--json")
	if err != nil || !strings.Contains(deleted, `"status": "deleted"`) {
		t.Fatalf("delete: %v %s", err, deleted)
	}
	if len(idempotencyHeaders) != 2 || idempotencyHeaders[0] != "create-1" || idempotencyHeaders[1] != "" {
		t.Fatalf("unexpected idempotency headers: %#v", idempotencyHeaders)
	}
	if createVisibility != "public" {
		t.Fatalf("create sent visibility %q, want public", createVisibility)
	}
}

func TestForgeRepoDeleteRequiresExplicitConfirmation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request must not be sent without confirmation: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	cleanup := setupEnv(t, server)
	defer cleanup()

	commands.ResetForTest()
	root := commands.Root()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetIn(strings.NewReader("repo-1\n"))
	root.SetArgs([]string{"forge", "repo", "delete", "repo-1"})
	err := root.Execute()
	if err == nil {
		t.Fatal("piped confirmation must fail without --yes")
	}
}

func TestForgeHelpOmitsUnsupportedCommands(t *testing.T) {
	out, err := run(t, "forge", "repo", "--help")
	if err != nil {
		t.Fatal(err)
	}
	for _, unsupported := range []string{"clone", "pull-request", "check", "release", "sync"} {
		if strings.Contains(out, unsupported) {
			t.Fatalf("help advertises unsupported command %q: %s", unsupported, out)
		}
	}
}

func TestForgeJSONFailureIsStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"code": "FORGE_STORAGE_UNAVAILABLE", "detail": "offline", "retryable": true}})
	}))
	defer server.Close()
	cleanup := setupEnv(t, server)
	defer cleanup()

	out, err := run(t, "forge", "repo", "list", "--json")
	if err == nil || !strings.Contains(out, `"ok": false`) || !strings.Contains(out, `"code": "FORGE_STORAGE_UNAVAILABLE"`) {
		t.Fatalf("got err=%v output=%s", err, out)
	}
}

func TestForgeCommandContractMatchesBothDistributions(t *testing.T) {
	goContract, err := os.ReadFile(filepath.Join("..", "testdata", "forge-cli-command-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, contractPath := range []string{
		filepath.Join("..", "..", "..", "tests", "fixtures", "forge-cli-command-contract.json"),
		filepath.Join("..", "..", "..", "miosa-cli", "test", "fixtures", "forge-cli-command-contract.json"),
	} {
		distributionContract, readErr := os.ReadFile(contractPath)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(goContract, distributionContract) {
			t.Fatalf("Forge CLI command contract drifted from %s", contractPath)
		}
	}
	var contract struct {
		Commands map[string]struct {
			Flags []string `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(goContract, &contract); err != nil {
		t.Fatal(err)
	}
	commands.ResetForTest()
	forge, _, err := commands.Root().Find([]string{"forge", "repo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(forge.Commands()) != len(contract.Commands) {
		t.Fatalf("got %d commands, contract has %d", len(forge.Commands()), len(contract.Commands))
	}
	for name, expected := range contract.Commands {
		command, _, err := forge.Find([]string{name})
		if err != nil {
			t.Fatalf("missing command %s: %v", name, err)
		}
		for _, flag := range expected.Flags {
			if command.Flags().Lookup(strings.TrimPrefix(flag, "--")) == nil && command.InheritedFlags().Lookup(strings.TrimPrefix(flag, "--")) == nil {
				t.Errorf("command %s is missing %s", name, flag)
			}
		}
	}
}
