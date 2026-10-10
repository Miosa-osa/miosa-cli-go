package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() { rootCmd.AddCommand(batchCmd()) }

var batchDetail = []Col{
	{Head: "Id", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Requested", Path: "requested"}, {Head: "Pending", Path: "pending"},
	{Head: "Dispatched", Path: "dispatched"}, {Head: "Live", Path: "live_count"}, {Head: "Failed", Path: "failed_total"},
	{Head: "Canceled", Path: "cancelled"}, {Head: "Concurrency", Path: "concurrency_limit"}, {Head: "Template", Path: "template_id"},
	{Head: "Settled", Path: "settled?"},
}

// batchCmd creates many sandboxes in one request and watches them come up.
func batchCmd() *cobra.Command {
	cmd := group("batch", "Create many sandboxes in one request",
		`A batch asks for N identical sandboxes and creates them with bounded
concurrency. The call returns at once with a batch id; 'status' and 'watch'
show how many are up, and 'items' lists the sandboxes. Quota, credit and
placement apply to every sandbox as for a single 'miosa create'.

  miosa batch new --count 50 --size small --name-prefix ci --wait
  miosa batch status <batch>
  miosa batch items <batch>
  miosa batch cancel <batch>`, []string{"batches"},
		Op{Use: "status <batch>", Aliases: []string{"get"}, Short: "Show how a batch is going", Method: "GET", Path: "/sandboxes/batches/{0}", Args: []Arg{{Name: "batch"}}, Detail: batchDetail},
		Op{
			Use: "items <batch>", Short: "List the sandboxes of a batch", Method: "GET", Path: "/sandboxes/batches/{0}/items", Args: []Arg{{Name: "batch"}},
			Flags: []Flag{{Name: "limit", Type: "int", Usage: "How many to show"}},
			Cols:  []Col{{Head: "#", Path: "batch_item_index"}, {Head: "ID", Path: "id"}, {Head: "NAME", Path: "name"}, {Head: "STATE", Path: "state"}, {Head: "READY", Path: "ready"}},
		},
		Op{
			Use: "cancel <batch>", Short: "Stop creating the sandboxes still pending", Method: "POST", Path: "/sandboxes/batches/{0}/cancel",
			Args: []Arg{{Name: "batch"}}, Detail: batchDetail,
			Long: "Sandboxes already created are left alone; destroy them with 'miosa destroy'. A batch that has finished cannot be canceled.",
		},
	)
	cmd.AddCommand(newBatchNewCmd(), newBatchWatchCmd())
	return cmd
}

func newBatchNewCmd() *cobra.Command {
	var (
		count, concurrency int
		template, size     string
		prefix, region     string
		ttl, idle          string
		workspace          string
		tags               []string
		wait               bool
		waitTimeout        time.Duration
	)
	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create a batch of identical sandboxes",
		Long: `Creates --count sandboxes (1 or more; the server states the maximum for your
plan) with at most --concurrency creating at once. Prints the batch id.
With --wait the command blocks until the batch has settled and exits non-zero
if any sandbox failed.

  miosa batch new --count 20 --template nextjs --name-prefix demo --ttl 2h --wait`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if count < 1 {
				return usagef("--count must be at least 1")
			}
			body := map[string]any{"count": count}
			if concurrency > 0 {
				body["concurrency"] = concurrency
			}
			if template != "" {
				body["template_id"] = template
			}
			if size != "" {
				sz, err := parseSandboxSize(size)
				if err != nil {
					return usagef("%v", err)
				}
				body["size"] = string(sz)
			}
			if prefix != "" {
				body["name_prefix"] = prefix
			}
			if region != "" {
				body["region"] = region
			}
			if workspace != "" {
				body["workspace_slug"] = workspace
			}
			if ttl != "" {
				n, err := parseSeconds(ttl)
				if err != nil {
					return usagef("--ttl: %v", err)
				}
				body["timeout_sec"] = n
			}
			if idle != "" {
				n, err := parseSeconds(idle)
				if err != nil {
					return usagef("--idle-timeout: %v", err)
				}
				body["idle_timeout_sec"] = n
			}
			if len(tags) > 0 {
				m := map[string]string{}
				for _, kv := range tags {
					k, v, ok := strings.Cut(kv, "=")
					if !ok || k == "" {
						return usagef("--tag expects KEY=VALUE (got %q)", kv)
					}
					m[k] = v
				}
				body["tags"] = m
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			resp, err := c.API.JSONAny(cmd.Context(), reqPost("/sandboxes/batch", body))
			if err != nil {
				return die(err)
			}
			obj, _ := unwrapObject(resp).(map[string]any)
			id := fmt.Sprint(obj["id"])
			p := printerFor(cmd)
			if !wait {
				if isJSON() {
					return p.JSON(resp)
				}
				p.Success("Started batch %s for %d sandboxes", id, count)
				p.Line("Follow it with: miosa batch watch %s", id)
				return nil
			}
			final, err := watchBatch(cmd.Context(), c.API, id, waitTimeout, func(s map[string]any) {
				if !isJSON() {
					p.Line("%s", batchProgressLine(s))
				}
			})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(final)
			}
			return batchOutcome(final)
		},
	}
	f := cmd.Flags()
	f.IntVar(&count, "count", 0, "How many sandboxes to create")
	f.IntVar(&concurrency, "concurrency", 0, "How many to create at once (default: the server's)")
	f.StringVar(&template, "template", "", "Sandbox template (default miosa-sandbox)")
	f.StringVar(&size, "size", "", "Sandbox size, as for 'miosa create'")
	f.StringVar(&prefix, "name-prefix", "", "Prefix for the sandbox names, numbered from 1")
	f.StringVar(&region, "region", "", "Region")
	f.StringVar(&workspace, "workspace", "", "Workspace slug")
	f.StringVar(&ttl, "ttl", "", "Time to live of each sandbox, for example 2h")
	f.StringVar(&idle, "idle-timeout", "", "Idle timeout of each sandbox, for example 15m")
	f.StringArrayVar(&tags, "tag", nil, "Tag KEY=VALUE for every sandbox (repeatable)")
	f.BoolVar(&wait, "wait", false, "Wait until the batch has settled")
	f.DurationVar(&waitTimeout, "wait-timeout", 15*time.Minute, "How long --wait may take")
	_ = cmd.MarkFlagRequired("count")
	return cmd
}

func newBatchWatchCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "watch <batch>",
		Short: "Follow a batch until it settles",
		Long: `Prints a progress line each time the batch changes and exits when it has
settled: 0 when every sandbox came up, non-zero when any failed or the batch was
canceled.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)
			final, err := watchBatch(cmd.Context(), c.API, args[0], timeout, func(s map[string]any) {
				if !isJSON() {
					p.Line("%s", batchProgressLine(s))
				}
			})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(final)
			}
			return batchOutcome(final)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "Give up after this long")
	return cmd
}

func batchProgressLine(s map[string]any) string {
	return fmt.Sprintf("%s: %s of %s up, %s pending, %s failed",
		formatValue(s["status"], ""), formatValue(s["live_count"], ""), formatValue(s["requested"], ""),
		formatValue(s["pending"], ""), formatValue(s["failed_total"], ""))
}

// batchOutcome turns a settled batch into success or an error.
func batchOutcome(s map[string]any) error {
	failed, _ := toFloat(s["failed_total"])
	canceled, _ := toFloat(s["cancelled"])
	switch {
	case failed > 0:
		return fmt.Errorf("could not create %d sandbox(es); see 'miosa batch status %s'", int(failed), formatValue(s["id"], ""))
	case canceled > 0:
		return fmt.Errorf("the batch was canceled with %d sandbox(es) not created", int(canceled))
	}
	return nil
}

// watchBatch polls a batch until it settles, calling progress when the counts
// change.
func watchBatch(ctx context.Context, c *api.Client, id string, timeout time.Duration, progress func(map[string]any)) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last string
	for {
		var out struct {
			Data map[string]any `json:"data"`
		}
		if err := c.Get(ctx, "/sandboxes/batches/"+id, nil, &out); err != nil {
			if ctx.Err() != nil {
				return out.Data, fmt.Errorf("timed out after %s waiting for the batch to settle", timeout)
			}
			return nil, err
		}
		if line := batchProgressLine(out.Data); line != last {
			last = line
			progress(out.Data)
		}
		if settled, _ := out.Data["settled?"].(bool); settled {
			return out.Data, nil
		}
		select {
		case <-ctx.Done():
			return out.Data, fmt.Errorf("timed out after %s waiting for the batch to settle", timeout)
		case <-time.After(pollEvery):
		}
	}
}
