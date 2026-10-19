package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// Wiring two Optimal Engines is a multi-step operation over two Engine URLs,
// not one call to the MIOSA API, so this command owns its HTTP client instead
// of going through internal/api. The keys it handles are Engine admin keys for
// the two endpoints; they are never printed, and the replica keys it mints are
// workspace-scoped and short-lived by policy.

type engineSyncFlags struct {
	local     string
	cloud     string
	workspace string
	localKey  string
	cloudKey  string
	policy    string
	noRun     bool
}

type engineSyncOptions struct {
	localURL     string
	cloudURL     string
	workspace    string
	localKey     string
	cloudKey     string
	policy       string
	runOneCycle  bool
	requestLimit time.Duration
}

type engineClient struct {
	base *url.URL
	key  string
	http *http.Client
}

func newEngineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "engine",
		Short: "Operate an Optimal Engine: wire two of them together and read sync status",
		Long: `Commands here talk to Optimal Engine endpoints directly.

They take the two Engine URLs and an admin key per Engine. The admin key is used
to mint a workspace-scoped replica key on each side, so the peer key each Engine
stores can read and sync one workspace and nothing else.`,
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(newEngineSyncCmd())
	return cmd
}

func newEngineSyncCmd() *cobra.Command {
	f := &engineSyncFlags{}

	cmd := &cobra.Command{
		Use:   "sync <local> <cloud>",
		Short: "Keep one workspace in sync between a local and a cloud Engine",
		Long: `Wires two Engines for one workspace:

  1. mint a replica key on each Engine (scopes sync and read, one workspace)
  2. enable the sync ledger for the workspace on both
  3. register each Engine as the other's peer with policy both
  4. run one cycle unless --no-run, then print both statuses

The admin keys come from --local-key/--cloud-key or MIOSA_ENGINE_LOCAL_KEY and
MIOSA_ENGINE_CLOUD_KEY. They are never printed.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := engineSyncOptions{
				localURL:     firstNonEmpty(f.local, args[0]),
				cloudURL:     firstNonEmpty(f.cloud, args[1]),
				workspace:    f.workspace,
				localKey:     firstNonEmpty(f.localKey, os.Getenv("MIOSA_ENGINE_LOCAL_KEY")),
				cloudKey:     firstNonEmpty(f.cloudKey, os.Getenv("MIOSA_ENGINE_CLOUD_KEY")),
				policy:       firstNonEmpty(f.policy, "both"),
				runOneCycle:  !f.noRun,
				requestLimit: time.Duration(globalFlags.Timeout) * time.Second,
			}
			if err := opts.validate(); err != nil {
				return die(err)
			}
			return die(runEngineSync(cmd.Context(), printerFor(cmd), opts))
		},
	}

	pf := cmd.Flags()
	pf.StringVar(&f.local, "local", "", "Local Engine URL (defaults to the first positional argument)")
	pf.StringVar(&f.cloud, "cloud", "", "Cloud Engine URL (defaults to the second positional argument)")
	pf.StringVar(&f.workspace, "workspace", "", "Workspace id to sync (required)")
	pf.StringVar(&f.localKey, "local-key", "", "Admin key for the local Engine (env: MIOSA_ENGINE_LOCAL_KEY)")
	pf.StringVar(&f.cloudKey, "cloud-key", "", "Admin key for the cloud Engine (env: MIOSA_ENGINE_CLOUD_KEY)")
	pf.StringVar(&f.policy, "policy", "both", "Peer policy: push, pull, both, or none")
	pf.BoolVar(&f.noRun, "no-run", false, "Wire the peers but do not run a sync cycle now")

	cmd.AddCommand(newEngineSyncStatusCmd())
	return cmd
}

func newEngineSyncStatusCmd() *cobra.Command {
	f := &engineSyncFlags{}

	cmd := &cobra.Command{
		Use:   "status <local> <cloud>",
		Short: "Show sync status and open conflicts on both Engines",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := engineSyncOptions{
				localURL:     firstNonEmpty(f.local, args[0]),
				cloudURL:     firstNonEmpty(f.cloud, args[1]),
				workspace:    f.workspace,
				localKey:     firstNonEmpty(f.localKey, os.Getenv("MIOSA_ENGINE_LOCAL_KEY")),
				cloudKey:     firstNonEmpty(f.cloudKey, os.Getenv("MIOSA_ENGINE_CLOUD_KEY")),
				requestLimit: time.Duration(globalFlags.Timeout) * time.Second,
			}
			if err := opts.validate(); err != nil {
				return die(err)
			}
			return die(showEngineSyncStatus(cmd.Context(), printerFor(cmd), opts))
		},
	}

	pf := cmd.Flags()
	pf.StringVar(&f.local, "local", "", "Local Engine URL (defaults to the first positional argument)")
	pf.StringVar(&f.cloud, "cloud", "", "Cloud Engine URL (defaults to the second positional argument)")
	pf.StringVar(&f.workspace, "workspace", "", "Workspace id (required)")
	pf.StringVar(&f.localKey, "local-key", "", "Admin key for the local Engine (env: MIOSA_ENGINE_LOCAL_KEY)")
	pf.StringVar(&f.cloudKey, "cloud-key", "", "Admin key for the cloud Engine (env: MIOSA_ENGINE_CLOUD_KEY)")
	return cmd
}

func (o engineSyncOptions) validate() error {
	if strings.TrimSpace(o.workspace) == "" {
		return fmt.Errorf("--workspace is required: sync is per workspace, never the whole Engine")
	}
	if strings.TrimSpace(o.localKey) == "" || strings.TrimSpace(o.cloudKey) == "" {
		return fmt.Errorf("an admin key is required for each Engine: --local-key and --cloud-key")
	}
	if o.policy != "" && o.policy != "push" && o.policy != "pull" && o.policy != "both" && o.policy != "none" {
		return fmt.Errorf("--policy must be push, pull, both, or none")
	}
	if _, err := newEngineClient(o.localURL, o.localKey, o.requestLimit); err != nil {
		return err
	}
	if _, err := newEngineClient(o.cloudURL, o.cloudKey, o.requestLimit); err != nil {
		return err
	}
	return nil
}

func runEngineSync(ctx context.Context, p *output.Printer, o engineSyncOptions) error {
	local, err := newEngineClient(o.localURL, o.localKey, o.requestLimit)
	if err != nil {
		return err
	}
	cloud, err := newEngineClient(o.cloudURL, o.cloudKey, o.requestLimit)
	if err != nil {
		return err
	}

	// A replica key each side: the cloud uses the local key to reach the local
	// Engine, and the local Engine uses the cloud key to reach the cloud.
	localReplica, err := mintReplicaKey(ctx, local, o.workspace)
	if err != nil {
		return fmt.Errorf("mint local replica key: %w", err)
	}
	cloudReplica, err := mintReplicaKey(ctx, cloud, o.workspace)
	if err != nil {
		return fmt.Errorf("mint cloud replica key: %w", err)
	}

	for _, side := range []struct {
		name   string
		client *engineClient
	}{{"local", local}, {"cloud", cloud}} {
		if err := side.client.do(ctx, http.MethodPost, "/api/sync/enable",
			map[string]any{"workspace": o.workspace}, nil); err != nil {
			return fmt.Errorf("enable sync on %s: %w", side.name, err)
		}
	}

	peer := func(name, peerURL, key string, client *engineClient) error {
		body := map[string]any{
			"workspace_id": o.workspace,
			"url":          peerURL,
			"api_key":      key,
			"policy":       o.policy,
			"name":         "miosa-cli-" + name,
		}
		if err := client.do(ctx, http.MethodPost, "/api/sync/peers", body, nil); err != nil {
			return fmt.Errorf("register %s peer: %w", name, err)
		}
		return nil
	}

	if err := peer("cloud", o.cloudURL, cloudReplica, local); err != nil {
		return err
	}
	if err := peer("local", o.localURL, localReplica, cloud); err != nil {
		return err
	}

	if o.runOneCycle {
		if err := local.do(ctx, http.MethodPost, "/api/sync/run", nil, nil); err != nil {
			return fmt.Errorf("run a local sync cycle: %w", err)
		}
		if err := cloud.do(ctx, http.MethodPost, "/api/sync/run", nil, nil); err != nil {
			return fmt.Errorf("run a cloud sync cycle: %w", err)
		}
	}

	localStatus, err := fetchSyncStatus(ctx, local, o.workspace)
	if err != nil {
		return fmt.Errorf("read local status: %w", err)
	}
	cloudStatus, err := fetchSyncStatus(ctx, cloud, o.workspace)
	if err != nil {
		return fmt.Errorf("read cloud status: %w", err)
	}

	return renderEngineSync(p, o, localStatus, cloudStatus)
}

func showEngineSyncStatus(ctx context.Context, p *output.Printer, o engineSyncOptions) error {
	local, err := newEngineClient(o.localURL, o.localKey, o.requestLimit)
	if err != nil {
		return err
	}
	cloud, err := newEngineClient(o.cloudURL, o.cloudKey, o.requestLimit)
	if err != nil {
		return err
	}

	localStatus, err := fetchSyncStatus(ctx, local, o.workspace)
	if err != nil {
		return fmt.Errorf("read local status: %w", err)
	}
	cloudStatus, err := fetchSyncStatus(ctx, cloud, o.workspace)
	if err != nil {
		return fmt.Errorf("read cloud status: %w", err)
	}

	return renderEngineSync(p, o, localStatus, cloudStatus)
}

func renderEngineSync(p *output.Printer, o engineSyncOptions, local, cloud map[string]any) error {
	if globalFlags.JSON {
		return p.JSON(map[string]any{
			"workspace": o.workspace,
			"local":     map[string]any{"url": o.localURL, "status": local},
			"cloud":     map[string]any{"url": o.cloudURL, "status": cloud},
		})
	}

	p.Fields([][2]string{
		{"Workspace", o.workspace},
		{"Local", o.localURL},
		{"Cloud", o.cloudURL},
	})

	for _, side := range []struct {
		label  string
		status map[string]any
	}{{"Local", local}, {"Cloud", cloud}} {
		p.Fields([][2]string{
			{side.label + " enabled", fmt.Sprint(side.status["enabled"])},
			{side.label + " ledger", fmt.Sprint(side.status["ledger_sequence"])},
			{side.label + " conflicts", fmt.Sprint(side.status["open_conflicts"])},
			{side.label + " peers", fmt.Sprint(peerCount(side.status))},
		})
	}

	return nil
}

func peerCount(status map[string]any) int {
	peers, ok := status["peers"].([]any)
	if !ok {
		return 0
	}
	return len(peers)
}

func fetchSyncStatus(ctx context.Context, c *engineClient, workspace string) (map[string]any, error) {
	var status map[string]any
	path := "/api/sync/status?workspace=" + url.QueryEscape(workspace)
	if err := c.do(ctx, http.MethodGet, path, nil, &status); err != nil {
		return nil, err
	}
	return status, nil
}

func mintReplicaKey(ctx context.Context, c *engineClient, workspace string) (string, error) {
	name := "miosa-sync-" + workspace

	// Running sync again must not pile up replica keys: revoke the previous
	// key with this exact name before minting the new one.
	if err := revokeReplicaKeys(ctx, c, name); err != nil {
		return "", err
	}

	body := map[string]any{
		"name":            name,
		"scopes":          []string{"sync", "read"},
		"workspace_scope": []string{workspace},
	}

	var minted map[string]any
	if err := c.do(ctx, http.MethodPost, "/api/auth/keys", body, &minted); err != nil {
		return "", err
	}

	key, _ := minted["key"].(string)
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("the Engine did not return a key")
	}
	return key, nil
}

func revokeReplicaKeys(ctx context.Context, c *engineClient, name string) error {
	var listed struct {
		Keys []map[string]any `json:"keys"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/auth/keys?limit=100", nil, &listed); err != nil {
		return err
	}

	for _, key := range listed.Keys {
		if keyName, _ := key["name"].(string); keyName != name {
			continue
		}
		id, _ := key["id"].(string)
		if id == "" {
			continue
		}
		if err := c.do(ctx, http.MethodPost, "/api/auth/keys/"+url.PathEscape(id)+"/revoke", nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Engine HTTP client
// ---------------------------------------------------------------------------

func newEngineClient(rawURL, key string, timeout time.Duration) (*engineClient, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("an Engine URL is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("Engine URL must include scheme and host: %s", rawURL)
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &engineClient{
		base: u,
		key:  strings.TrimSpace(key),
		http: &http.Client{Timeout: timeout},
	}, nil
}

func (c *engineClient) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}

	endpoint := strings.TrimRight(c.base.String(), "/") + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	if out != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w", method, path, err)
		}
	}
	return nil
}
