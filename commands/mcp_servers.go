package commands

import (
	"github.com/spf13/cobra"
)

// Tool servers (MCP connectors) agents can call, and the pre-built catalogue to
// add them from. These are the `/mcp-servers` and `/mcp-catalog` surfaces, next
// to the MCP protocol server itself (`miosa mcp`). Each route needs
// connections:read or connections:write of an API key.
func newMCPServersCmd() *cobra.Command {
	return group("server", "Tool servers agents can call",
		`A tool server is a tool your agents can call over MCP. Define it once with
its secret stored encrypted, and every agent allowed to use it inherits the
connection.

  list                 the tool servers you defined
  add github           define one, with its transport, url and secrets
  test <server>        reach it and list its tools
  rm <server>          delete it

Secrets are given as --secret NAME=VALUE and are never returned.`,
		[]string{"servers"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List the tool servers",
			Method: "GET", Path: "/mcp-servers", Cols: mcpServerCols,
			Flags: []Flag{
				{Name: "workspace-id", Usage: "Only the servers of this workspace", In: "query"},
				{Name: "status", Usage: "Only servers in this status", In: "query"},
			},
		},
		Op{
			Use: "add <name>", Aliases: []string{"create"}, Short: "Define a tool server", Method: "POST", Path: "/mcp-servers",
			Args: []Arg{{Name: "name"}},
			Flags: []Flag{
				{Name: "transport", Usage: "Transport to reach it: http, stdio or sse"},
				{Name: "url", Usage: "The server URL (http and sse)"},
				{Name: "command", Usage: "The command to run (stdio)"},
				{Name: "args", Type: "strings", Usage: "An argument for the command (repeatable)"},
				{Name: "secret-header", Type: "strings", Field: "secret_headers", Usage: "A header whose value comes from --secret (repeatable)"},
				{Name: "secret-env", Type: "strings", Field: "secret_env", Usage: "An environment variable from --secret (repeatable)"},
				{Name: "secret", Type: "kv", Field: "secrets", Usage: "NAME=VALUE stored encrypted, never returned (repeatable)"},
				{Name: "workspace-id", Usage: "Bind the server to one workspace"},
			},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				body["name"] = args[0]
				return body, nil
			},
			Detail: mcpServerDetail,
		},
		Op{
			Use: "get <server>", Aliases: []string{"show"}, Short: "Show one tool server",
			Method: "GET", Path: "/mcp-servers/{0}", Args: []Arg{{Name: "server"}}, Detail: mcpServerDetail,
		},
		Op{
			Use: "test <server>", Short: "Reach the server and list its tools",
			Method: "POST", Path: "/mcp-servers/{0}/test", Args: []Arg{{Name: "server"}},
			Detail: []Col{{Head: "Status", Path: "status"}, {Head: "Tools", Path: "tools", Fmt: "count"}, {Head: "Message", Path: "message"}},
		},
		Op{
			Use: "rm <server>", Aliases: []string{"delete"}, Short: "Delete a tool server",
			Method: "DELETE", Path: "/mcp-servers/{0}", Args: []Arg{{Name: "server"}},
			Confirm: "Delete tool server {0}? Agents using it lose the connection.", Done: "Deleted {0}",
		},
	)
}

var mcpServerCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "TRANSPORT", Path: "transport"},
	{Head: "STATUS", Path: "status"}, {Head: "WORKSPACE", Path: "workspace_id", Fmt: "short"},
}

var mcpServerDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Transport", Path: "transport"},
	{Head: "Url", Path: "url"}, {Head: "Command", Path: "command"}, {Head: "Args", Path: "args", Fmt: "list"},
	{Head: "Status", Path: "status"}, {Head: "Workspace", Path: "workspace_id"},
	{Head: "Tools", Path: "tools", Fmt: "count"},
}

// The pre-built catalogue to add a tool server from.
func newMCPCatalogCmd() *cobra.Command {
	cmd := group("catalog", "Add a tool server from the pre-built catalogue",
		`The catalogue lists tool servers that are ready to add, each with the method it
takes to authenticate. Try one before adding it, then install it as a tool server.

  miosa mcp catalog list
  miosa mcp catalog test github --value token=ghp_x
  miosa mcp catalog install github --name github --value token=ghp_x`,
		[]string{"catalogue"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List the catalogue entries",
			Method: "GET", Path: "/mcp-catalog",
			Flags: []Flag{{Name: "workspace-id", Usage: "Include what this workspace already added", In: "query"}},
			Cols: []Col{
				{Head: "SLUG", Path: "slug"}, {Head: "NAME", Path: "name"}, {Head: "CATEGORY", Path: "category"},
				{Head: "ADDED", Path: "added", Fmt: "count"}, {Head: "DESCRIPTION", Path: "description"},
			},
		},
		Op{
			Use: "test <slug>", Short: "Check a catalogue entry before adding it",
			Method: "POST", Path: "/mcp-catalog/{0}/test", Args: []Arg{{Name: "slug"}},
			Flags: []Flag{
				{Name: "method", Usage: "The authentication method to use"},
				{Name: "value", Type: "kv", Field: "values", Usage: "NAME=VALUE for the method (repeatable)"},
			},
			Detail: []Col{{Head: "Status", Path: "status"}, {Head: "Tools", Path: "tools", Fmt: "count"}, {Head: "Message", Path: "message"}},
		},
		Op{
			Use: "install <slug>", Short: "Add a catalogue entry as a tool server",
			Method: "POST", Path: "/mcp-catalog/{0}/install", Args: []Arg{{Name: "slug"}},
			Flags: []Flag{
				{Name: "method", Usage: "The authentication method to use"},
				{Name: "value", Type: "kv", Field: "values", Usage: "NAME=VALUE for the method (repeatable)"},
				{Name: "name", Usage: "Name for the new tool server"},
				{Name: "workspace-id", Usage: "Add it to this workspace"},
			},
			Detail: mcpServerDetail,
		},
	)
	return cmd
}
