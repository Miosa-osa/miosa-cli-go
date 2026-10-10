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
	meta        []string
}

// sandboxSizes are the sizes the platform offers (see 'miosa catalog').
var sandboxSizes = []string{"micro", "tiny", "xs", "small", "quad", "medium", "large", "xl"}

func newCreateCmd() *cobra.Command {
	var (
		size      string
		template  string
		workspace string
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
  miosa create --size large
  miosa create my-box --env staging
  miosa create my-box --no-env
  miosa create my-box --setup-file ./setup.sh
  miosa create my-box --wait --ttl 2h --meta team=infra

Creation returns while the sandbox boots. --wait blocks until it accepts
commands, so 'miosa create x --wait && miosa exec x -- ...' never races.

--env picks the environment (repositories, secrets, connections) the sandbox
starts from; without it your default environment is used. --no-env passes
nothing of yours. --setup-file runs a script (UTF-8, up to 64KB) in the
background once the sandbox is ready; watch it with 'miosa info'.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args, size, template, workspace, envFlags, opts)
		},
	}

	cmd.Flags().StringVar(&size, "size", "small", "Sandbox size: "+strings.Join(sandboxSizes, ", "))
	cmd.Flags().BoolVar(&opts.wait, "wait", false, "Wait until the sandbox is ready for commands")
	cmd.Flags().DurationVar(&opts.waitTimeout, "wait-timeout", 3*time.Minute, "How long --wait may take")
	cmd.Flags().StringVar(&opts.ttl, "ttl", "", "Time to live: seconds, 90m, 2h or 2d (default: the plan's)")
	cmd.Flags().StringVar(&opts.idle, "idle-timeout", "", "Pause after this much inactivity: seconds, 15m, 1h")
	cmd.Flags().StringArrayVar(&opts.meta, "meta", nil, "Metadata, KEY=VALUE (repeatable)")
	cmd.Flags().StringVar(&template, "template", "miosa-sandbox", "Sandbox template, for example miosa-sandbox, nextjs, fastapi, or hono")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace slug to assign the sandbox to")
	envFlags.bind(cmd, "sandbox")

	return cmd
}

func runCreate(cmd *cobra.Command, args []string, size, template, workspace string, envFlags machineEnvFlags, opts createOptions) error {
	p := printerFor(cmd)

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

	input := miosa.CreateSandboxInput{
		Name:        name,
		TemplateID:  template,
		Size:        sz,
		Environment: envName,
		NoEnv:       noEnv,
		SetupFile:   setup,
	}
	if workspace != "" {
		input.WorkspaceSlug = workspace
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
	if !isJSON() {
		p.Line("Creating sandbox…")
	}

	sandbox, err := c.SDK.Sandboxes.Create(cmd.Context(), input)
	if err != nil {
		return die(err)
	}

	if opts.wait && !(sandbox.Ready && sandbox.State == "running") {
		if !isJSON() {
			p.Line("Waiting for %s to be ready...", orDefault(sandbox.Name, sandbox.ID))
		}
		st, err := waitSandbox(cmd.Context(), c.API, sandbox.ID, "ready", opts.waitTimeout, nil)
		if err != nil {
			return die(err)
		}
		sandbox.State = miosa.ComputerStatus(st.State)
		sandbox.Ready = st.Ready
	}

	if isJSON() {
		return p.JSON(sandbox.SandboxData)
	}

	p.Success("Created sandbox %q (%s)", sandbox.Name, sandbox.ID)
	p.Line("  Status:   %s", sandbox.State)
	p.Line("  Size:     %s", sandbox.Size)
	p.Line("  Template: %s", sandbox.TemplateID)
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
