package commands

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	apiclient "github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// 'engine connect' and 'engine shell' give a person (or a script) the same
// access an agent run in a MIOSA machine gets: a short-lived, workspace-scoped
// Engine client key, used through the control plane's engine-api proxy, in the
// form the 'optimal' CLI and the optimal MCP server read. The key is shown to
// nobody and written only to a 0600 profile or a child process environment.

const (
	connectDefaultTTL = 24 * time.Hour
	shellDefaultTTL   = time.Hour
	maxClientKeyTTL   = 30 * 24 * time.Hour
)

type engineClientKey struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspace_id"`
	Scopes      []string `json:"scopes"`
	ExpiresAt   string   `json:"expires_at"`
	Key         string   `json:"key"`
}

// engineAccess is what a client key is used with.
type engineAccess struct {
	URL       string
	Key       string
	Workspace string
	KeyID     string
	ExpiresAt string
	EngineID  string
}

func newEngineConnectCmd() *cobra.Command {
	var workspace, profile string
	var ttl time.Duration
	var printEnv bool

	cmd := &cobra.Command{
		Use:   "connect <engine>",
		Short: "Write an optimal CLI profile for an Engine workspace",
		Long: `Mints a read/write key for one workspace of an Engine and writes an 'optimal'
CLI profile (url, key, workspace) so 'optimal' and 'oe' reach the Engine through
MIOSA. The key expires (default 24h, at most 30 days); run connect again to
renew it, or revoke it from the console.

The profile goes to ~/.config/optimal/profile (mode 0600), or
~/.config/optimal/profiles/<name> with --profile <name>. --print writes nothing
and prints shell exports instead. The workspace is the Engine's only attached
workspace, or the one named with --workspace.

  miosa engine connect brain
  miosa engine connect brain --workspace kii --profile kii --ttl 72h
  eval "$(miosa engine connect brain --print)"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineConnect(cmd, args[0], workspace, profile, ttl, printEnv)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace to connect (default: the Engine's only attached workspace)")
	cmd.Flags().StringVar(&profile, "profile", "", "Write ~/.config/optimal/profiles/<name> instead of the default profile")
	cmd.Flags().DurationVar(&ttl, "ttl", connectDefaultTTL, "Key lifetime (at most 30 days)")
	cmd.Flags().BoolVar(&printEnv, "print", false, "Print shell exports instead of writing a profile")
	return cmd
}

func newEngineShellCmd() *cobra.Command {
	var workspace string
	var ttl time.Duration

	cmd := &cobra.Command{
		Use:   "shell <engine> [-- command [args...]]",
		Short: "Run a shell (or a command) with access to an Engine workspace",
		Long: `Mints a short-lived read/write key for one workspace of an Engine, runs your
shell (or the command after --) with OPTIMAL_ENGINE_URL, OPTIMAL_ENGINE_API_KEY
and OPTIMAL_WORKSPACE set, and revokes the key when it exits. Nothing is written
to disk.

  miosa engine shell brain
  miosa engine shell brain --workspace kii -- optimal boot`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineShell(cmd, args[0], args[1:], workspace, ttl)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace to connect (default: the Engine's only attached workspace)")
	cmd.Flags().DurationVar(&ttl, "ttl", shellDefaultTTL, "Key lifetime (at most 30 days)")
	return cmd
}

func runEngineConnect(cmd *cobra.Command, engineRefArg, workspaceRef, profile string, ttl time.Duration, printEnv bool) error {
	if err := validateTTL(ttl); err != nil {
		return err
	}
	if profile != "" && strings.ContainsAny(profile, `/\`) {
		return usagef("--profile is a name, not a path")
	}

	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	access, err := mintEngineAccess(cmd.Context(), c.API, engineRefArg, workspaceRef, ttl)
	if err != nil {
		return die(err)
	}

	if printEnv {
		p.Line("export OPTIMAL_ENGINE_URL=%s", exportQuote(access.URL))
		p.Line("export OPTIMAL_ENGINE_API_KEY=%s", exportQuote(access.Key))
		p.Line("export OPTIMAL_WORKSPACE=%s", exportQuote(access.Workspace))
		return nil
	}

	path, err := writeOptimalProfile(profile, access)
	if err != nil {
		return die(err)
	}
	if isJSON() {
		return p.JSON(map[string]any{
			"profile": path, "url": access.URL, "workspace": access.Workspace,
			"key_id": access.KeyID, "expires_at": access.ExpiresAt,
		})
	}
	p.Success("Connected %s workspace %s", shortRef(access.EngineID), access.Workspace)
	p.Line("Profile: %s", path)
	p.Line("Expires: %s", access.ExpiresAt)
	return nil
}

func runEngineShell(cmd *cobra.Command, engineRefArg string, argv []string, workspaceRef string, ttl time.Duration) error {
	if err := validateTTL(ttl); err != nil {
		return err
	}
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	access, err := mintEngineAccess(ctx, c.API, engineRefArg, workspaceRef, ttl)
	if err != nil {
		return die(err)
	}

	// Revoke on every exit path, with a context that outlives a cancelled one.
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		path := "/databases/" + url.PathEscape(access.EngineID) + "/client-keys/" + url.PathEscape(access.KeyID)
		_ = c.API.Delete(rctx, path, nil, nil)
	}()

	if len(argv) == 0 {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		argv = []string{shell}
	}
	child := exec.CommandContext(ctx, argv[0], argv[1:]...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	child.Env = append(os.Environ(),
		"OPTIMAL_ENGINE_URL="+access.URL,
		"OPTIMAL_ENGINE_API_KEY="+access.Key,
		"OPTIMAL_WORKSPACE="+access.Workspace,
	)
	if err := child.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return die(fmt.Errorf("%s exited with status %d", argv[0], exit.ExitCode()))
		}
		return die(err)
	}
	return nil
}

func validateTTL(ttl time.Duration) error {
	if ttl < time.Minute || ttl > maxClientKeyTTL {
		return usagef("--ttl must be between 1m and 720h")
	}
	return nil
}

// mintEngineAccess finds the Engine and workspace and mints one client key.
func mintEngineAccess(ctx context.Context, api *apiclient.Client, engineRefArg, workspaceRef string, ttl time.Duration) (engineAccess, error) {
	engine, err := findEngine(ctx, api.Get, engineRefArg)
	if err != nil {
		return engineAccess{}, err
	}
	workspaceID, slug, err := pickConnectWorkspace(ctx, api, engine.ID, workspaceRef)
	if err != nil {
		return engineAccess{}, err
	}

	var out struct {
		Data engineClientKey `json:"data"`
	}
	body := map[string]any{"workspace_id": workspaceID, "ttl_seconds": int(ttl.Seconds())}
	if err := api.Post(ctx, "/databases/"+url.PathEscape(engine.ID)+"/client-keys", body, &out); err != nil {
		return engineAccess{}, err
	}
	if out.Data.Key == "" {
		return engineAccess{}, fmt.Errorf("the server returned no key")
	}
	return engineAccess{
		URL:       engineAPIURL(api.BaseURL, engine.ID),
		Key:       out.Data.Key,
		Workspace: slug,
		KeyID:     out.Data.ID,
		ExpiresAt: out.Data.ExpiresAt,
		EngineID:  engine.ID,
	}, nil
}

// findEngine takes a managed Engine's name or id.
func findEngine(ctx context.Context, get apiGetter, ref string) (*engineRef, error) {
	if engine, err := findEngineByName(ctx, get, ref); err != nil {
		return nil, err
	} else if engine != nil {
		return engine, nil
	}
	var one engineRef
	if err := get(ctx, "/databases/"+url.PathEscape(ref), nil, &one); err == nil && one.Engine == "optimal" {
		return &one, nil
	}
	return nil, fmt.Errorf("no Optimal Engine named or numbered %q", ref)
}

// pickConnectWorkspace names the workspace a key is for: the one given, else
// the Engine's only attached workspace. It returns the workspace id and the
// Engine workspace slug the optimal CLI addresses.
func pickConnectWorkspace(ctx context.Context, api engineAPI, engineID, ref string) (string, string, error) {
	var attached struct {
		Data []engineAttachment `json:"data"`
	}
	if err := api.Get(ctx, "/databases/"+url.PathEscape(engineID)+"/attachments", nil, &attached); err != nil {
		return "", "", err
	}
	if ref != "" {
		id, err := api.ResolveWorkspace(ctx, ref)
		if err != nil {
			return "", "", err
		}
		for _, a := range attached.Data {
			if a.WorkspaceID == id {
				return id, a.EngineWorkspaceSlug, nil
			}
		}
		return "", "", fmt.Errorf("%s is not attached to this Engine; run 'miosa engine attach %s --to <engine>'", ref, ref)
	}
	switch len(attached.Data) {
	case 0:
		return "", "", fmt.Errorf("no workspace is attached to this Engine yet; run 'miosa engine attach <workspace> --to <engine>'")
	case 1:
		return attached.Data[0].WorkspaceID, attached.Data[0].EngineWorkspaceSlug, nil
	default:
		return "", "", usagef("this Engine serves %d workspaces; choose one with --workspace", len(attached.Data))
	}
}

// engineAPIURL is the proxy base the optimal CLI is configured with. The CLI
// appends /api/<route> itself.
func engineAPIURL(baseURL, engineID string) string {
	return strings.TrimRight(baseURL, "/") + "/engines/" + url.PathEscape(engineID) + "/engine-api"
}

// writeOptimalProfile writes the 'optimal' CLI profile with mode 0600, through
// a temporary file so a reader never sees half of one.
func writeOptimalProfile(name string, access engineAccess) (string, error) {
	home := os.Getenv("XDG_CONFIG_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".config")
	}
	dir := filepath.Join(home, "optimal")
	path := filepath.Join(dir, "profile")
	if name != "" {
		dir = filepath.Join(dir, "profiles")
		path = filepath.Join(dir, name)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	content := fmt.Sprintf("# written by 'miosa engine connect'; the key expires %s\nurl=%s\nkey=%s\nworkspace=%s\n",
		access.ExpiresAt, access.URL, access.Key, access.Workspace)
	tmp, err := os.CreateTemp(dir, ".profile-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

func exportQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
