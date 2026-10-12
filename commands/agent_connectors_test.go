package commands_test

import (
	"strings"
	"testing"
)

func TestMCPServerListAndAdd(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /mcp-servers":  `{"data":[{"id":"srv_1","name":"github","transport":"http","status":"reachable"}],"total":1}`,
		"POST /mcp-servers": `{"data":{"id":"srv_1","name":"github","transport":"http"}}`,
	})

	out, err := run(t, "mcp", "server", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github") {
		t.Fatalf("out = %q", out)
	}
	if len(f.find("GET", "/mcp-servers")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}

	if _, err := run(t, "mcp", "server", "add", "github",
		"--transport", "http",
		"--url", "https://mcp.example.com",
		"--secret-header", "Authorization",
		"--secret", "token=ghp_x",
	); err != nil {
		t.Fatal(err)
	}
	calls := f.find("POST", "/mcp-servers")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	body := calls[0].Body
	if body["name"] != "github" || body["transport"] != "http" || body["url"] != "https://mcp.example.com" {
		t.Fatalf("body = %+v", body)
	}
	headers, _ := body["secret_headers"].([]any)
	if len(headers) != 1 || headers[0] != "Authorization" {
		t.Fatalf("secret_headers = %+v", body["secret_headers"])
	}
	secrets, _ := body["secrets"].(map[string]any)
	if secrets["token"] != "ghp_x" {
		t.Fatalf("secrets = %+v", body["secrets"])
	}
}

func TestMCPServerTestAndDelete(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /mcp-servers/srv_1/test": `{"data":{"status":"reachable","tools":[{"name":"search"}]}}`,
		"DELETE /mcp-servers/srv_1":    `{"data":{"id":"srv_1"},"deleted":true}`,
	})

	out, err := run(t, "mcp", "server", "test", "srv_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "reachable") {
		t.Fatalf("out = %q", out)
	}
	if _, err := run(t, "mcp", "server", "rm", "srv_1", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("DELETE", "/mcp-servers/srv_1")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestMCPCatalogListAndInstall(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /mcp-catalog":                 `{"data":[{"slug":"github","name":"GitHub","category":"dev","added":[]}],"total":1,"categories":["dev"],"excluded":[]}`,
		"POST /mcp-catalog/github/install": `{"data":{"id":"srv_1","name":"github"},"secrets":["token"]}`,
	})

	out, err := run(t, "mcp", "catalog", "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "github") {
		t.Fatalf("out = %q", out)
	}

	if _, err := run(t, "mcp", "catalog", "install", "github", "--value", "token=ghp_x", "--name", "github"); err != nil {
		t.Fatal(err)
	}
	calls := f.find("POST", "/mcp-catalog/github/install")
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	values, _ := calls[0].Body["values"].(map[string]any)
	if values["token"] != "ghp_x" || calls[0].Body["name"] != "github" {
		t.Fatalf("body = %+v", calls[0].Body)
	}
}

func TestTokenCreateListAndRevoke(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /tokens/scoped":         `{"id":"tok_1","token":"eyJhbGci","expires_at":"2026-10-12T03:00:00Z","scopes":["sandboxes:read"]}`,
		"GET /tokens/scoped":          `{"data":[{"id":"tok_1","user_id":"cus_1","workspace_id":"ws_1","scopes":["sandboxes:read"]}],"next_cursor":null}`,
		"DELETE /tokens/scoped/tok_1": `{"revoked":true}`,
	})

	out, err := run(t, "token", "create", "cus_1", "ws_1", "--scope", "sandboxes:read", "--expires-in", "600")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "eyJhbGci") {
		t.Fatalf("out = %q", out)
	}
	create := f.find("POST", "/tokens/scoped")
	if len(create) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	if create[0].Body["user_id"] != "cus_1" || create[0].Body["workspace_id"] != "ws_1" {
		t.Fatalf("body = %+v", create[0].Body)
	}
	if create[0].Body["expires_in_seconds"] != float64(600) {
		t.Fatalf("expires_in_seconds = %+v", create[0].Body["expires_in_seconds"])
	}

	if _, err := run(t, "token", "list"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("GET", "/tokens/scoped")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}

	if _, err := run(t, "token", "revoke", "tok_1", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("DELETE", "/tokens/scoped/tok_1")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestAPIKeyGetAndEvents(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"GET /api-keys/k_1":        `{"data":{"id":"k_1","name":"ci","key_prefix":"msk_u_ab12","status":"active"}}`,
		"GET /api-keys/k_1/events": `{"data":[{"id":"e1","type":"api_key.used","inserted_at":"2026-10-12T00:00:00Z"}]}`,
	})

	out, err := run(t, "api-key", "get", "k_1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "ci") {
		t.Fatalf("out = %q", out)
	}

	if _, err := run(t, "api-key", "events", "k_1", "--limit", "5"); err != nil {
		t.Fatal(err)
	}
	calls := f.find("GET", "/api-keys/k_1/events")
	if len(calls) != 1 || calls[0].Query != "limit=5" {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestAPIKeyPresetNewAndRm(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /api-keys/presets":           `{"data":{"name":"backend","scopes":["sandboxes:read"],"description":"backend"}}`,
		"DELETE /api-keys/presets/backend": `{}`,
	})

	if _, err := run(t, "api-key", "preset", "new", "backend", "--scope", "sandboxes:read", "--description", "backend"); err != nil {
		t.Fatal(err)
	}
	create := f.find("POST", "/api-keys/presets")
	if len(create) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	if create[0].Body["name"] != "backend" {
		t.Fatalf("body = %+v", create[0].Body)
	}
	scopes, _ := create[0].Body["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "sandboxes:read" {
		t.Fatalf("scopes = %+v", create[0].Body["scopes"])
	}

	if _, err := run(t, "api-key", "preset", "rm", "backend", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("DELETE", "/api-keys/presets/backend")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}
