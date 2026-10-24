package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// attachOneShot makes `miosa run -- <command>` a one-shot: create a sandbox,
// optionally push a directory into it, run the command with no time cap,
// print its output, report what it cost, delete the sandbox (unless --keep),
// and exit with the command's code. Ctrl-C deletes the sandbox too. The
// subcommands of `run` (list, get, follow ...) are agent runs and are unchanged.
func attachOneShot(cmd *cobra.Command) {
	var (
		keep, rm, sandbox bool
		size, template    string
		cwd, name, push   string
		workspace         string
		vars, tags        []string
	)
	cmd.Args = cobra.ArbitraryArgs
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			if link, err := readLink(); err == nil && link.Service != "" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"In %s: `miosa run -- <cmd>` runs a command here with this service's variables; `miosa run list` lists agent runs.\n",
					link.Service)
			}
			return cmd.Help()
		}
		if cmd.ArgsLenAtDash() < 0 {
			return usagef("a command goes after --: miosa run -- %s", strings.Join(args, " "))
		}
		command := strings.TrimSpace(joinCommand(args[cmd.ArgsLenAtDash():]))
		if command == "" {
			return usagef("no command given after --")
		}
		if keep && rm {
			return usagef("--keep and --rm contradict each other")
		}
		// In a linked directory the command runs here with the service's
		// variables (Railway's `run`), unless a sandbox is asked for.
		if !sandbox && !keep && !rm && size == "" && template == "" && cwd == "" && name == "" && push == "" {
			if _, err := readLink(); err == nil {
				return runLinkedLocal(cmd, args[cmd.ArgsLenAtDash():])
			}
		}
		if timeoutGiven(cmd) && (globalFlags.Timeout < 0 || globalFlags.Timeout > execStreamMaxSeconds) {
			return usagef("--timeout must be 0 (no limit) to %d seconds", execStreamMaxSeconds)
		}
		in := miosa.CreateSandboxInput{Name: name, TemplateID: template}
		if size != "" {
			sz, err := parseSandboxSize(size)
			if err != nil {
				return usagef("%v", err)
			}
			in.Size = sz
		}
		if len(vars) > 0 {
			m, err := parseSandboxVars(vars)
			if err != nil {
				return usagef("%v", err)
			}
			in.Env = m
		}
		if len(tags) > 0 {
			in.Metadata = map[string]string{}
			for _, kv := range tags {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return usagef("--meta expects KEY=VALUE (got %q)", kv)
				}
				in.Metadata[k] = v
			}
		}
		var pushDir string
		if push != "" {
			abs, err := filepath.Abs(push)
			if err != nil {
				return usagef("--push: %v", err)
			}
			if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
				return usagef("--push %s is not a directory", push)
			}
			pushDir = abs
		}
		c, _, err := buildClientTimeout(0)
		if err != nil {
			return die(err)
		}
		if workspace == "" {
			workspace = defaultWorkspaceRef()
		}
		if workspace != "" {
			id, err := c.API.ResolveWorkspace(cmd.Context(), workspace)
			if err != nil {
				return usagef("workspace %q: %v", workspace, err)
			}
			in.WorkspaceID = id
		}
		return runOneShot(cmd, c.API, c.SDK, in, command, cwd, pushDir, keep)
	}
	f := cmd.Flags()
	f.BoolVar(&keep, "keep", false, "Keep the sandbox after the command (default: delete it)")
	f.BoolVar(&rm, "rm", false, "Delete the sandbox when the command finishes (the default)")
	_ = f.MarkHidden("rm")
	f.BoolVar(&sandbox, "sandbox", false, "In a linked directory, use a sandbox instead of running here")
	f.StringVar(&size, "size", "", "Sandbox size, as for 'miosa create'")
	f.StringVar(&template, "template", "", "Sandbox template (default miosa-sandbox)")
	f.StringVar(&cwd, "cwd", "", "Working directory for the command (default: the pushed directory)")
	f.StringVar(&name, "name", "", "Name for the sandbox")
	f.StringVar(&push, "push", "", "Copy this local directory in first, as 'miosa push' does, and run there")
	f.StringVar(&workspace, "workspace", "", "Workspace slug (default: the profile's default workspace)")
	f.StringArrayVarP(&vars, "set", "e", nil, "Environment variable KEY=VALUE for the command (repeatable)")
	f.StringArrayVar(&tags, "meta", nil, "Metadata KEY=VALUE on the sandbox (repeatable)")
	f.BoolVar(&failFast, "fail-fast", false, failFastUsage)
}

// runOneShot creates the sandbox, runs the command and cleans up. The cost
// line goes to standard error, so standard output is only the command's.
func runOneShot(cmd *cobra.Command, c *api.Client, sdk *miosa.Client, in miosa.CreateSandboxInput, command, cwd, pushDir string, keep bool) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	started := time.Now()
	in.WaitSec = 120
	sb, err := sdk.Sandboxes.Create(ctx, in)
	if err != nil {
		return die(err)
	}
	id := sb.ID
	label := orDefault(sb.Name, id)
	deleted := false
	cleanup := func() {
		if keep || deleted {
			return
		}
		dctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if derr := c.Delete(dctx, "/sandboxes/"+id, nil, nil); derr != nil && !api.IsStatus(derr, 404) {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not delete sandbox %s: %v ('miosa destroy %s' removes it)\n", label, derr, label)
			return
		}
		deleted = true
	}
	defer cleanup()

	if !(sb.Ready && sb.State == "running") {
		if _, err := waitSandbox(ctx, c, id, "ready", 5*time.Minute, nil); err != nil {
			return die(err)
		}
	}
	if pushDir != "" {
		to := path.Join("/workspace", filepath.Base(pushDir))
		skip, err := pushSkipper(pushDir)
		if err != nil {
			return die(err)
		}
		if _, err := uploadDirTo(ctx, c, pushDir, target{ref: id, path: to}, skip); err != nil {
			return die(err)
		}
		if cwd == "" {
			cwd = to
		}
	}
	timeout := 0
	if timeoutGiven(cmd) {
		timeout = globalFlags.Timeout
	}
	parent := cmd.Context()
	cmd.SetContext(ctx)
	runErr := streamExec(cmd, c, id, command, cwd, timeout)
	cmd.SetContext(parent)
	code := 0
	var ee *ExitError
	switch {
	case errors.As(runErr, &ee):
		code = ee.Code
	case runErr != nil:
		return runErr
	}

	var usage struct {
		Data struct {
			RuntimeSec float64 `json:"runtime_sec"`
			CostCents  float64 `json:"estimated_cost_cents"`
		} `json:"data"`
	}
	uerr := c.Get(context.Background(), "/sandboxes/"+id+"/usage", nil, &usage)
	cleanup()
	elapsed := time.Since(started).Round(100 * time.Millisecond)
	if isJSON() {
		summary := map[string]any{"event": "summary", "sandbox": id, "exit_code": code, "elapsed_sec": elapsed.Seconds(), "deleted": deleted}
		if uerr == nil {
			summary["runtime_sec"] = usage.Data.RuntimeSec
			summary["cost_usd"] = usage.Data.CostCents / 100
		}
		_ = printerFor(cmd).JSONLine(summary)
	} else {
		line := fmt.Sprintf("miosa: %s exited %d after %s", label, code, elapsed)
		if uerr == nil {
			line += fmt.Sprintf(", cost $%.4f", usage.Data.CostCents/100)
		}
		if deleted {
			line += ", sandbox deleted"
		} else if keep {
			line += fmt.Sprintf(", sandbox kept ('miosa destroy %s' removes it)", label)
		}
		fmt.Fprintln(cmd.ErrOrStderr(), line)
	}
	return runErr
}
