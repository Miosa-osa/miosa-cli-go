package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"
)

func newListCmd() *cobra.Command {
	var workspace string

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sandboxes",
		Long:    `List all sandboxes in your account, optionally filtered by workspace.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, workspace)
		},
	}

	cmd.Flags().StringVar(&workspace, "workspace", "", "Filter by workspace slug")
	return cmd
}

func runList(cmd *cobra.Command, workspace string) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	resp, err := c.SDK.Sandboxes.List(cmd.Context(), miosa.ListSandboxesInput{})
	if err != nil {
		return die(err)
	}

	sandboxes := resp.Data

	// Filter by workspace if requested.
	if workspace != "" {
		filtered := sandboxes[:0]
		for _, sandbox := range sandboxes {
			if ws, ok := sandbox.Metadata["workspace"]; ok && ws == workspace {
				filtered = append(filtered, sandbox)
			}
		}
		sandboxes = filtered
	}

	if isJSON() {
		return p.JSON(map[string]interface{}{
			"data": sandboxes,
			"meta": resp.Meta,
		})
	}

	if len(sandboxes) == 0 {
		p.Line("No sandboxes found.")
		if workspace != "" {
			p.Line("(filtered by workspace %q)", workspace)
		}
		p.Line("")
		p.Line("Create one with: miosa create <name>")
		return nil
	}

	current := cfg.CurrentSandbox

	headers := []string{"NAME", "ID", "STATUS", "SIZE", "TEMPLATE", "CREATED"}
	rows := make([][]string, 0, len(sandboxes))
	for _, sandbox := range sandboxes {
		name := sandbox.Name
		if name == current {
			name = name + " *"
		}
		rows = append(rows, []string{
			name,
			sandbox.ID,
			statusBadge(sandbox.State),
			string(sandbox.Size),
			sandbox.TemplateID,
			formatAge(sandbox.CreatedAt),
		})
	}

	p.Table(headers, rows)
	p.Line("")
	p.Line("Total: %d  (page %d)", resp.Meta.Total, resp.Meta.Page)
	if current != "" {
		p.Line("* = current (set with 'miosa use <name>')")
	}
	return nil
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
