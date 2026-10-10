package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(agentCmd())
}

var agentCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"},
	{Head: "VERSION", Path: "current_version"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"}, {Head: "DESCRIPTION", Path: "description", Fmt: "clip"},
}

var agentDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Description", Path: "description"},
	{Head: "Workspace", Path: "workspace_id"}, {Head: "Project", Path: "project_id"}, {Head: "Version", Path: "version.version"},
	{Head: "Harness", Path: "version.configuration.harness"}, {Head: "Model", Path: "version.configuration.model"},
	{Head: "Overdrive", Path: "version.configuration.execution.overdrive"}, {Head: "Created", Path: "created_at", Fmt: "age"},
}

var versionCols = []Col{
	{Head: "VERSION", Path: "version"}, {Head: "FINGERPRINT", Path: "fingerprint", Fmt: "short"}, {Head: "HARNESS", Path: "configuration.harness"},
	{Head: "MODEL", Path: "configuration.model"}, {Head: "CREATED", Path: "created_at", Fmt: "age"},
}

var agentRef = Arg{Name: "agent"}

func agentCmd() *cobra.Command {
	cmd := group("agent", "Define, version and run agents",
		`An agent is a saved, versioned definition: harness, model, instructions, the
machine it runs on, and what it may do. Runs always use your own model
connections ('miosa connections add models'). <agent> is a name or an id.

  miosa agent new reviewer --harness claude-code --instructions-file review.md
  miosa agent publish reviewer --file config.yaml
  miosa agent diff reviewer 2 3
  miosa agent run reviewer "review PR 42" --sandbox my-box
  miosa agent harnesses`, []string{"agents"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List agents",
			Method: "GET", Path: "/agents", Cols: agentCols,
			Flags: []Flag{
				{Name: "status", Usage: "Only active or archived agents"},
				{Name: "workspace-id", Usage: "Only this workspace", Field: "workspace_id"},
				{Name: "limit", Short: "n", Type: "int", Usage: "How many"},
			},
		},
		Op{
			Use: "get <agent>", Aliases: []string{"show"}, Short: "Show an agent and its current version",
			Method: "GET", Path: "/agents/{0}", Args: []Arg{agentRef}, Detail: agentDetail,
		},
		Op{
			Use: "update <agent>", Short: "Change an agent's name or description",
			Method: "PATCH", Path: "/agents/{0}", Args: []Arg{agentRef}, Detail: agentDetail,
			Flags: []Flag{{Name: "name", Usage: "New name"}, {Name: "description", Usage: "New description"}},
		},
		Op{
			Use: "rm <agent>", Aliases: []string{"archive", "delete"}, Short: "Archive an agent (runs stop being accepted)",
			Method: "DELETE", Path: "/agents/{0}", Args: []Arg{agentRef}, Confirm: "Archive agent {0}?", Done: "Archived {0}",
		},
		Op{
			Use: "unarchive <agent>", Short: "Bring an archived agent back",
			Method: "POST", Path: "/agents/{0}/restore", Args: []Arg{agentRef}, Done: "Unarchived {0}",
		},
		Op{
			Use: "duplicate <agent>", Short: "Copy an agent with its current version",
			Method: "POST", Path: "/agents/{0}/duplicate", Args: []Arg{agentRef}, Detail: agentDetail,
			Flags: []Flag{{Name: "name", Usage: "Name of the copy"}},
		},
		Op{
			Use: "versions <agent>", Short: "List an agent's published versions",
			Method: "GET", Path: "/agents/{0}/versions", Args: []Arg{agentRef}, Cols: versionCols,
		},
		Op{
			Use: "version <agent> <number>", Short: "Show one published version",
			Method: "GET", Path: "/agents/{0}/versions/{1}", Args: []Arg{agentRef, {Name: "version"}},
		},
		Op{
			Use: "runs <agent>", Short: "List an agent's runs",
			Method: "GET", Path: "/agents/{0}/runs", Args: []Arg{agentRef}, Cols: runCols,
			Flags: []Flag{{Name: "status", Usage: "Only runs in this status: queued, running, succeeded, failed or canceled"}, {Name: "source", Usage: "Only runs from this source: console, api, workflow, trigger or test"}, {Name: "limit", Short: "n", Type: "int", Usage: "How many"}},
		},
		Op{
			Use: "harnesses", Short: "Show the available harnesses, models and what each can do",
			Long: `Show every harness (Claude Code, Codex, OSA, custom) with its models, default model
and capability flags such as new-machine runs and computer use. Needs the
agents:read scope.`,
			Method: "GET", Path: "/agents/harnesses",
			Cols: []Col{{Head: "HARNESS", Path: "harness"}, {Head: "NAME", Path: "name"}, {Head: "DEFAULT MODEL", Path: "default_model"}, {Head: "MODELS", Path: "models", Fmt: "list"}},
		},
		Op{
			Use: "harness-versions", Short: "Show installed and latest harness CLI versions",
			Method: "GET", Path: "/agents/harness-versions",
		},
		Op{
			Use: "templates", Short: "List starter agent templates",
			Method: "GET", Path: "/agents/templates",
			Cols: []Col{{Head: "ID", Path: "id"}, {Head: "NAME", Path: "name"}, {Head: "HARNESS", Path: "configuration.harness"}, {Head: "DESCRIPTION", Path: "description", Fmt: "clip"}},
		},
		Op{
			Use: "defaults", Short: "Show the workspace's defaults for new agents (harness, model)",
			Method: "GET", Path: "/agents/defaults",
		},
	)
	// 'harnesses' lists; its subcommands change the defaults new agents start with.
	for _, c := range cmd.Commands() {
		if c.Name() == "harnesses" {
			c.AddCommand(newHarnessDefaultCmd(), newHarnessSetCmd())
		}
	}
	// Older spellings keep working for a release and point at connections.
	for _, old := range []string{"credentials", "accounts"} {
		legacy := connectionsCmd()
		legacy.Use, legacy.Aliases = old, nil
		legacy.Hidden = true
		legacy.Deprecated = "use 'miosa connections'"
		cmd.AddCommand(legacy)
	}
	cmd.AddCommand(
		newAgentNewCmd(),
		newAgentPublishCmd(),
		newAgentDiffCmd(),
		newAgentRunCmd(),
		agentTriggersCmd(),
		agentApprovalsCmd(),
		agentAuthorityCmd(),
	)
	return cmd
}

// configFlags builds an agent configuration from a file and flags. Flags win.
type configFlags struct {
	file             string
	harness, model   string
	instructions     string
	instructionsFile string
	overdrive        bool
	timeout          int
}

func (f *configFlags) bind(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVarP(&f.file, "file", "f", "", "Configuration as YAML or JSON (the version's `configuration` object)")
	fl.StringVar(&f.harness, "harness", "", "Harness: claude-code, codex, osa or custom")
	fl.StringVar(&f.model, "model", "", "Model")
	fl.StringVar(&f.instructions, "instructions", "", "Instructions text")
	fl.StringVar(&f.instructionsFile, "instructions-file", "", "Read the instructions from a file")
	fl.BoolVar(&f.overdrive, "overdrive", false, "Run the harness without approval prompts (stored on the version)")
	fl.IntVar(&f.timeout, "timeout-sec", 0, "Run time limit in seconds (execution.timeout_seconds)")
}

func (f *configFlags) build() (map[string]any, error) {
	cfg := map[string]any{}
	if f.file != "" {
		data, err := os.ReadFile(f.file)
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			if jerr := json.Unmarshal(data, &raw); jerr != nil {
				return nil, fmt.Errorf("parsing %s (tried YAML and JSON): %w", f.file, err)
			}
		}
		cfg = normalizeYAML(raw).(map[string]any)
		// Accept a whole version or definition body too.
		if inner, ok := cfg["configuration"].(map[string]any); ok {
			cfg = inner
		}
	}
	if f.harness != "" {
		cfg["harness"] = f.harness
	}
	if f.model != "" {
		cfg["model"] = f.model
	}
	switch {
	case f.instructionsFile != "":
		b, err := os.ReadFile(f.instructionsFile)
		if err != nil {
			return nil, err
		}
		cfg["instructions"] = strings.TrimSpace(string(b))
	case f.instructions != "":
		cfg["instructions"] = f.instructions
	}
	if f.overdrive {
		setPath(cfg, "execution.overdrive", true)
	}
	if f.timeout > 0 {
		setPath(cfg, "execution.timeout_seconds", f.timeout)
	}
	return cfg, nil
}

// normalizeYAML turns yaml.v3's map[string]any / map[any]any into JSON-safe maps.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			t[k] = normalizeYAML(e)
		}
		return t
	case map[any]any:
		m := map[string]any{}
		for k, e := range t {
			m[fmt.Sprint(k)] = normalizeYAML(e)
		}
		return m
	case []any:
		for i, e := range t {
			t[i] = normalizeYAML(e)
		}
		return t
	}
	return v
}

func newAgentNewCmd() *cobra.Command {
	var (
		cf          configFlags
		description string
		workspaceID string
		projectID   string
	)
	cmd := &cobra.Command{
		Use:     "new <name>",
		Aliases: []string{"create"},
		Short:   "Create an agent with its first version",
		Long: `Create an agent. The configuration comes from --file, from flags, or both (flags
win). Unknown keys are rejected by the server with the offending path.

  miosa agent new reviewer --harness claude-code --model sonnet --instructions-file review.md
  miosa agent new builder -f builder.yaml --overdrive`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			cfg, err := cf.build()
			if err != nil {
				return usagef("%v", err)
			}
			body := map[string]any{"name": args[0], "configuration": cfg}
			if description != "" {
				body["description"] = description
			}
			if workspaceID != "" {
				body["workspace_id"] = workspaceID
			}
			if projectID != "" {
				body["project_id"] = projectID
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/agents", Body: body})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			def, _ := obj["definition"].(map[string]any)
			p.Success("Created agent %q (%s)", args[0], formatValue(lookupPath(def, "id"), ""))
			return nil
		},
	}
	cf.bind(cmd)
	cmd.Flags().StringVar(&description, "description", "", "What the agent is for")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace id")
	cmd.Flags().StringVar(&projectID, "project-id", "", "Project id")
	return cmd
}

func newAgentPublishCmd() *cobra.Command {
	var cf configFlags
	cmd := &cobra.Command{
		Use:   "publish <agent>",
		Short: "Publish a new immutable version",
		Long: `Publish a new version of an agent from --file and/or flags. The full configuration
is validated and fingerprinted; a version never changes after publication, so
a run can always prove what it ran. Runs started before keep their version.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			cfg, err := cf.build()
			if err != nil {
				return usagef("%v", err)
			}
			if len(cfg) == 0 {
				return usagef("nothing to publish: pass --file or configuration flags")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/agents/" + args[0] + "/versions", Body: map[string]any{"configuration": cfg}})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(resp)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			p.Success("Published version %s of %s", formatValue(obj["version"], ""), args[0])
			return nil
		},
	}
	cf.bind(cmd)
	return cmd
}

func newAgentDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <agent> <from> <to>",
		Short: "Show what changed between two versions",
		Long:  "Compare two published versions key by key. Only changed settings are listed.",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			get := func(v string) (map[string]any, error) {
				resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: "/agents/" + args[0] + "/versions/" + v})
				if err != nil {
					return nil, err
				}
				obj, _ := unwrapObject(resp).(map[string]any)
				cfg, _ := obj["configuration"].(map[string]any)
				return cfg, nil
			}
			a, err := get(args[1])
			if err != nil {
				return die(err)
			}
			b, err := get(args[2])
			if err != nil {
				return die(err)
			}
			changes := diffConfig(a, b)
			if isJSON() {
				return p.JSON(map[string]any{"from": args[1], "to": args[2], "changes": changes})
			}
			if len(changes) == 0 {
				p.Line("Versions %s and %s have the same configuration.", args[1], args[2])
				return nil
			}
			rows := make([][]string, 0, len(changes))
			for _, ch := range changes {
				rows = append(rows, []string{ch.Path, clipText(ch.From, 40), clipText(ch.To, 40)})
			}
			p.Table([]string{"SETTING", "v" + args[1], "v" + args[2]}, rows)
			return nil
		},
	}
}

type configChange struct {
	Path string `json:"path"`
	From string `json:"from"`
	To   string `json:"to"`
}

// diffConfig lists the leaf settings that differ between two configurations.
func diffConfig(a, b map[string]any) []configChange {
	fa, fb := flattenConfig("", a), flattenConfig("", b)
	keys := map[string]bool{}
	for k := range fa {
		keys[k] = true
	}
	for k := range fb {
		keys[k] = true
	}
	var out []configChange
	for k := range keys {
		if fa[k] != fb[k] {
			out = append(out, configChange{Path: k, From: orDash(fa[k]), To: orDash(fb[k])})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func flattenConfig(prefix string, v any) map[string]string {
	out := map[string]string{}
	switch t := v.(type) {
	case map[string]any:
		for k, e := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			for fk, fv := range flattenConfig(key, e) {
				out[fk] = fv
			}
		}
	default:
		b, _ := json.Marshal(t)
		out[prefix] = strings.Trim(string(b), `"`)
	}
	return out
}

func newAgentRunCmd() *cobra.Command {
	var o promptOptions
	cmd := &cobra.Command{
		Use:   "run <agent> [text...]",
		Short: "Run a saved agent (same as 'prompt --agent')",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.agent = args[0]
			return runPrompt(cmd, args[1:], o)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.sandbox, "sandbox", "s", "", "Sandbox to run in")
	f.StringVar(&o.computer, "computer", "", "Computer to run in")
	f.BoolVar(&o.newMachine, "new-machine", false, "Provision a machine for this run")
	f.StringVar(&o.chat, "chat", "", "Chat id")
	f.BoolVar(&o.noFollow, "no-follow", false, "Print the run id and return")
	f.StringVar(&o.idem, "idempotency-key", "", "Replay-safe key")
	return cmd
}

// ─── triggers, approvals, authority ──────────────────────────────────────────

func agentTriggersCmd() *cobra.Command {
	trig := Arg{Name: "trigger"}
	return group("triggers", "Run an agent on a schedule, webhook or event",
		"Triggers start runs of an agent without a person: a schedule, a trigger URL, or an event.", []string{"trigger"},
		Op{
			Use: "list <agent>", Aliases: []string{"ls"}, Short: "List an agent's triggers",
			Method: "GET", Path: "/agents/{0}/triggers", Args: []Arg{agentRef},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "KIND", Path: "kind"}, {Head: "STATUS", Path: "status"}, {Head: "SCHEDULE", Path: "schedule"}, {Head: "NAME", Path: "name"}},
		},
		Op{
			Use: "add <agent>", Short: "Create a trigger",
			Method: "POST", Path: "/agents/{0}/triggers", Args: []Arg{agentRef},
			Flags: []Flag{
				{Name: "kind", Usage: "Kind of trigger", Required: true, Enum: []string{"schedule", "webhook"}},
				{Name: "name", Usage: "A label"},
				{Name: "schedule", Usage: "Cron expression for a schedule trigger"},
				{Name: "input", Usage: "The instruction each run receives"},
			},
		},
		Op{
			Use: "rm <agent> <trigger>", Short: "Delete a trigger",
			Method: "DELETE", Path: "/agents/{0}/triggers/{1}", Args: []Arg{agentRef, trig}, Confirm: "Delete trigger {1}?", Done: "Deleted trigger {1}",
		},
		Op{
			Use: "fire <agent> <trigger>", Short: "Run a trigger now",
			Method: "POST", Path: "/agents/{0}/triggers/{1}/fire", Args: []Arg{agentRef, trig},
		},
		Op{
			Use: "rotate-secret <agent> <trigger>", Short: "Rotate a webhook trigger's secret (shown once)",
			Method: "POST", Path: "/agents/{0}/triggers/{1}/rotate-secret", Args: []Arg{agentRef, trig},
		},
	)
}

func agentApprovalsCmd() *cobra.Command {
	return group("approvals", "Approve or deny actions an agent is waiting on",
		"An agent that hits a gated action (a deploy, a delete, an external write) pauses until someone approves it.", nil,
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List pending approvals",
			Method: "GET", Path: "/actions/approvals",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "ACTION", Path: "action"}, {Head: "STATUS", Path: "status"}, {Head: "AGENT", Path: "agent_definition_id", Fmt: "short"}, {Head: "REQUESTED", Path: "inserted_at", Fmt: "age"}},
		},
		Op{
			Use: "approve <id>", Short: "Approve an action",
			Method: "POST", Path: "/actions/approvals/{0}/approve", Args: []Arg{{Name: "approval"}}, Done: "Approved {0}",
		},
		Op{
			Use: "deny <id>", Short: "Deny an action",
			Method: "POST", Path: "/actions/approvals/{0}/deny", Args: []Arg{{Name: "approval"}}, Done: "Denied {0}",
		},
	)
}

func agentAuthorityCmd() *cobra.Command {
	return group("authority", "What agents are allowed to do",
		"The catalog of gated actions, standing grants, and the receipts of what ran.", nil,
		Op{
			Use: "catalog", Short: "List gated actions and their scope",
			Method: "GET", Path: "/actions/catalog",
			Cols: []Col{{Head: "ACTION", Path: "name"}, {Head: "SCOPE", Path: "scope"}, {Head: "RISK", Path: "risk"}, {Head: "DESCRIPTION", Path: "description", Fmt: "clip"}},
		},
		Op{
			Use: "grants", Short: "List standing grants",
			Method: "GET", Path: "/actions/grants",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "ACTION", Path: "action"}, {Head: "SCOPE", Path: "scope"}, {Head: "EXPIRES", Path: "expires_at"}},
		},
		Op{
			Use: "revoke <grant>", Short: "Revoke a standing grant",
			Method: "DELETE", Path: "/actions/grants/{0}", Args: []Arg{{Name: "grant"}}, Confirm: "Revoke grant {0}?", Done: "Revoked {0}",
		},
		Op{
			Use: "receipts", Short: "List receipts of authorized actions",
			Method: "GET", Path: "/actions/receipts",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "ACTION", Path: "action"}, {Head: "DECISION", Path: "decision"}, {Head: "WHEN", Path: "inserted_at", Fmt: "age"}},
		},
	)
}

func newHarnessDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "default <harness>",
		Short: "Make a harness the default for new agents and prompts",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return putAgentDefaults(cmd, map[string]any{"harness": args[0]})
		},
	}
}

func newHarnessSetCmd() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:   "set <harness> --model <model>",
		Short: "Set a harness and the model new agents use with it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return putAgentDefaults(cmd, map[string]any{"harness": args[0], "model": model})
		},
	}
	cmd.Flags().StringVar(&model, "model", "", "Model for the harness")
	_ = cmd.MarkFlagRequired("model")
	return cmd
}

func putAgentDefaults(cmd *cobra.Command, body map[string]any) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	if err := c.API.Put(cmd.Context(), "/agents/defaults", body, nil); err != nil {
		return die(err)
	}
	if isJSON() {
		return p.JSON(body)
	}
	p.Success("New agents default to %s%s", body["harness"], map[bool]string{true: " / " + fmt.Sprint(body["model"]), false: ""}[body["model"] != nil])
	return nil
}
