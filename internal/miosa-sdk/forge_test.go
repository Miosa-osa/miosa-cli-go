package miosa

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func forgeRepositoryFixture() ForgeRepository {
	return ForgeRepository{
		ID: "repo-1", Name: "Platform", Slug: "platform", DefaultBranch: "main",
		Visibility: "private", State: "active", CloneReady: true, CloneURL: stringPointer("https://forge.miosa.ai/acme/platform.git"), ProjectIDs: []string{},
		CreatedAt: "2026-08-14T12:00:00Z", UpdatedAt: "2026-08-14T12:00:00Z",
	}
}

func TestForgeRepositoryCRUDContract(t *testing.T) {
	apiKey := "msk_secret_never_leak"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer "+apiKey {
			t.Errorf("missing bearer authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		repository := forgeRepositoryFixture()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/forge/repositories":
			if r.Header.Get("Idempotency-Key") != "create-1" {
				t.Errorf("missing create idempotency key")
			}
			var body CreateForgeRepositoryInput
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Name != "Platform" {
				t.Errorf("unexpected create body")
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"data": repository})
		case r.Method == http.MethodGet && r.URL.Path == "/forge/repositories":
			json.NewEncoder(w).Encode(map[string]interface{}{"data": []ForgeRepository{repository}})
		case r.Method == http.MethodGet && r.URL.Path == "/forge/repositories/repo-1":
			json.NewEncoder(w).Encode(map[string]interface{}{"data": repository})
		case r.Method == http.MethodDelete && r.URL.Path == "/forge/repositories/repo-1":
			w.Header().Set("X-Forge-Operation-Id", "repo-1")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(apiKey, WithBaseURL(server.URL), WithMaxRetries(0))
	ctx := context.Background()
	created, err := client.Forge.Create(ctx, CreateForgeRepositoryInput{Name: "Platform", IdempotencyKey: "create-1"})
	if err != nil || created.ID != "repo-1" {
		t.Fatalf("create failed: %#v %v", created, err)
	}
	listed, err := client.Forge.List(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list failed: %#v %v", listed, err)
	}
	shown, err := client.Forge.Get(ctx, "repo-1")
	if err != nil || shown.Slug != "platform" {
		t.Fatalf("show failed: %#v %v", shown, err)
	}
	if _, err := client.Forge.Delete(ctx, "repo-1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if requests != 4 {
		t.Fatalf("got %d requests, want 4", requests)
	}
}

func stringPointer(value string) *string { return &value }

func TestForgeTypedErrorDoesNotLeakAuthorization(t *testing.T) {
	apiKey := "msk_secret_never_leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"FORGE_DISABLED","message":"not enabled"}}`))
	}))
	defer server.Close()

	client := NewClient(apiKey, WithBaseURL(server.URL), WithMaxRetries(0))
	_, err := client.Forge.List(context.Background())
	var unavailable *ForgeUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("got %T, want ForgeUnavailableError", err)
	}
	if strings.Contains(err.Error(), apiKey) || strings.Contains(string(unavailable.Body), apiKey) {
		t.Fatal("error leaked API key")
	}
}

func TestForgeScopesGenericPolicyErrorToRepositoryService(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":{"code":"POLICY_VIOLATION","message":"private only"}}`))
	}))
	defer server.Close()

	client := NewClient("msk_test", WithBaseURL(server.URL), WithMaxRetries(0))
	_, err := client.Forge.Create(context.Background(), CreateForgeRepositoryInput{Name: "Platform"})
	var policy *ForgePolicyViolationError
	if !errors.As(err, &policy) {
		t.Fatalf("got %T, want ForgePolicyViolationError", err)
	}
}

func TestForgeRejectsMalformedRepositoryContract(t *testing.T) {
	repository := forgeRepositoryFixture()
	repository.Visibility = "secret"
	if err := validateForgeRepository(&repository); err == nil {
		t.Fatal("expected invalid visibility to fail contract validation")
	}
}

func TestForgeAcceptsPublicRepositoryContract(t *testing.T) {
	repository := forgeRepositoryFixture()
	repository.Visibility = "public"
	if err := validateForgeRepository(&repository); err != nil {
		t.Fatalf("expected public visibility to match contract: %v", err)
	}
}
