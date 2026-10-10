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
		tenant    string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Sign in to MIOSA",
		Long: `Sign in and store an API key for this machine.

With no flags on a terminal, login opens your browser, shows a short code to
confirm there, and stores the key MIOSA issues for this CLI. The key is bound to
the organization you approve; sign in again with --profile NAME to keep several.

For scripts and CI, pass an existing key without putting it in the process
arguments:

  miosa login --key-stdin <<< "$MIOSA_API_KEY"

or skip login and export MIOSA_API_KEY.

  miosa login                       # browser sign-in
  miosa login --no-browser          # print the URL instead of opening it
  miosa login --with-key            # paste a key (input hidden)
  miosa login --profile work        # store as a separate profile`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd, loginOptions{keyStdin: keyStdin, withKey: withKey, device: device, noBrowser: noBrowser, tenant: tenant})
		},
	}
	cmd.Flags().BoolVar(&keyStdin, "key-stdin", false, "Read the API key from standard input")
	cmd.Flags().BoolVar(&withKey, "with-key", false, "Paste an API key instead of using the browser")
	cmd.Flags().BoolVar(&device, "device", false, "Use the browser flow even when stdin is not a terminal")
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the sign-in URL instead of opening a browser")
	cmd.Flags().StringVar(&tenant, "org", "", "Organization slug or id to sign in to (browser flow)")
	_ = cmd.Flags().MarkHidden("org")
	return cmd
}

type loginOptions struct {
	keyStdin, withKey, device, noBrowser bool
	tenant                               string
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
	if key == "" {
		k, slug, err := deviceLogin(cmd, ac, o)
		if err != nil {
			return die(err)
		}
		key, tenantSlug = k, slug
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

	if err := storeLogin(baseURL, key, tenantSlug); err != nil {
		return die(fmt.Errorf("saving config: %w", err))
	}
	profile := profileLabel()
	if isJSON() {
		return p.JSON(map[string]any{"status": "authenticated", "profile": profile, "organization": me.Name, "slug": me.Slug})
	}
	path, _ := config.Path()
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
func storeLogin(baseURL, key, tenant string) error {
	f, err := config.LoadFile()
	if err != nil {
		return err
	}
	name := f.ActiveName()
	cur, _ := f.Get(name)
	cur.APIKey = key
	cur.Tenant = tenant
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
	Tenant struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"tenant"`
}

// deviceLogin runs the browser flow: start a session, send the user to the
// approval page, and poll until the key is issued exactly once.
func deviceLogin(cmd *cobra.Command, ac *api.Client, o loginOptions) (key, tenantSlug string, err error) {
	host, _ := os.Hostname()
	body := map[string]any{"client_name": "MIOSA CLI on " + host}
	if o.tenant != "" {
		body["tenant"] = o.tenant
	}
	var st deviceStart
	if err := ac.Post(cmd.Context(), "/auth/cli/start", body, &st); err != nil {
		return "", "", fmt.Errorf("starting browser sign-in: %w", err)
	}
	if st.DeviceCode == "" || st.UserCode == "" {
		return "", "", errors.New("the server did not return a sign-in code")
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
			return "", "", errors.New("sign-in timed out; run 'miosa login' again")
		}
		var tok deviceToken
		err := ac.JSON(ctx, api.Request{Method: http.MethodPost, Path: "/auth/cli/token", Body: map[string]string{"device_code": st.DeviceCode}, NoRetry: true}, &tok)
		if err == nil {
			if tok.APIKey == "" {
				return "", "", errors.New("the server approved the sign-in but returned no key")
			}
			return tok.APIKey, tok.Tenant.Slug, nil
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
				return "", "", errors.New("the sign-in code expired; run 'miosa login' again")
			case http.StatusForbidden:
				return "", "", errors.New("sign-in was denied in the browser")
			case http.StatusConflict:
				return "", "", errors.New("this sign-in code was already used; run 'miosa login' again")
			}
		}
		var te *api.TransportError
		if errors.As(err, &te) {
			continue // a flaky network must not abort the wait
		}
		return "", "", fmt.Errorf("sign-in failed: %w", err)
	}
}

func newLogoutCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		Long: `Sign out of the current profile: its API key and current sandbox are removed.
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
				[2]string{"Plan", fmt.Sprint(orNil(plan))},
			)
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
