package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOrgLimitsIsTheSameAsLimits(t *testing.T) {
	routes := map[string]any{"GET /limits": `{"data":{"organization":{"name":"Acme","type":"team"},"can_start":true,"credits":{"available_cents":500},"concurrency":{"running":1,"limit":5}}}`}
	fakeRoutes(t, routes)
	a, err := run(t, "limits")
	if err != nil {
		t.Fatal(err)
	}
	fakeRoutes(t, routes)
	b, err := run(t, "org", "limits")
	if err != nil {
		t.Fatalf("org limits: %v", err)
	}
	if a != b || !strings.Contains(b, "Acme") {
		t.Fatalf("org limits differs from limits:\n%s\n---\n%s", a, b)
	}
}

func TestListJSONSaysWhetherThereIsMore(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /sandboxes": `{"data":[{"id":"a","name":"one"}],"meta":{"total":120,"page":1,"limit":1,"has_next_page":true}}`,
	})
	out, err := run(t, "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(dataJSONWhole(out), &v); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if v["has_more"] != true || v["total"] != float64(120) {
		t.Fatalf("has_more/total = %v / %v", v["has_more"], v["total"])
	}
	text, err := run(t, "list")
	if err != nil || !strings.Contains(text, "showing 1 of 120, use --all") {
		t.Fatalf("text = %q %v", text, err)
	}
}

func dataJSONWhole(out string) []byte { return []byte(out) }

func TestExecStopsWaitingAtItsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/commands") || strings.Contains(r.URL.Path, "/exec") {
			time.Sleep(12 * time.Second) // a background child keeps the output open
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"abc","name":"wo23"}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()
	if testing.Short() {
		t.Skip("waits for the client deadline")
	}
	start := time.Now()
	_, err := run(t, "exec", "abc", "--timeout", "2", "--", "sh", "-c", "sleep 100000 &")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if took := time.Since(start); took > 11*time.Second {
		t.Fatalf("took %s, must stop near the 2 s timeout plus headroom", took)
	}
}
