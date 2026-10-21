package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
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
	relay     string
	engineID  string
	miosaKey  string
	since     int
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
	relayURL     string
	engineID     string
	miosaKey     string
	since        int
}

func (o engineSyncOptions) relayMode() bool {
	return strings.TrimSpace(o.relayURL) != "" || strings.TrimSpace(o.engineID) != ""
}

type engineClient struct {
	base *url.URL
	key  string
	http *http.Client
}

func newEngineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "engine",
		Short: "Operate an Optimal Engine: managed Engines on MIOSA, and two-Engine sync",
		Long: `Managed Engines run on MIOSA as managed databases with engine "optimal":
create one, read it, list them. They are the same objects 'miosa db' shows.

The sync commands talk to Engine endpoints directly. They take the two Engine
URLs and an admin key per Engine. The admin key is used to mint a
workspace-scoped replica key on each side, so the peer key each Engine stores
can read and sync one workspace and nothing else.`,
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(newEngineCreateCmd())
	cmd.AddCommand(newEngineGetCmd())
	cmd.AddCommand(newEngineListCmd())
	cmd.AddCommand(newEngineUpgradeCmd())
	cmd.AddCommand(newEngineRollbackCmd())
	cmd.AddCommand(newEngineBackupCmd())
	cmd.AddCommand(newEngineBackupsCmd())
	cmd.AddCommand(newEngineRestoreCmd())
	cmd.AddCommand(newEngineRestoreDeprecatedCmd())
	cmd.AddCommand(newEngineRmCmd())
	cmd.AddCommand(newEngineAttachCmd())
	cmd.AddCommand(newEngineConnectCmd())
	cmd.AddCommand(newEngineShellCmd())
	cmd.AddCommand(newEngineDetachCmd())
	cmd.AddCommand(newEngineStatusCmd())
	cmd.AddCommand(newEngineSyncCmd())
	return cmd
}

// engineDetail is what 'engine get' prints. The values come from the live
// databases record; fields the server leaves out render empty rather than fail.
var engineDetail = []Col{
	{Head: "Name", Path: "name"},
	{Head: "Id", Path: "id"},
	{Head: "Engine", Path: "engine"},
	{Head: "Version", Path: "engine_version"},
	{Head: "State", Path: "state"},
	{Head: "Host", Path: "host"},
	{Head: "Port", Path: "port"},
	{Head: "Workspace", Path: "workspace_id"},
	{Head: "vCPU", Path: "cpu_count"},
	{Head: "Memory (MB)", Path: "memory_mb"},
	{Head: "Disk (MB)", Path: "storage_mb"},
	{Head: "Created", Path: "created_at"},
	{Head: "Error", Path: "last_error"},
	{Head: "Stop reason", Path: "stop_reason"},
}

func newEngineGetCmd() *cobra.Command {
	// usage is read before the record and merged into it, so text mode can show
	// the Engine's gauges and --json carries the same numbers under "usage".
	var usage *engineUsage

	op := Op{
		Use:     "get <engine>",
		Aliases: []string{"show"},
		Short:   "Show a managed Optimal Engine, with its usage gauges",
		Long: `Shows one managed Engine by name or id. The record is the managed database:
its version, state, private address and workspace, followed by what the Engine
reports it is using against its size's limits (vectors, memories, contexts,
disk, memory, write rate).

A gauge the Engine did not report is left out rather than shown as zero; an
Engine that cannot answer leaves the gauges out and the record prints anyway.

  miosa engine get qa-brain
  miosa engine get qa-brain --json | jq '{version: .engine_version, usage}'`,
		Method: "GET", Path: "/databases/{0}", Args: []Arg{{Name: "engine"}},
		Detail: append(engineDetail, engineGaugeDetail...),
		Before: func(cmd *cobra.Command, c *api.Client) error {
			usage = fetchEngineUsage(cmd, c)
			return nil
		},
		After: func(cmd *cobra.Command, resp any) error {
			if usage != nil {
				mergeEngineUsage(resp, usage)
			}
			return nil
		},
	}
	return op.command("engine")
}

// engineGaugeDetail are the usage rows appended to the record. Each is a
// dotted path into the "usage" object mergeEngineUsage writes.
var engineGaugeDetail = []Col{
	{Head: "Vectors", Path: "usage.vectors"},
	{Head: "Memories", Path: "usage.memories"},
	{Head: "Contexts", Path: "usage.contexts"},
	{Head: "Disk", Path: "usage.disk"},
	{Head: "Memory", Path: "usage.memory"},
	{Head: "Write rate", Path: "usage.write_rate"},
	{Head: "Usage sampled", Path: "usage.sampled_at"},
}

// engineUsage is GET /databases/:id/engine-usage: the Engine's own gauges in
// the console's shape, already scaled by the control plane.
type engineUsage struct {
	SampledAt      string        `json:"sampled_at"`
	Size           string        `json:"size"`
	Limits         []engineGauge `json:"limits"`
	Recommendation any           `json:"recommendation"`
}

type engineGauge struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Used  float64  `json:"used"`
	Limit *float64 `json:"limit"`
	Unit  string   `json:"unit"`
}

// fetchEngineUsage reads the gauges for the Engine the argument names. It is
// best effort by design: the usage route answers 503 when the Engine is down,
// and that must not turn 'engine get' into a failure.
func fetchEngineUsage(cmd *cobra.Command, c *api.Client) *engineUsage {
	args := cmd.Flags().Args()
	if len(args) == 0 {
		return nil
	}
	var body struct {
		Data engineUsage `json:"data"`
	}
	path := "/databases/" + url.PathEscape(args[0]) + "/engine-usage"
	if err := c.Get(cmd.Context(), path, nil, &body); err != nil {
		return nil
	}
	usage := body.Data
	return &usage
}

// mergeEngineUsage writes the gauges into the record the renderer sees: the
// raw object under "usage" for --json, plus one formatted string per gauge id
// for the detail rows.
func mergeEngineUsage(resp any, usage *engineUsage) {
	target, ok := unwrapObject(resp).(map[string]any)
	if !ok {
		return
	}
	merged, ok := toMap(usage)
	if !ok {
		return
	}
	for _, g := range usage.Limits {
		merged[g.ID] = formatGauge(g)
	}
	target["usage"] = merged
}

func toMap(v any) (map[string]any, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, false
	}
	return m, true
}

// formatGauge is one gauge as a person reads it: "12 / 100 (12%)",
// "1.4 GiB / 10 GiB (14%)", "3.2/s".
func formatGauge(g engineGauge) string {
	used := formatGaugeValue(g.Used, g.Unit)
	if g.Limit == nil {
		return used
	}
	limit := *g.Limit
	if limit <= 0 {
		return used
	}
	return fmt.Sprintf("%s / %s (%s)", used, formatGaugeValue(limit, g.Unit), formatPercent(g.Used, limit))
}

func formatGaugeValue(v float64, unit string) string {
	switch unit {
	case "bytes":
		return humanBytes(v)
	case "per_second":
		return trimNumber(v) + "/s"
	default:
		return trimNumber(v)
	}
}

// trimNumber prints 12 for 12.0 and 12.5 for 12.5.
func trimNumber(v float64) string {
	if v == math.Trunc(v) {
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func formatPercent(used, limit float64) string {
	if limit <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.0f%%", used/limit*100)
}

func newEngineListCmd() *cobra.Command {
	op := Op{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List managed Optimal Engines",
		Long: `Lists the managed Engines in the organizations your key covers. Other managed
databases (Postgres, MySQL, Redis, Qdrant) are left out; use 'miosa db list' for
every database.`,
		Method: "GET", Path: "/databases", AllOrgs: true,
		Cols: []Col{
			{Head: "NAME", Path: "name"},
			{Head: "ID", Path: "id", Fmt: "short"},
			{Head: "VERSION", Path: "engine_version"},
			{Head: "STATE", Path: "state"},
			{Head: "HOST", Path: "host"},
			{Head: "PORT", Path: "port"},
			{Head: "CREATED", Path: "created_at", Fmt: "age"},
		},
		Rows: func(_ *cobra.Command, rows []any) []any {
			engines := make([]any, 0, len(rows))
			for _, row := range rows {
				if record, ok := row.(map[string]any); ok && record["engine"] == "optimal" {
					engines = append(engines, row)
				}
			}
			return engines
		},
	}
	return op.command("engine")
}

func newEngineSyncCmd() *cobra.Command {
	f := &engineSyncFlags{}

	cmd := &cobra.Command{
		Use:   "sync <local> [cloud]",
		Short: "Keep one workspace in sync between a local and a cloud Engine",
		Long: `Direct mode wires two reachable Engines for one workspace:

  1. mint a replica key on each Engine (scopes sync and read, one workspace)
  2. enable the sync ledger for the workspace on both
  3. register each Engine as the other's peer with policy both
  4. run one cycle unless --no-run, then print both statuses

Relay mode (--relay and --engine-id) moves the same changes through MIOSA
instead, for a managed Engine that cannot dial another Engine. The local Engine
is the initiator: it pulls the cloud's changes through the relay, applies them
locally, then pushes its own ledger back. Each side carries the sender's engine
envelope (migration, replica_id, version), which the receiving Engine checks
before it applies anything.

The admin keys come from --local-key/--cloud-key or MIOSA_ENGINE_LOCAL_KEY and
MIOSA_ENGINE_CLOUD_KEY. The relay takes a MIOSA API key with engine:sync, from
--api-key or MIOSA_API_KEY. No key is ever printed.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := engineSyncOptions{
				localURL:     firstNonEmpty(f.local, argAt(args, 0)),
				cloudURL:     firstNonEmpty(f.cloud, argAt(args, 1)),
				workspace:    f.workspace,
				localKey:     firstNonEmpty(f.localKey, os.Getenv("MIOSA_ENGINE_LOCAL_KEY")),
				cloudKey:     firstNonEmpty(f.cloudKey, os.Getenv("MIOSA_ENGINE_CLOUD_KEY")),
				policy:       firstNonEmpty(f.policy, "both"),
				runOneCycle:  !f.noRun,
				requestLimit: time.Duration(globalFlags.Timeout) * time.Second,
				relayURL:     firstNonEmpty(f.relay, os.Getenv("MIOSA_RELAY_URL")),
				engineID:     f.engineID,
				miosaKey:     firstNonEmpty(f.miosaKey, os.Getenv("MIOSA_API_KEY")),
				since:        f.since,
			}
			if err := opts.validate(); err != nil {
				return die(err)
			}
			if opts.relayMode() {
				return die(runEngineRelaySync(cmd.Context(), printerFor(cmd), opts))
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
	pf.StringVar(&f.relay, "relay", "", "MIOSA relay base URL (for example https://api.miosa.ai/api/v1); enables relay mode")
	pf.StringVar(&f.engineID, "engine-id", "", "Managed Engine database id on the relay")
	pf.StringVar(&f.miosaKey, "api-key", "", "MIOSA API key with engine:sync (env: MIOSA_API_KEY)")
	pf.IntVar(&f.since, "since", 0, "Ledger sequence to pull from on both sides")

	cmd.AddCommand(newEngineSyncStatusCmd())
	return cmd
}

func newEngineSyncStatusCmd() *cobra.Command {
	f := &engineSyncFlags{}

	cmd := &cobra.Command{
		Use:   "status <local> [cloud]",
		Short: "Show sync status and open conflicts on both Engines",
		Long: `Reads the sync status of both sides. In relay mode it reads the cloud
through MIOSA and reports the migration and version the cloud expects, so a
schema mismatch is visible before a push fails.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := engineSyncOptions{
				localURL:     firstNonEmpty(f.local, argAt(args, 0)),
				cloudURL:     firstNonEmpty(f.cloud, argAt(args, 1)),
				workspace:    f.workspace,
				localKey:     firstNonEmpty(f.localKey, os.Getenv("MIOSA_ENGINE_LOCAL_KEY")),
				cloudKey:     firstNonEmpty(f.cloudKey, os.Getenv("MIOSA_ENGINE_CLOUD_KEY")),
				requestLimit: time.Duration(globalFlags.Timeout) * time.Second,
				relayURL:     firstNonEmpty(f.relay, os.Getenv("MIOSA_RELAY_URL")),
				engineID:     f.engineID,
				miosaKey:     firstNonEmpty(f.miosaKey, os.Getenv("MIOSA_API_KEY")),
				since:        f.since,
			}
			if err := opts.validate(); err != nil {
				return die(err)
			}
			if opts.relayMode() {
				return die(showEngineRelayStatus(cmd.Context(), printerFor(cmd), opts))
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
	pf.StringVar(&f.relay, "relay", "", "MIOSA relay base URL; enables relay mode")
	pf.StringVar(&f.engineID, "engine-id", "", "Managed Engine database id on the relay")
	pf.StringVar(&f.miosaKey, "api-key", "", "MIOSA API key with engine:sync (env: MIOSA_API_KEY)")
	return cmd
}

func (o engineSyncOptions) validate() error {
	if strings.TrimSpace(o.workspace) == "" {
		return fmt.Errorf("--workspace is required: sync is per workspace, never the whole Engine")
	}
	if o.relayMode() {
		if strings.TrimSpace(o.relayURL) == "" || strings.TrimSpace(o.engineID) == "" {
			return fmt.Errorf("relay mode needs --relay (for example https://api.miosa.ai/api/v1) and --engine-id")
		}
		if strings.TrimSpace(o.miosaKey) == "" {
			return fmt.Errorf("relay mode needs a MIOSA key with engine:sync: --api-key or MIOSA_API_KEY")
		}
		if strings.TrimSpace(o.localURL) == "" || strings.TrimSpace(o.localKey) == "" {
			return fmt.Errorf("relay mode still needs the local Engine URL and its admin key")
		}
		if _, err := newEngineClient(o.localURL, o.localKey, o.requestLimit); err != nil {
			return err
		}
		if _, err := newEngineClient(o.relayURL, o.miosaKey, o.requestLimit); err != nil {
			return err
		}
		return nil
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

func argAt(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}

func (c *engineClient) do(ctx context.Context, method, path string, body, out any) error {
	return c.doWithHeaders(ctx, method, path, body, out, nil)
}

func (c *engineClient) doWithHeaders(ctx context.Context, method, path string, body, out any, headers map[string]string) error {
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
	for name, value := range headers {
		req.Header.Set(name, value)
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

// ---------------------------------------------------------------------------
// Relay mode (WO-46)
// ---------------------------------------------------------------------------

// runEngineRelaySync moves changes through the MIOSA control-plane relay: no
// Engine dials another Engine, and a cloud Engine never dials out. The local
// Engine is always the initiator: pull the cloud's changes, apply them
// locally, then push the local ledger back.
//
// The Engine compares the *sender's* schema migration with its own, so each
// POST /api/sync/changes carries the sender's engine envelope: the cloud's
// when applying the cloud's changes locally, and the local Engine's when
// pushing the local ledger. The relay status reports the target's migration
// and version, which is what this command checks before it starts.
func runEngineRelaySync(ctx context.Context, p *output.Printer, o engineSyncOptions) error {
	local, err := newEngineClient(o.localURL, o.localKey, o.requestLimit)
	if err != nil {
		return err
	}
	relay, err := newEngineClient(o.relayURL, o.miosaKey, o.requestLimit)
	if err != nil {
		return err
	}

	cloudStatus, err := fetchRelayStatus(ctx, relay, o.engineID, o.workspace)
	if err != nil {
		return fmt.Errorf("relay status: %w", err)
	}
	if enabled, _ := cloudStatus["enabled"].(bool); !enabled {
		return fmt.Errorf("sync is not enabled for workspace %s on the cloud Engine", o.workspace)
	}

	pulled, cloudCursor, cloudReplica, err := pullAndApply(ctx, local, relay, o, cloudStatus)
	if err != nil {
		return err
	}
	pushed, localCursor, err := pullAndPush(ctx, local, relay, o, cloudReplica)
	if err != nil {
		return err
	}

	cloudStatus, err = fetchRelayStatus(ctx, relay, o.engineID, o.workspace)
	if err != nil {
		return err
	}
	localStatus, err := fetchLocalStatus(ctx, local, o.workspace)
	if err != nil {
		return err
	}

	return renderEngineRelay(p, o, cloudStatus, localStatus, pulled, pushed, cloudCursor, localCursor, true)
}

func showEngineRelayStatus(ctx context.Context, p *output.Printer, o engineSyncOptions) error {
	local, err := newEngineClient(o.localURL, o.localKey, o.requestLimit)
	if err != nil {
		return err
	}
	relay, err := newEngineClient(o.relayURL, o.miosaKey, o.requestLimit)
	if err != nil {
		return err
	}

	cloudStatus, err := fetchRelayStatus(ctx, relay, o.engineID, o.workspace)
	if err != nil {
		return fmt.Errorf("relay status: %w", err)
	}
	localStatus, err := fetchLocalStatus(ctx, local, o.workspace)
	if err != nil {
		return err
	}

	return renderEngineRelay(p, o, cloudStatus, localStatus, 0, 0, 0, 0, false)
}

func pullAndApply(ctx context.Context, local, relay *engineClient, o engineSyncOptions, cloudStatus map[string]any) (int, int, string, error) {
	var wrap map[string]any
	path := fmt.Sprintf("/engines/%s/sync/pull?workspace=%s&since=%d&limit=500",
		url.PathEscape(o.engineID), url.QueryEscape(o.workspace), o.since)

	if err := relay.do(ctx, http.MethodGet, path, nil, &wrap); err != nil {
		return 0, 0, "", fmt.Errorf("pull from the cloud: %w", err)
	}

	batch := mapOf(wrap["data"])
	mutations := sliceOf(batch["mutations"])
	cursor := intOf(batch["cursor"])

	// The pull answers with the *cloud's* engine envelope. That is the sender's
	// identity for the local apply, and its replica id is what the local ledger
	// must echo-suppress when it is read back for the push.
	envelope := mapOf(batch["engine"])
	if len(envelope) == 0 {
		envelope = engineEnvelope(cloudStatus)
	}
	cloudReplica, _ := envelope["replica_id"].(string)

	if len(mutations) == 0 {
		return 0, cursor, cloudReplica, nil
	}

	body := map[string]any{"workspace": o.workspace, "engine": envelope, "mutations": mutations}
	if err := local.do(ctx, http.MethodPost, "/api/sync/changes", body, nil); err != nil {
		return 0, cursor, cloudReplica, fmt.Errorf("apply the cloud's changes locally: %w", err)
	}

	return len(mutations), cursor, cloudReplica, nil
}

func pullAndPush(ctx context.Context, local, relay *engineClient, o engineSyncOptions, cloudReplica string) (int, int, error) {
	envelope, err := localEngineEnvelope(ctx, local, o.workspace)
	if err != nil {
		return 0, 0, err
	}

	var batch map[string]any
	path := fmt.Sprintf("/api/sync/changes?workspace=%s&since=%d&limit=500",
		url.QueryEscape(o.workspace), o.since)

	// Rows that originated on the cloud must not travel back to it: the Engine
	// excludes mutations from the requester named in x-optimal-replica.
	var headers map[string]string
	if cloudReplica != "" {
		headers = map[string]string{"x-optimal-replica": cloudReplica}
	}

	if err := local.doWithHeaders(ctx, http.MethodGet, path, nil, &batch, headers); err != nil {
		return 0, 0, fmt.Errorf("read the local ledger: %w", err)
	}

	mutations := sliceOf(batch["mutations"])
	cursor := intOf(batch["cursor"])

	if len(mutations) == 0 {
		return 0, cursor, nil
	}

	body := map[string]any{"workspace": o.workspace, "engine": envelope, "mutations": mutations}
	if replica, ok := envelope["replica_id"]; ok {
		body["replica_id"] = replica
	}
	pushPath := fmt.Sprintf("/engines/%s/sync/push?workspace=%s",
		url.PathEscape(o.engineID), url.QueryEscape(o.workspace))

	if err := relay.do(ctx, http.MethodPost, pushPath, body, nil); err != nil {
		return 0, cursor, fmt.Errorf("push the local ledger to the cloud: %w", err)
	}

	return len(mutations), cursor, nil
}

func localEngineEnvelope(ctx context.Context, local *engineClient, workspace string) (map[string]any, error) {
	var version map[string]any
	if err := local.do(ctx, http.MethodGet, "/api/version", nil, &version); err != nil {
		return nil, fmt.Errorf("read the local Engine version: %w", err)
	}

	var status map[string]any
	_ = local.do(ctx, http.MethodGet, "/api/sync/status?workspace="+url.QueryEscape(workspace), nil, &status)

	envelope := map[string]any{}
	if v, ok := version["expected_migration"]; ok {
		envelope["migration"] = v
	}
	if v, ok := version["version"]; ok {
		envelope["version"] = v
	}
	if v, ok := status["replica_id"]; ok {
		envelope["replica_id"] = v
	}

	if _, ok := envelope["migration"]; !ok {
		return nil, fmt.Errorf("the local Engine did not report its schema migration")
	}
	return envelope, nil
}

func engineEnvelope(source map[string]any) map[string]any {
	envelope := map[string]any{}
	for _, key := range []string{"migration", "version", "replica_id"} {
		if value, ok := source[key]; ok {
			envelope[key] = value
		}
	}
	return envelope
}

func fetchRelayStatus(ctx context.Context, relay *engineClient, engineID, workspace string) (map[string]any, error) {
	var wrap map[string]any
	path := fmt.Sprintf("/engines/%s/sync/status?workspace=%s",
		url.PathEscape(engineID), url.QueryEscape(workspace))

	if err := relay.do(ctx, http.MethodGet, path, nil, &wrap); err != nil {
		return nil, err
	}
	return mapOf(wrap["data"]), nil
}

func fetchLocalStatus(ctx context.Context, local *engineClient, workspace string) (map[string]any, error) {
	var status map[string]any
	if err := local.do(ctx, http.MethodGet, "/api/sync/status?workspace="+url.QueryEscape(workspace), nil, &status); err != nil {
		return nil, fmt.Errorf("read the local Engine status: %w", err)
	}
	return status, nil
}

func renderEngineRelay(p *output.Printer, o engineSyncOptions, cloud, local map[string]any, pulled, pushed, cloudCursor, localCursor int, moved bool) error {
	if globalFlags.JSON {
		return p.JSON(map[string]any{
			"workspace":    o.workspace,
			"engine_id":    o.engineID,
			"relay":        o.relayURL,
			"pulled":       pulled,
			"pushed":       pushed,
			"cloud_cursor": cloudCursor,
			"local_cursor": localCursor,
			"cloud":        cloud,
			"local":        local,
		})
	}

	rows := [][2]string{
		{"Workspace", o.workspace},
		{"Cloud Engine", o.engineID},
		{"Relay", o.relayURL},
	}
	if moved {
		rows = append(rows, [2]string{"Pulled / pushed", fmt.Sprintf("%d / %d", pulled, pushed)})
	}

	rows = append(rows,
		[2]string{"Cloud enabled", fmt.Sprint(cloud["enabled"])},
		[2]string{"Cloud ledger", fmt.Sprint(cloud["ledger_sequence"])},
		[2]string{"Cloud conflicts", fmt.Sprint(cloud["open_conflicts"])},
		[2]string{"Local enabled", fmt.Sprint(local["enabled"])},
		[2]string{"Local ledger", fmt.Sprint(local["ledger_sequence"])},
		[2]string{"Local conflicts", fmt.Sprint(local["open_conflicts"])},
	)

	p.Fields(rows)
	return nil
}

func mapOf(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func sliceOf(value any) []any {
	if s, ok := value.([]any); ok {
		return s
	}
	return nil
}

func intOf(value any) int {
	switch n := value.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}
