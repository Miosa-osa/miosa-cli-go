package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

const resizeSbx = "11111111-1111-1111-1111-111111111111"

func TestSandboxResources_Get(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + resizeSbx + `","resources":{"cpu":2,"memory_mb":4096},"capabilities":{"live_cpu":true,"live_cpu_max":2,"cpu_mechanism":"cgroup_quota","live_memory":false,"memory_reason":"requires_restart","runtime_engine":"firecracker"}}`))
	}))
	defer srv.Close()

	c := &Client{API: api.New(srv.URL, "key")}

	info, err := c.SandboxResources(context.Background(), resizeSbx)
	if err != nil {
		t.Fatalf("SandboxResources: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/sandboxes/"+resizeSbx+"/resources" {
		t.Fatalf("request %s %s, want GET /sandboxes/%s/resources", gotMethod, gotPath, resizeSbx)
	}
	if info.Resources.CPU != 2 || info.Resources.MemoryMB != 4096 {
		t.Fatalf("resources = %+v", info.Resources)
	}
	if info.Capabilities["live_cpu"] != true {
		t.Fatalf("capabilities = %+v", info.Capabilities)
	}
	if info.Capabilities["runtime_engine"] != "firecracker" {
		t.Fatalf("runtime_engine = %v", info.Capabilities["runtime_engine"])
	}
}

func TestResizeSandbox_SendsOnlyGivenFields(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + resizeSbx + `","applied_live":true,"requires_restart":false,"changes":{"cpu":"applied_live"},"resources":{"cpu":4,"memory_mb":4096},"capabilities":{}}`))
	}))
	defer srv.Close()

	c := &Client{API: api.New(srv.URL, "key")}

	cpu := 4
	out, err := c.ResizeSandbox(context.Background(), resizeSbx, SandboxResizeInput{CPUCount: &cpu})
	if err != nil {
		t.Fatalf("ResizeSandbox: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/sandboxes/"+resizeSbx+"/resources" {
		t.Fatalf("request %s %s, want PATCH /sandboxes/%s/resources", gotMethod, gotPath, resizeSbx)
	}
	if _, ok := gotBody["memory_mb"]; ok {
		t.Fatalf("memory_mb must be omitted when only cpu is set: %v", gotBody)
	}
	if gotBody["cpu_count"] != float64(4) {
		t.Fatalf("cpu_count = %v, want 4", gotBody["cpu_count"])
	}
	if !out.AppliedLive {
		t.Fatalf("applied_live = false, want true")
	}
}

func TestResizeSandbox_Recreated202(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"` + resizeSbx + `","applied_live":false,"requires_restart":false,"changes":{"memory_mb":"recreated"},"resources":{"cpu":2,"memory_mb":8192},"capabilities":{},"restarted":true,"mode":"stop_start","new_sandbox":"new-id","source_sandbox":"` + resizeSbx + `"}`))
	}))
	defer srv.Close()

	c := &Client{API: api.New(srv.URL, "key")}
	memory := 8192
	out, err := c.ResizeSandbox(context.Background(), resizeSbx, SandboxResizeInput{MemoryMB: &memory})
	if err != nil {
		t.Fatalf("ResizeSandbox: %v", err)
	}
	if !out.Restarted || out.NewSandbox != "new-id" || out.SourceSandbox != resizeSbx || out.Mode != "stop_start" {
		t.Fatalf("outcome = %+v", out)
	}
}

func TestResizeSandbox_409Code(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"RESIZE_REQUIRES_RESTART"}}`))
	}))
	defer srv.Close()

	c := &Client{API: api.New(srv.URL, "key")}
	memory := 8192
	_, err := c.ResizeSandbox(context.Background(), resizeSbx, SandboxResizeInput{MemoryMB: &memory})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != "RESIZE_REQUIRES_RESTART" || ae.Status != http.StatusConflict {
		t.Fatalf("err = %v, want *api.Error 409 RESIZE_REQUIRES_RESTART", err)
	}
}

func TestResizeSandbox_422PlanCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"PLAN_CAP_EXCEEDED","field":"memory_mb","requested":32768,"cap":16384}}`))
	}))
	defer srv.Close()

	c := &Client{API: api.New(srv.URL, "key")}
	memory := 32768
	_, err := c.ResizeSandbox(context.Background(), resizeSbx, SandboxResizeInput{MemoryMB: &memory})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != "PLAN_CAP_EXCEEDED" || ae.Status != http.StatusUnprocessableEntity {
		t.Fatalf("err = %v, want *api.Error 422 PLAN_CAP_EXCEEDED", err)
	}
	if !api.IsCode(err, "PLAN_CAP_EXCEEDED") {
		t.Fatalf("IsCode(PLAN_CAP_EXCEEDED) = false")
	}
}
