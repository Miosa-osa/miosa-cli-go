package commands

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Managed Optimal Engines are managed databases with engine "optimal". The
// commands here call the live databases routes; they add no route of their
// own. `engine create` is a command rather than a declarative op because it
// has two behaviours the generic layer does not cover: re-running it with the
// same name returns the existing Engine instead of a duplicate, and --wait
// follows the readiness states.

// engineRef is the flat shape of a managed database response.
type engineRef struct {
	ID                  string         `json:"id"`
	Name                string         `json:"name"`
	Engine              string         `json:"engine"`
	EngineVersion       string         `json:"engine_version"`
	State               string         `json:"state"`
	Host                string         `json:"host"`
	Port                int            `json:"port"`
	CPUCount            int            `json:"cpu_count"`
	MemoryMB            int            `json:"memory_mb"`
	StorageMB           int            `json:"storage_mb"`
	WorkspaceID         string         `json:"workspace_id"`
	ReadinessStatus     string         `json:"readiness_status"`
	ReadinessComponents map[string]any `json:"readiness_components"`
	CreatedAt           string         `json:"created_at"`
	LastError           any            `json:"last_error"`
	// Metadata carries the Engine's declared scope ("org" or "workspace").
	Metadata map[string]any `json:"metadata"`
}

func newEngineCreateCmd() *cobra.Command {
	var (
		workspace   string
		workspaceID string
		forWhat     string
		size        string
		version     string
		cpu         int
		memoryMB    int
		storageMB   int
		disk        int
		wait        bool
		timeout     int
	)

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a managed Optimal Engine (or return the existing one)",
		Long: `Creates a managed Optimal Engine: an Engine in the workspace network with a
durable data volume, an API key and a private URL.

Re-running it with the same name returns the existing Engine, so a script can
call it unconditionally. --wait follows provisioning until the Engine is
running (the target is under 120 s) and prints each state change.

  miosa engine create qa-brain --workspace qa --size small --wait
  miosa engine create qa-brain --for workspace --wait
  miosa engine create qa-brain --wait --json | jq '{id, state, engine_version}'

--for picks a starting point for how the Engine is used: agent (tiny, 2 GiB),
workspace (xs, 10 GiB) or business (small, 25 GiB). --size and --disk override
either half. --disk is in GiB; --storage-mb stays for exact megabytes and the
two cannot be given together.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineCreate(cmd, engineCreateOptions{
				name:        args[0],
				workspace:   workspace,
				workspaceID: workspaceID,
				forWhat:     forWhat,
				size:        size,
				version:     version,
				cpu:         cpu,
				memoryMB:    memoryMB,
				storageMB:   storageMB,
				diskGB:      disk,
				wait:        wait,
				timeout:     time.Duration(timeout) * time.Second,
			})
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&workspace, "workspace", "", "Workspace slug (default: the profile's default workspace)")
	flags.StringVar(&workspaceID, "workspace-id", "", "Workspace id")
	_ = flags.MarkHidden("workspace-id")
	flags.StringVar(&forWhat, "for", "", "How the Engine is used: agent, workspace or business (sets --size and --disk)")
	flags.StringVar(&size, "size", "", "Size: tiny, xs, small, medium or large")
	flags.StringVar(&version, "version", "", "Engine version (default: the engine's current one)")
	flags.IntVar(&cpu, "cpu", 0, "CPUs (overrides --size)")
	flags.IntVar(&memoryMB, "memory-mb", 0, "Memory in MB (overrides --size)")
	flags.IntVar(&storageMB, "storage-mb", 0, "Disk in MB (overrides --size)")
	flags.IntVar(&disk, "disk", 0, "Disk in GiB (overrides --size and --storage-mb)")
	flags.BoolVar(&wait, "wait", false, "Wait until the Engine is running")
	flags.IntVar(&timeout, "timeout", 120, "Seconds to wait for the Engine to run")
	return cmd
}

type engineCreateOptions struct {
	name        string
	workspace   string
	workspaceID string
	forWhat     string
	size        string
	version     string
	cpu         int
	memoryMB    int
	storageMB   int
	diskGB      int
	wait        bool
	timeout     time.Duration
}

// enginePreset is what --for picks: a named size and a disk in GiB. The size
// names are the database size list's own ids, not a second vocabulary.
type enginePreset struct {
	size   string
	diskGB int
}

var enginePresets = map[string]enginePreset{
	"agent":     {size: "tiny", diskGB: 2},
	"workspace": {size: "xs", diskGB: 10},
	"business":  {size: "small", diskGB: 25},
}

// applyEnginePreset resolves --for into --size and --disk, then lets the
// explicit flags win. It is called before the body is built so a bad value
// stops the command before anything is created.
func applyEnginePreset(opts *engineCreateOptions) error {
	if opts.diskGB < 0 {
		return usagef("--disk must be a positive number of GiB")
	}
	if opts.diskGB > 0 && opts.storageMB > 0 {
		return usagef("use --disk (GiB) or --storage-mb (MB), not both")
	}

	forWhat := strings.ToLower(strings.TrimSpace(opts.forWhat))
	if forWhat == "" {
		return nil
	}

	preset, ok := enginePresets[forWhat]
	if !ok {
		return usagef("--for must be agent, workspace or business (got %q)", opts.forWhat)
	}
	if opts.size == "" {
		opts.size = preset.size
	}
	if opts.diskGB == 0 && opts.storageMB == 0 {
		opts.diskGB = preset.diskGB
	}
	return nil
}

func runEngineCreate(cmd *cobra.Command, opts engineCreateOptions) error {
	if err := applyEnginePreset(&opts); err != nil {
		return err
	}
	if opts.diskGB > 0 {
		opts.storageMB = opts.diskGB * 1024
	}

	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	existing, err := findEngineByName(ctx, c.API.Get, opts.name)
	if err != nil {
		return die(err)
	}
	if existing != nil {
		engine := *existing
		if !isJSON() {
			p.Line("%s already exists (%s), state %s", engine.Name, shortRef(engine.ID), engine.State)
		}
		if opts.wait {
			engine, err = waitForEngine(ctx, c.API.Get, engine, opts.timeout, p)
			if err != nil {
				return die(err)
			}
		}
		if isJSON() {
			return p.JSON(engine)
		}
		return nil
	}

	if opts.workspace == "" && opts.workspaceID == "" {
		opts.workspace = defaultWorkspaceRef()
	}
	if err := validateEngineSize(ctx, c.API.Get, opts.size); err != nil {
		return err
	}
	body := map[string]any{"name": opts.name, "engine": "optimal"}
	putIfSet(body, "workspace_slug", opts.workspace)
	putIfSet(body, "workspace_id", opts.workspaceID)
	putIfSet(body, "size", opts.size)
	putIfSet(body, "engine_version", opts.version)
	putIfNonZero(body, "cpu_count", opts.cpu)
	putIfNonZero(body, "memory_mb", opts.memoryMB)
	putIfNonZero(body, "storage_mb", opts.storageMB)

	var created engineRef
	if err := c.API.Post(ctx, "/databases", body, &created); err != nil {
		return die(err)
	}
	if created.ID == "" {
		return fmt.Errorf("the API did not return the new Engine's id")
	}
	if !isJSON() {
		p.Line("Created %s (%s), state %s", created.Name, shortRef(created.ID), created.State)
	}

	engine := created
	if opts.wait {
		engine, err = waitForEngine(ctx, c.API.Get, created, opts.timeout, p)
		if err != nil {
			return die(err)
		}
	}
	if isJSON() {
		return p.JSON(engine)
	}
	p.Success("%s is %s", engine.Name, engine.State)
	return nil
}

// findEngineByName looks the name up among managed Engines. A match of another
// engine kind is not this command's Engine and is left alone.
func findEngineByName(ctx context.Context, get apiGetter, name string) (*engineRef, error) {
	query := url.Values{"limit": {"200"}}
	var list struct {
		Data []engineRef `json:"data"`
	}
	if err := get(ctx, "/databases", query, &list); err != nil {
		return nil, err
	}
	for i := range list.Data {
		if list.Data[i].Name == name && list.Data[i].Engine == "optimal" {
			return &list.Data[i], nil
		}
	}
	return nil, nil
}

func waitForEngine(ctx context.Context, get apiGetter, engine engineRef, timeout time.Duration, p printerLike) (engineRef, error) {
	deadline := time.Now().Add(timeout)
	last := engine.State

	for {
		if engine.State == "running" {
			return engine, nil
		}
		if engine.State == "error" || engine.State == "destroyed" {
			return engine, fmt.Errorf("%s is %s: %v", engine.Name, engine.State, engine.LastError)
		}
		if time.Now().After(deadline) {
			return engine, fmt.Errorf("%s is still %s after %s", engine.Name, engine.State, timeout)
		}

		select {
		case <-ctx.Done():
			return engine, ctx.Err()
		case <-time.After(2 * time.Second):
		}

		var current engineRef
		if err := get(ctx, "/databases/"+url.PathEscape(engine.ID), nil, &current); err != nil {
			return engine, err
		}
		if !isJSON() && current.State != last {
			p.Line("%s: %s", current.Name, current.State)
			last = current.State
		}
		engine = current
	}
}

// printerLike is the slice of output.Printer this file needs, so the wait loop
// stays testable and does not depend on the concrete printer.
type printerLike interface {
	Line(format string, args ...any)
}

// validateEngineSize checks a resolved --size against the database size list
// the API publishes (GET /databases meta), so --for can never send a name the
// server does not know. When the catalog cannot be read the server's own
// validation decides; the create is not blocked on a catalog read.
func validateEngineSize(ctx context.Context, get apiGetter, size string) error {
	if size == "" {
		return nil
	}
	sizes := engineSizeIDs(ctx, get)
	if len(sizes) == 0 {
		return nil
	}
	for _, id := range sizes {
		if id == size {
			return nil
		}
	}
	return usagef("--size must be one of %s (got %q)", strings.Join(sizes, ", "), size)
}

// engineSizeIDs reads the size ids from the databases catalog entry.
func engineSizeIDs(ctx context.Context, get apiGetter) []string {
	var resp struct {
		Meta struct {
			Engines struct {
				Catalog map[string]struct {
					Sizes []struct {
						ID string `json:"id"`
					} `json:"sizes"`
				} `json:"catalog"`
			} `json:"engines"`
		} `json:"meta"`
	}
	q := url.Values{"limit": {"1"}}
	if err := get(ctx, "/databases", q, &resp); err != nil {
		return nil
	}
	entry := resp.Meta.Engines.Catalog["optimal"]
	ids := make([]string, 0, len(entry.Sizes))
	for _, s := range entry.Sizes {
		if s.ID != "" {
			ids = append(ids, s.ID)
		}
	}
	return ids
}

func putIfSet(body map[string]any, key, value string) {
	if value != "" {
		body[key] = value
	}
}

func putIfNonZero(body map[string]any, key string, value int) {
	if value != 0 {
		body[key] = value
	}
}
