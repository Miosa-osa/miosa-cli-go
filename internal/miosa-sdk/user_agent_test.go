package miosa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserAgentDefaultsToCLI(t *testing.T) {
	var got, gotClientID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		gotClientID = r.Header.Get("X-Miosa-Client-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL))
	_, _ = c.Computers.List(context.Background(), ListComputersInput{})
	if want := "miosa-cli/" + sdkVersion; got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}

	c = NewClient("k", WithBaseURL(srv.URL), WithUserAgent("miosa-cli/9.9.9"))
	_, _ = c.Computers.List(context.Background(), ListComputersInput{})
	if got != "miosa-cli/9.9.9" {
		t.Fatalf("User-Agent = %q after WithUserAgent", got)
	}
	if gotClientID != "miosa-cli/9.9.9" {
		t.Fatalf("X-Miosa-Client-Id = %q", gotClientID)
	}
}
