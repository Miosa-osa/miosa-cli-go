package commands

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// Connecting agent accounts for a workspace, or for one of your end users.
//
// Every command here takes the same two flags:
//
//	--workspace-id   the workspace the credential belongs to
//	--external-user  YOUR id for the end user; the credential is then theirs
//	                 alone and a run on one of their machines uses it first
//
// The API never returns a secret: a credential is a preview, a status and the
// account it signs in as.

// connScope is whose credentials a command reads or writes.
type connScope struct {
	Workspace    string
	ExternalUser string
}

func addScopeFlags(cmd *cobra.Command, s *connScope, workspace bool) {
	cmd.Flags().StringVar(&s.ExternalUser, "external-user", "", "Act for one of your end users (your own id for them)")
	cmd.Flags().StringVar(&s.ExternalUser, "external-user-id", "", "Same as --external-user")
	_ = cmd.Flags().MarkHidden("external-user-id")
	if workspace {
		cmd.Flags().StringVar(&s.Workspace, "workspace-id", "", "Workspace the connection belongs to")
	}
}

// set reports whether the command names a scope narrower than the organization.
func (s connScope) set() bool { return s.Workspace != "" || s.ExternalUser != "" }

func (s connScope) query() url.Values {
	q := url.Values{}
	if s.Workspace != "" {
		q.Set("workspace_id", s.Workspace)
	}
	if s.ExternalUser != "" {
		q.Set("external_user_id", s.ExternalUser)
	}
	return q
}

func (s connScope) apply(body map[string]any) map[string]any {
	if s.Workspace != "" {
		body["workspace_id"] = s.Workspace
	}
	if s.ExternalUser != "" {
		body["external_user_id"] = s.ExternalUser
	}
	return body
}

// plans maps what a person types to the credential and the sign-in provider.
// "mistral" alone is the API key; the Mistral plan is "vibe".
var plans = map[string]struct{ Credential, Signin string }{
	"claude":       {"claude_subscription", "claude_code"},
	"claude-code":  {"claude_subscription", "claude_code"},
	"claude_code":  {"claude_subscription", "claude_code"},
	"chatgpt":      {"chatgpt_subscription", "codex"},
	"codex":        {"chatgpt_subscription", "codex"},
	"kimi":         {"kimi_subscription", "kimi_code"},
	"kimi-code":    {"kimi_subscription", "kimi_code"},
	"vibe":         {"mistral_subscription", "mistral_vibe"},
	"mistral-vibe": {"mistral_subscription", "mistral_vibe"},
	"mistral-plan": {"mistral_subscription", "mistral_vibe"},
}

// agentKeyCredentials are the credentials that take a pasted key.
var agentKeyCredentials = map[string]bool{
	"anthropic": true, "openai": true, "openrouter": true, "deepseek": true,
	"llmgateway": true, "moonshot": true, "mistral": true,
}

// keyCredential resolves the credential a key is for. "claude" and "chatgpt"
// with a key mean the Anthropic and OpenAI API keys.
func keyCredential(provider string) (string, bool) {
	switch provider {
	case "claude", "claude-code", "claude_code":
		return "anthropic", true
	case "chatgpt", "codex":
		return "openai", true
	}
	return provider, agentKeyCredentials[provider]
}

// planCredential resolves the credential id of a plan or a key provider, for
// removing and testing.
func planCredential(provider string) string {
	if p, ok := plans[provider]; ok {
		return p.Credential
	}
	return provider
}

// requireEndUserScope refuses a write for an end user on a server that would
// ignore --external-user and store the connection for the whole workspace or
// organization. One read before the write: the server says whether it scoped
// the request to the end user.
func requireEndUserScope(ctx context.Context, c *api.Client, scope connScope) error {
	if scope.ExternalUser == "" {
		return nil
	}
	var out struct {
		Data struct {
			Scope struct {
				Level string `json:"level"`
			} `json:"scope"`
		} `json:"data"`
	}
	q := url.Values{"external_user_id": {scope.ExternalUser}}
	if err := c.Get(ctx, "/agents/settings", q, &out); err != nil {
		return die(sessionHint(err))
	}
	if out.Data.Scope.Level != "external_user" {
		return die(errors.New("this MIOSA server does not support connections for end users yet (--external-user would be ignored and the connection stored for everyone). Nothing was changed"))
	}
	return nil
}

// putAgentKey connects an API key at exactly the given scope.
func putAgentKey(cmd *cobra.Command, c *api.Client, provider, key string, scope connScope) error {
	credential, ok := keyCredential(provider)
	if !ok {
		return usagef("%q is not an agent credential; use one of: anthropic, openai, openrouter, deepseek, llmgateway, moonshot, mistral", provider)
	}
	if err := requireEndUserScope(cmd.Context(), c, scope); err != nil {
		return err
	}
	body := scope.apply(map[string]any{"api_key": key})
	if err := c.Put(cmd.Context(), "/agents/credentials/"+url.PathEscape(credential), body, nil); err != nil {
		return die(sessionHint(err))
	}
	return nil
}

// scopedConnectionRows reads the connections of a scope with the read-model.
func scopedConnectionRows(ctx context.Context, c *api.Client, scope connScope, kind string) ([]connRow, map[string]any, error) {
	q := scope.query()
	if kind != "" {
		q.Set("family", kind)
	}
	var out struct {
		Data []struct {
			Family   string `json:"family"`
			Provider string `json:"provider"`
			Label    string `json:"label"`
			ID       string `json:"id"`
			Status   string `json:"status"`
			Preview  string `json:"preview"`
			Account  struct {
				DisplayName string `json:"display_name"`
			} `json:"account"`
			Scope struct {
				Level string `json:"level"`
			} `json:"scope"`
			Inherited bool `json:"inherited"`
		} `json:"data"`
		Counts map[string]any `json:"counts"`
	}
	if err := c.Get(ctx, "/connections", q, &out); err != nil {
		return nil, nil, err
	}
	rows := make([]connRow, 0, len(out.Data))
	for _, d := range out.Data {
		detail := d.Preview
		if detail == "" {
			detail = d.Account.DisplayName
		}
		level := d.Scope.Level
		if d.Inherited {
			level += " (inherited)"
		}
		rows = append(rows, connRow{
			Kind: d.Family, Name: d.Provider, ID: d.ID, Status: d.Status,
			Detail: strings.TrimSpace(detail + " " + level), Provider: d.Provider,
		})
	}
	return rows, out.Counts, nil
}

// ── sign-in ──────────────────────────────────────────────────────────────────

type signinInfo struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Mode            string `json:"mode"`
	Status          string `json:"status"`
	VerificationURL string `json:"verification_url"`
	UserCode        string `json:"user_code"`
	ExpiresAt       string `json:"expires_at"`
	Error           string `json:"error"`
	Account         struct {
		Email string `json:"account_email"`
		Plan  string `json:"account_plan"`
	} `json:"account"`
}

// startSigninNoWait starts a sign-in and prints what to show the user, leaving
// the rest to the caller (their UI): the point of --no-wait.
func startSigninNoWait(cmd *cobra.Command, c *api.Client, p interface {
	JSON(v interface{}) error
	Line(format string, args ...interface{})
}, body map[string]any) error {
	var start struct {
		Data signinInfo `json:"data"`
	}
	if err := c.Post(cmd.Context(), "/agent-signins", body, &start); err != nil {
		return die(signinHint(err))
	}
	s := start.Data
	if isJSON() {
		return p.JSON(s)
	}
	p.Line("Sign-in %s (%s)", s.ID, s.Mode)
	if s.VerificationURL != "" {
		p.Line("  Open: %s", s.VerificationURL)
	}
	if s.UserCode != "" {
		p.Line("  Code: %s", s.UserCode)
	}
	if s.Mode == "paste_code" {
		p.Line("Then: miosa connections signin code %s --code-stdin%s", s.ID, scopeHint(body))
	} else {
		p.Line("Then: miosa connections signin wait %s%s", s.ID, scopeHint(body))
	}
	return nil
}

func scopeHint(body map[string]any) string {
	if ext, ok := body["external_user_id"].(string); ok && ext != "" {
		return " --external-user " + ext
	}
	return ""
}

// signinHint adds what the sign-in routes need to know to the generic hint.
func signinHint(err error) error {
	var ae *api.Error
	if errors.As(err, &ae) && (ae.Code == "SIGNIN_TARGET_NOT_FOUND" || ae.Code == "SIGNIN_TARGET_NOT_READY") {
		return fmt.Errorf("this server signs in inside a machine: pass --sandbox NAME or --computer NAME: %w", err)
	}
	return sessionHint(err)
}

func newConnSigninCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "signin",
		Short: "Follow a subscription sign-in started with --no-wait",
		Long: `A subscription sign-in started with 'miosa connections add models claude --no-wait'
prints a link (and a code). Show them to the user in your own UI, then drive
the sign-in from your side:

  miosa connections signin status SIGNIN_ID
  miosa connections signin code SIGNIN_ID --code-stdin <<< "$PASTED_CODE"   # Claude
  miosa connections signin wait SIGNIN_ID                                     # ChatGPT, Kimi
  miosa connections signin cancel SIGNIN_ID`,
	}
	cmd.AddCommand(newSigninStatusCmd(), newSigninCodeCmd(), newSigninWaitCmd(), newSigninCancelCmd())
	return cmd
}

func printSignin(cmd *cobra.Command, s signinInfo) error {
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(s)
	}
	switch s.Status {
	case "succeeded":
		who := s.Account.Email
		if s.Account.Plan != "" {
			who += " (" + s.Account.Plan + ")"
		}
		p.Success("Connected %s %s", s.Provider, strings.TrimSpace(who))
	default:
		line := s.Status
		if s.Error != "" {
			line += ": " + s.Error
		}
		p.Line("%s", line)
	}
	return nil
}

func newSigninStatusCmd() *cobra.Command {
	var scope connScope
	cmd := &cobra.Command{
		Use:   "status <signin-id>",
		Short: "Where a sign-in stands",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data signinInfo `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/agent-signins/"+url.PathEscape(args[0]), scope.query(), &out); err != nil {
				return die(sessionHint(err))
			}
			return printSignin(cmd, out.Data)
		},
	}
	addScopeFlags(cmd, &scope, false)
	return cmd
}

func newSigninCodeCmd() *cobra.Command {
	var (
		scope     connScope
		codeStdin bool
		code      string
	)
	cmd := &cobra.Command{
		Use:   "code <signin-id>",
		Short: "Hand over the code the user pasted (Claude)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pasted := code
			if pasted == "" {
				if !codeStdin {
					return usagef("pass the pasted code with --code-stdin (or --code)")
				}
				line, err := readSecret(cmd, "", true)
				if err != nil {
					return die(err)
				}
				pasted = line
			}
			if pasted == "" {
				return usagef("no code provided")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data signinInfo `json:"data"`
			}
			body := scope.apply(map[string]any{"code": pasted})
			if err := c.API.Post(cmd.Context(), "/agent-signins/"+url.PathEscape(args[0])+"/code", body, &out); err != nil {
				return die(sessionHint(err))
			}
			return printSignin(cmd, out.Data)
		},
	}
	addScopeFlags(cmd, &scope, false)
	cmd.Flags().BoolVar(&codeStdin, "code-stdin", false, "Read the pasted code from standard input")
	cmd.Flags().StringVar(&code, "code", "", "The pasted code")
	return cmd
}

func newSigninWaitCmd() *cobra.Command {
	var (
		scope   connScope
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "wait <signin-id>",
		Short: "Wait until the user has approved the sign-in",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			for {
				var out struct {
					Data signinInfo `json:"data"`
				}
				if err := c.API.Get(ctx, "/agent-signins/"+url.PathEscape(args[0]), scope.query(), &out); err != nil {
					return die(sessionHint(err))
				}
				switch out.Data.Status {
				case "succeeded":
					return printSignin(cmd, out.Data)
				case "failed", "expired", "cancelled":
					return die(fmt.Errorf("sign-in %s%s", out.Data.Status, map[bool]string{true: ": " + out.Data.Error, false: ""}[out.Data.Error != ""]))
				}
				select {
				case <-ctx.Done():
					return die(errors.New("timed out waiting for the sign-in"))
				case <-time.After(signinPollEvery):
				}
			}
		},
	}
	addScopeFlags(cmd, &scope, false)
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "Give up after this long")
	return cmd
}

func newSigninCancelCmd() *cobra.Command {
	var scope connScope
	cmd := &cobra.Command{
		Use:   "cancel <signin-id>",
		Short: "Cancel a sign-in",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if err := c.API.Delete(cmd.Context(), "/agent-signins/"+url.PathEscape(args[0]), scope.query(), nil); err != nil {
				return die(sessionHint(err))
			}
			printerFor(cmd).Success("Cancelled sign-in %s", args[0])
			return nil
		},
	}
	addScopeFlags(cmd, &scope, false)
	return cmd
}

// ── test a credential ────────────────────────────────────────────────────────

func testCredential(cmd *cobra.Command, provider string, scope connScope) error {
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	var out struct {
		Data struct {
			Credential string `json:"credential"`
			OK         *bool  `json:"ok"`
			Check      string `json:"check"`
			HTTPStatus *int   `json:"http_status"`
			Detail     string `json:"detail"`
			Scope      string `json:"scope"`
		} `json:"data"`
	}
	path := "/agents/credentials/" + url.PathEscape(planCredential(provider)) + "/test"
	if err := c.API.Post(cmd.Context(), path, scope.apply(map[string]any{}), &out); err != nil {
		return die(sessionHint(err))
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(out.Data)
	}
	d := out.Data
	switch {
	case d.OK != nil && *d.OK:
		p.Success("%s works (%s check, %s scope)", d.Credential, d.Check, d.Scope)
	case d.OK != nil:
		return die(fmt.Errorf("%s does not work: %s", d.Credential, orString(d.Detail, "not usable")))
	default:
		p.Line("%s could not be checked: %s", d.Credential, orString(d.Detail, "the provider did not answer"))
	}
	return nil
}

func orString(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ── tool-server keys ─────────────────────────────────────────────────────────

func newConnToolKeysCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "tool-keys",
		Aliases: []string{"tool-key"},
		Short:   "Keys for your tool servers, per workspace or end user",
		Long: `A tool server (Apify, Refero, your own) is defined once with its key as a
{{secret:NAME}} placeholder. Each end user then supplies their own value for NAME, and an agent
run for that end user reaches the tool server with their key, falling back to
the workspace's.

  miosa connections tool-keys list --external-user cus_123
  miosa connections tool-keys set APIFY_TOKEN --key-stdin --external-user cus_123 <<< "$APIFY_TOKEN"
  miosa connections tool-keys rm APIFY_TOKEN --external-user cus_123`,
	}
	cmd.AddCommand(newToolKeysListCmd(), newToolKeysSetCmd(), newToolKeysRmCmd())
	return cmd
}

type toolKey struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Scope     string `json:"scope"`
	Inherited bool   `json:"inherited"`
	Preview   string `json:"preview"`
	Servers   []struct {
		Name string `json:"name"`
	} `json:"servers"`
}

func newToolKeysListCmd() *cobra.Command {
	var scope connScope
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Keys the tool servers need, and whether the scope has them",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data []toolKey `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/agents/tool-keys", scope.query(), &out); err != nil {
				return die(sessionHint(err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(out)
			}
			if len(out.Data) == 0 {
				p.Line("No tool server references a secret yet.")
				return nil
			}
			rows := make([][]string, 0, len(out.Data))
			for _, k := range out.Data {
				servers := make([]string, 0, len(k.Servers))
				for _, s := range k.Servers {
					servers = append(servers, s.Name)
				}
				sort.Strings(servers)
				state := "missing"
				if k.Connected {
					state = k.Scope
					if k.Inherited {
						state += " (inherited)"
					}
				}
				rows = append(rows, []string{k.Name, state, k.Preview, strings.Join(servers, ", ")})
			}
			p.Table([]string{"KEY", "SET AT", "PREVIEW", "SERVERS"}, rows)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, true)
	return cmd
}

func newToolKeysSetCmd() *cobra.Command {
	var (
		scope    connScope
		keyStdin bool
	)
	cmd := &cobra.Command{
		Use:   "set <NAME>",
		Short: "Set or rotate a tool-server key",
		Long: `Reads the value from standard input (--key-stdin) or hidden at the prompt; it is
never accepted as an argument. Fails with UNKNOWN_TOOL_KEY when no tool server
references NAME.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := readSecret(cmd, "Value (input hidden): ", keyStdin)
			if err != nil {
				return die(err)
			}
			if value == "" {
				return usagef("no value provided (use --key-stdin or type it at the prompt)")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if err := requireEndUserScope(cmd.Context(), c.API, scope); err != nil {
				return err
			}
			var out struct {
				Data toolKey `json:"data"`
			}
			body := scope.apply(map[string]any{"value": value})
			if err := c.API.Put(cmd.Context(), "/agents/tool-keys/"+url.PathEscape(args[0]), body, &out); err != nil {
				return die(sessionHint(err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Set %s (%s)", out.Data.Name, out.Data.Scope)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, true)
	cmd.Flags().BoolVar(&keyStdin, "key-stdin", false, "Read the value from standard input")
	return cmd
}

func newToolKeysRmCmd() *cobra.Command {
	var (
		scope connScope
		yes   bool
	)
	cmd := &cobra.Command{
		Use:     "rm <NAME>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove the key held at exactly this scope",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Remove tool key %s? Runs that use it will fall back to a wider scope or fail.", args[0])); err != nil {
					return err
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if err := requireEndUserScope(cmd.Context(), c.API, scope); err != nil {
				return err
			}
			if err := c.API.Delete(cmd.Context(), "/agents/tool-keys/"+url.PathEscape(args[0]), scope.query(), nil); err != nil {
				return die(sessionHint(err))
			}
			printerFor(cmd).Success("Removed %s", args[0])
			return nil
		},
	}
	addScopeFlags(cmd, &scope, true)
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	return cmd
}

// ── harnesses ────────────────────────────────────────────────────────────────

func newConnHarnessesCmd() *cobra.Command {
	var scope connScope
	cmd := &cobra.Command{
		Use:   "harnesses [harness]",
		Short: "Which harnesses can run, and with which connections",
		Long: `Shows each harness (Claude Code, Codex, OpenCode, ...), whether a run would
have a connection to think with, and which of the connections could power it.
One plan can power several harnesses.

With a harness name: its models, and whether each is available with what is
connected.

  miosa connections harnesses --external-user cus_123
  miosa connections harnesses opencode`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if len(args) == 1 {
				return showHarnessModels(cmd, c.API, strings.ToLower(args[0]), scope)
			}
			var settings struct {
				Data struct {
					Harnesses []struct {
						ID      string `json:"id"`
						Label   string `json:"label"`
						Default bool   `json:"is_default"`
						Status  struct {
							State string `json:"state"`
						} `json:"status"`
						AuthMethods []struct {
							ID        string `json:"id"`
							Connected bool   `json:"connected"`
						} `json:"auth_methods"`
						Providers []struct {
							ID        string `json:"id"`
							Connected bool   `json:"connected"`
							Enabled   bool   `json:"enabled"`
						} `json:"providers"`
					} `json:"harnesses"`
				} `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/agents/settings", scope.query(), &settings); err != nil {
				return die(sessionHint(err))
			}
			type row struct {
				ID     string   `json:"id"`
				Label  string   `json:"label"`
				Ready  bool     `json:"ready"`
				Usable []string `json:"usable"`
			}
			var rows []row
			table := [][]string{}
			for _, h := range settings.Data.Harnesses {
				r := row{ID: h.ID, Label: h.Label, Ready: h.Status.State == "ready"}
				for _, m := range h.AuthMethods {
					if m.Connected {
						r.Usable = append(r.Usable, m.ID)
					}
				}
				for _, m := range h.Providers {
					if m.Connected && m.Enabled {
						r.Usable = append(r.Usable, m.ID)
					}
				}
				rows = append(rows, r)
				ready := "no credential"
				if r.Ready {
					ready = "ready"
				}
				table = append(table, []string{h.ID, ready, strings.Join(r.Usable, ", ")})
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"data": rows})
			}
			p.Table([]string{"HARNESS", "STATE", "USABLE CONNECTIONS"}, table)
			return nil
		},
	}
	addScopeFlags(cmd, &scope, true)
	return cmd
}

func showHarnessModels(cmd *cobra.Command, c *api.Client, harness string, scope connScope) error {
	if alias, ok := harnessAliases[harness]; ok {
		harness = alias
	}
	q := scope.query()
	q.Set("harness", harness)
	var out struct {
		Data struct {
			Harness      string   `json:"harness"`
			AuthMethod   string   `json:"auth_method"`
			Usable       []string `json:"usable_credentials"`
			DefaultModel string   `json:"default_model"`
			Models       []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Available bool   `json:"available"`
				Reason    string `json:"unavailable_reason"`
				Via       string `json:"via"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/agents/models", q, &out); err != nil {
		return die(sessionHint(err))
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(out)
	}
	rows := make([][]string, 0, len(out.Data.Models))
	for _, m := range out.Data.Models {
		state := "yes"
		if !m.Available {
			state = "no: " + orString(m.Reason, "not served by what is connected")
		} else if m.Via != "" {
			state = "yes, via " + m.Via
		}
		def := ""
		if m.ID == out.Data.DefaultModel {
			def = "default"
		}
		rows = append(rows, []string{m.ID, m.Name, state, def})
	}
	p.Table([]string{"MODEL", "NAME", "AVAILABLE", ""}, rows)
	if len(out.Data.Usable) == 0 {
		p.Line("Nothing is connected for %s yet: miosa connections add models <provider>", harness)
	}
	return nil
}

// ── choose which connection a harness uses ──────────────────────────────────

func newConnUseCmd() *cobra.Command {
	var (
		workspaceID string
		all         bool
		model       string
		effort      string
	)
	cmd := &cobra.Command{
		Use:   "use <harness> [connection...]",
		Short: "Choose which connections a harness runs on",
		Long: `A connection is connected once and powers every harness that accepts it. This
chooses which ones a harness uses, for the organization or one workspace.

A harness that takes one method at a time (claude-code, codex) takes exactly one
connection. A harness that takes several (opencode, pi, prime, kimi, mistral,
hermes) takes a list, or --all for every connected one. --model and --effort set
the harness's defaults.

  miosa connections use claude-code claude            # the Claude plan
  miosa connections use claude-code anthropic         # the Anthropic key
  miosa connections use opencode chatgpt openrouter   # the ChatGPT plan and the OpenRouter key
  miosa connections use opencode --all
  miosa connections use codex --model gpt-6.1-sol --effort high

Connections are named claude, chatgpt, kimi, vibe (plans) or anthropic, openai,
openrouter, deepseek, llmgateway, moonshot, mistral, bedrock (keys). The harness
settings belong to you, the builder: they are the same for all of your end users,
who bring only their connections.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			harness := strings.ToLower(args[0])
			if alias, ok := harnessAliases[harness]; ok {
				harness = alias
			}
			names := args[1:]
			if len(names) == 0 && !all && model == "" && effort == "" {
				return usagef("name the connection(s) to use, or --all, --model or --effort")
			}
			if all && len(names) > 0 {
				return usagef("--all and a list of connections cannot be combined")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			scope := connScope{Workspace: workspaceID}
			ids := make([]string, 0, len(names))
			for _, n := range names {
				ids = append(ids, planCredential(strings.ToLower(n)))
			}

			body := map[string]any{}
			if workspaceID != "" {
				body["workspace_id"] = workspaceID
			}
			if model != "" {
				body["default_model"] = model
			}
			if effort != "" {
				body["default_effort"] = effort
			}
			if len(ids) > 0 || all {
				mode, err := harnessMode(ctx, c.API, harness, scope)
				if err != nil {
					return err
				}
				switch {
				case mode == "single" && all:
					return usagef("%s takes one connection at a time: name it", harness)
				case mode == "single" && len(ids) != 1:
					return usagef("%s takes exactly one connection (got %d)", harness, len(ids))
				case mode == "single":
					body["auth_method"] = ids[0]
				case mode == "multi" && all:
					body["enabled_providers"] = nil
				case mode == "multi":
					body["enabled_providers"] = ids
				default:
					return usagef("%s has no connections to choose (%s)", harness, orString(mode, "unknown harness"))
				}
			}
			if err := c.API.Patch(ctx, "/agents/settings/harnesses/"+url.PathEscape(harness), body, nil); err != nil {
				return die(sessionHint(err))
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"status": "updated", "harness": harness, "settings": body})
			}
			p.Success("%s updated", harness)
			return nil
		},
	}
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Set it for one workspace instead of the organization")
	cmd.Flags().BoolVar(&all, "all", false, "Every connected provider (harnesses that take several)")
	cmd.Flags().StringVar(&model, "model", "", "Default model of the harness")
	cmd.Flags().StringVar(&effort, "effort", "", "Default reasoning effort: max, xhigh, high, medium, low, minimal or none")
	return cmd
}

// harnessMode reads whether a harness takes one connection ("single") or
// several ("multi"), from the platform's own settings.
func harnessMode(ctx context.Context, c *api.Client, harness string, scope connScope) (string, error) {
	var out struct {
		Data struct {
			Harnesses []struct {
				ID   string `json:"id"`
				Mode string `json:"mode"`
			} `json:"harnesses"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/agents/settings", scope.query(), &out); err != nil {
		return "", die(sessionHint(err))
	}
	for _, h := range out.Data.Harnesses {
		if h.ID == harness {
			return h.Mode, nil
		}
	}
	return "", usagef("unknown harness %q", harness)
}
