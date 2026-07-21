package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogCommand_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/compute/catalog" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakeComputeCatalogResponse())
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "catalog", "--product", "sandbox", "--template", "nextjs")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "sandbox") || !strings.Contains(out, "nextjs") {
		t.Fatalf("expected sandbox nextjs row, got: %s", out)
	}
	if !strings.Contains(out, "fast_ready") {
		t.Fatalf("expected readiness state, got: %s", out)
	}
	if strings.Contains(out, "miosa-desktop") {
		t.Fatalf("desktop row should have been filtered out, got: %s", out)
	}
}

func TestCatalogCommand_JSONOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/compute/catalog" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fakeComputeCatalogResponse())
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	out, err := run(t, "catalog", "--state", "fast_ready", "--output", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result struct {
		Data []struct {
			Product string `json:"product"`
			State   string `json:"state"`
		} `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %s", err, out)
	}
	if result.Meta.Total != 1 || len(result.Data) != 1 {
		t.Fatalf("expected one fast_ready row, got: %+v", result)
	}
	if result.Data[0].Product != "sandbox" || result.Data[0].State != "fast_ready" {
		t.Fatalf("unexpected filtered row: %+v", result.Data[0])
	}
}

func fakeComputeCatalogResponse() []byte {
	return []byte(`{
  "data": {
    "generated_at": "2026-06-22T00:00:00Z",
    "products": [
      {
        "id": "sandbox",
        "default_template": "miosa-sandbox-prod-1",
        "templates": [
          {
            "id": "nextjs",
            "artifact_readiness": [
              {
                "size": "medium",
                "state": "fast_ready",
                "checked_nodes": 10,
                "ready_nodes": 10,
                "cold_boot_nodes": 0,
                "missing_nodes": 0
              }
            ]
          }
        ]
      },
      {
        "id": "computer",
        "default_template": "miosa-desktop",
        "templates": [
          {
            "id": "miosa-desktop",
            "artifact_readiness": [
              {
                "size": "medium",
                "state": "cold_boot_only",
                "checked_nodes": 10,
                "ready_nodes": 0,
                "cold_boot_nodes": 10,
                "missing_nodes": 0
              }
            ]
          }
        ]
      }
    ]
  }
}`)
}
