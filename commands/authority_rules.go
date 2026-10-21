package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// policyClasses are the tool classes an approval-policy rule can name.
var policyClasses = []string{"read", "internal_write", "external_write", "destructive", "publish"}

var policyDetail = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id"}, {Head: "SCOPE", Path: "scope"},
	{Head: "WORKSPACE", Path: "workspace_id"}, {Head: "AGENT", Path: "agent_id"}, {Head: "RULES", Compute: policyRulesCell},
	{Head: "PER RUN", Path: "budget_run_credits"}, {Head: "PER DAY", Path: "budget_day_credits"}, {Head: "STATUS", Path: "status"},
}

func authorityRulesCmd() *cobra.Command {
	set := Arg{Name: "rule-set"}
	return group("rules", "What unattended runs may do alone",
		`A rule set decides what a scheduled or triggered agent run may do on its own.
It applies to the organization, one workspace (--workspace-id) or one agent (--agent).

A rule is class[:resource]. Classes: read, internal_write, external_write,
destructive, publish. The resource is what the call touches (storage, deployment,
a tool server, a shell program such as npm), or * for any.

  miosa agent authority rules create --name drafts --agent drafter \
      --allow internal_write:storage --ask external_write:email --budget-run 50
  miosa agent authority rules list --workspace-id <id>
  miosa agent authority rules rm drafts

A deny anywhere wins; otherwise the most specific scope decides, and in it ask beats
allow. destructive and publish are allowed only by a rule naming that class, and
external_write only by a rule naming its resource. With no rule an unattended run
asks, and an ask nobody answers fails safe. For time windows give the rules in
--file as JSON: {"rules": [{"effect": "allow", "class": "read", "resource": "*",
"window": {"days": [1,2,3,4,5], "from": "09:00", "to": "17:00", "tz": "UTC"}}]}.`, []string{"rule"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List rule sets",
			Method: "GET", Path: "/agents/policies",
			Flags: []Flag{
				{Name: "workspace-id", Usage: "Only this workspace's rule sets", Field: "workspace_id"},
				{Name: "agent", Usage: "Only this agent's rule sets (name or id)", Field: "agent_id"},
			},
			Before: resolvePolicyAgent,
			Cols: []Col{
				{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "SCOPE", Path: "scope"},
				{Head: "RULES", Compute: policyRulesCell}, {Head: "PER RUN", Path: "budget_run_credits"},
				{Head: "PER DAY", Path: "budget_day_credits"}, {Head: "STATUS", Path: "status"},
			},
		},
		Op{
			Use: "create", Short: "Create a rule set",
			Method: "POST", Path: "/agents/policies", FileBody: true,
			Flags: []Flag{
				{Name: "name", Usage: "A label", Required: true},
				{Name: "workspace-id", Usage: "Apply to this workspace (default: the whole organization)", Field: "workspace_id"},
				{Name: "agent", Usage: "Apply to this agent only (name or id)", Field: "agent_id"},
				{Name: "allow", Usage: "Rule class[:resource] the agent may do on its own (repeatable)", Type: "strings"},
				{Name: "ask", Usage: "Rule class[:resource] that needs a person's approval (repeatable)", Type: "strings"},
				{Name: "deny", Usage: "Rule class[:resource] that is always refused (repeatable)", Type: "strings"},
				{Name: "budget-run", Usage: "Most credits one run may spend", Type: "int", Field: "budget_run_credits"},
				{Name: "budget-day", Usage: "Most credits the scope's runs may spend in a day", Type: "int", Field: "budget_day_credits"},
			},
			Before: resolvePolicyAgent,
			Body:   policyCreateBody,
			Detail: policyDetail,
		},
		Op{
			Use: "get <rule-set>", Aliases: []string{"show"}, Short: "Show a rule set",
			Method: "GET", Path: "/agents/policies/{0}", Args: []Arg{set}, ResolveArgs: resolvePolicyArgs,
			Detail: policyDetail,
		},
		Op{
			Use: "rm <rule-set>", Aliases: []string{"delete", "disable"}, Short: "Disable a rule set (its receipts stay)",
			Method: "DELETE", Path: "/agents/policies/{0}", Args: []Arg{set}, ResolveArgs: resolvePolicyArgs,
			Confirm: "Disable rule set {0}?", Done: "Disabled {0}",
		},
	)
}

// policyCreateBody turns --allow/--ask/--deny into rules, after any from --file.
func policyCreateBody(_ []string, body map[string]any) (map[string]any, error) {
	rules, _ := body["rules"].([]any)
	for _, effect := range []string{"allow", "ask", "deny"} {
		specs, _ := body[effect].([]string)
		delete(body, effect)
		for _, spec := range specs {
			rule, err := policyRule(effect, spec)
			if err != nil {
				return nil, err
			}
			rules = append(rules, rule)
		}
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("a rule set needs at least one rule: --allow, --ask, --deny or rules in --file")
	}
	body["rules"] = rules
	for _, key := range []string{"budget_run_credits", "budget_day_credits"} {
		if n, ok := body[key].(int); ok && n < 1 {
			return nil, fmt.Errorf("--%s must be at least 1 credit", strings.ReplaceAll(strings.TrimSuffix(key, "_credits"), "_", "-"))
		}
	}
	return body, nil
}

func policyRule(effect, spec string) (map[string]any, error) {
	class, resource, _ := strings.Cut(strings.TrimSpace(spec), ":")
	if resource == "" {
		resource = "*"
	}
	if class != "*" && !contains(policyClasses, class) {
		return nil, fmt.Errorf("--%s %s: the class must be one of %s or *", effect, spec, strings.Join(policyClasses, ", "))
	}
	return map[string]any{"effect": effect, "class": class, "resource": resource}, nil
}

// policyRulesCell shows rules compactly: "allow internal_write:storage, ask *:*".
func policyRulesCell(row map[string]any) string {
	rules, _ := row["rules"].([]any)
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		m, _ := r.(map[string]any)
		part := fmt.Sprintf("%v %v:%v", m["effect"], m["class"], m["resource"])
		if m["window"] != nil {
			part += " (window)"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// resolvePolicyAgent turns --agent into the agent's id.
func resolvePolicyAgent(cmd *cobra.Command, c *api.Client) error {
	agent, _ := cmd.Flags().GetString("agent")
	if agent == "" {
		return nil
	}
	id, err := c.ResolveAgent(cmd.Context(), agent)
	if err != nil {
		return die(err)
	}
	_ = cmd.Flags().Set("agent", id)
	return nil
}

func resolvePolicyArgs(ctx context.Context, c *api.Client, args []string) ([]string, error) {
	id, err := resolvePolicy(ctx, c, args[0])
	if err != nil {
		return nil, err
	}
	return append([]string{id}, args[1:]...), nil
}

// resolvePolicy finds an active rule set (an approval policy in the API) by id, short id or name.
func resolvePolicy(ctx context.Context, c *api.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if api.IsUUID(ref) {
		return ref, nil
	}
	var out struct {
		Data []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/agents/policies", nil, &out); err != nil {
		return "", err
	}
	var matches []string
	for _, p := range out.Data {
		if (p.Name == ref && p.Status != "disabled") || (len(ref) >= 4 && strings.HasPrefix(p.ID, ref)) {
			matches = append(matches, p.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no rule set named %q (see 'miosa agent authority rules list')", ref)
	default:
		return "", fmt.Errorf("%q matches %d rule sets; use the id", ref, len(matches))
	}
}
