package commands_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyShow_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("policy show must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "policy", "show", "abc123")
	if err == nil {
		t.Fatal("expected unsupported native sandbox policy error")
	}
}

func TestPolicyShow_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("policy show must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "policy", "show", "abc123", "--output", "json")
	if err == nil {
		t.Fatalf("expected unsupported native sandbox policy error, output: %s", out)
	}
}

func TestPolicySet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("policy set must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	// Write a policy YAML file.
	policyFile := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(policyFile, []byte("default_effect: deny\nrules: []\n"), 0o644); err != nil {
		t.Fatalf("writing policy file: %v", err)
	}

	_, err := run(t, "policy", "set", "abc123", "--file", policyFile)
	if err == nil {
		t.Fatal("expected unsupported native sandbox policy error")
	}
}

func TestPolicySet_InvalidFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "policy", "set", "abc123", "--file", "/nonexistent/policy.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent policy file")
	}
}

func TestPolicySet_NoSandbox(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	policyFile := filepath.Join(t.TempDir(), "policy.yaml")
	os.WriteFile(policyFile, []byte("default_effect: allow\n"), 0o644)

	_, err := run(t, "policy", "set", "--file", policyFile)
	if err == nil {
		t.Fatal("expected error when no sandbox specified")
	}
}
