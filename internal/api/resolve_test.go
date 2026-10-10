package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const id1 = "8ba07e95-5085-4600-b758-60b6951c5786"

func TestRewritePath(t *testing.T) {
	var lookups int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&lookups, 1)
		switch r.URL.Path {
		case "/sandboxes/by-name/my-box":
			w.Write([]byte(`{"id":"` + id1 + `"}`))
		case "/computers":
			w.Write([]byte(`{"data":[{"id":"` + id1 + `","name":"desk"},{"id":"22222222-2222-2222-2222-222222222222","name":"other"}]}`))
		case "/deployments":
			w.Write([]byte(`{"data":[{"id":"` + id1 + `","name":"My App","slug":"my-app"}]}`))
		case "/workspaces":
			w.Write([]byte(`{"data":[{"id":"` + id1 + `","name":"Acme","slug":"acme"}]}`))
		case "/agents":
			w.Write([]byte(`{"data":[{"id":"` + id1 + `","name":"reviewer"}]}`))
		case "/sandboxes/by-name/enveloped":
			w.Write([]byte(`{"data":{"id":"` + id1 + `"}}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"error":{"code":"NOT_FOUND"}}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "k")
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	c.Resolver = &Resolver{Aliases: map[string]string{"current": "my-box", "self": "", "uuidalias": id1}}
	ctx := context.Background()

	cases := []struct{ in, want string }{
		{"/sandboxes/my-box/exec", "/sandboxes/" + id1 + "/exec"},
		{"/sandboxes/my-box", "/sandboxes/" + id1},
		{"/sandboxes/enveloped/files?path=%2Fa", "/sandboxes/" + id1 + "/files?path=%2Fa"},
		{"/sandboxes/current/ports", "/sandboxes/" + id1 + "/ports"},
		{"/sandboxes/" + id1 + "/exec", "/sandboxes/" + id1 + "/exec"},
		{"/sandboxes", "/sandboxes"},
		{"/sandboxes?limit=5", "/sandboxes?limit=5"},
		{"/sandboxes/batch", "/sandboxes/batch"},
		{"/sandboxes/batches/abc", "/sandboxes/batches/abc"},
		{"/sandboxes/by-name/x", "/sandboxes/by-name/x"},
		{"/sandboxes/run", "/sandboxes/run"},
		{"/computers/desk/urls", "/computers/" + id1 + "/urls"},
		{"/computers/" + id1, "/computers/" + id1},
		{"/computers/ghost", "/computers/ghost"},
		{"/computers", "/computers"},
		{"/agents/reviewer/versions", "/agents/" + id1 + "/versions"},
		{"/agents/harnesses", "/agents/harnesses"},
		{"/agents/defaults", "/agents/defaults"},
		{"/agents/" + id1, "/agents/" + id1},
		{"/agents", "/agents"},
		{"/deployments/my-app/logs", "/deployments/" + id1 + "/logs"},
		{"/deployments/My App", "/deployments/" + id1},
		{"/deployments/drop/sessions", "/deployments/drop/sessions"},
		{"/deployments/nope", "/deployments/nope"},
		{"/workspaces/acme/stats?x=1", "/workspaces/" + id1 + "/stats?x=1"},
		{"/workflows/runs/abc", "/workflows/runs/abc"},
		{"/sandboxes/unknown-name/exec", "/sandboxes/unknown-name/exec"}, // 404 by name: left for the server to judge
	}
	for _, tc := range cases {
		got, err := c.RewritePath(ctx, tc.in)
		if err != nil || got != tc.want {
			t.Errorf("RewritePath(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
	before := atomic.LoadInt32(&lookups)
	if _, err := c.RewritePath(ctx, "/sandboxes/my-box/exec"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&lookups) != before {
		t.Error("a resolved name must be served from the cache")
	}
	if _, err := c.RewritePath(ctx, "/sandboxes/self/exec"); err == nil {
		t.Error("an unset alias must be an error")
	}
}

func TestUnknownAgentNameIsNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[]}`)) }))
	defer srv.Close()
	c := New(srv.URL, "k")
	_, err := c.RewritePath(context.Background(), "/agents/ghost")
	if !IsStatus(err, 404) || !strings.Contains(err.Error(), "no agent named") {
		t.Fatalf("err = %v", err)
	}
}
