package commands

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

type orgChange struct {
	Action    string `json:"action"` // create | exists | extra
	Kind      string `json:"kind"`   // workspace | project
	Workspace string `json:"workspace"`
	Name      string `json:"name,omitempty"`
}

func parseTopology(data []byte) (*topologyFile, error) {
	var t topologyFile
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("topology is not valid YAML: %w", err)
	}
	if t.SchemaVersion != 1 {
		return nil, fmt.Errorf("schema_version %d is not supported (want 1)", t.SchemaVersion)
	}
	if len(t.Entities) == 0 {
		return nil, fmt.Errorf("topology has no entities")
	}
	return &t, nil
}

func newOrgApplyCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "apply <topology.yaml>",
		Short: "Create the workspaces and projects a topology file lists",
		Long: `Reads the company topology: every workspace under entities, and its projects, are created if
missing. It never deletes. Workspaces in the organization that the file does not list, other than the
keep_workspaces ones, are reported as extras and left alone. A second run creates nothing.

  miosa --org osa org apply topology/miosa-company.yaml --dry-run

The entity, access and bridge attributes are not stored yet (they need workspace metadata); members
and invites are never created.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runOrgApply(cmd, args[0], dryRun) },
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be created and create nothing")
	return cmd
}

func runOrgApply(cmd *cobra.Command, file string, dryRun bool) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return usagef("%v", err)
	}
	t, err := parseTopology(data)
	if err != nil {
		return usagef("%v", err)
	}
	bc, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	c := bc.API

	have := map[string]string{} // slug -> id
	resp, err := c.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: "/workspaces"})
	if err != nil {
		return die(err)
	}
	for _, w := range moduleItems(resp) {
		slug, _ := w["slug"].(string)
		id, _ := w["id"].(string)
		have[slug] = id
	}

	wanted := map[string][]string{}
	for _, e := range t.Entities {
		for slug, w := range e.Workspaces {
			wanted[slug] = append(wanted[slug], w.Projects...)
		}
	}
	slugs := make([]string, 0, len(wanted))
	for s := range wanted {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)

	var changes []orgChange
	for _, slug := range slugs {
		id, ok := have[slug]
		if !ok {
			changes = append(changes, orgChange{Action: "create", Kind: "workspace", Workspace: slug})
			for _, p := range wanted[slug] {
				changes = append(changes, orgChange{Action: "create", Kind: "project", Workspace: slug, Name: p})
			}
			continue
		}
		changes = append(changes, orgChange{Action: "exists", Kind: "workspace", Workspace: slug})
		projects := map[string]bool{}
		q := url.Values{}
		q.Set("workspace_id", id)
		presp, err := c.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: "/projects", Query: q})
		if err != nil {
			return die(err)
		}
		for _, p := range moduleItems(presp) {
			n, _ := p["name"].(string)
			s, _ := p["slug"].(string)
			projects[n], projects[s] = true, true
		}
		for _, p := range wanted[slug] {
			act := "create"
			if projects[p] {
				act = "exists"
			}
			changes = append(changes, orgChange{Action: act, Kind: "project", Workspace: slug, Name: p})
		}
	}
	keep := map[string]bool{}
	for _, k := range t.KeepWorkspaces {
		keep[k] = true
	}
	extras := make([]string, 0)
	for slug := range have {
		if _, listed := wanted[slug]; !listed && !keep[slug] {
			extras = append(extras, slug)
		}
	}
	sort.Strings(extras)
	for _, s := range extras {
		changes = append(changes, orgChange{Action: "extra", Kind: "workspace", Workspace: s})
	}

	if !dryRun {
		for i, ch := range changes {
			if ch.Action != "create" {
				continue
			}
			switch ch.Kind {
			case "workspace":
				resp, err := c.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/workspaces", Body: map[string]any{"name": ch.Workspace, "slug": ch.Workspace}})
				if err != nil {
					return die(fmt.Errorf("workspace %s: %w", ch.Workspace, err))
				}
				if id, _ := lookupPath(resp, "data.id").(string); id != "" {
					have[ch.Workspace] = id
				}
			case "project":
				if _, err := c.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/projects", Body: map[string]any{"name": ch.Name, "workspace_id": have[ch.Workspace]}}); err != nil {
					return die(fmt.Errorf("project %s/%s: %w", ch.Workspace, ch.Name, err))
				}
			}
			changes[i].Action = "created"
		}
	}

	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(map[string]any{"org": t.MiosaOrg, "dry_run": dryRun, "changes": changes})
	}
	counts := map[string]int{}
	for _, ch := range changes {
		counts[ch.Action+" "+ch.Kind]++
		if ch.Action == "create" || ch.Action == "created" || ch.Action == "extra" {
			p.Line("%-8s %s %s", ch.Action, ch.Kind, strings.Trim(ch.Workspace+"/"+ch.Name, "/"))
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	p.Line("org apply: %s", strings.Join(parts, ", "))
	return nil
}
