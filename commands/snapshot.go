package commands

import (
	"archive/tar"
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// Registered from here, not from root.go, so this file stands alone.
func init() { rootCmd.AddCommand(newSnapshotCmd()) }

var snapUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type snapshotResource struct {
	Type        string `json:"type"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	WorkspaceID string `json:"workspace_id"`
}

type snapshotRow struct {
	ID            string            `json:"id"`
	Resource      *snapshotResource `json:"resource"`
	Status        string            `json:"status"`
	Comment       string            `json:"comment"`
	SizeBytes     int64             `json:"size_bytes"`
	DiskBytes     int64             `json:"disk_bytes"`
	NamedSnapshot string            `json:"named_snapshot"`
	Browsable     bool              `json:"browsable"`
	CreatedAt     string            `json:"created_at"`
	ExpiresAt     string            `json:"expires_at"`
}

type snapNamedRow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	SnapshotID string `json:"snapshot_id"`
	Source     struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"source"`
	SizeBytes int64  `json:"size_bytes"`
	Error     string `json:"error"`
	CreatedAt string `json:"created_at"`
}

type snapAllowance struct {
	Included          int `json:"included"`
	Used              int `json:"used"`
	RemainingFree     int `json:"remaining_free"`
	Billable          int `json:"billable"`
	MonthlyPriceCents int `json:"monthly_price_cents"`
	MonthlyCostCents  int `json:"monthly_cost_cents"`
}

type snapDependent struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Label string `json:"label"`
}

func newSnapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "snapshot",
		Aliases: []string{"snapshots"},
		Short:   "Browse, name, fork and delete snapshots of sandboxes and computers",
		Long: `Snapshots are point-in-time copies of a machine. Every snapshot of a machine
is its history. A named snapshot is a pinned copy you keep on purpose:
it stays after the machine is gone and is never part of history.

  miosa snapshot list my-box
  miosa snapshot save my-box web-stack
  miosa snapshot named
  miosa snapshot tree <snapshot-id> /home/user
  miosa snapshot pull <snapshot-id> /home/user/app -o ./recovered
  miosa snapshot fork --name web-stack
  miosa snapshot delete <snapshot-id> --yes

Nothing is deleted unless you ask for it by id, and a snapshot that something
still depends on is never deleted.`,
	}
	cmd.AddCommand(
		newSnapshotListCmd(),
		newSnapshotNamedCmd(),
		newSnapshotSaveCmd(),
		newSnapshotRmCmd(),
		newSnapshotLatestCmd(),
		newSnapshotInfoCmd(),
		newSnapshotTreeCmd(),
		newSnapshotPullCmd(),
		newSnapshotForkCmd(),
		newSnapshotDeleteCmd(),
	)
	return cmd
}

// ─── list / latest / info ─────────────────────────────────────────────────────

func newSnapshotListCmd() *cobra.Command {
	var workspace, search string
	var limit int
	var computer bool
	cmd := &cobra.Command{
		Use:     "list [machine]",
		Aliases: []string{"ls"},
		Short:   "List snapshot history across your machines, or for one machine",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			q := map[string]string{"workspace_id": workspace, "q": search}
			if limit > 0 {
				q["limit"] = fmt.Sprint(limit)
			}
			if len(args) == 1 {
				m, err := snapResolveMachine(c.Context(), sc, args[0], computer)
				if err != nil {
					return die(err)
				}
				q["resource_id"] = m.ID
			}
			var out struct {
				Data []snapshotRow `json:"data"`
			}
			if _, err := sc.call(c.Context(), http.MethodGet, "/snapshots"+snapQuery(q), nil, &out); err != nil {
				return die(err)
			}
			return snapPrintRows(c, out.Data)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Only machines in this workspace id")
	cmd.Flags().StringVar(&search, "search", "", "Match machine name, machine id or snapshot id")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum rows (default 50, maximum 200)")
	cmd.Flags().BoolVar(&computer, "computer", false, "Treat <machine> as a computer, not a sandbox")
	return cmd
}

func newSnapshotLatestCmd() *cobra.Command {
	var computer bool
	cmd := &cobra.Command{
		Use:   "latest <machine>",
		Short: "Show the newest snapshot of a machine",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			m, err := snapResolveMachine(c.Context(), sc, args[0], computer)
			if err != nil {
				return die(err)
			}
			var out struct {
				Data []snapshotRow `json:"data"`
			}
			if _, err := sc.call(c.Context(), http.MethodGet, "/snapshots?limit=1&resource_id="+m.ID, nil, &out); err != nil {
				return die(err)
			}
			if len(out.Data) == 0 {
				return die(fmt.Errorf("%s has no snapshots", args[0]))
			}
			if isJSON() {
				return printerFor(c).JSON(out.Data[0])
			}
			return snapPrintRows(c, out.Data[:1])
		},
	}
	cmd.Flags().BoolVar(&computer, "computer", false, "Treat <machine> as a computer, not a sandbox")
	return cmd
}

func newSnapshotInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <snapshot-id>",
		Short: "Show a snapshot and what depends on it",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data       snapshotRow     `json:"data"`
				Dependents []snapDependent `json:"dependents"`
			}
			if _, err := sc.call(c.Context(), http.MethodGet, "/snapshots/"+args[0], nil, &out); err != nil {
				return die(err)
			}
			p := printerFor(c)
			if isJSON() {
				return p.JSON(out)
			}
			s := out.Data
			p.Line("ID:        %s", s.ID)
			p.Line("Machine:   %s", snapMachineLabel(s.Resource))
			p.Line("Status:    %s", s.Status)
			p.Line("Size:      %s (disk %s)", snapHumanBytes(s.SizeBytes), snapHumanBytes(s.DiskBytes))
			p.Line("Created:   %s", s.CreatedAt)
			if s.NamedSnapshot != "" {
				p.Line("Named:     %s", s.NamedSnapshot)
			}
			p.Line("Browsable: %t", s.Browsable)
			if len(out.Dependents) == 0 {
				p.Line("Depended on by: nothing, it can be deleted")
			} else {
				p.Line("Depended on by:")
				for _, d := range out.Dependents {
					p.Line("  %s", snapDescribeDependent(d))
				}
			}
			return nil
		},
	}
}

// ─── named snapshots ──────────────────────────────────────────────────────────

func newSnapshotNamedCmd() *cobra.Command {
	var workspace string
	cmd := &cobra.Command{
		Use:     "named",
		Aliases: []string{"names"},
		Short:   "List your named snapshots and the free allowance",
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data      []snapNamedRow `json:"data"`
				Allowance snapAllowance  `json:"allowance"`
			}
			if _, err := sc.call(c.Context(), http.MethodGet, "/named-snapshots"+snapQuery(map[string]string{"workspace_id": workspace}), nil, &out); err != nil {
				return die(err)
			}
			p := printerFor(c)
			if isJSON() {
				return p.JSON(out)
			}
			if len(out.Data) == 0 {
				p.Line("No named snapshots.")
			} else {
				rows := make([][]string, 0, len(out.Data))
				for _, n := range out.Data {
					rows = append(rows, []string{n.Name, n.Status, n.Source.Name, snapHumanBytes(n.SizeBytes), n.SnapshotID, n.CreatedAt})
				}
				p.Table([]string{"NAME", "STATUS", "FROM", "SIZE", "SNAPSHOT", "CREATED"}, rows)
			}
			p.Line("%s", snapAllowanceLine(out.Allowance))
			return nil
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Only names whose source lived in this workspace id")
	return cmd
}

func newSnapshotSaveCmd() *cobra.Command {
	var fromSnapshot string
	var computer, wait bool
	cmd := &cobra.Command{
		Use:   "save <machine> <name>",
		Short: "Save a machine, or an existing snapshot, under a name",
		Long: `Pins a copy under a name that outlives the machine.

  miosa snapshot save my-box web-stack          a running machine is captured now;
                                                a stopped one pins its newest snapshot
  miosa snapshot save --snapshot <id> web-stack pin one existing snapshot

You keep 10 named snapshots free per organization. Beyond that each name costs
credit; the save is refused while there is none.`,
		Args: func(c *cobra.Command, args []string) error {
			if fromSnapshot != "" {
				return cobra.ExactArgs(1)(c, args)
			}
			return cobra.ExactArgs(2)(c, args)
		},
		RunE: func(c *cobra.Command, args []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			name := args[len(args)-1]
			body := map[string]string{"name": name}
			if fromSnapshot != "" {
				body["snapshot_id"] = fromSnapshot
			} else {
				m, err := snapResolveMachine(c.Context(), sc, args[0], computer)
				if err != nil {
					return die(err)
				}
				body[m.Type+"_id"] = m.ID
			}
			// Say what a name past the free allowance costs before saving it, so the
			// charge is never a surprise.
			var current struct {
				Allowance snapAllowance `json:"allowance"`
			}
			var charge snapAllowance
			if _, err := sc.call(c.Context(), http.MethodGet, "/named-snapshots", nil, &current); err == nil {
				charge = current.Allowance
				if charge.Included > 0 && charge.RemainingFree <= 0 && !isJSON() {
					fmt.Fprintf(c.ErrOrStderr(), "note: %s\n", snapChargeLine(charge))
				}
			}
			var out struct {
				Data snapNamedRow `json:"data"`
			}
			if _, err := sc.call(c.Context(), http.MethodPost, "/named-snapshots", body, &out); err != nil {
				if strings.Contains(err.Error(), "CREDIT_REQUIRED") || strings.Contains(err.Error(), "402") {
					price := charge.MonthlyPriceCents
					if price == 0 {
						price = 170
					}
					return die(fmt.Errorf("%w\nAll %d free named snapshots are used and each further name costs $%.2f a month: add credit with 'miosa billing' at https://miosa.ai/billing, or free a name with 'miosa snapshot rm'", err, charge.Included, float64(price)/100))
				}
				return die(err)
			}
			named := out.Data
			if wait && named.Status == "saving" {
				named, err = snapWaitNamed(c, sc, name)
				if err != nil {
					return die(err)
				}
			}
			p := printerFor(c)
			if isJSON() {
				return p.JSON(named)
			}
			p.Success("Saved %q (%s)", named.Name, named.Status)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromSnapshot, "snapshot", "", "Pin this existing snapshot id instead of a machine")
	cmd.Flags().BoolVar(&computer, "computer", false, "Treat <machine> as a computer, not a sandbox")
	cmd.Flags().BoolVar(&wait, "wait", true, "Wait until a new capture is ready")
	return cmd
}

func snapWaitNamed(c *cobra.Command, sc *snapshotAPI, name string) (snapNamedRow, error) {
	deadline := time.Now().Add(30 * time.Minute)
	for {
		var out struct {
			Data snapNamedRow `json:"data"`
		}
		if _, err := sc.call(c.Context(), http.MethodGet, "/named-snapshots/"+name, nil, &out); err != nil {
			return out.Data, err
		}
		switch out.Data.Status {
		case "ready":
			return out.Data, nil
		case "failed":
			return out.Data, fmt.Errorf("saving %q failed: %s", name, firstNonEmpty(out.Data.Error, "the capture did not complete"))
		}
		if time.Now().After(deadline) {
			return out.Data, fmt.Errorf("%q is still saving; check 'miosa snapshot named'", name)
		}
		select {
		case <-c.Context().Done():
			return out.Data, c.Context().Err()
		case <-time.After(snapPollInterval):
		}
	}
}

func newSnapshotRmCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove a named snapshot",
		Long: `Removes the name. If the name pinned an existing snapshot, that snapshot goes
back to the machine's history untouched. If the name owned a snapshot taken for
it, that snapshot is deleted unless something still depends on it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := snapConfirm(c, yes, fmt.Sprintf("Remove named snapshot %q?", args[0])); err != nil {
				return die(err)
			}
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data struct {
					Name    string `json:"name"`
					Outcome string `json:"outcome"`
				} `json:"data"`
			}
			if _, err := sc.call(c.Context(), http.MethodDelete, "/named-snapshots/"+args[0], nil, &out); err != nil {
				return die(err)
			}
			p := printerFor(c)
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Removed %q (%s)", out.Data.Name, snapOutcomeText(out.Data.Outcome))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	return cmd
}

func snapOutcomeText(outcome string) string {
	switch outcome {
	case "released":
		return "its snapshot is back in history"
	case "deleted":
		return "its snapshot was deleted"
	case "kept":
		return "its snapshot is kept in history because something still depends on it"
	}
	return outcome
}

// ─── browse and download ──────────────────────────────────────────────────────

func newSnapshotTreeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tree <snapshot-id> [path]",
		Short: "List the files in a snapshot (works while the machine is stopped)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(c *cobra.Command, args []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			dir := "/"
			if len(args) == 2 {
				dir = args[1]
			}
			resp, err := sc.pollWarming(c.Context(), 10*time.Minute, func() (*http.Response, error) {
				return sc.open(c.Context(), "/snapshots/"+args[0]+"/tree"+snapQuery(map[string]string{"path": dir}))
			})
			if err != nil {
				return die(err)
			}
			defer resp.Body.Close()
			var out struct {
				Data struct {
					Path    string `json:"path"`
					Entries []struct {
						Name string `json:"name"`
						Type string `json:"type"`
						Size int64  `json:"size"`
						Mode string `json:"mode"`
					} `json:"entries"`
				} `json:"data"`
			}
			if err := snapDecode(resp.Body, &out); err != nil {
				return die(err)
			}
			p := printerFor(c)
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Line("%s", out.Data.Path)
			rows := make([][]string, 0, len(out.Data.Entries))
			for _, e := range out.Data.Entries {
				name := e.Name
				if e.Type == "directory" {
					name += "/"
				}
				rows = append(rows, []string{e.Mode, e.Type, snapHumanBytes(e.Size), name})
			}
			p.Table([]string{"MODE", "TYPE", "SIZE", "NAME"}, rows)
			return nil
		},
	}
}

func newSnapshotPullCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "pull <snapshot-id> <path>",
		Short: "Download a file or a folder from a snapshot",
		Long: `Downloads one file, or a folder (extracted from a tar) into the output
directory. Nothing is overwritten: existing files stop the download.

  miosa snapshot pull <id> /home/user/.env -o ./recovered
  miosa snapshot pull <id> /home/user/app  -o ./recovered`,
		Args: cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			resp, err := sc.pollWarming(c.Context(), 10*time.Minute, func() (*http.Response, error) {
				return sc.open(c.Context(), "/snapshots/"+args[0]+"/files"+snapQuery(map[string]string{"path": args[1]}))
			})
			if err != nil {
				return die(err)
			}
			defer resp.Body.Close()
			if err := os.MkdirAll(out, 0o755); err != nil {
				return die(err)
			}
			isTar := strings.HasPrefix(resp.Header.Get("Content-Type"), "application/x-tar")
			var written string
			if isTar {
				n, err := snapExtractTar(resp.Body, out)
				if err != nil {
					return die(err)
				}
				written = fmt.Sprintf("%d entries into %s", n, out)
			} else {
				dest := filepath.Join(out, path.Base(args[1]))
				if err := snapWriteNew(dest, resp.Body); err != nil {
					return die(err)
				}
				written = dest
			}
			printerFor(c).Success("Downloaded %s", written)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output-dir", "d", ".", "Directory to write into")
	cmd.Flags().StringVar(&out, "to", ".", "Alias of --output-dir")
	return cmd
}

func snapWriteNew(dest string, r io.Reader) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// snapExtractTar writes regular files and folders only. Entries that would leave
// the destination, links, and anything that already exists are refused, so a
// download can never write outside the directory you chose or overwrite data.
func snapExtractTar(r io.Reader, dest string) (int, error) {
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return 0, err
	}
	tr := tar.NewReader(bufio.NewReader(r))
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return count, nil
		}
		if err != nil {
			return count, err
		}
		target := filepath.Join(absDest, filepath.FromSlash(hdr.Name))
		if target != absDest && !strings.HasPrefix(target, absDest+string(os.PathSeparator)) {
			return count, fmt.Errorf("refusing entry outside the output directory: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return count, err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return count, err
			}
			if err := snapWriteNew(target, tr); err != nil {
				return count, fmt.Errorf("%s: %w", hdr.Name, err)
			}
		default:
			// Symlinks, devices and the like are skipped on purpose.
			continue
		}
		count++
	}
}

// ─── fork / deploy ────────────────────────────────────────────────────────────

func newSnapshotForkCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "fork [snapshot-id]",
		Short: "Create a new machine from a snapshot",
		Long: `Creates a new, independent sandbox or computer (the same kind as the source)
from a snapshot. The source machine and the snapshot are untouched.

With --name the copy comes from a named snapshot, which works even after the
machine it came from is gone. The source's environment variables are not
carried over.

  miosa snapshot fork <snapshot-id>
  miosa snapshot fork --name web-stack`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			switch {
			case name != "" && len(args) == 0:
				return snapCreateFrom(c, "/named-snapshots/"+name+"/deploy")
			case name == "" && len(args) == 1:
				return snapCreateFrom(c, "/snapshots/"+args[0]+"/fork")
			}
			return usagef("give a snapshot id, or --name for a named snapshot")
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Fork the named snapshot with this name")
	return cmd
}

func snapCreateFrom(c *cobra.Command, route string) error {
	sc, err := newSnapshotAPI()
	if err != nil {
		return die(err)
	}
	var out struct {
		Data struct {
			MachineType string `json:"machine_type"`
			ID          string `json:"id"`
			Name        string `json:"name"`
			State       string `json:"state"`
			Status      string `json:"status"`
		} `json:"data"`
	}
	if _, err := sc.callKeyed(c.Context(), http.MethodPost, route, map[string]string{}, &out); err != nil {
		return die(err)
	}
	p := printerFor(c)
	if isJSON() {
		return p.JSON(out.Data)
	}
	d := out.Data
	p.Success("Creating %s %s (%s)", d.MachineType, firstNonEmpty(d.Name, d.ID), firstNonEmpty(d.State, d.Status, "provisioning"))
	return nil
}

// ─── delete ───────────────────────────────────────────────────────────────────

func newSnapshotDeleteCmd() *cobra.Command {
	var all string
	var yes, computer bool
	cmd := &cobra.Command{
		Use:   "delete <snapshot-id>...",
		Short: "Delete snapshots you name, or a machine's whole history",
		Long: `Permanently deletes snapshots. Only what you name is touched.

  miosa snapshot delete <id> <id> --yes
  miosa snapshot delete --all my-box --yes

A snapshot that a name, a fork in progress, a backup, a paused computer
or another snapshot depends on is skipped and reported, never deleted. Named
snapshots are not part of history; remove those with 'miosa snapshot rm'.`,
		Args: func(c *cobra.Command, args []string) error {
			if all != "" && len(args) > 0 {
				return fmt.Errorf("use either snapshot ids or --all, not both")
			}
			if all == "" && len(args) == 0 {
				return fmt.Errorf("give at least one snapshot id, or --all <machine>")
			}
			return nil
		},
		RunE: func(c *cobra.Command, args []string) error {
			sc, err := newSnapshotAPI()
			if err != nil {
				return die(err)
			}
			body := map[string]interface{}{}
			prompt := fmt.Sprintf("Permanently delete %d snapshot(s)?", len(args))
			if all != "" {
				m, err := snapResolveMachine(c.Context(), sc, all, computer)
				if err != nil {
					return die(err)
				}
				body["resource_id"], body["all"], body["confirm"] = m.ID, true, m.ID
				prompt = fmt.Sprintf("Permanently delete the whole snapshot history of %s?", all)
			} else {
				body["ids"] = args
			}
			if err := snapConfirm(c, yes, prompt); err != nil {
				return die(err)
			}

			var out struct {
				Data struct {
					Deleted []string `json:"deleted"`
					Blocked []struct {
						SnapshotID string          `json:"snapshot_id"`
						Dependents []snapDependent `json:"dependents"`
					} `json:"blocked"`
					NotFound []string `json:"not_found"`
				} `json:"data"`
			}
			// 409 means everything asked for was blocked; the body still says why.
			if _, err := sc.callAllowing(c.Context(), http.MethodPost, "/snapshots/delete", body, &out, http.StatusConflict); err != nil {
				return die(err)
			}
			p := printerFor(c)
			if isJSON() {
				return p.JSON(out.Data)
			}
			for _, id := range out.Data.Deleted {
				p.Success("Deleted %s", id)
			}
			for _, b := range out.Data.Blocked {
				p.Line("Skipped %s, still in use:", b.SnapshotID)
				for _, d := range b.Dependents {
					p.Line("  %s", snapDescribeDependent(d))
				}
			}
			for _, id := range out.Data.NotFound {
				p.Line("Not found: %s", id)
			}
			if len(out.Data.Deleted) == 0 && len(out.Data.Blocked) > 0 {
				return &api.Error{
					Status:  http.StatusConflict,
					Code:    "SNAPSHOT_IN_USE",
					Message: "nothing was deleted: everything requested is still in use",
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&all, "all", "", "Delete every snapshot in this machine's history")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	cmd.Flags().BoolVar(&computer, "computer", false, "Treat the --all machine as a computer, not a sandbox")
	return cmd
}

// ─── helpers ──────────────────────────────────────────────────────────────────

type snapMachineRef struct {
	Type string // "sandbox" or "computer"
	ID   string
}

// snapResolveMachine turns a name or id into a typed id. A sandbox is tried first
// unless --computer was given; the sandbox and computer endpoints accept both
// names and ids.
func snapResolveMachine(ctx context.Context, sc *snapshotAPI, ref string, computer bool) (snapMachineRef, error) {
	kinds := []string{"sandbox", "computer"}
	if computer {
		kinds = []string{"computer"}
	}
	plural := map[string]string{"sandbox": "sandboxes", "computer": "computers"}
	var lastErr error
	for _, kind := range kinds {
		var out struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
			ID string `json:"id"`
		}
		_, err := sc.call(ctx, http.MethodGet, "/"+plural[kind]+"/"+ref, nil, &out)
		if err == nil {
			id := firstNonEmpty(out.Data.ID, out.ID)
			if id != "" {
				return snapMachineRef{Type: kind, ID: id}, nil
			}
		}
		if err != nil && !api.IsStatus(err, http.StatusNotFound) {
			return snapMachineRef{}, err
		}
		lastErr = err
	}
	if snapUUIDPattern.MatchString(ref) && !computer {
		// The machine row may be gone while its snapshots remain; ids still work for history.
		return snapMachineRef{Type: "sandbox", ID: ref}, nil
	}
	// A 404 from the shared client keeps the CLI's not-found exit code.
	return snapMachineRef{}, &api.Error{
		Status:  http.StatusNotFound,
		Code:    "NOT_FOUND",
		Message: fmt.Sprintf("no sandbox or computer named %q (%v)", ref, lastErr),
	}
}

func snapDecode(r io.Reader, out interface{}) error {
	return json.NewDecoder(r).Decode(out)
}

func snapConfirm(c *cobra.Command, yes bool, prompt string) error {
	if yes {
		return nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return usagef("refusing to delete without confirmation: pass --yes")
	}
	fmt.Fprintf(c.ErrOrStderr(), "%s [y/N] ", prompt)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
		return fmt.Errorf("canceled")
	}
	return nil
}

func snapPrintRows(c *cobra.Command, rows []snapshotRow) error {
	p := printerFor(c)
	if isJSON() {
		return p.JSON(rows)
	}
	if len(rows) == 0 {
		p.Line("No snapshots.")
		return nil
	}
	table := make([][]string, 0, len(rows))
	for _, s := range rows {
		table = append(table, []string{s.ID, snapMachineLabel(s.Resource), s.Status, snapHumanBytes(s.SizeBytes), s.CreatedAt})
	}
	p.Table([]string{"ID", "MACHINE", "STATUS", "SIZE", "CREATED"}, table)
	return nil
}

func snapMachineLabel(r *snapshotResource) string {
	if r == nil {
		return "(machine removed)"
	}
	label := firstNonEmpty(r.Name, r.ID)
	if r.State != "" {
		return fmt.Sprintf("%s (%s, %s)", label, r.Type, r.State)
	}
	return fmt.Sprintf("%s (%s)", label, r.Type)
}

// snapChargeLine says what saving one more name costs once the free ones are used.
func snapChargeLine(a snapAllowance) string {
	return fmt.Sprintf("all %d free named snapshots are used; this name costs $%.2f a month and needs credit", a.Included, float64(a.MonthlyPriceCents)/100)
}

func snapAllowanceLine(a snapAllowance) string {
	line := fmt.Sprintf("%d named snapshots on your account - %d free", a.Used, a.Included)
	if a.Billable > 0 {
		line += fmt.Sprintf(", %d above the free allowance at $%.2f a month each", a.Billable, float64(a.MonthlyPriceCents)/100)
	}
	return line
}

func snapDescribeDependent(d snapDependent) string {
	switch d.Type {
	case "named_snapshot":
		return fmt.Sprintf("named snapshot %q", d.Label)
	case "child_snapshot":
		return "a snapshot built on top of it (" + d.ID + ")"
	case "computer_backup":
		return "a computer backup"
	case "suspended_computer":
		return fmt.Sprintf("computer %s, which wakes from it", firstNonEmpty(d.Label, d.ID))
	case "restore_in_progress":
		return fmt.Sprintf("a fork in progress (%s)", firstNonEmpty(d.Label, d.ID))
	case "snapshot_in_progress":
		return "its own capture, still running"
	}
	return d.Type
}

func snapHumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
