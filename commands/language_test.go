package commands_test

// Product language guard for the miosa CLI.
//
// It checks two things against the glossary (docs/product/product-language.md):
//
//  1. Help text: every visible command's Use, Short, Long, Example, aliases and
//     flag usage strings, walked from the real cobra tree.
//  2. Message text: string literals passed to fmt.Errorf, errors.New and the
//     output package, found by parsing the CLI sources.
//
// Violations are compared with testdata/product-language-baseline.json. The
// check is a ratchet: new violations fail, fixed ones are reported so the
// baseline can shrink. Regenerate with:
//
//	go test ./commands -run TestProductLanguage -update-language-baseline
//
// Suppress one literal with a trailing "// product-language-ignore: reason"
// comment on the same source line (message text only).
//
// The rule ids match scripts/check-product-language.mjs in MiosaOS, so a term
// is banned in the console and in the CLI at the same time.

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

var updateLanguageBaseline = flag.Bool("update-language-baseline", false, "rewrite testdata/product-language-baseline.json")

const languageBaselinePath = "../testdata/product-language-baseline.json"

type languageRule struct {
	ID       string
	Pattern  *regexp.Regexp
	Replace  string
	SkipText *regexp.Regexp
}

func lr(id, pattern, replace string, skip string) languageRule {
	r := languageRule{ID: id, Pattern: regexp.MustCompile(pattern), Replace: replace}
	if skip != "" {
		r.SkipText = regexp.MustCompile(skip)
	}
	return r
}

// languageRules mirrors RULES in the console script. Keep ids in sync.
var languageRules = []languageRule{
	// Connections.
	lr("connected-account", `(?i)\bconnected accounts?\b`, "Connection", ""),
	lr("integration", `(?i)\bintegrations?\b`, "App (the kind) or Connection (the module)", ""),
	lr("connector", `(?i)\bconnectors?\b`, "App or Tool server", ""),
	lr("mcp-servers", `(?i)\bMCP servers?\b`, "Tool server (outbound) or MIOSA MCP (inbound)", ""),
	lr("provider-account", `(?i)\b(?:provider|agent|AI|model|LLM) accounts?\b`, "Connection (model provider)", `\b(?:AWS|GCP|Google Cloud)\b`),
	lr("provider-key", `(?i)\bprovider keys?\b`, "API key on a Model provider connection", ""),
	lr("credentials", `(?i)\bcredentials?\b`, "the specific noun: Connection, API key, Gateway key, Machine key, Variable", ""),
	lr("access-token", `(?i)\b(?:access|auth|API) tokens?\b`, "API key or Scoped token", ""),
	lr("access-key", `(?i)\baccess keys?\b`, "API key", `AWS|Access key ID|Secret access key|IAM`),
	lr("secret-variable", `(?i)\b(?:secret|config) (?:key|variable|var)s?\b`, "Variable (secret by default) or Secret file", ""),
	lr("env-vars-abbrev", `(?i)\benv vars?\b`, "Variables", ""),
	lr("setup-file", `(?i)\bsetup files?\b`, "Setup script or Secret file", ""),

	// Environments and machines.
	lr("machine-setup", `(?i)\b(?:machine|runtime|agent) (?:setups?|profiles?)\b`, "Environment", ""),
	lr("deploy-target-env", `(?i)\b(?:deployment|deploy|project) environments?\b`, "Deploy target", ""),
	lr("safe-for-third-parties", `(?i)safe for third[- ]part(?:y|ies)|third[- ]party[- ]safe`, "Protected", ""),
	lr("vm", `\b(?:VMs?|microVMs?|virtual machines?)\b`, "machine (or sandbox / computer)", ""),
	lr("machine-instance", `(?i)\b(?:sandbox|computer|machine|desktop) instances?\b`, "sandbox, computer or machine", ""),
	lr("terminate", `(?i)\bterminat(?:e|ed|es|ing)\b`, "Destroy (machines) or Delete (records)", ""),
	lr("suspend", `(?i)\b(?:auto-)?suspend(?:ed|s|ing)?\b`, "Pause / Paused / Auto-pause", ""),
	lr("hibernate", `(?i)\bhibernat(?:e|ed|ing|ion)\b`, "Pause or Stop", ""),
	lr("checkpoint", `(?i)\bcheckpoints?\b`, "Snapshot", ""),
	lr("restore-snapshot", `(?i)\brestor(?:e|ed|es|ing)\b[^.]{0,40}\b(?:snapshot|checkpoint)s?\b|\b(?:snapshot|checkpoint)s?\b[^.]{0,30}\brestor(?:e|ed|es|ing)\b`, "Fork (a snapshot becomes a new machine; the source is unchanged)", ""),
	lr("promote-to-deployment", `(?i)\bpromote to deployment\b`, "Deploy", ""),

	// Scope.
	lr("tenant", `(?i)\btenants?\b`, "organization", ""),
	lr("account-as-organization", `(?i)\b(?:your|this|the|per|across the|across your) account(?:'s)? (?:balance|capacity|plan|limits?|credits?|usage|spend)\b|\bacross the account\b|\bin your account\b`, "organization (or wallet for balance and credits)", ""),

	// Agents.
	lr("bot-assistant", `(?i)\bbots?\b|\bAI assistants?\b`, "Agent", ""),
	lr("agent-session", `(?i)\b(?:agent|harness|chat) sessions?\b`, "Chat or Run", ""),
	lr("conversation", `(?i)\bconversations?\b`, "Chat", ""),
	lr("agent-task", `(?i)\b(?:agent|AI) (?:tasks?|jobs?)\b`, "Run", ""),
	lr("system-prompt", `(?i)\bsystem prompt\b`, "Instructions", ""),
	lr("action-authority", `(?i)\baction authority\b|\bagent polic(?:y|ies)\b|\bagent permissions\b`, "Authority", ""),
	lr("inbound-webhook", `(?i)\binbound webhooks?\b`, "Trigger URL", ""),
	lr("cron", `(?i)\bcron(?: jobs?)?\b`, "Schedule", `(?i)cron (?:expression|syntax)|crontab|\*/`),

	// Billing.
	lr("balance-variants", `(?i)\b(?:prepaid|spendable|available prepaid) balance\b`, "Balance", ""),
	lr("buy-credits", `(?i)\b(?:buy|purchase) credits\b|\btop[- ]?ups?\b|\btop up\b`, "Add credits", ""),
	lr("auto-funding", `(?i)\bautomatic funding\b|\bauto[- ]?recharge\b`, "Auto-reload", ""),

	// Style.
	lr("em-dash", "—", "a comma, colon, period or hyphen", ""),
	lr("please", `\b[Pp]lease\b`, "an imperative ('Try again.')", ""),
	lr("oops", `(?i)\b(?:oops|whoops|uh[- ]oh)\b`, "state what happened", ""),
	lr("successfully", `(?i)\bsuccessfully\b`, "the past-tense verb alone ('Saved')", ""),
	lr("click-here", `(?i)\bclick here\b`, "a descriptive link", ""),
	lr("log-in", `(?i)\b(?:log ?in|log-in)\b`, "Sign in (the command name `login` is exempt)", "(?i)miosa (?:auth )?login|`login`|'login'|login shell"),
	lr("log-out", `(?i)\b(?:log ?out|log-out)\b`, "Sign out (the command name `logout` is exempt)", "(?i)miosa logout|`logout`|'logout'"),
	lr("failed-to", `(?i)\b(?:failed to|unable to)\b`, "Could not <verb>, then what to do next", ""),
	lr("something-went-wrong", `(?i)\bsomething went wrong\b`, "say what happened and what to do", ""),
	lr("are-you-sure", `(?i)\bare you sure\b`, "name the thing and the consequence", ""),
	lr("cancelled-spelling", `(?i)\bcancell(?:ed|ing|ation)\b`, "canceled / canceling", ""),
	lr("stub-copy", `(?i)\bcoming soon\b|\barrives shortly\b|\bbeing switched on\b|\bnot available on MIOSA yet\b`, "ship the feature or hide the command", ""),
}

// bannedCommandNames are command names and aliases that name a concept the
// glossary retired. Value is the canonical replacement. A command that is
// Hidden or Deprecated is exempt: that is how a rename ships.
var bannedCommandNames = map[string]string{
	"checkpoint":  "snapshot",
	"checkpoints": "snapshot",
	"restore":     "snapshot fork",
	"setup":       "env",
	"integration": "connections",
	"connector":   "connections",
	"tenant":      "org",
	"vm":          "sandbox",
	"hibernate":   "pause",
	"suspend":     "pause",
	"terminate":   "destroy",
	"cron":        "schedule",

	"safe-third-parties": "protect",
}

// retiredCommandPaths are full command paths that collide with another module's
// word. Same exemption as bannedCommandNames.
var retiredCommandPaths = map[string]string{
	"miosa snapshot deploy": "miosa snapshot fork <name> (a Deployment is production; a fork is a new machine)",
}

// secretPlaceholder matches Use placeholders that would take a secret as a
// positional argument. Secrets come from --key-stdin or an interactive prompt.
var secretPlaceholder = regexp.MustCompile(`(?i)<(?:api-?key|token|secret|password)>`)

type languageFinding struct {
	Scope string
	Rule  string
	Text  string
	Fix   string
}

func (f languageFinding) key() string { return f.Scope + "::" + f.Rule }

func applyLanguageRules(scope, text string) []languageFinding {
	var out []languageFinding
	for _, r := range languageRules {
		if !r.Pattern.MatchString(text) {
			continue
		}
		if r.SkipText != nil && r.SkipText.MatchString(text) {
			continue
		}
		out = append(out, languageFinding{Scope: scope, Rule: r.ID, Text: text, Fix: r.Replace})
	}
	return out
}

func commandPath(c *cobra.Command) string { return c.CommandPath() }

// cobraGenerated are commands and flags cobra adds on first Execute. They
// appear only after another test has run the tree, so they are never checked.
var cobraGenerated = map[string]bool{"help": true, "completion": true}

func walkCommands(c *cobra.Command, visit func(*cobra.Command)) {
	if c.Hidden || c.Deprecated != "" || cobraGenerated[c.Name()] {
		return
	}
	visit(c)
	for _, sub := range c.Commands() {
		walkCommands(sub, visit)
	}
}

func helpFindings() []languageFinding {
	var out []languageFinding
	walkCommands(commands.Root(), func(c *cobra.Command) {
		path := commandPath(c)
		texts := map[string]string{"Short": c.Short, "Long": c.Long, "Example": c.Example}
		for field, text := range texts {
			if strings.TrimSpace(text) == "" {
				continue
			}
			out = append(out, applyLanguageRules(path+" ["+field+"]", text)...)
		}
		// Structure rules for Short.
		if s := c.Short; s != "" {
			first := []rune(s)[0]
			if !unicode.IsUpper(first) && !strings.HasPrefix(s, "miosa") {
				out = append(out, languageFinding{Scope: path + " [Short]", Rule: "cli-short-capital", Text: s, Fix: "start Short with a capital letter"})
			}
			if strings.HasSuffix(s, ".") {
				out = append(out, languageFinding{Scope: path + " [Short]", Rule: "cli-short-period", Text: s, Fix: "drop the trailing period from Short"})
			}
			if len([]rune(s)) > 80 {
				out = append(out, languageFinding{Scope: path + " [Short]", Rule: "cli-short-length", Text: s, Fix: "keep Short to 80 characters"})
			}
		}
		// Retired command names and aliases.
		names := append([]string{c.Name()}, c.Aliases...)
		for _, n := range names {
			if fix, bad := bannedCommandNames[strings.ToLower(n)]; bad {
				out = append(out, languageFinding{Scope: path + " [name]", Rule: "cli-retired-command", Text: n, Fix: "rename to `" + fix + "` and keep `" + n + "` as a hidden deprecated alias"})
			}
		}
		if fix, bad := retiredCommandPaths[path]; bad {
			out = append(out, languageFinding{Scope: path + " [name]", Rule: "cli-retired-command", Text: c.Name(), Fix: "rename to " + fix})
		}
		// Secrets never travel as positional arguments.
		if secretPlaceholder.MatchString(c.Use) {
			out = append(out, languageFinding{Scope: path + " [Use]", Rule: "cli-secret-positional", Text: c.Use, Fix: "read the secret with --key-stdin or an interactive prompt"})
		}
		// Flag usage text.
		visitFlag := func(f *pflag.Flag) {
			if f.Name == "help" {
				return
			}
			scope := path + " [--" + f.Name + "]"
			out = append(out, applyLanguageRules(scope, f.Usage)...)
			if f.Usage != "" && !unicode.IsUpper([]rune(f.Usage)[0]) && !strings.HasPrefix(f.Usage, "miosa") {
				out = append(out, languageFinding{Scope: scope, Rule: "cli-flag-capital", Text: f.Usage, Fix: "start flag usage with a capital letter"})
			}
			if strings.HasSuffix(f.Usage, ".") {
				out = append(out, languageFinding{Scope: scope, Rule: "cli-flag-period", Text: f.Usage, Fix: "drop the trailing period from flag usage"})
			}
		}
		c.LocalFlags().VisitAll(visitFlag)
	})
	return out
}

// messageSinks are calls whose first string literal is shown to a person.
var messageSinks = map[string]bool{
	"fmt.Errorf":   true,
	"errors.New":   true,
	"output.Error": true,
	"output.Warn":  true,
	"p.Line":       true,
	"p.Success":    true,
	"printer.Line": true,
}

func literalString(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if v.Op != token.ADD {
			return "", false
		}
		l, lok := literalString(v.X)
		r, rok := literalString(v.Y)
		if lok && rok {
			return l + r, true
		}
	}
	return "", false
}

func messageFindings(t *testing.T) []languageFinding {
	t.Helper()
	var out []languageFinding
	roots := []string{".", "../cmd", "../internal/client", "../internal/config", "../internal/output"}
	fset := token.NewFileSet()
	for _, dir := range roots {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			lines := strings.Split(string(src), "\n")
			file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok || !messageSinks[id.Name+"."+sel.Sel.Name] {
					return true
				}
				text, ok := literalString(call.Args[0])
				if !ok {
					return true
				}
				pos := fset.Position(call.Pos())
				if pos.Line-1 < len(lines) && strings.Contains(lines[pos.Line-1], "product-language-ignore") {
					return true
				}
				scope := filepath.ToSlash(filepath.Clean(path)) + ":" + strconv.Itoa(pos.Line)
				out = append(out, applyLanguageRules(scope, text)...)
				return true
			})
		}
	}
	return out
}

type languageBaseline struct {
	Note   string         `json:"note"`
	Total  int            `json:"total"`
	Counts map[string]int `json:"counts"`
}

func countFindings(fs []languageFinding) map[string]int {
	m := map[string]int{}
	for _, f := range fs {
		m[baselineKey(f)]++
	}
	return m
}

// baselineKey groups source-line scopes by file so editing a file does not
// churn the baseline; help-text scopes stay per command and field.
func baselineKey(f languageFinding) string {
	scope := f.Scope
	if i := strings.LastIndex(scope, ".go:"); i >= 0 {
		scope = scope[:i+3]
	}
	return scope + "::" + f.Rule
}

func TestProductLanguage(t *testing.T) {
	findings := append(helpFindings(), messageFindings(t)...)
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].key() < findings[j].key() })
	counts := countFindings(findings)

	// PRODUCT_LANGUAGE_REPORT=<path> writes every finding as TSV for audits.
	if path := os.Getenv("PRODUCT_LANGUAGE_REPORT"); path != "" {
		var sb strings.Builder
		for _, f := range findings {
			fmt.Fprintf(&sb, "%s\t%s\t%s\t%s\n", f.Scope, f.Rule, strings.ReplaceAll(f.Text, "\n", " "), f.Fix)
		}
		if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if *updateLanguageBaseline {
		b := languageBaseline{
			Note:   "Known glossary violations in CLI help text and messages. Shrink this file; never grow it. Regenerate with -update-language-baseline.",
			Total:  len(findings),
			Counts: counts,
		}
		data, err := json.MarshalIndent(b, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(languageBaselinePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(languageBaselinePath, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("baseline written: %d known violations", len(findings))
		return
	}

	var base languageBaseline
	if data, err := os.ReadFile(languageBaselinePath); err == nil {
		if err := json.Unmarshal(data, &base); err != nil {
			t.Fatalf("parse baseline: %v", err)
		}
	}

	seen := map[string]int{}
	var fresh []string
	for _, f := range findings {
		k := baselineKey(f)
		seen[k]++
		if seen[k] > base.Counts[k] {
			fresh = append(fresh, fmt.Sprintf("  %s [%s] %q -> use: %s", f.Scope, f.Rule, truncateRunes(f.Text, 90), f.Fix))
		}
	}
	fixed := 0
	for k, n := range base.Counts {
		if counts[k] < n {
			fixed += n - counts[k]
		}
	}
	if fixed > 0 {
		t.Logf("%d baseline violation(s) fixed; run with -update-language-baseline to lock it in", fixed)
	}
	if len(fresh) > 0 {
		t.Errorf("product language guard failed (glossary: docs/product/product-language.md)\n%s\n%d new violation(s). Fix the wording, or add `// product-language-ignore: reason` to the source line.",
			strings.Join(fresh, "\n"), len(fresh))
	}
}

// TestProductLanguageRulesCatchTheirOwnExamples keeps the rules honest: each
// banned phrase must trip its rule, and the canonical replacement must not.
func TestProductLanguageRulesCatchTheirOwnExamples(t *testing.T) {
	cases := []struct {
		text     string
		wantRule string
	}{
		{"List your connected accounts", "connected-account"},
		{"Create a checkpoint", "checkpoint"},
		{"Restore a sandbox from a snapshot", "restore-snapshot"},
		{"Failed to start the sandbox", "failed-to"},
		{"Your tenant has no credits", "tenant"},
		{"Remove stored credentials", "credentials"},
		{"miosa — the official CLI", "em-dash"},
		{"Suspend a computer", "suspend"},
	}
	for _, c := range cases {
		found := false
		for _, f := range applyLanguageRules("test", c.text) {
			if f.Rule == c.wantRule {
				found = true
			}
		}
		if !found {
			t.Errorf("rule %s did not match %q", c.wantRule, c.text)
		}
	}
	clean := []string{
		"List your connections",
		"Create a snapshot",
		"Fork a sandbox from a snapshot",
		"Could not start the sandbox. Run `miosa list` to see its state",
		"Run `miosa login` to sign in",
		"Sandboxes and computers in this workspace",
	}
	for _, text := range clean {
		if fs := applyLanguageRules("test", text); len(fs) > 0 {
			t.Errorf("%q should be clean, got rule %s", text, fs[0].Rule)
		}
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(strings.ReplaceAll(s, "\n", " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "..."
}
