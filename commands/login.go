package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
)

func newLoginCmd() *cobra.Command {
	var (
		keyStdin  bool
		withKey   bool
		device    bool
		noBrowser bool
		orgs      []string
		allOrgs   bool
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to MIOSA",
		Long: `Sign in and store an API key for this machine.

With no flags on a terminal, login opens your browser, shows a short code to
confirm there, and stores the key MIOSA issues for this CLI. On that page you
choose which of your organizations this terminal may work in (all of them, or
some) and which one it starts in; one key covers them all. Switch with
'miosa org use <org>', change the set with 'miosa org select'.

For scripts and CI, pass an existing key without putting it in the process
arguments:

  miosa login --key-stdin <<< "$MIOSA_API_KEY"

or skip login and export MIOSA_API_KEY.

  miosa login                       # browser sign-in
  miosa login --all-orgs            # every organization pre-selected
  miosa login --org osa --org acme  # these organizations pre-selected
  miosa login --no-browser          # print the URL instead of opening it
  miosa login --with-key            # paste a key (input hidden)
  miosa login --profile work        # store as a separate profile`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, loginOptions{keyStdin: keyStdin, withKey: withKey, device: device, noBrowser: noBrowser, orgs: orgs, allOrgs: allOrgs})
		},
	}
	cmd.Flags().BoolVar(&keyStdin, "key-stdin", false, "Read the API key from standard input")
	cmd.Flags().BoolVar(&withKey, "with-key", false, "Paste an API key instead of using the browser")
	cmd.Flags().BoolVar(&device, "device", false, "Use the browser flow even when stdin is not a terminal")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the sign-in URL instead of opening a browser")
	cmd.Flags().StringArrayVar(&orgs, "org", nil, "Pre-select this organization on the approval page (repeatable)")
	cmd.Flags().BoolVar(&allOrgs, "all-orgs", false, "Pre-select every organization on the approval page")
	return cmd
}

type loginOptions struct {
	keyStdin, withKey, device, noBrowser bool
	// orgs and allOrgs pre-select organizations on the approval page;
	// lockedOrg asks for exactly one (the page shows no list).
	orgs      []string
	allOrgs   bool
	lockedOrg string
}

// sleepFn lets tests skip the poll interval.
var sleepFn = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// openBrowser is replaced in tests.
var openBrowser = func(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}

func stdinIsTerminal(cmd *cobra.Command) bool {
	f, ok := cmd.InOrStdin().(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func runLogin(cmd *cobra.Command, o loginOptions) error {
	p := printerFor(cmd)
	// The new key decides its organizations: never send the old profile's
	// active org while signing in.
	orgHeaderOff = true
	defer func() { orgHeaderOff = false }()

	var key string
	switch {
	case o.keyStdin || (!o.withKey && !o.device && !stdinIsTerminal(cmd)):
		k, err := readKeyFromReader(cmd.InOrStdin())
		if err != nil {
			return die(err)
		}
		key = k
	case o.withKey:
		k, err := promptKey(cmd)
		if err != nil {
			return die(err)
		}
		key = k
	}

	baseURL := globalFlags.APIURL
	if baseURL == "" {
		baseURL = os.Getenv("MIOSA_BASE_URL")
	}
	if baseURL == "" {
		if cur, err := config.Load(); err == nil {
			baseURL = cur.APIURL
		}
	}
	if baseURL == "" {
		baseURL = config.DefaultBaseURL
	}
	ac := api.New(baseURL, key)
	ac.UserAgent = "miosa-cli/" + cliVersion
	ac.MaxRetries = 2

	var tenantSlug string
	var grant deviceToken
	if key == "" {
		tok, err := deviceLogin(cmd, ac, o)
		if err != nil {
			return die(err)
		}
		key, tenantSlug, grant = tok.APIKey, tok.Tenant.Slug, tok
		ac.Key = key
	} else if !strings.HasPrefix(key, "msk_") {
		fmt.Fprintln(cmd.ErrOrStderr(), "warning: key does not start with 'msk_'; continuing")
	}

	// Verify the key before it is stored.
	var me struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
		Plan string `json:"plan"`
	}
	if err := ac.Get(cmd.Context(), "/platform/tenants/current", nil, &me); err != nil {
		return die(fmt.Errorf("key validation failed: %w", err))
	}
	if me.Slug != "" {
		tenantSlug = me.Slug
	}

	slugs := make([]string, 0, len(grant.Orgs))
	for _, o := range grant.Orgs {
		slugs = append(slugs, o.Slug)
	}
	if err := storeLogin(baseURL, key, tenantSlug, slugs, grant.Key.OrgScope); err != nil {
		return die(fmt.Errorf("saving config: %w", err))
	}
	profile := profileLabel()
	// A server without multi-organization sign-in ignores the request and
	// answers with no orgs: say so instead of letting it pass silently.
	if (o.allOrgs || len(o.orgs) > 0) && grant.Orgs == nil && key == grant.APIKey {
		fmt.Fprintf(cmd.ErrOrStderr(), "miosa: this MIOSA server does not support signing in to several organizations yet; this sign-in covers %s only\n", orDash(me.Slug))
	}
	if isJSON() {
		return p.JSON(map[string]any{"status": "authenticated", "profile": profile, "organization": me.Name, "slug": me.Slug, "orgs": grant.Orgs})
	}
	path, _ := config.Path()
	if len(grant.Orgs) > 1 {
		p.Success("Signed in to %d organizations, starting in %s. Profile %q saved to %s", len(grant.Orgs), orDash(me.Slug), profile, path)
		p.Line("Switch with 'miosa org use <org>'; see them all with 'miosa org list'.")
		return nil
	}
	p.Success("Signed in to %s (%s). Profile %q saved to %s", orDash(me.Name), orDash(me.Slug), profile, path)
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// profileLabel is the profile name in effect.
func profileLabel() string {
	f, err := config.LoadFile()
	if err != nil {
		return config.DefaultProfile
	}
	return f.ActiveName()
}

// storeLogin writes the key into the selected profile, creating it if needed.
func storeLogin(baseURL, key, tenant string, orgs []string, scope string) error {
	f, err := config.LoadFile()
	if err != nil {
		return err
	}
	name := f.ActiveName()
	cur, _ := f.Get(name)
	cur.APIKey = key
	cur.Tenant = tenant
	cur.ActiveOrg = tenant
	cur.Orgs = orgs
	cur.OrgScope = scope
	// Only a non-default URL is worth pinning to a named profile; the default
	// profile always records it, as before.
	if name == config.DefaultProfile || baseURL != config.DefaultBaseURL {
		cur.APIURL = baseURL
	}
	if cur.DefaultWorkspace == "" {
		cur.DefaultWorkspace = "default"
	}
	cur.CurrentSandbox = "" // a sandbox from another account is meaningless now
	f.Set(name, cur)
	if name != config.DefaultProfile && f.ActiveProfile == "" && len(f.Profiles) == 1 && f.Config.APIKey == "" {
		f.ActiveProfile = name
	}
	return config.SaveFile(f)
}

func readKeyFromReader(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	if sc.Scan() {
		if k := strings.TrimSpace(sc.Text()); k != "" {
			return k, nil
		}
	}
	return "", errors.New("no API key on standard input")
}

func promptKey(cmd *cobra.Command) (string, error) {
	if f, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), "Paste your MIOSA API key (input hidden): ")
		raw, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", fmt.Errorf("reading API key: %w", err)
		}
		if k := strings.TrimSpace(string(raw)); k != "" {
			return k, nil
		}
		return "", errors.New("no API key provided")
	}
	return readKeyFromReader(cmd.InOrStdin())
}

type deviceStart struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type deviceToken struct {
	APIKey string `json:"api_key"`
	Key    struct {
		OrgScope string `json:"org_scope"`
	} `json:"key"`
	Tenant struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"tenant"`
	// Orgs are the organizations the key covers, start org first (servers
	// since multi-org CLI access; older ones send none).
	Orgs []struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"orgs"`
}

// deviceLogin runs the browser flow: start a session, send the user to the
// approval page, and poll until the key is issued exactly once.
func deviceLogin(cmd *cobra.Command, ac *api.Client, o loginOptions) (deviceToken, error) {
	host, _ := os.Hostname()
	body := map[string]any{
		"client_name": "MIOSA CLI on " + host,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"cli_version": cliVersion,
	}
	switch {
	case o.lockedOrg != "":
		body["tenant"] = o.lockedOrg
	case o.allOrgs:
		body["all_orgs"] = true
	case len(o.orgs) > 0:
		body["orgs"] = o.orgs
	}
	var st deviceStart
	if err := ac.Post(cmd.Context(), "/auth/cli/start", body, &st); err != nil {
		return deviceToken{}, fmt.Errorf("starting browser sign-in: %w", err)
	}
	if st.DeviceCode == "" || st.UserCode == "" {
		return deviceToken{}, errors.New("the server did not return a sign-in code")
	}
	target := st.VerificationURIComplete
	if target == "" {
		target = st.VerificationURI
	}
	errOut := cmd.ErrOrStderr()
	fmt.Fprintf(errOut, "Your sign-in code is %s\n", st.UserCode)
	opened := false
	if !o.noBrowser && !isJSON() {
		opened = openBrowser(target) == nil
	}
	if opened {
		fmt.Fprintf(errOut, "Opened %s in your browser. Confirm the code there.\n", st.VerificationURI)
	} else {
		fmt.Fprintf(errOut, "Open this URL and confirm the code:\n  %s\n", target)
	}
	fmt.Fprintln(errOut, "Waiting for approval...")

	interval := time.Duration(st.Interval) * time.Second
	if interval <= 0 {
		interval = 3 * time.Second
	}
	expires := time.Duration(st.ExpiresIn) * time.Second
	if expires <= 0 {
		expires = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), expires)
	defer cancel()
	for {
		if err := sleepFn(ctx, interval); err != nil {
			return deviceToken{}, errors.New("sign-in timed out; run 'miosa login' again")
		}
		var tok deviceToken
		err := ac.JSON(ctx, api.Request{Method: http.MethodPost, Path: "/auth/cli/token", Body: map[string]string{"device_code": st.DeviceCode}, NoRetry: true}, &tok)
		if err == nil {
			if tok.APIKey == "" {
				return deviceToken{}, errors.New("the server approved the sign-in but returned no key")
			}
			return tok, nil
		}
		var ae *api.Error
		if errors.As(err, &ae) {
			switch ae.Status {
			case http.StatusPreconditionRequired: // 428 authorization_pending
				continue
			case http.StatusTooManyRequests:
				interval += 2 * time.Second
				continue
			case http.StatusGone:
				return deviceToken{}, errors.New("the sign-in code expired; run 'miosa login' again")
			case http.StatusForbidden:
				return deviceToken{}, errors.New("sign-in was denied in the browser")
			case http.StatusConflict:
				return deviceToken{}, errors.New("this sign-in code was already used; run 'miosa login' again")
			}
		}
		var te *api.TransportError
		if errors.As(err, &te) {
			continue // a flaky network must not abort the wait
		}
		return deviceToken{}, fmt.Errorf("sign-in failed: %w", err)
	}
}

func newLogoutCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		Long: `Sign out of the current profile: its API key, current sandbox and the Forge
key it minted for git (if any) are removed.
With --all the whole config file (every profile) is deleted. The key is not
revoked on the server; revoke it with 'miosa api-key revoke' if it leaked.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			var err error
			if all {
				err = config.ClearAll()
			} else {
				err = config.Clear()
			}
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "logged_out"})
			}
			if all {
				p.Success("Logged out of every profile. Config deleted.")
			} else {
				p.Success("Logged out of profile %q.", profileLabel())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Delete the config file (all profiles)")
	return cmd
}

func newWhoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who this CLI is signed in as: user, organization, plan and access",
		Long: `Asks the API who the key or session is (GET /whoami), which works for an API key as
well as a browser session. Servers that predate that route fall back to the
organization record, which has no user, workspace or scopes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			var who map[string]any
			werr := c.API.Get(cmd.Context(), "/whoami", nil, &who)
			if werr != nil && !api.IsStatus(werr, http.StatusNotFound) {
				return die(werr)
			}
			// Credits are not part of /whoami; read them best-effort.
			var cur map[string]any
			curErr := c.API.Get(cmd.Context(), "/platform/tenants/current", nil, &cur)
			if werr != nil && curErr != nil {
				return die(curErr)
			}
			org, _ := who["organization"].(map[string]any)
			if org == nil {
				org = map[string]any{"name": cur["name"], "slug": cur["slug"], "id": cur["id"]}
			}
			plan := firstNonNil(cur["plan_name"], cur["plan"])
			if pm, ok := who["plan"].(map[string]any); ok && pm["name"] != nil {
				plan = pm["name"]
			}
			auth, _ := who["auth"].(map[string]any)
			user, _ := who["user"].(map[string]any)
			ws, _ := who["workspace"].(map[string]any)
			scopes := toStrings(auth["scopes"])
			unrestricted, _ := auth["unrestricted"].(bool)

			info := map[string]any{
				"organization": org["name"], "slug": org["slug"], "id": org["id"],
				"plan": plan, "credits": cur["credit_balance"],
				"profile": profileLabel(), "api_url": c.BaseURL, "key": maskKey(c.Key),
				"default_workspace": cfg.DefaultWorkspace, "current_sandbox": cfg.CurrentSandbox,
			}
			activeOrg, activeFrom := effectiveOrg()
			if activeOrg == "" {
				activeOrg, activeFrom = fmt.Sprint(orNil(org["slug"])), "key"
			}
			info["active_org"] = activeOrg
			info["active_org_source"] = activeFrom
			if len(cfg.Orgs) > 0 {
				info["orgs"] = cfg.Orgs
				info["org_scope"] = cfg.OrgScope
			}
			if user != nil {
				info["user"] = user
			}
			if ws != nil {
				info["workspace"] = ws
			}
			if auth != nil {
				info["auth"] = auth
			}
			if isJSON() {
				return p.JSON(info)
			}
			rows := [][2]string{}
			if user != nil {
				rows = append(rows, [2]string{"User", strings.TrimSpace(fmt.Sprint(orNil(user["name"])) + " <" + fmt.Sprint(orNil(user["email"])) + ">")})
			}
			rows = append(rows,
				[2]string{"Organization", fmt.Sprint(orNil(org["name"])) + " (" + fmt.Sprint(orNil(org["slug"])) + ")"},
				[2]string{"Org id", fmt.Sprint(orNil(org["id"]))},
				[2]string{"Working in", activeOrg + " (" + map[string]string{"flag": "--org", "env": "MIOSA_ORG", "profile": "miosa org use", "key": "the key's organization"}[activeFrom] + ")"},
				[2]string{"Plan", fmt.Sprint(orNil(plan))},
			)
			if label := orgsLabel(cfg.Orgs, cfg.OrgScope); label != "" {
				rows = append(rows, [2]string{"Covers", label})
			}
			if cur != nil {
				rows = append(rows, [2]string{"Credits", creditsLabel(cur["credit_balance"])})
			}
			rows = append(rows,
				[2]string{"Profile", profileLabel()},
				[2]string{"API", c.BaseURL},
				[2]string{"Key", maskKey(c.Key)},
			)
			wsLabel := cfg.DefaultWorkspace
			if ws != nil {
				wsLabel = fmt.Sprint(orNil(ws["slug"])) + " (key is bound to it)"
			}
			rows = append(rows, [2]string{"Workspace", wsLabel}, [2]string{"Sandbox", cfg.CurrentSandbox})
			if auth != nil {
				access := fmt.Sprintf("%d scopes", len(scopes))
				if unrestricted {
					access = "unrestricted (owner or admin)"
				}
				rows = append(rows, [2]string{"Access", access})
			}
			p.Fields(rows)
			return nil
		},
	}
}

func firstNonNil(vs ...any) any {
	for _, v := range vs {
		if v != nil && v != "" {
			return v
		}
	}
	return nil
}

func orNil(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func creditsLabel(v any) string {
	if f, ok := toFloat(v); ok {
		return fmt.Sprintf("%s (about $%.2f)", formatValue(v, ""), f/100)
	}
	return ""
}

// maskKey shows the prefix and last four characters only.
func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 12 {
		return "****"
	}
	prefix := k
	if i := strings.LastIndex(k[:12], "_"); i >= 0 {
		prefix = k[:i+1]
	} else {
		prefix = k[:4]
	}
	return prefix + "..." + k[len(k)-4:]
}

// consoleURL derives the web console from the API URL (api.miosa.ai -> miosa.ai).
func consoleURL(apiURL string) string {
	if v := os.Getenv("MIOSA_CONSOLE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "https://miosa.ai"
	}
	host := strings.TrimPrefix(u.Host, "api.")
	return u.Scheme + "://" + host
}
