package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"
)

// createOptions are the flags of 'create' beyond size, template and the
// environment ones.
type createOptions struct {
	wait        bool
	waitTimeout time.Duration
	ttl         string
	idle        string
	alwaysOn    bool
	meta        []string
	tag         []string
	expires     string
	vars        []string
	count       int
	concurrency int
	from        string
}

// sandboxSizes are the sizes the platform offers (see 'miosa catalog').
var sandboxSizes = []string{"micro", "tiny", "xs", "small", "quad", "medium", "large", "xl"}

// sandboxRuntimes are the runtime selectors 'miosa create --runtime' accepts;
// see 'miosa runtimes list' for what each one offers right now.
var sandboxRuntimes = []string{"firecracker", "soma", "auto"}

func newCreateCmd() *cobra.Command {
	var (
		size      string
		template  string
		workspace string
		runtime   string
		envFlags  machineEnvFlags
		opts      createOptions
	)

	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new sandbox",
		Long: `Provision a new MIOSA sandbox.

If no name is provided, one is generated automatically.

Examples:
  miosa create my-box
  miosa create my-box --size medium --template miosa-sandbox
  miosa create my-box --runtime soma
  miosa create --size large
  miosa create my-box --env staging
  miosa create my-box --no-env
  miosa create my-box --setup-file ./setup.sh
  miosa create my-box --wait --ttl 2h --meta team=infra
  miosa create my-box -e API_URL=https://api.example.com -e DEBUG=1
  miosa create ci --count 20 --wait
  miosa create --from web-stack --wait     # from a named snapshot

Creation returns while the sandbox boots. --wait blocks until it accepts
commands, so 'miosa create x --wait && miosa exec x -- ...' never races.

--env picks the environment (repositories, secrets, connections) the sandbox
starts from; without it your default environment is used. --no-env passes
nothing of yours. --setup-file runs a script (UTF-8, up to 64KB) in the
background once the sandbox is ready; watch it with 'miosa info'.

-e KEY=VALUE sets a variable in this sandbox only, on top of the environment's.

--count N creates N identical sandboxes in one batch, their names starting with
<name> (--concurrency caps how many start at once); with --wait it blocks until all
have settled and exits non-zero if any failed. 'miosa batch' manages the batch.
--env, --no-env, --runtime, --meta and --expires apply to one sandbox only.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args, size, template, workspace, runtime, envFlags, opts)
		},
	}

	cmd.Flags().StringVar(&size, "size", "small", "Sandbox size: "+strings.Join(sandboxSizes, ", "))
	cmd.Flags().BoolVar(&opts.wait, "wait", false, "Wait until the sandbox is ready for commands")
	cmd.Flags().DurationVar(&opts.waitTimeout, "wait-timeout", 3*time.Minute, "How long --wait may take")
	cmd.Flags().StringVar(&opts.ttl, "ttl", "", "Time to live: seconds, 90m, 2h or 2d (default: the plan's)")
	cmd.Flags().StringVar(&opts.idle, "idle-timeout", "", "Pause after this much inactivity: seconds, 15m, 1h")
	cmd.Flags().BoolVar(&opts.alwaysOn, "always-on", false, "Keep the sandbox running: no idle pause, and the platform resumes it after a host restart")
	cmd.Flags().StringArrayVar(&opts.meta, "meta", nil, "Metadata, KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&opts.tag, "tag", nil, "Governance tag, KEY=VALUE (repeatable): owner, entity, purpose, project")
	cmd.Flags().StringVar(&opts.expires, "expires", "", "Governance deadline: 24h, 7d or an RFC 3339 time (purpose=test defaults to 24h)")
	cmd.Flags().StringVar(&template, "template", "miosa-sandbox", "Sandbox template, for example miosa-sandbox, nextjs, static-html, or fastapi-auth")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace slug to assign the sandbox to (default: the profile's default workspace)")
	cmd.Flags().StringVar(&runtime, "runtime", "", "Sandbox runtime: firecracker, soma or auto")
	cmd.Flags().StringArrayVarP(&opts.vars, "var", "e", nil, "Variable for this sandbox only, KEY=VALUE (repeatable)")
	cmd.Flags().IntVar(&opts.count, "count", 0, "Create this many identical sandboxes in one batch")
	cmd.Flags().StringVar(&opts.from, "from", "", "Start from a snapshot: its id, or the name of a named snapshot")
	cmd.Flags().IntVar(&opts.concurrency, "concurrency", 0, "With --count: how many to create at once (default: the server's)")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, failFastUsage)
	envFlags.bind(cmd, "sandbox")

	return cmd
}

func runCreate(cmd *cobra.Command, args []string, size, template, workspace, runtime string, envFlags machineEnvFlags, opts createOptions) error {
	p := printerFor(cmd)
	if failFast && opts.count > 0 {
		return usagef("--fail-fast starts one machine; it does not combine with --count")
	}
	if opts.from != "" {
		return createFromSnapshot(cmd, opts)
	}

	envName, noEnv, setup, err := envFlags.resolve()
	if err != nil {
		return die(err)
	}

	// A create that waits for readiness may hold the connection open for
	// minutes, so its HTTP timeout must outlast the wait.
	var httpTimeout time.Duration
	if opts.wait {
		httpTimeout = opts.waitTimeout + 15*time.Second
	}
	c, cfg, err := buildClientTimeout(httpTimeout)
	if err != nil {
		return die(err)
	}
	_ = cfg

	var name string
	if len(args) > 0 {
		name = args[0]
	}

	sz, err := parseSandboxSize(size)
	if err != nil {
		return usagef("%v", err)
	}

	if runtime != "" {
		if _, ok := parseSandboxRuntime(runtime); !ok {
			return usagef("--runtime: invalid runtime %q: must be one of %s", runtime, strings.Join(sandboxRuntimes, ", "))
		}
	}

	// A computer image is not a sandbox template. Sending it to the sandbox
	// API can only come back INVALID_TEMPLATE, so name the command that takes it.
	if canonical, ok := computerTemplateRef(template); ok {
		return usagef("--template %s is a computer template; run 'miosa computer create <name> --template %s' to create a desktop machine", template, canonical)
	}

	input := miosa.CreateSandboxInput{
		Name:        name,
		TemplateID:  template,
		Size:        sz,
		Environment: envName,
		NoEnv:       noEnv,
		SetupFile:   setup,
		Runtime:     runtime,
	}
	if workspace == "" {
		workspace = defaultWorkspaceRef()
	}
	if workspace != "" {
		// --workspace takes a slug, a name or an id. The create route takes an
		// id (workspace_id); sending the reference as workspace_slug failed
		// WORKSPACE_NOT_FOUND for anything but a literal slug.
		id, err := c.API.ResolveWorkspace(cmd.Context(), workspace)
		if err != nil {
			return usagef("workspace %q: %v", workspace, err)
		}
		input.WorkspaceID = id
	}
	if len(opts.tag) > 0 {
		tags, err := parseTagFlags(opts.tag)
		if err != nil {
			return usagef("%v", err)
		}
		input.Tags = tags
	}
	if strings.TrimSpace(opts.expires) != "" {
		// A duration travels as `expires` and is resolved by the server; a
		// timestamp travels as `expires_at`. Sending a duration in the
		// timestamp field would be refused.
		if looksLikeTimestamp(opts.expires) {
			input.ExpiresAt = strings.TrimSpace(opts.expires)
		} else {
			input.Expires = strings.TrimSpace(opts.expires)
		}
	}
	if opts.ttl != "" {
		n, err := parseSeconds(opts.ttl)
		if err != nil {
			return usagef("--ttl: %v", err)
		}
		input.TimeoutSec = n
	}
	if opts.idle != "" {
		n, err := parseSeconds(opts.idle)
		if err != nil {
			return usagef("--idle-timeout: %v", err)
		}
		input.IdleTimeoutSec = n
	}
	input.AlwaysOn = opts.alwaysOn
	if len(opts.vars) > 0 {
		vars, err := parseSandboxVars(opts.vars)
		if err != nil {
			return usagef("%v", err)
		}
		input.Env = vars
	}
	if len(opts.meta) > 0 {
		input.Metadata = map[string]string{}
		for _, kv := range opts.meta {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || k == "" {
				return usagef("--meta expects KEY=VALUE (got %q)", kv)
			}
			input.Metadata[k] = v
		}
	}

	if opts.wait {
		input.WaitSec = createWaitSeconds(opts.waitTimeout)
	}
	if opts.count > 0 || cmd.Flags().Changed("concurrency") {
		return runCreateBatch(cmd, c.API, input, opts, envName, noEnv)
	}

	if !isJSON() {
		p.Line("Creating sandbox…")
	}

	// With --wait the create request itself waits on the server, then the
	// CLI polls; the progress line covers both.
	var progress *waitProgress
	if opts.wait {
		progress = startWaitProgress(cmd, "the sandbox to be ready")
		progress.Set("creating")
	}
	sandbox, err := c.SDK.Sandboxes.Create(cmd.Context(), input)
	if err != nil {
		progress.Stop()
		return die(err)
	}

	if opts.wait && !(sandbox.Ready && sandbox.State == "running") {
		progress.Set(string(sandbox.State))
		st, err := waitSandbox(cmd.Context(), c.API, sandbox.ID, "ready", opts.waitTimeout, progress.Set)
		progress.Stop()
		if err != nil {
			return die(err)
		}
		sandbox.State = miosa.ComputerStatus(st.State)
		sandbox.Ready = st.Ready
	}
	progress.Stop()

	if isJSON() {
		return p.JSON(sandbox.SandboxData)
	}

	p.Success("Created sandbox %q (%s)", sandbox.Name, sandbox.ID)
	p.Line("  Status:   %s", sandbox.State)
	p.Line("  Size:     %s", sandbox.Size)
	p.Line("  Template: %s", sandbox.TemplateID)
	_ = reportWorkspaceLanding(cmd, sandbox.SandboxData)
	if envName != "" || noEnv || sandbox.Environment != "" || setup != "" {
		printMachineEnv(p, sandbox.MachineEnvironment)
	}
	p.Line("")
	ref := sandbox.Name
	if ref == "" {
		ref = sandbox.ID
	}
	if !opts.wait && !sandbox.Ready {
		p.Line("It is still booting; 'miosa wait %s' blocks until it is ready.", ref)
	}
	p.Line("Run 'miosa use %s' to set it as your default.", ref)
	return nil
}

// parseSandboxSize validates a --size value against the platform's sizes.
func parseSandboxSize(size string) (miosa.ComputerSize, error) {
	if size == "xlarge" {
		size = "xl"
	}
	for _, s := range sandboxSizes {
		if s == size {
			return miosa.ComputerSize(size), nil
		}
	}
	return "", fmt.Errorf("invalid size %q: must be one of %s", size, strings.Join(sandboxSizes, ", "))
}

// parseSandboxRuntime validates a --runtime value against the runtime
// selectors the CLI offers. The empty selector is valid and means "ask the
// server to decide", so it is not reported as a match.
func parseSandboxRuntime(runtime string) (string, bool) {
	for _, r := range sandboxRuntimes {
		if r == runtime {
			return r, true
		}
	}
	return "", false
}

// createWaitSeconds is how long to ask the create request to wait for
// readiness (the server accepts 2 to 120). 0 means do not ask and poll only,
// which is also what an older server needs.
func createWaitSeconds(wait time.Duration) int {
	n := int(wait / time.Second)
	if n > 120 {
		n = 120
	}
	if n < 2 {
		return 0
	}
	return n
}

// parseTagFlags turns repeated KEY=VALUE flags into the tag map the API takes.
// A bare KEY is refused: an empty value says nothing, and a typo in a reserved
// key (purpose, owner) should not create a resource that looks tagged.
func parseTagFlags(values []string) (map[string]string, error) {
	tags := make(map[string]string, len(values))

	for _, entry := range values {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("--tag %q: use KEY=VALUE", entry)
		}
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("--tag %q: the value is empty", entry)
		}
		tags[key] = strings.TrimSpace(value)
	}

	return tags, nil
}

// computerTemplates are the computer machine images. They are resolved by
// 'miosa computer create' and do not exist in the sandbox template catalog.
var computerTemplates = map[string]string{
	"miosa-desktop": "miosa-desktop",
	"miosa_desktop": "miosa-desktop",
	"desktop":       "miosa-desktop",
}

// computerTemplateRef reports whether a --template value names a computer
// image, and the canonical id to hand to 'miosa computer create'.
func computerTemplateRef(template string) (string, bool) {
	canonical, ok := computerTemplates[strings.ToLower(strings.TrimSpace(template))]
	return canonical, ok
}

// looksLikeTimestamp separates an RFC 3339 deadline from a duration.
func looksLikeTimestamp(value string) bool {
	value = strings.TrimSpace(value)
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}
