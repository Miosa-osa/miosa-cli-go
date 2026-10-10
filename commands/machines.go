package commands

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(
		sandboxVerb(Op{
			Use: "pause [sandbox]", Short: "Pause a sandbox (it keeps its disk and memory state)",
			Long: `Pause a running sandbox. Compute billing stops; storage keeps billing at the paused rate.
Resume it with 'miosa resume'. Memory is checkpointed, so processes continue where they were.`,
			Method: "POST", Path: "/sandboxes/{0}/pause", Detail: stateRows, Done: "Paused {0}",
		}),
		sandboxVerb(Op{
			Use: "resume [sandbox]", Short: "Resume a paused sandbox",
			Method: "POST", Path: "/sandboxes/{0}/resume", Detail: stateRows, Done: "Resumed {0}",
		}),
		sandboxVerb(Op{
			Use: "stop [sandbox]", Short: "Stop a sandbox, keeping its disk",
			Long:   "Stop a sandbox. The disk persists and the sandbox can be resumed; use 'destroy' to delete it.",
			Method: "POST", Path: "/sandboxes/{0}/stop", Detail: stateRows, Done: "Stopped {0}",
		}),
		sandboxVerb(Op{
			Use: "recover [sandbox]", Short: "Recover a sandbox from its disk into a NEW sandbox",
			Long: `Recover a failed or errored sandbox: MIOSA provisions a new sandbox from the preserved
disk and returns its id. The original keeps existing until you destroy it.`,
			Method: "POST", Path: "/sandboxes/{0}/recover",
			Detail: []Col{{Head: "New sandbox", Path: "id"}, {Head: "State", Path: "state"}, {Head: "Restored from", Path: "restored_from"}},
		}),
		sandboxVerb(Op{
			Use: "fork [sandbox]", Short: "Fork a running sandbox into a copy",
			Method: "POST", Path: "/sandboxes/{0}/fork",
			Flags: []Flag{
				{Name: "ttl", Usage: "Lifetime of the fork: seconds, 90m, 2h or 2d", Field: "timeout_sec"},
				{Name: "template", Usage: "Template for the fork", Field: "template_id"},
			},
			Body:   durationBody("timeout_sec"),
			Detail: []Col{{Head: "Fork", Path: "id"}, {Head: "State", Path: "state"}, {Head: "Name", Path: "name"}},
		}),
		sandboxVerb(Op{
			Use: "extend [sandbox]", Short: "Replace a sandbox's time to live",
			Long: `Set how long the sandbox may keep running from now. The new value replaces the old
one, it does not add to it. See the time left with 'miosa usage'.

  miosa extend my-box --for 2h
  miosa extend --for 1d`,
			Method: "POST", Path: "/sandboxes/{0}/extend",
			Flags:  []Flag{{Name: "for", Usage: "New time to live: seconds, 90m, 2h or 2d", Field: "timeout_sec", Required: true}},
			Body:   durationBody("timeout_sec"),
			Detail: []Col{{Head: "State", Path: "state"}, {Head: "Time to live (s)", Path: "timeout_sec"}, {Head: "Remaining (ms)", Path: "timeout_remaining_ms"}},
		}),
		sandboxVerb(Op{
			Use: "usage [sandbox]", Short: "Show runtime, time left and estimated cost",
			Method: "GET", Path: "/sandboxes/{0}/usage",
			Detail: []Col{
				{Head: "State", Path: "state"}, {Head: "Runtime (s)", Path: "runtime_sec"}, {Head: "Time to live (s)", Path: "timeout_sec"},
				{Head: "Remaining (ms)", Path: "timeout_remaining_ms"}, {Head: "Rate (cents/h)", Path: "billing_rate_cents_per_hour"},
				{Head: "Paused rate (cents/h)", Path: "paused_rate_cents_per_hour"}, {Head: "Estimated cost (cents)", Path: "estimated_cost_cents"},
			},
		}),
		sandboxVerb(Op{
			Use: "ports [sandbox]", Short: "List the ports listening inside a sandbox",
			Method: "GET", Path: "/sandboxes/{0}/ports",
			Cols: []Col{{Head: "PORT", Path: "port"}, {Head: "PROTO", Path: "protocol"}, {Head: "ADDRESS", Path: "address"}, {Head: "PROCESS", Path: "process.name"}, {Head: "PID", Path: "process.pid"}},
		}),
		newWaitCmd(),
		newLogsCmd(),
		newEventsCmd(),
		newTagCmd(),
	)
}

// stateRows is the detail view of lifecycle responses ({id, state}).
var stateRows = []Col{{Head: "Sandbox", Path: "id"}, {Head: "State", Path: "state"}}

// sandboxVerb marks the first argument as an optional sandbox that defaults
// to the current one.
func sandboxVerb(o Op) *cobra.Command {
	o.Args = append([]Arg{{Name: "sandbox", Optional: true, Current: true, Complete: "sandbox"}}, o.Args...)
	return o.command("")
}

// durationBody turns the duration flag stored under field into seconds.
func durationBody(field string) func([]string, map[string]any) (map[string]any, error) {
	return func(_ []string, body map[string]any) (map[string]any, error) {
		if v, ok := body[field].(string); ok {
			n, err := parseSeconds(v)
			if err != nil {
				return nil, err
			}
			body[field] = n
		}
		return body, nil
	}
}

// ─── wait ─────────────────────────────────────────────────────────────────────

func newWaitCmd() *cobra.Command {
	var (
		until   string
		timeout time.Duration
	)
	cmd := &cobra.Command{
		Use:   "wait [sandbox]",
		Short: "Wait until a sandbox reaches a state",
		Long: `Block until the sandbox is ready for commands (default), running, paused or destroyed.
Exits 0 when reached, non-zero on timeout or if the sandbox lands in the error state.

  miosa create my-box && miosa wait my-box
  miosa wait my-box --for paused --timeout 5m`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			st, err := waitSandbox(cmd.Context(), c.API, id, until, timeout, func(state string) {
				if !isJSON() && !globalFlags.Quiet {
					fmt.Fprintf(cmd.ErrOrStderr(), "waiting for %s: %s\n", until, state)
				}
			})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"id": id, "state": st.State, "ready": st.Ready})
			}
			p.Success("%s is %s", ref, until)
			return nil
		},
	}
	cmd.Flags().StringVar(&until, "for", "ready", "State to wait for: ready, running, paused, destroyed")
	cmd.Flags().DurationVar(&timeout, "timeout", 3*time.Minute, "Give up after this long")
	return cmd
}

type sandboxState struct {
	State string `json:"state"`
	Ready bool   `json:"ready"`
}

// pollEvery is the wait interval; tests shorten it.
var pollEvery = time.Second

// waitSandbox polls GET /sandboxes/:id until it reaches target. target is one
// of ready (running and accepting commands), running, paused, destroyed.
func waitSandbox(ctx context.Context, c *api.Client, id, target string, timeout time.Duration, progress func(string)) (sandboxState, error) {
	switch target {
	case "ready", "running", "paused", "destroyed":
	default:
		return sandboxState{}, usageErr{fmt.Errorf("--for must be ready, running, paused or destroyed (got %q)", target)}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last sandboxState
	for {
		var s sandboxState
		err := c.Get(ctx, "/sandboxes/"+id, nil, &s)
		switch {
		case err == nil:
			last = s
		case api.IsStatus(err, 404) && target == "destroyed":
			return sandboxState{State: "destroyed"}, nil
		case ctx.Err() != nil:
			return last, fmt.Errorf("timed out after %s waiting for the sandbox to be %s (last state: %s)", timeout, target, orDash(last.State))
		default:
			return last, err
		}
		switch {
		case s.State == "error" && target != "destroyed":
			return s, fmt.Errorf("the sandbox entered the error state; see 'miosa info' and 'miosa recover'")
		case target == "ready" && s.State == "running" && s.Ready:
			return s, nil
		case target == s.State && target != "ready":
			return s, nil
		}
		if progress != nil {
			progress(s.State)
		}
		select {
		case <-ctx.Done():
			return last, fmt.Errorf("timed out after %s waiting for the sandbox to be %s (last state: %s)", timeout, target, orDash(last.State))
		case <-time.After(pollEvery):
		}
	}
}

// ─── logs and events ──────────────────────────────────────────────────────────

func newLogsCmd() *cobra.Command {
	var (
		lines  int
		follow bool
	)
	cmd := &cobra.Command{
		Use:   "logs [sandbox]",
		Short: "Show a sandbox's system log",
		Long: `Print the last lines of the sandbox's boot and system log; --follow keeps streaming.
For a background process use 'miosa process logs'. With --json each line is an object.

  miosa logs my-box --lines 200
  miosa logs my-box -f`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			q := url.Values{}
			if lines > 0 {
				q.Set("lines", fmt.Sprint(lines))
			}
			var out struct {
				Logs []struct {
					Line   string `json:"line"`
					Stream string `json:"stream"`
					T      string `json:"t"`
				} `json:"logs"`
			}
			if err := c.API.Get(cmd.Context(), "/sandboxes/"+id+"/logs", q, &out); err != nil {
				return die(err)
			}
			for _, l := range out.Logs {
				if isJSON() {
					_ = p.JSONLine(map[string]string{"t": l.T, "stream": l.Stream, "line": l.Line})
				} else {
					fmt.Fprintln(p.Writer(), l.Line)
				}
			}
			if !follow {
				return nil
			}
			return followStream(cmd, c.API, "/sandboxes/"+id+"/logs/stream", nil, func(ev api.Event) error {
				if isJSON() {
					return p.JSONLine(map[string]string{"event": ev.Name, "data": ev.Data})
				}
				fmt.Fprintln(p.Writer(), logLineOf(ev.Data))
				return nil
			})
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "Number of recent lines")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep streaming new lines")
	return cmd
}

// logLineOf extracts the text of a log stream event: a JSON object with a
// "line" field, or the raw data.
func logLineOf(data string) string {
	var v struct {
		Line string `json:"line"`
	}
	if strings.HasPrefix(strings.TrimSpace(data), "{") && jsonUnmarshal(data, &v) == nil && v.Line != "" {
		return v.Line
	}
	return data
}

func newEventsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "events [sandbox]",
		Short: "Stream a sandbox's lifecycle events",
		Long: `Stream events (state changes, readiness, previews) as they happen. Ctrl-C stops.
With --json each event is one JSON object per line.

  miosa events my-box
  miosa events my-box --json | jq -c 'select(.event == "state")'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			return followStream(cmd, c.API, "/sandboxes/"+id+"/events", nil, func(ev api.Event) error {
				if isJSON() {
					return p.JSONLine(map[string]any{"event": ev.Name, "id": ev.ID, "data": rawOrString(ev.Data)})
				}
				name := ev.Name
				if name == "" {
					name = "message"
				}
				fmt.Fprintf(p.Writer(), "%s  %s\n", name, ev.Data)
				return nil
			})
		},
	}
	return cmd
}

// ─── tag ──────────────────────────────────────────────────────────────────────

func newTagCmd() *cobra.Command {
	var (
		set    []string
		remove []string
		clear  bool
	)
	cmd := &cobra.Command{
		Use:   "tag [sandbox]",
		Short: "Show, add or remove sandbox tags",
		Long: `Tags are key/value labels you can filter on ('miosa list --tag k=v').
Without flags the current tags are printed. Changes are merged into the existing tags.

  miosa tag my-box --set env=staging --set owner=ana
  miosa tag my-box --remove owner
  miosa tag my-box --clear`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
			var cur struct {
				Tags map[string]any `json:"tags"`
			}
			if err := c.API.Get(cmd.Context(), "/sandboxes/"+id, nil, &cur); err != nil {
				return die(err)
			}
			tags := map[string]any{}
			if !clear {
				for k, v := range cur.Tags {
					tags[k] = v
				}
			}
			for _, kv := range set {
				k, v, ok := strings.Cut(kv, "=")
				if !ok || k == "" {
					return usagef("--set expects KEY=VALUE (got %q)", kv)
				}
				tags[k] = v
			}
			for _, k := range remove {
				delete(tags, k)
			}
			if len(set) > 0 || len(remove) > 0 || clear {
				if err := c.API.Patch(cmd.Context(), "/sandboxes/"+id+"/tags", map[string]any{"tags": tags}, nil); err != nil {
					return die(err)
				}
			}
			if isJSON() {
				return p.JSON(map[string]any{"id": id, "tags": tags})
			}
			if len(tags) == 0 {
				p.Line("No tags.")
				return nil
			}
			rows := make([][]string, 0, len(tags))
			for k, v := range tags {
				rows = append(rows, []string{k, fmt.Sprint(v)})
			}
			sortRows(rows)
			p.Table([]string{"KEY", "VALUE"}, rows)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&set, "set", nil, "Add or change a tag, KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&remove, "remove", nil, "Remove a tag by key (repeatable)")
	cmd.Flags().BoolVar(&clear, "clear", false, "Remove every tag first")
	return cmd
}
