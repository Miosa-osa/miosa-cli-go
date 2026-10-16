package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func testRestClient(srv *httptest.Server) *Client {
	return &Client{
		RC: &restClient{
			baseURL: srv.URL,
			apiKey:  "test-key",
			http:    srv.Client(),
			api:     api.New(srv.URL, "test-key"),
		},
	}
}

func TestListSlotRevocations(t *testing.T) {
	var gotMethod, gotPath, gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"r1","revision":"1a2b3c","reason":"bad bake","inserted_at":"2026-10-12T10:00:00Z"}],"total":1}`))
	}))
	defer srv.Close()

	c := testRestClient(srv)

	revocations, err := c.ListSlotRevocations(context.Background())
	if err != nil {
		t.Fatalf("ListSlotRevocations: %v", err)
	}

	if gotMethod != http.MethodGet || gotPath != "/admin/slot-producer-revocations" {
		t.Fatalf("request %s %s, want GET /admin/slot-producer-revocations", gotMethod, gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if len(revocations) != 1 || revocations[0].Revision != "1a2b3c" || revocations[0].Reason != "bad bake" {
		t.Fatalf("revocations = %+v", revocations)
	}
}

func TestRevokeSlotRevisionSendsRevisionAndReason(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"r2","revision":"4d5e6f","reason":"mmds token absent"},"outcome":"revoked"}`))
	}))
	defer srv.Close()

	c := testRestClient(srv)

	revocation, outcome, err := c.RevokeSlotRevision(context.Background(), "  4d5e6f  ", "mmds token absent")
	if err != nil {
		t.Fatalf("RevokeSlotRevision: %v", err)
	}

	if gotMethod != http.MethodPost || gotPath != "/admin/slot-producer-revocations" {
		t.Fatalf("request %s %s, want POST /admin/slot-producer-revocations", gotMethod, gotPath)
	}
	if gotBody["revision"] != "4d5e6f" {
		t.Fatalf("revision body = %q, want the trimmed revision", gotBody["revision"])
	}
	if gotBody["reason"] != "mmds token absent" {
		t.Fatalf("reason body = %q", gotBody["reason"])
	}
	if outcome != "revoked" || revocation.Revision != "4d5e6f" {
		t.Fatalf("outcome %q revocation %+v", outcome, revocation)
	}
}

func TestRevokeSlotRevisionOmitsAnEmptyReason(t *testing.T) {
	var gotBody map[string]string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"r3","revision":"aaaa"},"outcome":"already_revoked"}`))
	}))
	defer srv.Close()

	c := testRestClient(srv)

	_, outcome, err := c.RevokeSlotRevision(context.Background(), "aaaa", "   ")
	if err != nil {
		t.Fatalf("RevokeSlotRevision: %v", err)
	}

	if _, present := gotBody["reason"]; present {
		t.Fatalf("reason should be omitted when blank, body = %+v", gotBody)
	}
	if outcome != "already_revoked" {
		t.Fatalf("outcome = %q", outcome)
	}
}

func TestListSlotRevocationsSurfacesAnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"owner_or_admin_required"}`))
	}))
	defer srv.Close()

	c := testRestClient(srv)

	if _, err := c.ListSlotRevocations(context.Background()); err == nil {
		t.Fatal("expected an error for a 403")
	}
}
