package commands

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// 'miosa sandbox' covers the sandbox resource contract that is not lifecycle:
// reading vCPU and memory with what can change live, and changing them.
// Lifecycle (create, pause, resize by recreate, destroy) lives under
// 'miosa create' / 'miosa machines'.

func newSandboxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Inspect and resize a sandbox's vCPU and memory",
		Long: `Show a sandbox's vCPU and memory and what it can change on the running
sandbox right now, and change them.

What can change live depends on the runtime and the host; 'miosa sandbox
resources' shows it for one sandbox. A running Firecracker sandbox can take
more vCPU up to the count it booted with (on hosts with the cpu controller)
and more memory up to its live ceiling. SOMA sandboxes cannot resize live.
A change that cannot apply live is not saved: resize says so and exits 5;
--restart recreates the sandbox at the new size instead.

  miosa sandbox resources my-box
  miosa sandbox resize my-box --cpu 4
  miosa sandbox resize my-box --memory 8192`,
	}
	cmd.AddCommand(newSandboxResourcesCmd(), newSandboxResizeCmd())
	return cmd
}

func newSandboxResourcesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resources [sandbox]",
		Short: "Show vCPU, memory, and what can change live",
		Long: `Read the sandbox's current vCPU and memory and the capabilities the host
reports for it: whether vCPU and memory can change on the running sandbox
right now, up to what, and why not when they cannot.

Takes a sandbox name, an id, or nothing for the current sandbox.`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{routeAnnotation: "GET /sandboxes/{0}/resources"},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeKind(cmd, "sandbox", toComplete)
		},
		RunE: runSandboxResources,
	}
}

func runSandboxResources(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ref := ""
	if len(args) > 0 {
		ref = args[0]
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}
	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return die(err)
	}
	info, err := c.SandboxResources(cmd.Context(), id)
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(info)
	}
	p.Line("Sandbox: %s", info.ID)
	p.Line("vCPU:    %d", info.Resources.CPU)
	p.Line("Memory:  %d MB", info.Resources.MemoryMB)
	p.Line("")
	p.Line("What can change on the running sandbox now:")
	p.Line("  vCPU:   %s", describeCPULive(info.Capabilities))
	p.Line("  Memory: %s", describeMemoryLive(info.Capabilities))
	return nil
}

func newSandboxResizeCmd() *cobra.Command {
	var cpu, memory int
	var restart bool
	cmd := &cobra.Command{
		Use:   "resize [sandbox] --cpu <n> --memory <mb> [--restart]",
		Short: "Change a sandbox's vCPU or memory",
		Long: `Change the vCPU count and/or memory of a sandbox. At least one of --cpu or
--memory is required.

The change is applied live where the runtime supports it (vCPU on a running
Firecracker sandbox, up to its booted count; memory up to the live ceiling).
A change that cannot apply live is NOT saved: the command says so and exits 5.
Pass --restart to recreate the sandbox from its disk at the new size instead:
the new sandbox gets a new id, running processes stop, files are kept, and the
source stays paused until you destroy it.`,
		Example: `  miosa sandbox resize my-box --cpu 1
  miosa sandbox resize my-box --memory 2048 --restart`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{routeAnnotation: "PATCH /sandboxes/{0}/resources"},
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return completeKind(cmd, "sandbox", toComplete)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSandboxResize(cmd, args, cpu, memory, restart)
		},
	}
	cmd.Flags().IntVar(&cpu, "cpu", 0, "New vCPU count (leave unset to keep it)")
	cmd.Flags().IntVar(&memory, "memory", 0, "New memory in MB (leave unset to keep it)")
	cmd.Flags().BoolVar(&restart, "restart", false, "Recreate the sandbox at the new size when the change cannot apply live (new id)")
	return cmd
}

func runSandboxResize(cmd *cobra.Command, args []string, cpu, memory int, restart bool) error {
	p := printerFor(cmd)
	if cpu <= 0 && memory <= 0 {
		return usagef("nothing to change: pass --cpu and/or --memory")
	}
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ref := ""
	if len(args) > 0 {
		ref = args[0]
	}
	ref, err = requireSandbox(ref, cfg.CurrentSandbox)
	if err != nil {
		return usagef("%v", err)
	}
	id, err := c.API.ResolveSandbox(cmd.Context(), ref)
	if err != nil {
		return die(err)
	}

	in := client.SandboxResizeInput{Restart: restart}
	if cpu > 0 {
		in.CPUCount = &cpu
	}
	if memory > 0 {
		in.MemoryMB = &memory
	}
	out, err := c.ResizeSandbox(cmd.Context(), id, in)
	if err != nil {
		return resizeError(cmd, err)
	}
	notApplied := resizeNotApplied(out)

	if isJSON() {
		if err := p.JSON(out); err != nil {
			return err
		}
	} else {
		printResizeOutcome(p, out, notApplied)
	}
	if len(notApplied) > 0 {
		return &ExitError{Code: ExitConflict}
	}
	return nil
}

// resizeNotApplied lists the dimensions the server did not change. The server
// answers 200 for them and saves nothing, so they must never read as done.
func resizeNotApplied(out *client.SandboxResizeOutcome) []string {
	if out.Restarted {
		return nil
	}
	var dims []string
	for _, dim := range []string{"cpu", "memory_mb"} {
		if ch, ok := out.Changes[dim]; ok {
			switch ch.Status {
			case "requires_restart", "unsupported":
				dims = append(dims, dim)
			}
		}
	}
	if len(dims) == 0 && out.RequiresRestart {
		dims = append(dims, "resources")
	}
	return dims
}

func printResizeOutcome(p *output.Printer, out *client.SandboxResizeOutcome, notApplied []string) {
	if out.Restarted {
		newID, sourceID := "", ""
		if out.NewSandbox != nil {
			newID = out.NewSandbox.ID
		}
		if out.SourceSandbox != nil {
			sourceID = out.SourceSandbox.ID
		}
		p.Success("sandbox recreated at the new size: %s (was %s, now paused)", newID, sourceID)
		for _, dim := range []string{"cpu", "memory_mb"} {
			if ch, ok := out.Changes[dim]; ok && ch.To > 0 {
				p.Line("%s: %d", resizeDimLabel(dim), ch.To)
			}
		}
		p.Line("Destroy the source when you no longer need it: miosa sandbox destroy %s", sourceID)
		return
	}
	for _, dim := range []string{"cpu", "memory_mb"} {
		ch, ok := out.Changes[dim]
		if !ok || ch.Status == "applied_live" || ch.Status == "unchanged" || ch.Status == "" {
			continue
		}
		reason := ""
		if ch.Reason != "" {
			reason = " (" + strings.ReplaceAll(ch.Reason, "_", " ") + ")"
		}
		p.Line("%s %d -> %d NOT applied: %s%s", resizeDimLabel(dim), ch.From, ch.To,
			strings.ReplaceAll(ch.Status, "_", " "), reason)
	}
	switch {
	case len(notApplied) > 0:
		p.Line("Now: vCPU %d, memory %d MB. Nothing else was saved; a plain restart will not apply it.",
			out.Resources.CPU, out.Resources.MemoryMB)
		p.Line("Rerun with --restart to recreate the sandbox at the new size (new id; processes stop, files are kept).")
	case out.AppliedLive:
		p.Success("applied live: vCPU %d, memory %d MB", out.Resources.CPU, out.Resources.MemoryMB)
	default:
		p.Line("Unchanged: vCPU %d, memory %d MB", out.Resources.CPU, out.Resources.MemoryMB)
	}
}

func resizeDimLabel(dim string) string {
	if dim == "memory_mb" {
		return "memory MB"
	}
	return "vCPU"
}

// resizeError turns a resize refusal into a plain sentence (text mode), while
// keeping the server's code and the exit status. In --json mode the structured
// error is printed unchanged.
func resizeError(cmd *cobra.Command, err error) error {
	ae, ok := asAPIError(err)
	if !ok || isJSON() {
		return die(err)
	}
	msg, ok := resizeRefusalMessage(ae)
	if !ok {
		return die(err)
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "miosa: "+msg)
	return &Failure{Err: err, quiet: true}
}

func resizeRefusalMessage(ae *api.Error) (string, bool) {
	switch ae.Code {
	case "RESIZE_REQUIRES_RESTART":
		return "the change cannot be applied live; rerun with --restart to recreate the sandbox at the new size", true
	case "RESIZE_UNSUPPORTED":
		return "this sandbox's runtime cannot resize resources live (SOMA, and hosts without the vCPU controller, cannot); recreate the sandbox at the new size instead", true
	case "RESIZE_FAILED":
		return "the host could not apply the resize; try again or recreate the sandbox at the new size", true
	case "RESIZE_SOURCE_NOT_PAUSED":
		return "pause the sandbox first, then run the resize", true
	case "RECOVERY_DISK_MISSING":
		return "this sandbox has no recovery disk, so it cannot be resized by restart", true
	case "NO_CHANGE_REQUESTED":
		return "nothing was asked to change: pass --cpu and/or --memory", true
	case "INVALID_PARAM":
		return resizeInvalidParamMessage(ae), true
	case "PLAN_CAP_EXCEEDED":
		return resizeCapMessage(ae), true
	}
	return "", false
}

func resizeCapMessage(ae *api.Error) string {
	field, requested, cap, ok := resizeErrorFields(ae)
	if !ok || field == "" {
		return "that is above your plan's cap"
	}
	if cap > 0 {
		return fmt.Sprintf("that is above your plan's cap for %s (requested %g, cap %g)", field, requested, cap)
	}
	return fmt.Sprintf("that is above your plan's cap for %s", field)
}

func resizeInvalidParamMessage(ae *api.Error) string {
	if field, _, _, ok := resizeErrorFields(ae); ok && field != "" {
		return field + " is out of range"
	}
	return "the requested value is out of range"
}

// resizeErrorFields reads the field/requested/cap the server put next to the
// error code (they are siblings of "code", not under "details").
func resizeErrorFields(ae *api.Error) (field string, requested, cap float64, ok bool) {
	if ae == nil || len(ae.Body) == 0 {
		return "", 0, 0, false
	}
	var body struct {
		Error struct {
			Field     string  `json:"field"`
			Requested float64 `json:"requested"`
			Cap       float64 `json:"cap"`
		} `json:"error"`
	}
	if err := json.Unmarshal(ae.Body, &body); err != nil {
		return "", 0, 0, false
	}
	return body.Error.Field, body.Error.Requested, body.Error.Cap, true
}

func describeCPULive(caps map[string]any) string {
	if capBool(caps, "live_cpu") {
		if max, ok := capInt(caps, "live_cpu_max"); ok && max > 0 {
			if mech := capString(caps, "cpu_mechanism"); mech != "" {
				return fmt.Sprintf("yes, up to %d vCPU (%s)", max, mech)
			}
			return fmt.Sprintf("yes, up to %d vCPU", max)
		}
		return "yes"
	}
	if reason := capString(caps, "cpu_reason"); reason != "" {
		return "no - " + reason
	}
	return "no"
}

func describeMemoryLive(caps map[string]any) string {
	if capBool(caps, "live_memory") {
		if max, ok := capInt(caps, "live_memory_max_mb"); ok && max > 0 {
			return fmt.Sprintf("yes, up to %d MB", max)
		}
		return "yes"
	}
	if reason := capString(caps, "memory_reason"); reason != "" {
		return "no - " + reason + " (resize --restart recreates the sandbox at the new size)"
	}
	return "no - resize --restart recreates the sandbox at the new size"
}

func capString(caps map[string]any, key string) string {
	v, _ := caps[key].(string)
	return v
}

func capBool(caps map[string]any, key string) bool {
	v, _ := caps[key].(bool)
	return v
}

func capInt(caps map[string]any, key string) (int, bool) {
	switch v := caps[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n), true
		}
	}
	return 0, false
}
