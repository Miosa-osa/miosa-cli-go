package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mcpToolsBody = `{"jsonrpc":"2.0","id":1,"result":{"tools":[
  {"name":"sandbox_create","description":"Create a new sandbox.\nReturns its id."},
  {"name":"computer_screenshot","description":"Capture a PNG."}
]}}`

func TestMCPToolsListsTheHostedToolBelt(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /mcp": mcpToolsBody})
	out, err := run(t, "mcp", "tools")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sandbox_create") || !strings.Contains(out, "computer_screenshot") {
		t.Fatalf("out = %q", out)
	}
	call := f.find("POST", "/mcp")
	if len(call) != 1 || call[0].Body["method"] != "tools/list" || call[0].Body["jsonrpc"] != "2.0" {
		t.Fatalf("calls = %+v", f.calls)
	}
	if !strings.HasPrefix(call[0].Headers.Get("Authorization"), "Bearer ") {
		t.Fatalf("the hosted server is API-key authenticated: %v", call[0].Headers)
	}
}

func TestMCPServeBridgesStdioToHTTP(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /mcp": func() any {
			return mcpToolsBody
		}(),
	})
	out, err := runIn(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n", "mcp", "serve")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("stdout must be one JSON line: %v (%q)", err, out)
	}
	if got["result"] == nil {
		t.Fatalf("the response must be relayed verbatim: %v", got)
	}
	if len(f.find("POST", "/mcp")) != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
}

func TestMCPServeReportsParseErrorsAndSkipsNotifications(t *testing.T) {
	f := fakeRoutes(t, map[string]any{"POST /mcp": `{"jsonrpc":"2.0","id":1,"result":{}}`})
	// A malformed line is answered locally; a notification (empty reply) prints nothing.
	out, err := runIn(t, "not json\n"+`{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n", "mcp", "serve")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "-32700") {
		t.Fatalf("a malformed line must produce a parse error: %q", out)
	}
	if len(f.find("POST", "/mcp")) != 1 {
		t.Fatalf("only the valid message reaches the server: %+v", f.calls)
	}
}

func TestMCPInstallWritesTheClientConfig(t *testing.T) {
	fakeRoutes(t, nil)
	home := os.Getenv("HOME")

	if out, err := run(t, "mcp", "install"); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(out, "Added MIOSA MCP") {
		t.Fatalf("out = %q", out)
	}
	path := filepath.Join(home, ".claude.json")
	doc := readJSON(t, path)
	servers := doc["mcpServers"].(map[string]any)
	entry := servers["miosa"].(map[string]any)
	if entry["type"] != "http" || !strings.HasSuffix(entry["url"].(string), "/mcp") {
		t.Fatalf("entry = %v", entry)
	}
	if !strings.HasPrefix(entry["url"].(string), os.Getenv("MIOSA_BASE_URL")) {
		t.Fatalf("the endpoint must follow the configured API base: %v", entry["url"])
	}
	headers := entry["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer msk_u_test" {
		t.Fatalf("headers = %v", headers)
	}

	// A second install keeps the servers already listed, and refuses to clobber.
	writeJSON(t, path, map[string]any{"mcpServers": map[string]any{"other": map[string]any{"command": "x"}}})
	if _, err := run(t, "mcp", "install", "--name", "other"); err == nil {
		t.Fatal("an existing entry is kept unless --force")
	}
	if _, err := run(t, "mcp", "install", "--force", "--name", "other"); err != nil {
		t.Fatal(err)
	}
	doc = readJSON(t, path)
	if len(doc["mcpServers"].(map[string]any)) != 1 {
		t.Fatalf("the other entries must be kept: %v", doc)
	}
}

func TestMCPInstallPerClientAndPrint(t *testing.T) {
	fakeRoutes(t, nil)
	home := os.Getenv("HOME")

	if _, err := run(t, "mcp", "install", "--client", "cursor"); err != nil {
		t.Fatal(err)
	}
	if doc := readJSON(t, filepath.Join(home, ".cursor", "mcp.json")); doc["mcpServers"] == nil {
		t.Fatalf("cursor config not written: %v", doc)
	}
	if _, err := run(t, "mcp", "install", "--client", "gemini"); err != nil {
		t.Fatal(err)
	}
	gem := readJSON(t, filepath.Join(home, ".gemini", "settings.json"))
	entry := gem["mcpServers"].(map[string]any)["miosa"].(map[string]any)
	if entry["httpUrl"] == nil {
		t.Fatalf("gemini uses httpUrl: %v", entry)
	}
	// --print writes nothing.
	before, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	out, err := run(t, "mcp", "install", "--print")
	if err != nil || !strings.Contains(out, "mcpServers") {
		t.Fatalf("%v\n%s", err, out)
	}
	after, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if string(before) != string(after) {
		t.Fatal("--print must not write")
	}
	if _, err := run(t, "mcp", "install", "--client", "nope"); err == nil {
		t.Fatal("an unknown client is a usage error")
	}
	if _, err := run(t, "mcp", "install", "--client", "cursor", "--scope", "project"); err == nil {
		t.Fatal("project scope is only for claude")
	}
	project := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(cwd) })
	if _, err := run(t, "mcp", "install", "--client", "claude", "--scope", "project"); err != nil {
		t.Fatal(err)
	}
	if doc := readJSON(t, filepath.Join(project, ".mcp.json")); doc["mcpServers"] == nil {
		t.Fatalf("project scope writes .mcp.json: %v", doc)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return doc
}

func writeJSON(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	body, _ := json.Marshal(doc)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A session token expires in 15 minutes; a client config keeps its token for
// months. Installing from a session must write a minted API key instead.
func TestMCPInstallFromASessionWritesAMintedKey(t *testing.T) {
	f := fakeRoutes(t, map[string]any{
		"POST /api-keys": `{"data":{"id":"k-mcp","key":"msk_u_mcp_minted"}}`,
	})
	t.Setenv("MIOSA_API_KEY", "eyJhbGciOiJIUzI1NiJ9.session.sig")
	out, err := run(t, "mcp", "install")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	headers := readJSON(t, filepath.Join(os.Getenv("HOME"), ".claude.json"))["mcpServers"].(map[string]any)["miosa"].(map[string]any)["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer msk_u_mcp_minted" {
		t.Fatalf("the config holds %v, want the minted key", headers["Authorization"])
	}
	mint := f.find("POST", "/api-keys")
	if len(mint) != 1 || mint[0].Body["preset"] != "cli" {
		t.Fatalf("mint = %+v", mint)
	}
	if strings.Contains(out, "msk_u_mcp_minted") || !strings.Contains(out, "minted an API key for MCP") {
		t.Fatalf("out = %s", out)
	}
}
