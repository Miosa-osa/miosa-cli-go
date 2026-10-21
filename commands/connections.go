package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

func init() {
	rootCmd.AddCommand(connectionsCmd())
}

// A connection is an external identity or key that MIOSA holds for you and
// calls outward with: a model provider (a subscription or an API key), an app
// (OAuth) or a tool server (MCP). Entered once, here.

func connectionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "connections",
		Aliases: []string{"connection", "conn"},
		Short:   "Model providers, apps and tool servers your agents use",
		Long: `Connections are what your agents and machines use to reach the outside world:
model providers (Claude, ChatGPT, API keys), apps (GitHub, Slack, Linear,
Discord, Google Workspace, Notion) and tool servers. MIOSA's own model keys never power your agents;
every run uses a connection of yours.

  miosa connections list
  miosa connections add models anthropic --key-stdin <<< "$ANTHROPIC_API_KEY"
  miosa connections add models claude --sandbox my-box     # sign in with a subscription
  miosa connections add apps github
  miosa connections add tools --name docs --url https://mcp.example.com/sse
  miosa connections rm models anthropic

For a platform builder: connect accounts for your end users, without sending
anyone to the console. Every command takes --external-user (your own id for the
user) and --workspace-id:

  miosa connections add models claude --external-user cus_123 --no-wait   # link + code to show them
  miosa connections add models anthropic --external-user cus_123 --key-stdin <<< "$KEY"
  miosa connections test models anthropic --external-user cus_123
  miosa connections tool-keys set APIFY_TOKEN --external-user cus_123 --key-stdin <<< "$TOKEN"
  miosa connections harnesses --external-user cus_123

An API key needs the connections:read scope to list and connections:write to
change them: 'miosa api-key create NAME --preset cli' has both. A list the key
may not read is reported as unavailable instead of failing the whole command.
Signing in with a subscription or an app prints a link or code to finish in a
browser.`,
	}
	cmd.AddCommand(newConnListCmd(), newConnAddCmd(), newConnRmCmd(), newConnTestCmd(), newConnShareCmd(), newConnUnshareCmd(),
		newConnSigninCmd(), newConnToolKeysCmd(), newConnHarnessesCmd(), newConnUseCmd())
	return cmd
}

type connRow struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	ID       string `json:"id,omitempty"`
	Status   string `json:"status,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Provider string `json:"provider,omitempty"`
}

func sessionOnlyNote(err error) string {
	switch {
	case api.IsStatus(err, 403):
		return "this key lacks the connections:read scope; mint one with: miosa api-key create NAME --preset cli"
	case api.IsStatus(err, 401):
		return "this server only accepts a console session for this route; update the server or use the console"
	}
	return friendlyMessage(err)
}

func newConnListCmd() *cobra.Command {
	var (
		kind  string
		scope connScope
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List connections",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			switch kind {
			case "", "models", "apps", "tools":
			default:
				return usagef("--kind must be models, apps or tools")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			var rows []connRow
			unavailable := map[string]string{}

			// A named scope reads the connections read-model, which knows
			// end users; the unscoped list keeps merging the older sources.
			if scope.set() {
				scoped, _, err := scopedConnectionRows(ctx, c.API, scope, kind)
				if err != nil {
					return die(sessionHint(err))
				}
				if isJSON() {
					return p.JSON(map[string]any{"data": scoped, "unavailable": unavailable})
				}
				if len(scoped) == 0 {
					p.Line("No connections at this scope yet.")
					return nil
				}
				out := make([][]string, 0, len(scoped))
				for _, r := range scoped {
					out = append(out, []string{r.Kind, r.Name, r.Status, r.Detail, r.ID})
				}
				p.Table([]string{"KIND", "NAME", "STATUS", "DETAIL", "ID"}, out)
				return nil
			}

			if kind == "" || kind == "models" {
				var keys struct {
					Data []map[string]any `json:"data"`
				}
				if err := c.API.Get(ctx, "/settings/provider-keys", nil, &keys); err != nil {
					unavailable["models (api keys)"] = sessionOnlyNote(err)
				}
				for _, k := range keys.Data {
					rows = append(rows, connRow{Kind: "models", Name: fmt.Sprint(k["provider"]), Status: "api key", Detail: fmt.Sprint(orEmpty(k["key_preview"], k["preview"]))})
				}
				var accts struct {
					Data []map[string]any `json:"data"`
				}
				if err := c.API.Get(ctx, "/connected-accounts", nil, &accts); err != nil {
					unavailable["models (subscriptions)"] = sessionOnlyNote(err)
				}
				for _, a := range accts.Data {
					rows = append(rows, connRow{Kind: "models", Name: fmt.Sprint(orEmpty(a["provider"], a["name"])), ID: fmt.Sprint(orEmpty(a["id"], "")), Status: fmt.Sprint(orEmpty(a["status"], "")), Detail: fmt.Sprint(orEmpty(a["kind"], ""))})
				}
			}
			if kind == "" || kind == "apps" {
				var apps struct {
					Data []map[string]any `json:"data"`
				}
				if err := c.API.Get(ctx, "/integrations", nil, &apps); err != nil {
					unavailable["apps"] = sessionOnlyNote(err)
				}
				for _, a := range apps.Data {
					rows = append(rows, connRow{Kind: "apps", Name: fmt.Sprint(orEmpty(a["provider"], a["name"])), ID: fmt.Sprint(orEmpty(a["id"], "")), Status: fmt.Sprint(orEmpty(a["status"], "connected")), Detail: fmt.Sprint(orEmpty(a["account"], a["account_name"]))})
				}
			}
			if kind == "" || kind == "tools" {
				var tools struct {
					Data []map[string]any `json:"data"`
				}
				if err := c.API.Get(ctx, "/mcp-servers", nil, &tools); err != nil {
					unavailable["tools"] = sessionOnlyNote(err)
				}
				for _, t := range tools.Data {
					rows = append(rows, connRow{Kind: "tools", Name: fmt.Sprint(orEmpty(t["name"], "")), ID: fmt.Sprint(orEmpty(t["id"], "")), Status: fmt.Sprint(orEmpty(t["status"], "")), Detail: fmt.Sprint(orEmpty(t["url"], t["endpoint"]))})
				}
			}

			if isJSON() {
				return p.JSON(map[string]any{"data": rows, "unavailable": unavailable})
			}
			if len(rows) == 0 {
				p.Line("No connections yet. Add one with: miosa connections add models <provider> --key-stdin")
			} else {
				out := make([][]string, 0, len(rows))
				for _, r := range rows {
					out = append(out, []string{r.Kind, r.Name, r.Status, r.Detail, r.ID})
				}
				p.Table([]string{"KIND", "NAME", "STATUS", "DETAIL", "ID"}, out)
			}
			for what, why := range unavailable {
				fmt.Fprintf(cmd.ErrOrStderr(), "note: %s: %s\n", what, why)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "Only models, apps or tools")
	addScopeFlags(cmd, &scope, true)
	return cmd
}

func orEmpty(vs ...any) any {
	for _, v := range vs {
		if v != nil && v != "" {
			return v
		}
	}
	return ""
}

func newConnAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Connect a model provider, an app or a tool server",
	}
	cmd.AddCommand(newConnAddModelsCmd(), newConnAddAppsCmd(), newConnAddToolsCmd())
	return cmd
}

// signinProviders maps CLI names to the sign-in API's provider ids.
var signinProviders = map[string]string{
	"claude": "claude_code", "claude-code": "claude_code", "claude_code": "claude_code",
	"chatgpt": "codex", "codex": "codex",
}

// signinProvider resolves what a person typed to the sign-in provider id: the
// subscription plans (Claude, ChatGPT, Kimi, the Mistral plan).
func signinProvider(name string) (string, bool) {
	if p, ok := plans[name]; ok {
		return p.Signin, true
	}
	return "", false
}

func readSecret(cmd *cobra.Command, prompt string, fromStdin bool) (string, error) {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) && !fromStdin {
		fmt.Fprint(cmd.ErrOrStderr(), prompt)
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	b, err := io.ReadAll(io.LimitReader(in, 1<<16))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func newConnAddModelsCmd() *cobra.Command {
	var (
		keyStdin    bool
		sandbox     string
		computer    string
		workspaceID string
		label       string
		noWait      bool
		replace     bool
		scope       connScope
	)
	cmd := &cobra.Command{
		Use:   "models <provider>",
		Short: "Connect a model provider",
		Long: `Connect a model provider for your agents.

With an API key (anthropic, openai, google, groq, mistral, openrouter, together,
...): the key is read from standard input (--key-stdin) or typed hidden. It is
never accepted as an argument, so it stays out of your shell history.

With a subscription plan (claude, chatgpt, kimi, vibe): MIOSA signs in on the
control plane, prints a URL (and a code), waits for you to approve it in the
browser and, for Claude, to paste the code back here.

For one of your end users, add --external-user ID: the account is theirs alone.
Add --no-wait to print the link and code and return, so your own UI can show them
(then follow up with 'miosa connections signin ...').

  miosa connections add models anthropic --key-stdin <<< "$ANTHROPIC_API_KEY"
  miosa connections add models chatgpt
  miosa connections add models claude --external-user cus_123 --no-wait
  miosa connections add models openrouter --external-user cus_123 --key-stdin <<< "$KEY"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := strings.ToLower(args[0])
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			ctx := cmd.Context()

			scope.Workspace = workspaceID

			if sp, ok := signinProvider(provider); ok && !keyStdin {
				body := scope.apply(map[string]any{"provider": sp})
				if replace {
					body["replace"] = true
				}
				// An end user's account is never connected from inside a
				// machine of yours; for a workspace a machine is optional.
				if scope.ExternalUser == "" {
					target := orDefault(sandbox, cfg.CurrentSandbox)
					switch {
					case computer != "":
						id, err := c.API.ResolveComputer(ctx, computer)
						if err != nil {
							return die(err)
						}
						body["computer_id"] = id
					case target != "":
						id, err := c.API.ResolveSandbox(ctx, target)
						if err != nil {
							return die(err)
						}
						body["sandbox_id"] = id
					}
				}
				if err := requireEndUserScope(ctx, c.API, scope); err != nil {
					return err
				}
				if noWait {
					return startSigninNoWait(cmd, c.API, p, body)
				}
				return runSignin(cmd, c.API, p, body)
			}
			if noWait {
				return usagef("--no-wait applies to subscription plans (claude, chatgpt, kimi, vibe)")
			}

			key, err := readSecret(cmd, "API key (input hidden): ", keyStdin)
			if err != nil {
				return die(err)
			}
			if key == "" {
				return usagef("no API key provided (use --key-stdin or type it at the prompt)")
			}
			// A key for an end user (or for a workspace, on a provider that is
			// not a subscription's own key) is an agent credential at that
			// scope. The unscoped key keeps its older routes.
			if _, legacyKey := signinProviders[provider]; scope.ExternalUser != "" || (scope.Workspace != "" && !legacyKey) {
				if err := putAgentKey(cmd, c.API, provider, key, scope); err != nil {
					return err
				}
			} else if sp, ok := signinProviders[provider]; ok {
				body := map[string]any{"provider": sp, "api_key": key}
				if workspaceID != "" {
					body["workspace_id"] = workspaceID
				}
				if label != "" {
					body["label"] = label
				}
				if err := c.API.Post(ctx, "/agent-accounts/api-keys", body, nil); err != nil {
					return die(sessionHint(err))
				}
			} else if err := c.API.Put(ctx, "/settings/provider-keys/"+url.PathEscape(provider), map[string]any{"api_key": key}, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "connected", "provider": provider})
			}
			p.Success("Connected %s. Agents can use it on their next run.", provider)
			return nil
		},
	}
	cmd.Flags().BoolVar(&keyStdin, "key-stdin", false, "Read the API key from standard input")
	cmd.Flags().StringVarP(&sandbox, "sandbox", "s", "", "Sandbox the sign-in runs in (subscriptions); it is saved for the org, workspace or end user, not this sandbox")
	cmd.Flags().StringVar(&computer, "computer", "", "Computer the sign-in runs in (subscriptions); it is saved for the org, workspace or end user, not this computer")
	cmd.Flags().BoolVar(&replace, "replace", false, "Replace the organization's existing sign-in for this provider")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Workspace the connection belongs to")
	cmd.Flags().StringVar(&label, "label", "", "A label for the connection")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Subscriptions: print the link and code and return; your UI shows them")
	cmd.Flags().StringVar(&scope.ExternalUser, "external-user", "", "Connect it for one of your end users (your own id for them)")
	cmd.Flags().StringVar(&scope.ExternalUser, "external-user-id", "", "Same as --external-user")
	_ = cmd.Flags().MarkHidden("external-user-id")
	return cmd
}

// sessionHint explains a 401 or 403 from a connections write.
func sessionHint(err error) error {
	switch {
	case api.IsStatus(err, 403):
		return fmt.Errorf("this key lacks the connections:write scope; mint one with: miosa api-key create NAME --preset cli: %w", err)
	case api.IsStatus(err, 401):
		return fmt.Errorf("this server only accepts a console session for this route: %w", err)
	}
	return err
}

type signinSession struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	Mode            string `json:"mode"`
	Status          string `json:"status"`
	VerificationURL string `json:"verification_url"`
	UserCode        string `json:"user_code"`
	Error           string `json:"error"`
}

// signinPollEvery is the sign-in poll interval; tests shorten it.
var signinPollEvery = 2 * time.Second

func runSignin(cmd *cobra.Command, c *api.Client, p interface {
	JSON(v interface{}) error
	Success(format string, args ...interface{})
}, body map[string]any) error {
	ctx := cmd.Context()
	var start struct {
		Data signinSession `json:"data"`
	}
	if err := c.Post(ctx, "/agent-signins", body, &start); err != nil {
		return die(signinHint(err))
	}
	s := start.Data
	errOut := cmd.ErrOrStderr()
	cancel := func() { _ = c.Delete(context.Background(), "/agent-signins/"+s.ID, nil, nil) }
	defer func() {
		if s.Status != "succeeded" {
			cancel()
		}
	}()
	if s.VerificationURL != "" {
		fmt.Fprintf(errOut, "Open this URL and approve the sign-in:\n  %s\n", s.VerificationURL)
	}
	if s.UserCode != "" {
		fmt.Fprintf(errOut, "Code: %s\n", s.UserCode)
	}
	pasted := false
	for {
		var cur struct {
			Data signinSession `json:"data"`
		}
		if err := c.Get(ctx, "/agent-signins/"+s.ID, nil, &cur); err != nil {
			return die(sessionHint(err))
		}
		s = cur.Data
		switch s.Status {
		case "succeeded":
			if isJSON() {
				return p.JSON(map[string]string{"status": "connected", "provider": s.Provider})
			}
			p.Success("Connected %s", s.Provider)
			return nil
		case "failed", "expired", "cancelled":
			return die(fmt.Errorf("sign-in %s%s", s.Status, map[bool]string{true: ": " + s.Error, false: ""}[s.Error != ""]))
		case "awaiting_code":
			if !pasted {
				fmt.Fprint(errOut, "Paste the code from the browser: ")
				code, err := readLine(cmd.InOrStdin())
				if err != nil || code == "" {
					return die(errors.New("no code entered; sign-in canceled"))
				}
				if err := c.Post(ctx, "/agent-signins/"+s.ID+"/code", map[string]string{"code": code}, nil); err != nil {
					return die(err)
				}
				pasted = true
			}
		}
		select {
		case <-ctx.Done():
			return die(ctx.Err())
		case <-time.After(signinPollEvery):
		}
	}
}

func readLine(r io.Reader) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimSpace(sb.String()), nil
			}
			sb.WriteByte(buf[0])
		}
		if err != nil {
			return strings.TrimSpace(sb.String()), err
		}
	}
}

func newConnAddAppsCmd() *cobra.Command {
	var (
		label       string
		workspaceID string
		products    string
		noWait      bool
	)
	cmd := &cobra.Command{
		Use:   "apps <github|slack|linear|discord|google|notion>",
		Short: "Connect an app through your browser",
		Long: `Starts the OAuth flow for an app, prints the consent URL, and - once the
browser finishes - prints the connection id.

A Google or Notion account is one connection: connect four Gmail accounts and
you get four connections, each with its own label. Google scopes follow the
products you name, never more:

  miosa connections add apps google --label work-gmail --products gmail,calendar,drive
  miosa connections add apps notion --label team-wiki
  miosa connections add apps google --label qa-gmail --products gmail --no-wait

Use --no-wait when the browser is on another device; check the result with
'miosa connections list --kind apps'.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			provider := strings.ToLower(args[0])
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			before := connAppIDs(cmd.Context(), c, provider)

			path := "/integrations/" + url.PathEscape(provider) + "/start"
			q := url.Values{}
			if label != "" {
				q.Set("label", label)
			}
			if workspaceID != "" {
				q.Set("workspace_id", workspaceID)
			}
			if products != "" {
				q.Set("products", products)
			}
			if len(q) > 0 {
				path += "?" + q.Encode()
			}

			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: path})
			if err != nil {
				return die(err)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			target, _ := orEmpty(obj["authorize_url"], obj["url"], obj["auth_url"], obj["redirect_url"]).(string)
			if isJSON() && (noWait || target == "") {
				return p.JSON(resp)
			}
			if target == "" {
				return p.JSON(resp)
			}
			p.Line("Open this URL to connect %s:", provider)
			p.Line("  %s", target)
			if noWait {
				return nil
			}
			// Waiting only makes sense at a terminal. A script, a pipe or a test
			// gets the link and returns, so nothing hangs where nobody can consent.
			if noWait || !term.IsTerminal(int(os.Stdout.Fd())) {
				p.Line("Finish the sign-in in the browser, then run: miosa connections list --kind apps")
				return nil
			}
			p.Line("Waiting for the browser sign-in (Ctrl-C to stop)...")
			id, err := waitForNewApp(cmd.Context(), c, provider, label, before, 3*time.Minute)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "connected", "provider": provider, "id": id})
			}
			p.Success("Connected %s: %s", provider, id)
			return nil
		},
	}
	cmd.Flags().StringVar(&label, "label", "", "Name this connection (for example work-gmail)")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Bind the connection to one workspace")
	cmd.Flags().StringVar(&products, "products", "", "Google products to request (gmail, gmail-send, calendar, drive, docs, sheets)")
	cmd.Flags().BoolVar(&noWait, "no-wait", false, "Print the consent URL and exit without waiting")
	return cmd
}

// connAppIDs is the set of app connection ids a provider has right now, so the
// wait can tell a new connection from one that already existed.
func connAppIDs(ctx context.Context, c *client.Client, provider string) map[string]bool {
	ids := map[string]bool{}
	var apps struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(ctx, "/integrations", nil, &apps); err != nil {
		return ids
	}
	for _, a := range apps.Data {
		if !strings.EqualFold(fmt.Sprint(a["provider"]), provider) {
			continue
		}
		if id := fmt.Sprint(orEmpty(a["id"], "")); id != "" {
			ids[id] = true
		}
	}
	return ids
}

// waitForNewApp polls until a connection this provider did not have before
// appears, and returns its id. Consent can take a minute (a password, a second
// factor, an account chooser), so this waits; Ctrl-C stops it.
func waitForNewApp(ctx context.Context, c *client.Client, provider, label string, before map[string]bool, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		var apps struct {
			Data []map[string]any `json:"data"`
		}
		if err := c.API.Get(ctx, "/integrations", nil, &apps); err == nil {
			for _, a := range apps.Data {
				if !strings.EqualFold(fmt.Sprint(a["provider"]), provider) {
					continue
				}
				id := fmt.Sprint(orEmpty(a["id"], ""))
				if id == "" || before[id] {
					continue
				}
				if label != "" && !strings.EqualFold(fmt.Sprint(orEmpty(a["label"], a["account_name"], "")), label) {
					continue
				}
				if status := strings.ToLower(fmt.Sprint(orEmpty(a["status"], a["state"], "active"))); status != "active" {
					continue
				}
				return id, nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no connection appeared within %s; finish the sign-in in the browser, then run: miosa connections list --kind apps", timeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func newConnAddToolsCmd() *cobra.Command {
	var (
		name    string
		target  string
		headers []string
	)
	cmd := &cobra.Command{
		Use:   "tools",
		Short: "Connect an MCP tool server",
		Long: `Register a remote tool server (MCP) so agents can use its tools. Header values
that are secrets are read from the environment, not the command line:

  miosa connections add tools --name docs --url https://mcp.example.com/sse
  DOCS_TOKEN=... miosa connections add tools --name docs --url https://mcp.example.com/sse \
      --header-env Authorization=DOCS_TOKEN`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			hdr := map[string]string{}
			for _, h := range headers {
				k, envName, ok := strings.Cut(h, "=")
				if !ok || k == "" || envName == "" {
					return usagef("--header-env expects HEADER=ENV_VAR (got %q)", h)
				}
				v := os.Getenv(envName)
				if v == "" {
					return usagef("environment variable %s is empty", envName)
				}
				hdr[k] = v
			}
			body := map[string]any{"name": name, "url": target}
			if len(hdr) > 0 {
				body["headers"] = hdr
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/mcp-servers", Body: body})
			if err != nil {
				return die(sessionHint(err))
			}
			if isJSON() {
				return p.JSON(resp)
			}
			p.Success("Connected tool server %q", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Name agents refer to it by")
	cmd.Flags().StringVar(&target, "url", "", "The server's MCP endpoint")
	cmd.Flags().StringArrayVar(&headers, "header-env", nil, "Header whose value is read from an environment variable, HEADER=ENV_VAR (repeatable)")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newConnRmCmd() *cobra.Command {
	var (
		yes   bool
		scope connScope
	)
	cmd := &cobra.Command{
		Use:     "rm <models|apps|tools> <provider-or-id>",
		Aliases: []string{"remove", "delete"},
		Short:   "Disconnect a model provider, an app or a tool server",
		Long: `Disconnect a model provider, an app or a tool server.

For a workspace or an end user (--workspace-id, --external-user) a model provider
is removed at exactly that scope by name; a wider scope's connection is never
touched, and the scope falls back to it:

  miosa connections rm models claude --external-user cus_123
  miosa connections rm models anthropic --external-user cus_123`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			kind, ref := args[0], args[1]
			var path string
			var query url.Values
			switch kind {
			case "models":
				if scope.set() && !api.IsUUID(ref) {
					path = "/agents/credentials/" + url.PathEscape(planCredential(strings.ToLower(ref)))
					query = scope.query()
				} else if _, ok := signinProviders[strings.ToLower(ref)]; ok {
					if api.IsUUID(ref) {
						path = "/agent-accounts/" + url.PathEscape(ref)
					} else {
						return usagef("remove a subscription by its id (see 'miosa connections list'): miosa connections rm models <id>")
					}
				} else if api.IsUUID(ref) {
					path = "/agent-accounts/" + url.PathEscape(ref)
				} else {
					path = "/settings/provider-keys/" + url.PathEscape(strings.ToLower(ref))
				}
			case "apps":
				if api.IsUUID(ref) {
					// One of several connections to the same provider (four Gmail
					// accounts); the id names the one to disconnect.
					path = "/integrations/apps/" + url.PathEscape(ref)
				} else {
					path = "/integrations/" + url.PathEscape(strings.ToLower(ref))
				}
			case "tools":
				path = "/mcp-servers/" + url.PathEscape(ref)
			default:
				return usagef("kind must be models, apps or tools")
			}
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Disconnect %s %s? Agents that use it will stop working.", kind, ref)); err != nil {
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
			if err := c.API.Delete(cmd.Context(), path, query, nil); err != nil {
				return die(sessionHint(err))
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "disconnected", "kind": kind, "id": ref})
			}
			p.Success("Disconnected %s %s", kind, ref)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	addScopeFlags(cmd, &scope, true)
	return cmd
}

func newConnTestCmd() *cobra.Command {
	var scope connScope
	cmd := &cobra.Command{
		Use:   "test <tool-server-id> | models <provider>",
		Short: "Check that a tool server answers, or that a model connection works",
		Long: `With a tool server id: check that it answers and list its tools.

With 'models <provider>': does the connection work? One cheap authenticated
request to the provider (a subscription is checked by its stored state). The key
and the provider's response are never shown. Add --external-user to test an end
user's own connection.

  miosa connections test m1
  miosa connections test models anthropic --external-user cus_123`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "models" {
				if len(args) != 2 {
					return usagef("usage: miosa connections test models <provider>")
				}
				return testCredential(cmd, strings.ToLower(args[1]), scope)
			}
			if len(args) != 1 {
				return usagef("usage: miosa connections test <tool-server-id> | models <provider>")
			}
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/mcp-servers/" + url.PathEscape(args[0]) + "/test"})
			if err != nil {
				return die(sessionHint(err))
			}
			return p.JSON(resp)
		},
	}
	addScopeFlags(cmd, &scope, true)
	return cmd
}
