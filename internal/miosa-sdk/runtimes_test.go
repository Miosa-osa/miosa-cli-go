package miosa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeProfileFor(t *testing.T) {
	cases := map[string]string{
		"firecracker": "standard",
		"soma":        "soma",
		"auto":        "auto",
		"standard":    "standard", // already a profile: passed through
		"":            "",
	}
	for in, want := range cases {
		if got := RuntimeProfileFor(in); got != want {
			t.Errorf("RuntimeProfileFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCreateMapsRuntimeSelector(t *testing.T) {
	var raw map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sandboxes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"s1","name":"box"}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL))
	if _, err := c.Sandboxes.Create(context.Background(), CreateSandboxInput{Runtime: "soma"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if raw["runtime_profile"] != "soma" {
		t.Fatalf("runtime_profile = %v, want soma (body: %#v)", raw["runtime_profile"], raw)
	}
	if _, ok := raw["runtime"]; ok {
		t.Fatalf("the selector must not be sent: %#v", raw)
	}
}

func TestRuntimesList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/sandboxes/runtimes" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"firecracker","runtime_profile":"standard","label":"Firecracker","default":true,"available":true,"unavailable_reason":null,"capabilities":{"max_shape":{"cpu_count":16,"memory_mb":32768,"disk_size_mb":81920}}},{"id":"soma","runtime_profile":"soma","label":"SOMA","available":false,"unavailable_reason":"GLOBAL_GATE_CLOSED","capabilities":{"max_shape":{"cpu_count":8,"memory_mb":16384,"disk_size_mb":40960}}}]}`))
	}))
	defer srv.Close()

	c := NewClient("k", WithBaseURL(srv.URL))
	got, err := c.Runtimes.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "firecracker" || !got[0].Available || got[0].UnavailableReason != nil {
		t.Fatalf("firecracker = %+v", got[0])
	}
	if got[0].Capabilities.MaxShape.MemoryMB != 32768 {
		t.Fatalf("max shape memory = %d", got[0].Capabilities.MaxShape.MemoryMB)
	}
	if got[1].Available || got[1].UnavailableReason == nil || *got[1].UnavailableReason != "GLOBAL_GATE_CLOSED" {
		t.Fatalf("soma = %+v", got[1])
	}
}
