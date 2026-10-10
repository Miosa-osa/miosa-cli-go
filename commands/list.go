package commands

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"
)

func newListCmd() *cobra.Command {
	var (
		workspace   string
		workspaceID string
		state       string
		template    string
		search      string
		tags        []string
		limit       int
		page        int
		all         bool
		errored     bool
		quietIDs    bool
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sandboxes",
		Long: `List sandboxes in your organization, newest first.

Filters run on the server. One page is shown by default (--limit, max 100);
--all follows every page. Errored sandboxes are hidden unless you ask for them
with --errored or --state error.

  miosa list
  miosa list --state running
  miosa list --tag env=staging --search api
  miosa list --all --json | jq -r '.data[].id'
  miosa list -q                      # ids only, one per line`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, listOptions{
				workspace: workspace, workspaceID: workspaceID, state: state, template: template, search: search,
				tags: tags, limit: limit, page: page, all: all, errored: errored, ids: quietIDs,
			})
		},
	}

	cmd.Flags().StringVar(&workspace, "workspace", "", "Filter by workspace slug")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Filter by workspace id (server side)")
	cmd.Flags().StringVar(&state, "state", "", "Only this state, for example running or paused")
	cmd.Flags().StringVar(&template, "template", "", "Only this template")
	cmd.Flags().StringVar(&search, "search", "", "Match on id, name or template")
	cmd.Flags().StringArrayVar(&tags, "tag", nil, "Only sandboxes with this tag, KEY=VALUE (repeatable)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Page size, 1-100 (default 50)")
	cmd.Flags().IntVar(&page, "page", 0, "Page number")
	cmd.Flags().BoolVar(&all, "all", false, "Follow every page")
	cmd.Flags().BoolVar(&errored, "errored", false, "Include sandboxes in the error state")
	cmd.Flags().BoolVar(&quietIDs, "ids", false, "Print only ids, one per line")
	return cmd
}

type listOptions struct {
	workspace, workspaceID, state, template, search string
	tags                                            []string
	limit, page                                     int
	all, errored, ids                               bool
}

type listPage struct {
	Data []miosa.SandboxData `json:"data"`
	Meta struct {
		Total       int            `json:"total"`
		Page        int            `json:"page"`
		Limit       int            `json:"limit"`
		TotalPages  int            `json:"total_pages"`
		HasNext     bool           `json:"has_next_page"`
		StateCounts map[string]int `json:"state_counts"`
	} `json:"meta"`
}

func (o listOptions) query(page int) url.Values {
	q := url.Values{}
	set := func(k, v string) {
		if v != "" {
			q.Set(k, v)
		}
	}
	set("state", o.state)
	set("template_id", o.template)
	set("search", o.search)
	set("workspace_id", o.workspaceID)
	if o.errored {
		q.Set("include_errored", "true")
	}
	if o.limit > 0 {
		q.Set("limit", strconv.Itoa(o.limit))
	}
	if page > 0 {
		q.Set("page", strconv.Itoa(page))
	}
	for _, t := range o.tags {
		if k, v, ok := strings.Cut(t, "="); ok && k != "" {
			q.Set("tags["+k+"]", v)
		}
	}
	return q
}

func runList(cmd *cobra.Command, o listOptions) error {
	p := printerFor(cmd)

	for _, t := range o.tags {
		if k, _, ok := strings.Cut(t, "="); !ok || k == "" {
			return usagef("--tag expects KEY=VALUE (got %q)", t)
		}
	}
	if o.limit < 0 || o.limit > 100 {
		return usagef("--limit must be between 1 and 100")
	}

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	var (
		sandboxes []miosa.SandboxData
		meta      = listPage{}.Meta
	)
	page := o.page
	for {
		var out listPage
		if err := c.API.Get(cmd.Context(), "/sandboxes", o.query(page), &out); err != nil {
			return die(err)
		}
		sandboxes = append(sandboxes, out.Data...)
		meta = out.Meta
		if !o.all || !out.Meta.HasNext || len(out.Data) == 0 {
			break
		}
		if page == 0 {
			page = 1
		}
		page++
	}

	// Legacy client-side filter on the workspace slug stored in metadata.
	if o.workspace != "" {
		filtered := sandboxes[:0]
		for _, sandbox := range sandboxes {
			if ws, ok := sandbox.Metadata["workspace"].(string); ok && ws == o.workspace {
				filtered = append(filtered, sandbox)
			}
		}
		sandboxes = filtered
	}

	if o.ids {
		for _, s := range sandboxes {
			fmt.Fprintln(p.Writer(), s.ID)
		}
		return nil
	}
	if isJSON() {
		return p.JSON(map[string]interface{}{
			"data": sandboxes,
			"meta": meta,
		})
	}

	if len(sandboxes) == 0 {
		p.Line("No sandboxes found.")
		if o.workspace != "" {
			p.Line("(filtered by workspace %q)", o.workspace)
		}
		p.Line("")
		p.Line("Create one with: miosa create <name>")
		return nil
	}

	current := cfg.CurrentSandbox

	showSetup := false
	for _, sandbox := range sandboxes {
		if sandbox.SetupStatus != "" {
			showSetup = true
		}
	}
	headers := []string{"NAME", "ID", "STATUS", "SIZE", "TEMPLATE", "ENV"}
	if showSetup {
		headers = append(headers, "SETUP")
	}
	headers = append(headers, "CREATED")
	rows := make([][]string, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		name := sandbox.Name
		if current != "" && (current == sandbox.ID || (name != "" && current == name)) {
			name = strings.TrimSpace(name + " *")
		}
		row := []string{
			name,
			sandbox.ID,
			statusBadge(sandbox.State),
			string(sandbox.Size),
			sandbox.TemplateID,
			envBadge(sandbox.MachineEnvironment),
		}
		if showSetup {
			row = append(row, orDefault(sandbox.SetupStatus, "-"))
		}
		rows = append(rows, append(row, formatAge(sandbox.CreatedAt)))
	}

	p.Table(headers, rows)
	p.Line("")
	total := meta.Total
	if total == 0 {
		total = len(sandboxes)
	}
	if meta.HasNext && !o.all {
		p.Line("Showing %d of %d - use --all or --page %d for more", len(sandboxes), total, meta.Page+1)
	} else {
		p.Line("Total: %d", total)
	}
	if current != "" {
		p.Line("* = current (set with 'miosa use <name>')")
	}
	return nil
}

// envBadge is the compact ENV cell: "name vN", with "(upgrade)" when the
// machine is behind its environment's latest version.
func envBadge(m miosa.MachineEnvironment) string {
	if m.Environment == "" {
		return "-"
	}
	s := m.Environment
	if m.EnvironmentVersion > 0 {
		s += fmt.Sprintf(" v%d", m.EnvironmentVersion)
	}
	if m.EnvironmentUpgradeAvailable {
		s += " (upgrade)"
	}
	return s
}

func statusBadge(s miosa.ComputerStatus) string {
	switch s {
	case miosa.StatusRunning:
		return "running"
	case miosa.StatusStopped:
		return "stopped"
	case miosa.StatusCreating, miosa.StatusStarting:
		return "starting"
	case miosa.StatusStopping:
		return "stopping"
	case miosa.StatusError:
		return "error"
	case miosa.StatusDestroyed:
		return "destroyed"
	default:
		return string(s)
	}
}

func formatAge(createdAt string) string {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		// Try without nanoseconds.
		t, err = time.Parse("2006-01-02T15:04:05", createdAt)
		if err != nil {
			return createdAt
		}
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// resolveComputer resolves a name-or-ID argument to a computer ID.
// If nameOrID is empty, it falls back to cfg.CurrentSandbox.
func resolveComputer(cmd interface {
	Context() interface{ Done() <-chan struct{} }
}, c interface {
	GetByName(name string) string
}, nameOrID, current string) string {
	if nameOrID != "" {
		return nameOrID
	}
	return current
}

// lookupComputerID resolves name-or-ID to an ID, fetching the list if needed.
// If nameOrID looks like a UUID (contains dashes, 36 chars), return as-is.
func lookupComputerID(nameOrID string) string {
	if len(nameOrID) == 36 && strings.Count(nameOrID, "-") == 4 {
		return nameOrID
	}
	// Return as-is; the API accepts both name and ID slugs.
	return nameOrID
}
