package commands

import "github.com/spf13/cobra"

func init() { rootCmd.AddCommand(gatewayCmd()) }

// gatewayCmd is the AI gateway control plane: which models serve which
// requests, what they may spend, and what they did. The routes live under
// /ai-gateway and accept an ordinary user key with the intelligence:gateway:read
// scope (and :write to change anything).
func gatewayCmd() *cobra.Command {
	policyFlags := []Flag{
		{Name: "name", Usage: "Policy name"},
		{Name: "primary-model", Usage: "Model that serves matching requests first"},
		{Name: "fallback-model", Usage: "Model to try when the primary fails (repeatable)", Type: "strings", Field: "fallback_models"},
		{Name: "alias", Usage: "Model name callers use to reach this policy", Field: "alias_name"},
		{Name: "strategy", Usage: "How models are chosen", Enum: []string{"priority", "weighted"}},
		{Name: "priority", Usage: "Lower runs first when several policies match", Type: "int"},
		{Name: "enabled", Usage: "Whether the policy applies (--enabled=false turns it off)", Type: "bool"},
	}
	policyCols := []Col{
		{Head: "ID", Path: "id", Fmt: "short"}, {Head: "NAME", Path: "name"}, {Head: "ALIAS", Path: "alias_name"}, {Head: "PRIMARY", Path: "primary_model"},
		{Head: "STRATEGY", Path: "strategy"}, {Head: "PRIORITY", Path: "priority"}, {Head: "ENABLED", Path: "enabled"},
	}
	policyDetail := []Col{
		{Head: "Id", Path: "id"}, {Head: "Name", Path: "name"}, {Head: "Alias", Path: "alias_name"}, {Head: "Primary model", Path: "primary_model"},
		{Head: "Fallbacks", Path: "fallback_models", Fmt: "list"}, {Head: "Strategy", Path: "strategy"}, {Head: "Priority", Path: "priority"},
		{Head: "Enabled", Path: "enabled"}, {Head: "Selector", Path: "selector", Fmt: "json"}, {Head: "Weights", Path: "weights", Fmt: "json"},
	}
	gw := group("gateway", "The AI gateway: model routing policies, budget, limits and request traces",
		`The AI gateway routes the model requests your applications send to MIOSA. A
policy says which model serves which requests and what to fall back to; the
budget and limits cap spend and rate.

Reading needs the intelligence:gateway:read scope on a key, changing needs
intelligence:gateway:write. Creating gateway keys and calling models stay in the
console and on the gateway's own address.

  miosa gateway policy list
  miosa gateway policy new fast --primary-model claude-haiku --alias fast
  miosa gateway budget show
  miosa gateway budget set --file budget.json
  miosa gateway requests --limit 20`, []string{"ai-gateway"},
		Op{Use: "health", Short: "Show whether the gateway is serving", Method: "GET", Path: "/ai-gateway/health"},
		Op{Use: "provider-health", Short: "Show the health of each model provider", Method: "GET", Path: "/ai-gateway/provider-health"},
		Op{Use: "environments", Short: "List gateway environments and the keys in each", Method: "GET", Path: "/ai-gateway/environments"},
		Op{
			Use: "requests", Short: "List recent gateway requests", Method: "GET", Path: "/ai-gateway/requests",
			Flags: []Flag{
				{Name: "limit", Usage: "How many to show", Type: "int"},
				{Name: "status", Usage: "Only requests with this status"},
				{Name: "model", Usage: "Only requests for this model"},
			},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "MODEL", Path: "model"}, {Head: "STATUS", Path: "status"}, {Head: "LATENCY MS", Path: "latency_ms"}, {Head: "WHEN", Path: "inserted_at", Fmt: "age"}},
		},
		Op{Use: "request <id>", Short: "Show one gateway request trace", Method: "GET", Path: "/ai-gateway/requests/{0}", Args: []Arg{{Name: "request-id"}}},
	)
	gw.AddCommand(
		group("policy", "Routing policies: which model serves which requests", "<policy> is an id.", []string{"policies"},
			Op{Use: "list", Aliases: []string{"ls"}, Short: "List policies", Method: "GET", Path: "/ai-gateway/policies", Cols: policyCols},
			Op{Use: "get <policy>", Short: "Show one policy", Method: "GET", Path: "/ai-gateway/policies/{0}", Args: []Arg{{Name: "policy"}}, Detail: policyDetail},
			Op{
				Use: "new <name>", Aliases: []string{"create"}, Short: "Create a policy", Method: "POST", Path: "/ai-gateway/policies",
				Args: []Arg{{Name: "name"}}, Flags: policyFlags[1:], Detail: policyDetail, FileBody: true,
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					body["name"] = args[0]
					return body, nil
				},
			},
			Op{
				Use: "update <policy>", Short: "Change a policy", Method: "PATCH", Path: "/ai-gateway/policies/{0}",
				Args: []Arg{{Name: "policy"}}, Flags: policyFlags, Detail: policyDetail, FileBody: true,
			},
			Op{
				Use: "rm <policy>", Aliases: []string{"delete"}, Short: "Delete a policy", Method: "DELETE", Path: "/ai-gateway/policies/{0}",
				Args: []Arg{{Name: "policy"}}, Confirm: "Delete gateway policy {0}?", Done: "Deleted {0}",
			},
		),
		group("budget", "What the gateway may spend", "The budget is one document per organization; 'set' replaces the fields you give.", nil,
			Op{Use: "show", Short: "Show the budget and what is spent", Method: "GET", Path: "/ai-gateway/budget"},
			Op{
				Use: "set", Short: "Set the budget from a JSON file", Method: "PUT", Path: "/ai-gateway/budget", FileBody: true,
				Long: "Reads the budget document from --file (or - for standard input) and stores it. Run 'miosa gateway budget show' for the field names.",
			},
		),
		group("limit", "Rate and spend limits per key or environment", "A limit applies to one scope: a gateway key or an environment.", []string{"limits"},
			Op{Use: "list", Aliases: []string{"ls"}, Short: "List limits", Method: "GET", Path: "/ai-gateway/limits"},
			Op{
				Use: "set <key|environment> <id-or-name>", Short: "Set the limits of one scope from a JSON file", Method: "PUT", Path: "/ai-gateway/limits/{0}/{1}",
				Args: []Arg{{Name: "scope-type"}, {Name: "scope-value"}}, FileBody: true,
			},
			Op{
				Use: "rm <key|environment> <id-or-name>", Aliases: []string{"delete"}, Short: "Remove the limits of one scope", Method: "DELETE", Path: "/ai-gateway/limits/{0}/{1}",
				Args: []Arg{{Name: "scope-type"}, {Name: "scope-value"}}, Confirm: "Remove the limits of {0} {1}?", Done: "Removed the limits of {0} {1}",
			},
		),
		group("settings", "Gateway-wide settings", "", nil,
			Op{Use: "show", Short: "Show gateway settings", Method: "GET", Path: "/ai-gateway/settings"},
			Op{
				Use: "set", Short: "Change gateway settings from a JSON file", Method: "PUT", Path: "/ai-gateway/settings", FileBody: true,
				Long: "Reads the settings from --file (or - for standard input). Turning on audio transcription needs \"acknowledge_no_baa\": true, because audio can contain health information.",
			},
		),
	)
	return gw
}
