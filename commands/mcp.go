package commands

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() { rootCmd.AddCommand(newMCPCmd()) }

// newMCPCmd exposes the MIOSA Model Context Protocol server: the hosted
// Streamable-HTTP endpoint at <api>/mcp, a local stdio bridge for clients that
// can only spawn a subprocess, and a helper that wires the endpoint into a
// client's config file.
func newMCPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Connect AI clients to the MIOSA Model Context Protocol server",
		Long: `MIOSA serves a Model Context Protocol server at <api-url>/mcp over
Streamable HTTP, authenticated with the same API key as the rest of the CLI.
It gives an AI client tools to create and manage sandboxes, computers,
deployments, storage, databases and volumes.

  miosa mcp tools                       list the tools the server exposes
  miosa mcp install --client claude     add the server to Claude Code's config
  miosa mcp install --client cursor     add the server to Cursor's config
  miosa mcp serve                       run a stdio bridge for clients that need one`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newMCPToolsCmd(), newMCPInstallCmd(), newMCPServeCmd())
	return cmd
}

// mcpEndpoint is the hosted MCP URL: the API base URL plus /mcp, unless the
// user named one.
func mcpEndpoint(c *api.Client, override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	return strings.TrimRight(c.BaseURL, "/") + "/mcp"
}

// mcpCall posts one JSON-RPC message to the hosted server. Retries are off:
// a tools/call is not idempotent, so a retried 5xx must not run the tool twice.
func mcpCall(cmd *cobra.Command, c *api.Client, url string, message any) (any, error) {
	path := url
	if strings.HasPrefix(url, c.BaseURL) {
		path = strings.TrimPrefix(url, c.BaseURL)
	}
	return c.JSONAny(cmd.Context(), api.Request{Method: http.MethodPost, Path: path, Body: message, NoRetry: true})
}

// ─── tools ────────────────────────────────────────────────────────────────────

func newMCPToolsCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List the tools MIOSA MCP exposes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := mcpCall(cmd, c.API, mcpEndpoint(c.API, url), map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
			if err != nil {
				return die(err)
			}
			tools := mcpToolsOf(resp)
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(resp)
			}
			if len(tools) == 0 {
				p.Line("The server returned no tools.")
				return nil
			}
			rows := make([][]string, 0, len(tools))
			for _, t := range tools {
				rows = append(rows, []string{t.Name, t.Description})
			}
			p.Table([]string{"TOOL", "DESCRIPTION"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "MCP endpoint (default: the API base URL + /mcp)")
	return cmd
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func mcpToolsOf(resp any) []mcpTool {
	m, _ := resp.(map[string]any)
	result, _ := m["result"].(map[string]any)
	raw, _ := result["tools"].([]any)
	tools := make([]mcpTool, 0, len(raw))
	for _, r := range raw {
		if obj, ok := r.(map[string]any); ok {
			name, _ := obj["name"].(string)
			desc, _ := obj["description"].(string)
			tools = append(tools, mcpTool{Name: name, Description: firstLine(desc)})
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clipText(strings.TrimSpace(s), 80)
}

// ─── serve ────────────────────────────────────────────────────────────────────

// newMCPServeCmd runs a stdio bridge to MIOSA MCP: one JSON-RPC message per
// line on stdin, the matching response on stdout. It forwards each message to
// the hosted endpoint, so a client that can only spawn a subprocess still
// reaches the same tool belt.
func newMCPServeCmd() *cobra.Command {
	var url string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a stdio bridge to MIOSA MCP for clients that need one",
		Long: `Reads newline-delimited JSON-RPC 2.0 messages from standard input, forwards
each to MIOSA MCP, and writes the response as one line on standard output. Notifications (which have no response) produce no output.

  {
    "mcpServers": {
      "miosa": {"command": "miosa", "args": ["mcp", "serve"]}
    }
  }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			return serveStdioMCP(cmd, c.API, mcpEndpoint(c.API, url))
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "MCP endpoint (default: the API base URL + /mcp)")
	return cmd
}

func serveStdioMCP(cmd *cobra.Command, c *api.Client, url string) error {
	out := cmd.OutOrStdout()
	sc := bufio.NewScanner(cmd.InOrStdin())
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if !json.Valid(line) {
			writeMCPMessage(out, mcpErrorResponse(nil, -32700, "parse error"))
			continue
		}
		resp, err := mcpCall(cmd, c, url, json.RawMessage(append([]byte(nil), line...)))
		if err != nil {
			writeMCPMessage(out, mcpErrorResponse(nil, -32603, err.Error()))
			continue
		}
		if resp == nil {
			continue // a notification: no response is expected
		}
		writeMCPMessage(out, resp)
	}
	return sc.Err()
}

func writeMCPMessage(w io.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Fprintln(w, string(b))
}

func mcpErrorResponse(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}
}

// ─── install ──────────────────────────────────────────────────────────────────

// mcpClient says where a client keeps its MCP server list.
type mcpClient struct {
	file  func(scope string) (string, error)
	entry func(url, key string) map[string]any
}

var mcpClients = map[string]mcpClient{
	"claude": {file: claudeMCPFile, entry: stdioOrHTTPEntry},
	"cursor": {file: fixedMCPFile(".cursor/mcp.json"), entry: stdioOrHTTPEntry},
	"gemini": {file: fixedMCPFile(".gemini/settings.json"), entry: geminiEntry},
}

func stdioOrHTTPEntry(url, key string) map[string]any {
	return map[string]any{
		"type":    "http",
		"url":     url,
		"headers": map[string]string{"Authorization": "Bearer " + key},
	}
}

func geminiEntry(url, key string) map[string]any {
	return map[string]any{
		"httpUrl": url,
		"headers": map[string]string{"Authorization": "Bearer " + key},
	}
}

func claudeMCPFile(scope string) (string, error) {
	if scope == "project" {
		return ".mcp.json", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude.json"), nil
}

func fixedMCPFile(rel string) func(string) (string, error) {
	return func(string) (string, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rel), nil
	}
}

func newMCPInstallCmd() *cobra.Command {
	var client, scope, url, name string
	var printOnly, force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Add MIOSA MCP to an AI client's config",
		Long: `Writes the hosted MCP endpoint (and your API key) into an AI client's config
file, keeping the other servers it already lists. --print shows the change
without writing anything.

  miosa mcp install                          Claude Code, user scope
  miosa mcp install --client cursor          Cursor
  miosa mcp install --client gemini          Gemini CLI
  miosa mcp install --client claude --scope project   a shared .mcp.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client = strings.ToLower(strings.TrimSpace(client))
			if client == "manual" {
				printOnly = true
				client = "claude"
			}
			spec, ok := mcpClients[client]
			if !ok {
				return usagef("--client must be one of claude, cursor, gemini, manual (got %q)", client)
			}
			if scope != "user" && scope != "project" {
				return usagef("--scope must be user or project (got %q)", scope)
			}
			if scope == "project" && client != "claude" {
				return usagef("--scope project is only supported for --client claude")
			}
			file, err := spec.file(scope)
			if err != nil {
				return die(err)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			entry := spec.entry(mcpEndpoint(c.API, url), c.Key)
			if err := installMCPServer(file, name, entry, !printOnly, force); err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			if printOnly {
				body, _ := json.MarshalIndent(map[string]any{"mcpServers": map[string]any{name: entry}}, "", "  ")
				fmt.Fprintf(p.Writer(), "%s\n", body)
				p.Line("Add this under \"mcpServers\" in %s.", file)
				return nil
			}
			p.Success("Added MIOSA MCP as %q in %s", name, file)
			p.Line("Restart %s to pick it up.", client)
			return nil
		},
	}
	cmd.Flags().StringVar(&client, "client", "claude", "Client to configure: claude, cursor, gemini or manual")
	cmd.Flags().StringVar(&scope, "scope", "user", "Config scope for claude: user or project")
	cmd.Flags().StringVar(&url, "url", "", "MCP endpoint (default: the API base URL + /mcp)")
	cmd.Flags().StringVar(&name, "name", "miosa", "Name to register MIOSA MCP under")
	cmd.Flags().BoolVar(&printOnly, "print", false, "Print the config instead of writing it")
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite an existing entry of the same name")
	return cmd
}

// installMCPServer merges one server entry into a client's JSON config file.
// With write false it only reports what it would do. An existing entry under
// the same name is left alone unless force is set.
func installMCPServer(file, name string, entry map[string]any, write, force bool) error {
	doc := map[string]any{}
	if raw, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s is not a JSON object: %w", file, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", file, err)
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, exists := servers[name]; exists && !force {
		return fmt.Errorf("%s already lists a tool server named %q; pass --force to replace it", file, name)
	}
	servers[name] = entry
	doc["mcpServers"] = servers

	if !write {
		return nil
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if dir := filepath.Dir(file); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(file); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(file, body, mode)
}
