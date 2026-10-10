package commands

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(apiKeyCmd(), webhookCmd())
}

// ─── api-key ──────────────────────────────────────────────────────────────────

// scopeResources are the resources a key can be scoped to.
var scopeResources = []string{
	"projects", "events", "products", "sandboxes", "previews", "deployments", "domains", "databases", "env", "osa", "cloud",
	"egress", "agent_runtime_profiles", "agents", "project_auth", "project_integrations", "billing", "storage", "cron_jobs",
}

// keyPresets expand to scope lists. They are conveniences: pass --scope for exact control.
func keyPreset(name string) ([]string, bool) {
	switch name {
	case "read-only":
		var out []string
		for _, r := range scopeResources {
			out = append(out, r+":read")
		}
		return out, true
	case "ci":
		return []string{"sandboxes:read", "sandboxes:write", "sandboxes:create", "sandboxes:exec", "deployments:read", "deployments:write", "previews:read", "previews:write", "env:read"}, true
	case "agent":
		return []string{"agents:read", "agents:write", "agents:run", "sandboxes:read", "sandboxes:write", "sandboxes:create", "sandboxes:exec", "previews:read", "previews:write", "env:read"}, true
	case "cli":
		return []string{"agents:read", "agents:write", "agents:run", "connections:read", "connections:write", "sandboxes:read", "sandboxes:write", "sandboxes:create", "sandboxes:exec", "previews:read", "previews:write", "deployments:read", "env:read"}, true
	}
	return nil, false
}

// serverPreset asks the API for a preset's scopes, so the server stays the
// source of truth; ok is false on an older server or an unknown name.
func serverPreset(ctx context.Context, c *api.Client, name string) ([]string, bool) {
	var out struct {
		Data []struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/api-keys/presets", nil, &out); err != nil {
		return nil, false
	}
	for _, p := range out.Data {
		if p.Name == name {
			return p.Scopes, true
		}
	}
	return nil, false
}

func hideRevoked(cmd *cobra.Command, rows []any) []any {
	if all, _ := cmd.Flags().GetBool("all"); all {
		return rows
	}
	out := rows[:0]
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok && m["status"] == "revoked" {
			continue
		}
		out = append(out, r)
	}
	return out
}

var keyCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "KEY", Path: "key_prefix"}, {Head: "LAST4", Path: "last_four"},
	{Head: "TYPE", Path: "key_type"}, {Head: "STATUS", Path: "status"}, {Head: "EXPIRES", Path: "expires_at"}, {Head: "LAST USED", Path: "last_used_at", Fmt: "age"},
}

var keyDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Key", Path: "key"}, {Head: "Prefix", Path: "key_prefix"},
	{Head: "Status", Path: "status"}, {Head: "Type", Path: "key_type"}, {Head: "Expires", Path: "expires_at"},
	{Head: "Workspace", Path: "workspace_id"}, {Head: "Scopes", Path: "scopes", Fmt: "list"}, {Head: "Rate limit (rpm)", Path: "rate_limit_rpm"},
}

func apiKeyCmd() *cobra.Command {
	cmd := group("api-key", "Create and manage API keys",
		`API keys let scripts, CI and other programs call MIOSA. A key is shown once, at
creation and rotation; store it right away. A key acts as your organization
with the permissions you give it.

  miosa api-key create ci --preset ci --expires-in 90
  miosa api-key create reviewer --scope agents:read --scope agents:run
  miosa api-key list
  miosa api-key rotate <id>
  miosa api-key rm <id>

The key goes to standard output on create, so you can capture it:
  KEY=$(miosa api-key create ci --preset ci --quiet-key)`, []string{"api-keys", "keys"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List keys (revoked ones hidden unless --all)",
			Method: "GET", Path: "/api-keys", Cols: keyCols, Rows: hideRevoked,
			Flags: []Flag{{Name: "all", Type: "bool", Usage: "Include revoked keys", In: "query", Hidden: false, Field: "all"}},
		},
		Op{
			Use: "presets", Short: "List the permission presets for new keys", Method: "GET", Path: "/api-keys/presets",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "SCOPES", Path: "scopes", Fmt: "count"}, {Head: "FOR", Path: "description"}},
		},
		Op{
			Use: "rm <id>", Aliases: []string{"revoke", "delete"}, Short: "Revoke a key (it stops working immediately)",
			Method: "DELETE", Path: "/api-keys/{0}", Args: []Arg{{Name: "key"}}, Confirm: "Revoke API key {0}? Anything using it stops working.", Done: "Revoked {0}",
		},
		Op{
			Use: "rotate <id>", Short: "Replace a key's secret (the old one stops working)",
			Method: "POST", Path: "/api-keys/{0}/rotate", Args: []Arg{{Name: "key"}}, Detail: keyDetail,
			Long: "Issue a new secret for the key. The old secret stops working at once, so update whatever uses it first or have both ready. The new secret is shown once.",
		},
		Op{
			Use: "update <id>", Short: "Rename a key or change its limits",
			Method: "PATCH", Path: "/api-keys/{0}", Args: []Arg{{Name: "key"}}, Detail: keyDetail,
			Flags: []Flag{{Name: "name", Usage: "New name"}, {Name: "rate-limit", Type: "int", Usage: "Requests per minute", Field: "rate_limit_rpm"}},
		},
	)
	cmd.AddCommand(newKeyCreateCmd(), newKeyScopedCmd())
	return cmd
}

func newKeyCreateCmd() *cobra.Command {
	return newKeyMintCmd("create <name>", "Create a key", cobra.ExactArgs(1), func(ctx context.Context, c *api.Client, args []string) (string, map[string]any, error) {
		return "/api-keys", map[string]any{"name": args[0], "key_type": "user"}, nil
	})
}

// newKeyMintCmd is the shared "mint an API key" command: scopes, presets,
// expiry, workspace, IP allowlist and rate limit. target says where the key is
// created (the user's own, or a service account's) and what body to start from.
func newKeyMintCmd(use, short string, argCheck cobra.PositionalArgs, target func(ctx context.Context, c *api.Client, args []string) (string, map[string]any, error)) *cobra.Command {
	var (
		scopes      []string
		preset      string
		expiresIn   int
		workspaceID string
		allowedIPs  []string
		rateLimit   int
		quietKey    bool
	)
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: `Create an API key. With no --scope or --preset the key gets the default set of
scopes. Presets: read-only (every :read scope), ci (sandboxes, deployments,
previews), agent (run agents on sandboxes), cli (what the miosa CLI uses day to
day, including connections). 'miosa api-key presets' lists them. Scope names look like sandboxes:exec
or agents:run; see 'miosa api-key list --json' for what existing keys hold.

The secret is printed once. --quiet-key prints only the secret.`,
		Args: argCheck,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			path, body, err := target(cmd.Context(), c.API, args)
			if err != nil {
				return die(err)
			}
			list := append([]string(nil), scopes...)
			if preset != "" {
				ps, ok := serverPreset(cmd.Context(), c.API, preset)
				if !ok {
					ps, ok = keyPreset(preset)
				}
				if !ok {
					return usagef("--preset must be read-only, ci, agent or cli (see 'miosa api-key presets')")
				}
				list = append(ps, list...)
			}
			if len(list) > 0 {
				sort.Strings(list)
				body["scopes"] = dedupe(list)
			}
			if expiresIn > 0 {
				body["expires_in_days"] = expiresIn
			}
			if workspaceID != "" {
				body["workspace_id"] = workspaceID
			}
			if len(allowedIPs) > 0 {
				body["allowed_ips"] = allowedIPs
			}
			if rateLimit > 0 {
				body["rate_limit_rpm"] = rateLimit
			}
			resp, err := c.API.JSONAny(cmd.Context(), reqPost(path, body))
			if err != nil {
				return die(err)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			secret, _ := obj["key"].(string)
			switch {
			case quietKey:
				fmt.Fprintln(cmd.OutOrStdout(), secret)
			case isJSON():
				return p.JSON(resp)
			default:
				p.Success("Created key %q. The secret is shown once - store it now:", formatValue(obj["name"], ""))
				fmt.Fprintln(p.Writer())
				fmt.Fprintf(p.Writer(), "  %s\n\n", secret)
				p.Fields([][2]string{{"Id", formatValue(obj["id"], "")}, {"Scopes", fmt.Sprint(len(toStrings(obj["scopes"])))}, {"Expires", formatValue(obj["expires_at"], "")}})
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&scopes, "scope", nil, "A scope to grant, for example sandboxes:exec (repeatable)")
	f.StringVar(&preset, "preset", "", "Permission preset: read-only, ci, agent or cli")
	f.IntVar(&expiresIn, "expires-in", 0, "Expire after this many days (default: never)")
	f.StringVar(&workspaceID, "workspace-id", "", "Bind the key to one workspace")
	f.StringArrayVar(&allowedIPs, "allowed-ip", nil, "Only accept requests from this IP or CIDR (repeatable)")
	f.IntVar(&rateLimit, "rate-limit", 0, "Requests per minute for this key")
	f.BoolVar(&quietKey, "quiet-key", false, "Print only the secret")
	return cmd
}

func newKeyScopedCmd() *cobra.Command {
	var (
		scopes      []string
		workspaceID string
		externalID  string
		expiresAt   string
	)
	cmd := &cobra.Command{
		Use:   "scoped <label>",
		Short: "Mint a restricted key for one end user of your product",
		Long: `Create a key bound to a workspace and an external user id, for products that act
for their own customers. Needs an owner or admin.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			body := map[string]any{"label": args[0], "workspace_id": workspaceID, "external_user_id": externalID, "scopes": scopes}
			if expiresAt != "" {
				body["expires_at"] = expiresAt
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), reqPost("/api-keys/scoped", body))
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			p.Success("Created scoped key %q. The token is shown once:", args[0])
			fmt.Fprintf(p.Writer(), "\n  %s\n\n", formatValue(obj["token"], ""))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&scopes, "scope", nil, "A scope to grant (repeatable)")
	f.StringVar(&workspaceID, "workspace-id", "", "Workspace the key is bound to")
	f.StringVar(&externalID, "external-user-id", "", "Your id for the end user")
	f.StringVar(&expiresAt, "expires-at", "", "RFC 3339 expiry")
	_ = cmd.MarkFlagRequired("workspace-id")
	_ = cmd.MarkFlagRequired("external-user-id")
	_ = cmd.MarkFlagRequired("scope")
	return cmd
}

func dedupe(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func toStrings(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, e := range l {
		out = append(out, strings.TrimSpace(fmt.Sprint(e)))
	}
	return out
}

// ─── webhook ──────────────────────────────────────────────────────────────────

var webhookCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "NAME", Path: "name"}, {Head: "URL", Path: "url"},
	{Head: "EVENTS", Path: "events", Fmt: "list"}, {Head: "ACTIVE", Path: "active"},
}

var webhookDetail = []Col{
	{Head: "Id", Path: "id"}, {Head: "Name", Path: "name"}, {Head: "URL", Path: "url"}, {Head: "Events", Path: "events", Fmt: "list"},
	{Head: "Active", Path: "active"}, {Head: "Secret", Path: "secret"}, {Head: "Secret preview", Path: "secret_preview"},
	{Head: "Retries", Path: "retry_count"}, {Head: "Workspace", Path: "workspace_id"},
}

func webhookCmd() *cobra.Command {
	hook := Arg{Name: "webhook"}
	return group("webhook", "Send events to your own HTTPS endpoints",
		`Webhooks call your URL when something happens (a sandbox becomes ready, a
deployment succeeds). Each delivery is signed: the x-miosa-signature header is
"sha256=" plus the hex HMAC-SHA256 of the raw body under the webhook's secret.
The secret is returned only when the webhook is created.

  miosa webhook events
  miosa webhook create https://example.com/hooks/miosa --event sandbox.ready --event sandbox.error
  miosa webhook test <id>
  miosa webhook deliveries <id>
  miosa webhook replay <id> <delivery-id>`, []string{"webhooks", "hook"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List webhooks",
			Method: "GET", Path: "/webhooks", Cols: webhookCols,
			Flags: []Flag{{Name: "workspace-id", Usage: "Only this workspace", Field: "workspace_id"}},
		},
		Op{
			Use: "events", Short: "List the event types you can subscribe to",
			Method: "GET", Path: "/webhooks/events", ListKey: "data",
			Cols: []Col{{Head: "CATEGORY", Path: "category"}, {Head: "EVENTS", Path: "events", Fmt: "list"}},
		},
		Op{
			Use: "get <id>", Aliases: []string{"show"}, Short: "Show a webhook",
			Method: "GET", Path: "/webhooks/{0}", Args: []Arg{hook}, Detail: webhookDetail,
		},
		Op{
			Use: "create <url>", Short: "Register an endpoint",
			Method: "POST", Path: "/webhooks", Args: []Arg{{Name: "url"}}, Detail: webhookDetail,
			Long: "Register an endpoint. The signing secret is printed once. Omit --event to receive every event type.",
			Flags: []Flag{
				{Name: "event", Type: "strings", Usage: "Event type to subscribe to (repeatable; see 'webhook events')", Field: "events"},
				{Name: "name", Usage: "A label"},
				{Name: "description", Usage: "What it is for"},
				{Name: "workspace-id", Usage: "Limit to one workspace", Field: "workspace_id"},
				{Name: "retries", Type: "int", Usage: "Delivery retries, 0-10", Field: "retry_count"},
				{Name: "header", Type: "kv", Usage: "Extra header sent with each delivery, NAME=VALUE (repeatable)", Field: "headers"},
			},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				body["url"] = args[0]
				return body, nil
			},
		},
		Op{
			Use: "update <id>", Short: "Change a webhook",
			Method: "PATCH", Path: "/webhooks/{0}", Args: []Arg{hook}, Detail: webhookDetail,
			Flags: []Flag{
				{Name: "url", Usage: "New endpoint"},
				{Name: "event", Type: "strings", Usage: "Replace the subscribed events (repeatable)", Field: "events"},
				{Name: "name", Usage: "A label"},
				{Name: "active", Type: "bool", Usage: "Enable or disable deliveries"},
				{Name: "retries", Type: "int", Usage: "Delivery retries, 0-10", Field: "retry_count"},
			},
		},
		Op{
			Use: "rm <id>", Aliases: []string{"delete"}, Short: "Delete a webhook",
			Method: "DELETE", Path: "/webhooks/{0}", Args: []Arg{hook}, Confirm: "Delete webhook {0}?", Done: "Deleted {0}",
		},
		Op{
			Use: "test <id>", Short: "Send a test delivery",
			Method: "POST", Path: "/webhooks/{0}/test", Args: []Arg{hook},
		},
		Op{
			Use: "deliveries <id>", Short: "List recent deliveries and their outcomes",
			Method: "GET", Path: "/webhooks/{0}/deliveries", Args: []Arg{hook},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "EVENT", Path: "event"}, {Head: "STATUS", Path: "status"}, {Head: "CODE", Path: "response_status"}, {Head: "ATTEMPTS", Path: "attempts"}, {Head: "WHEN", Path: "inserted_at", Fmt: "age"}},
		},
		Op{
			Use: "replay <id> <delivery>", Short: "Send a past delivery again",
			Method: "POST", Path: "/webhooks/{0}/deliveries/{1}/replay", Args: []Arg{hook, {Name: "delivery"}}, Done: "Replaying {1}",
		},
	)
}
