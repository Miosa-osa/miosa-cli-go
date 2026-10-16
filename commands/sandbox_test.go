package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const sandboxResizeID = "11111111-1111-1111-1111-111111111111"

func TestSandboxResize_RequiresAFlag(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "sandbox", "resize", sandboxResizeID)
	if err == nil {
		t.Fatal("expected a usage error when neither --cpu nor --memory is given")
	}
	if commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("exit code = %d, want %d (usage)", commands.ExitCode(err), commands.ExitUsage)
	}
	if called {
		t.Error("resize must not call the API without a field to change")
	}
}

func TestSandboxResize_SendsOnlyGivenFields(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + sandboxResizeID + `","applied_live":true,"requires_restart":false,"changes":{"cpu":"applied_live"},"resources":{"cpu":4,"memory_mb":4096},"capabilities":{}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "sandbox", "resize", sandboxResizeID, "--cpu", "4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/sandboxes/"+sandboxResizeID+"/resources" {
		t.Fatalf("request %s %s, want PATCH /sandboxes/%s/resources", gotMethod, gotPath, sandboxResizeID)
	}
	if _, ok := gotBody["memory_mb"]; ok {
		t.Errorf("memory_mb must not be sent when only --cpu is given: %v", gotBody)
	}
	if gotBody["cpu_count"] != float64(4) {
		t.Errorf("cpu_count = %v, want 4", gotBody["cpu_count"])
	}
	if !strings.Contains(out, "applied live") {
		t.Errorf("output %q missing 'applied live'", out)
	}
}

func TestSandboxResize_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + sandboxResizeID + `","applied_live":true,"requires_restart":false,"changes":{"cpu":"applied_live"},"resources":{"cpu":4,"memory_mb":4096},"capabilities":{}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "sandbox", "resize", sandboxResizeID, "--cpu", "4", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(dataJSON(out), &result); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out)
	}
	if result["applied_live"] != true {
		t.Errorf("applied_live = %v, want true", result["applied_live"])
	}
}

func TestSandboxResize_Recreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"` + sandboxResizeID + `","applied_live":false,"requires_restart":false,"changes":{"memory_mb":"recreated"},"resources":{"cpu":2,"memory_mb":8192},"capabilities":{},"restarted":true,"mode":"stop_start","new_sandbox":"new-id","source_sandbox":"` + sandboxResizeID + `"}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "sandbox", "resize", sandboxResizeID, "--memory", "8192")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "recreated") || !strings.Contains(out, "new-id") {
		t.Errorf("output %q should report the recreation and the new id", out)
	}
}

func TestSandboxResize_ConflictPrintsPlainSentence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"RESIZE_REQUIRES_RESTART"}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "sandbox", "resize", sandboxResizeID, "--memory", "8192")
	if err == nil {
		t.Fatal("expected an error for a 409 refusal")
	}
	if commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("exit code = %d, want %d (conflict)", commands.ExitCode(err), commands.ExitConflict)
	}
	if !strings.Contains(out, "restart the sandbox") {
		t.Errorf("output %q should explain that a restart is needed", out)
	}
	if !strings.Contains(err.Error(), "RESIZE_REQUIRES_RESTART") {
		t.Errorf("error %q should keep the server code", err.Error())
	}
}

func TestSandboxResources_PrintsCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sandboxes/"+sandboxResizeID+"/resources" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + sandboxResizeID + `","resources":{"cpu":2,"memory_mb":4096},"capabilities":{"live_cpu":true,"live_cpu_max":2,"cpu_mechanism":"cgroup_quota","live_memory":false,"memory_reason":"requires_restart","runtime_engine":"firecracker"}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "sandbox", "resources", sandboxResizeID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "vCPU:    2") {
		t.Errorf("output %q missing vCPU line", out)
	}
	if !strings.Contains(out, "4096 MB") {
		t.Errorf("output %q missing memory line", out)
	}
	if !strings.Contains(out, "up to 2 vCPU") {
		t.Errorf("output %q missing live vCPU capability", out)
	}
	if !strings.Contains(out, "a restart applies memory changes") {
		t.Errorf("output %q missing memory restart capability", out)
	}
}

func TestSandboxResources_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + sandboxResizeID + `","resources":{"cpu":2,"memory_mb":4096},"capabilities":{"live_cpu":true}}`))
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "sandbox", "resources", sandboxResizeID, "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(dataJSON(out), &result); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, out)
	}
	res, _ := result["resources"].(map[string]any)
	if res["cpu"] != float64(2) {
		t.Errorf("resources.cpu = %v, want 2", res["cpu"])
	}
}
