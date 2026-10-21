package commands

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// errProductionInWorkspace refuses a retire that would delete production.
var errProductionInWorkspace = errors.New("workspace holds production resources")

func newWorkspaceRetireCmd() *cobra.Command {
	var yes, dryRun, includeProduction bool

	cmd := &cobra.Command{
		Use:   "retire <workspace>",
		Short: "Empty a workspace, then delete it (asks you to type its name)",
		Long: `Retire a workspace in the documented order: list what it holds, delete those
resources in dependency order, then delete the workspace itself.

  miosa workspace retire acme --dry-run   # show what would go, change nothing
  miosa workspace retire acme             # asks you to type the name
  miosa workspace retire acme --yes       # no prompt (scripts, CI)

On a terminal it asks you to type the workspace name before anything is
deleted. Off a terminal it refuses unless --yes is given. <workspace> is its
name, slug or id.`,
		Example: "  miosa workspace retire acme --dry-run\n  miosa workspace retire acme --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceRetire(cmd, args[0], yes, dryRun, includeProduction)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be deleted, change nothing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Retire without the typed confirmation")
	cmd.Flags().BoolVar(&includeProduction, "include-production", false, "Retire even when the workspace holds production resources")
	return cmd
}

func runWorkspaceRetire(cmd *cobra.Command, ref string, yes, dryRun, includeProduction bool) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	var ws struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/workspaces/"+url.PathEscape(ref), nil, &ws); err != nil {
		return die(fmt.Errorf("workspace %q: %w", ref, err))
	}
	id, _ := ws.Data["id"].(string)
	name, _ := ws.Data["name"].(string)
	slug, _ := ws.Data["slug"].(string)
	label := firstNonEmpty(name, slug, ref)
	target := firstNonEmpty(id, ref)

	// What is inside, before anything is touched. The same listing the console
	// shows, so the answer to "what am I about to lose" comes first.
	var inv struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/workspaces/"+url.PathEscape(target)+"/inventory", nil, &inv); err != nil {
		return die(fmt.Errorf("reading what %q holds: %w", label, err))
	}
	counts, _ := inv.Data["counts"].(map[string]any)

	// Production is never retired by accident. A resource tagged
	// purpose=production, or a deployment that is running and therefore
	// serving traffic, stops the command unless --include-production is given.
	// A dry run still shows the list, because knowing what would block is the
	// reason to run it.
	blockers := productionBlockers(inv.Data)

	if len(blockers) > 0 && !dryRun && !includeProduction {
		if isJSON() {
			_ = p.JSON(map[string]any{
				"status":              "refused",
				"id":                  target,
				"production_blockers": blockers,
			})
		} else {
			p.Line("Refusing to retire %q: it holds production resources.", label)
			printBlockers(p, blockers)
			p.Line("")
			p.Line("Re-run with --include-production to retire anyway.")
		}
		return errProductionInWorkspace
	}

	if !dryRun && !yes {
		if err := confirmByTypedName(cmd, fmt.Sprintf("Retire workspace %q (%s)? This deletes everything in it.", label, shortRef(target)), label, slug); err != nil {
			return err
		}
	}

	// Cleanup deletes the resources in the engine's documented dependency
	// order. dry_run returns the same match without deleting.
	var cleanup any
	cleanup, err = c.API.JSONAny(cmd.Context(), api.Request{
		Method: "POST",
		Path:   "/workspaces/" + url.PathEscape(target) + "/cleanup",
		Body:   map[string]any{"force": true, "dry_run": dryRun},
	})
	if err != nil {
		return die(fmt.Errorf("cleaning up %q: %w", label, err))
	}

	deleted, _ := responseField(cleanup, "deleted").([]any)
	matched := responseField(cleanup, "resources")

	if dryRun {
		if isJSON() {
			return p.JSON(map[string]any{
				"status":       "dry_run",
				"id":           target,
				"counts":       counts,
				"would_delete": countResources(matched),
			})
		}
		p.Line("Workspace  %s (%s)", label, shortRef(target))
		printWorkspaceContents(p, counts)
		if len(blockers) > 0 {
			p.Line("")
			p.Line("Production would block this without --include-production:")
			printBlockers(p, blockers)
			p.Line("")
		}
		p.Line("Would delete, in order:")
		printDeletionOrder(p, cleanup)
		return nil
	}

	var released map[string]any
	deletedWorkspace, err := c.API.JSONAny(cmd.Context(), api.Request{
		Method: "DELETE",
		Path:   "/workspaces/" + url.PathEscape(target),
		Query:  url.Values{"force": {"true"}},
	})
	if err != nil {
		return die(fmt.Errorf("deleting workspace %q after cleanup: %w", label, err))
	}
	released, _ = responseField(deletedWorkspace, "released").(map[string]any)

	if isJSON() {
		return p.JSON(map[string]any{
			"status":   "retired",
			"id":       target,
			"counts":   counts,
			"deleted":  len(deleted),
			"released": released,
		})
	}

	p.Line("Workspace  %s (%s)", label, shortRef(target))
	printWorkspaceContents(p, counts)
	p.Line("Cleanup    deleted %d resource(s)", len(deleted))
	p.Success("Retired workspace %q", label)
	printReleased(p, released)
	return nil
}

func printWorkspaceContents(p printerLike, counts map[string]any) {
	if len(counts) == 0 {
		p.Line("Contains   nothing")
		return
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%v %s", counts[key], key))
	}
	p.Line("Contains   %s", strings.Join(parts, ", "))
}

func printReleased(p printerLike, released map[string]any) {
	keys := make([]string, 0, len(released))
	for key := range released {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p.Line("  released %v %s", released[key], key)
	}
}

// blocker is one resource that makes a workspace unsafe to retire blindly.
type blocker struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// productionBlockers finds resources tagged purpose=production and deployments
// that are serving traffic. Other kinds are covered as their inventory rows
// start carrying tags; the check reads whatever tags the row has.
func productionBlockers(data map[string]any) []blocker {
	resources, _ := data["resources"].(map[string]any)

	var found []blocker
	for _, kind := range sortedKeysOf(resources) {
		rows, _ := resources[kind].([]any)
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := row["name"].(string)
			if tags, ok := row["tags"].(map[string]any); ok {
				if purpose, _ := tags["purpose"].(string); purpose == "production" {
					found = append(found, blocker{Kind: kind, Name: name, Reason: "tagged purpose=production"})
					continue
				}
			}
			if kind == "deployments" {
				if state, _ := row["state"].(string); state == "running" {
					found = append(found, blocker{Kind: kind, Name: name, Reason: "running, serving traffic"})
				}
			}
		}
	}
	return found
}

func printBlockers(p printerLike, blockers []blocker) {
	for _, b := range blockers {
		p.Line("  %-12s %-24s %s", b.Kind, b.Name, b.Reason)
	}
}

// printDeletionOrder prints exactly what cleanup would remove, in the engine's
// dependency order.
func printDeletionOrder(p printerLike, cleanup any) {
	resources, _ := responseField(cleanup, "resources").(map[string]any)
	if len(resources) == 0 {
		p.Line("  nothing")
		return
	}

	order, _ := responseField(cleanup, "deletion_order").([]any)
	seen := map[string]bool{}
	for _, raw := range order {
		kind, _ := raw.(string)
		seen[kind] = true
		printKindRows(p, kind, resources)
	}
	for _, kind := range sortedKeysOf(resources) {
		if !seen[kind] {
			printKindRows(p, kind, resources)
		}
	}
}

func printKindRows(p printerLike, kind string, resources map[string]any) {
	rows, _ := resources[kind].([]any)
	if len(rows) == 0 {
		return
	}
	p.Line("  %s:", kind)
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		name, _ := row["name"].(string)
		state, _ := row["state"].(string)
		if state == "" {
			p.Line("    %s", name)
			continue
		}
		p.Line("    %-28s %s", name, state)
	}
}

func sortedKeysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// responseField reads a field from an API reply, whichever envelope shape the
// route uses: the usual {"data": {...}} or a bare object.
func responseField(body any, name string) any {
	if value := lookupPath(body, "data."+name); value != nil {
		return value
	}
	return lookupPath(body, name)
}

// countResources accepts the cleanup response's resources, which is a map of
// kind to rows, and counts the rows.
func countResources(resources any) int {
	total := 0
	switch typed := resources.(type) {
	case map[string]any:
		for _, rows := range typed {
			if list, ok := rows.([]any); ok {
				total += len(list)
			} else if _, ok := rows.(map[string]any); ok {
				total++
			}
		}
	case []any:
		total = len(typed)
	}
	return total
}
