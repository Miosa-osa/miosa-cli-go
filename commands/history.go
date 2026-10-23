package commands

import (
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() { rootCmd.AddCommand(newHistoryCmd()) }

// historyPollEvery is how often `history -f` looks for new commands; tests
// shorten it.
var historyPollEvery = 2 * time.Second

type historyRecord struct {
	ID         string  `json:"id"`
	Command    string  `json:"command"`
	Cwd        *string `json:"cwd"`
	Mode       string  `json:"mode"`
	Status     string  `json:"status"`
	ExitCode   *int    `json:"exit_code"`
	ErrorCode  *string `json:"error_code"`
	StartedAt  string  `json:"started_at"`
	DurationMS *int64  `json:"duration_ms"`
	OutputCut  bool    `json:"output_cut"`
	Stdout     string  `json:"stdout"`
	Stderr     string  `json:"stderr"`
}

func newHistoryCmd() *cobra.Command {
	var limit int
	var mode string
	var stderr, follow bool
	cmd := &cobra.Command{
		Use:   "history [sandbox] [command-id]",
		Short: "List the commands run in a sandbox, or print one's stored output",
		Long: `Lists the commands run in a sandbox through exec (and run), newest first: when,
how (exec or stream), the exit code and how long it took. Give a
command id to print its stored output: standard output, or with --stderr standard
error. The last 64 KiB of each is kept for 30 days, so this works while the
sandbox is paused or stopped. -f keeps listing new commands as they finish.

  miosa history my-box
  miosa history my-box --mode stream --limit 5
  miosa history my-box 3f0c...           # its output
  miosa history my-box 3f0c... --stderr
  miosa history my-box -f`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			ref, recordID := "", ""
			switch len(args) {
			case 1:
				ref = args[0]
			case 2:
				ref, recordID = args[0], args[1]
			}
			ref, err = requireSandbox(ref, cfg.CurrentSandbox)
			if err != nil {
				return usagef("%v", err)
			}
			id, err := c.API.ResolveSandbox(cmd.Context(), ref)
			if err != nil {
				return die(err)
			}
			base := "/sandboxes/" + url.PathEscape(id) + "/history"
			if recordID != "" {
				return printHistoryRecord(cmd, c.API, base+"/"+url.PathEscape(recordID), stderr)
			}
			return listHistory(cmd, c.API, base, limit, mode, follow)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "How many commands to list (1-200)")
	cmd.Flags().StringVar(&mode, "mode", "", "Only commands that ran this way: exec or stream")
	cmd.Flags().BoolVar(&stderr, "stderr", false, "With a command id: print its standard error instead")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Keep listing new commands until Ctrl-C")
	return cmd
}

func fetchHistory(cmd *cobra.Command, c *api.Client, base string, limit int, mode string) ([]historyRecord, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if mode != "" {
		q.Set("mode", mode)
	}
	var out struct {
		Data []historyRecord `json:"data"`
	}
	err := c.Get(cmd.Context(), base, q, &out)
	return out.Data, err
}

func listHistory(cmd *cobra.Command, c *api.Client, base string, limit int, mode string, follow bool) error {
	p := printerFor(cmd)
	records, err := fetchHistory(cmd, c, base, limit, mode)
	if err != nil {
		return die(err)
	}
	if !follow {
		if isJSON() {
			return p.JSON(records)
		}
		if len(records) == 0 {
			p.Line("No commands recorded yet.")
			return nil
		}
		printHistoryRows(p, records, true)
		return nil
	}

	// Follow: the existing ones oldest first, then each new one as it lands.
	seen := map[string]bool{}
	show := func(rs []historyRecord) error {
		var fresh []historyRecord
		for i := len(rs) - 1; i >= 0; i-- {
			if !seen[rs[i].ID] {
				seen[rs[i].ID] = true
				fresh = append(fresh, rs[i])
			}
		}
		if isJSON() {
			for _, r := range fresh {
				if err := p.JSONLine(r); err != nil {
					return err
				}
			}
			return nil
		}
		printHistoryRows(p, fresh, false)
		return nil
	}
	if err := show(records); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(historyPollEvery):
		}
		rs, err := fetchHistory(cmd, c, base, 50, mode)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return die(err)
		}
		if err := show(rs); err != nil {
			return err
		}
	}
}

func printHistoryRows(p interface {
	Table(head []string, rows [][]string)
}, rs []historyRecord, header bool) {
	rows := make([][]string, 0, len(rs))
	for _, r := range rs {
		exit := "-"
		if r.ExitCode != nil {
			exit = strconv.Itoa(*r.ExitCode)
		} else if r.ErrorCode != nil {
			exit = *r.ErrorCode
		}
		took := "-"
		if r.DurationMS != nil {
			took = (time.Duration(*r.DurationMS) * time.Millisecond).Round(100 * time.Millisecond).String()
		}
		rows = append(rows, []string{r.ID, formatAge(r.StartedAt), r.Mode, r.Status, exit, took, clipText(r.Command, 60)})
	}
	if header || len(rows) > 0 {
		p.Table([]string{"ID", "WHEN", "MODE", "STATUS", "EXIT", "TOOK", "COMMAND"}, rows)
	}
}

func printHistoryRecord(cmd *cobra.Command, c *api.Client, path string, stderr bool) error {
	var out struct {
		Data historyRecord `json:"data"`
	}
	if err := c.Get(cmd.Context(), path, nil, &out); err != nil {
		return die(err)
	}
	r := out.Data
	if isJSON() {
		return printerFor(cmd).JSON(r)
	}
	text := r.Stdout
	if stderr {
		text = r.Stderr
	}
	fmt.Fprint(cmd.OutOrStdout(), text)
	if r.OutputCut {
		fmt.Fprintln(cmd.ErrOrStderr(), "miosa: only the last 64 KiB of the output was kept")
	}
	return nil
}
