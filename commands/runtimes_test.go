package commands_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeRuntimesResponse is the frozen GET /sandboxes/runtimes contract: three
// runtimes, with SOMA gated off and its reason set.
func fakeRuntimesResponse() string {
	return `{"data":[
		{"id":"firecracker","runtime_profile":"standard","label":"Firecracker","description":"Full microVM. The default for every size and template.","default":true,"available":true,"unavailable_reason":null,"capabilities":{"browser":true,"network_egress":true,"persistence":true,"pause":true,"gpu":false,"max_shape":{"cpu_count":16,"memory_mb":32768,"disk_size_mb":81920}}},
		{"id":"soma","runtime_profile":"soma","label":"SOMA","description":"Fastest start for supported Linux sandbox images.","default":false,"available":false,"unavailable_reason":"GLOBAL_GATE_CLOSED","capabilities":{"browser":false,"network_egress":false,"persistence":false,"pause":true,"gpu":false,"max_shape":{"cpu_count":8,"memory_mb":16384,"disk_size_mb":40960}}},
		{"id":"auto","runtime_profile":"auto","label":"Auto","description":"Let the platform pick the best runtime for the request.","default":false,"available":true,"unavailable_reason":null,"capabilities":{"browser":true,"network_egress":true,"persistence":true,"pause":true,"gpu":false,"max_shape":{"cpu_count":16,"memory_mb":32768,"disk_size_mb":81920}}}
	]}`
}

func TestRuntimesListCommand_Table(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /sandboxes/runtimes": fakeRuntimesResponse(),
	})

	out, err := run(t, "runtimes", "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.find("GET", "/sandboxes/runtimes")) != 1 {
		t.Fatalf("expected one runtimes request, got: %+v", f.calls)
	}
	for _, want := range []string{"ID", "LABEL", "AVAILABLE", "REASON", "MAX SHAPE", "firecracker", "soma", "auto", "GLOBAL_GATE_CLOSED", "16 vCPU / 32768 MB / 81920 MB", "8 vCPU / 16384 MB / 40960 MB"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
	// firecracker is available -> "yes"; soma is gated -> "no".
	if !strings.Contains(out, "yes") || !strings.Contains(out, "no") {
		t.Errorf("availability column should read yes/no:\n%s", out)
	}
}

func TestRuntimesListCommand_JSON(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /sandboxes/runtimes": fakeRuntimesResponse(),
	})

	out, err := run(t, "runtimes", "list", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		Data []struct {
			ID                string  `json:"id"`
			RuntimeProfile    string  `json:"runtime_profile"`
			Available         bool    `json:"available"`
			UnavailableReason *string `json:"unavailable_reason"`
			Capabilities      struct {
				MaxShape struct {
					CPUCount int `json:"cpu_count"`
				} `json:"max_shape"`
			} `json:"capabilities"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if len(result.Data) != 3 {
		t.Fatalf("expected three runtimes, got %d: %s", len(result.Data), out)
	}
	first := result.Data[0]
	if first.ID != "firecracker" || first.RuntimeProfile != "standard" || !first.Available {
		t.Errorf("first runtime = %+v", first)
	}
	if first.UnavailableReason != nil {
		t.Errorf("available runtime must not carry a reason, got %q", *first.UnavailableReason)
	}
	if first.Capabilities.MaxShape.CPUCount != 16 {
		t.Errorf("firecracker max cpu = %d, want 16", first.Capabilities.MaxShape.CPUCount)
	}
	soma := result.Data[1]
	if soma.ID != "soma" || soma.Available {
		t.Errorf("soma runtime = %+v", soma)
	}
	if soma.UnavailableReason == nil || *soma.UnavailableReason != "GLOBAL_GATE_CLOSED" {
		t.Errorf("soma reason = %v, want GLOBAL_GATE_CLOSED", soma.UnavailableReason)
	}
}
