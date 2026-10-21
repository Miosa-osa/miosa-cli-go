package commands

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// errDrift is returned when the live organization and the topology file
// disagree, so the command exits non-zero and can gate a deploy.
var errDrift = errors.New("drift detected")

// topologyFile is an organization's desired-state file (schema_version 1):
//
//	schema_version: 1
//	miosa_org: osa
//	keep_workspaces: [default]
//	entities:
//	  <entity>:
//	    workspaces:
//	      <workspace-slug>:
//	        projects: [<project-slug>, ...]
//
// An organization's workspaces are flat, so the declared set is the union
// across entities; a project is scoped to its workspace.
type topologyFile struct {
	SchemaVersion  int                       `yaml:"schema_version"`
	MiosaOrg       string                    `yaml:"miosa_org"`
	KeepWorkspaces []string                  `yaml:"keep_workspaces"`
	Entities       map[string]topologyEntity `yaml:"entities"`
}

type topologyEntity struct {
	Workspaces map[string]topologyWorkspace `yaml:"workspaces"`
}

type topologyWorkspace struct {
	Projects []string `yaml:"projects"`
}

// driftReport is the machine-readable answer. The text output is a rendering
// of this.
type driftReport struct {
	Org                  string             `json:"org,omitempty"`
	UndeclaredWorkspaces []string           `json:"undeclared_workspaces"`
	MissingWorkspaces    []string           `json:"missing_workspaces"`
	UndeclaredProjects   []string           `json:"undeclared_projects"`
	MissingProjects      []string           `json:"missing_projects"`
	UntaggedResources    []untaggedResource `json:"untagged_resources"`
}

type untaggedResource struct {
	Workspace string `json:"workspace"`
	Kind      string `json:"kind"`
	Count     int    `json:"count"`
}

// findings reports whether anything is worth a non-zero exit.
func (r driftReport) findings() int {
	return len(r.UndeclaredWorkspaces) + len(r.MissingWorkspaces) +
		len(r.UndeclaredProjects) + len(r.MissingProjects) + len(r.UntaggedResources)
}

func newOrgDiffCmd() *cobra.Command {
	var (
		file     string
		withTags bool
	)

	cmd := &cobra.Command{
		Use:   "diff --file <topology.yaml>",
		Short: "Compare the organization with a topology file: undeclared, missing, untagged",
		Long: `Compare the live organization with a desired-state topology file.

  miosa org diff --file topology/org.yaml
  miosa org diff --file topology.yaml --json
  miosa org diff --file topology.yaml --no-tags      # skip the resource tag scan

Reported, in both directions:

  undeclared   exists in the organization, not in the file
  missing      in the file, not in the organization
  untagged     sandboxes in a declared workspace with no purpose tag

keep_workspaces are never reported as undeclared. Exits 1 when anything is
reported, so a deploy can gate on it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runOrgDiff(cmd, file, withTags)
		},
	}

	cmd.Flags().StringVar(&file, "file", "", "Path to the topology file")
	cmd.Flags().BoolVar(&withTags, "tags", true, "Scan declared workspaces for resources with no purpose tag")
	_ = cmd.MarkFlagRequired("file")

	return cmd
}

func runOrgDiff(cmd *cobra.Command, file string, withTags bool) error {
	p := printerFor(cmd)

	topo, err := loadTopology(file)
	if err != nil {
		return usagef("%v", err)
	}

	c, _, err := buildClient()
	if err != nil {
		return err
	}

	report, err := diffTopology(cmd, c.API, topo, withTags)
	if err != nil {
		return die(err)
	}

	if isJSON() {
		if err := p.JSON(report); err != nil {
			return err
		}
	} else {
		printDriftReport(p, file, report)
	}

	if report.findings() > 0 {
		return errDrift
	}
	return nil
}

func loadTopology(path string) (*topologyFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var topo topologyFile
	if err := yaml.Unmarshal(raw, &topo); err != nil {
		return nil, fmt.Errorf("%s is not valid YAML: %w", path, err)
	}
	if topo.SchemaVersion != 1 {
		return nil, fmt.Errorf("%s has schema_version %d; this command reads 1", path, topo.SchemaVersion)
	}
	if len(topo.Entities) == 0 {
		return nil, fmt.Errorf("%s declares no entities", path)
	}
	return &topo, nil
}

// declared flattens the file into the sets an organization actually has:
// workspace slugs, and project slugs per workspace.
func (t *topologyFile) declared() (map[string]bool, map[string]map[string]bool) {
	workspaces := map[string]bool{}
	projects := map[string]map[string]bool{}

	for _, entity := range t.Entities {
		for slug, ws := range entity.Workspaces {
			workspaces[slug] = true
			if projects[slug] == nil {
				projects[slug] = map[string]bool{}
			}
			for _, project := range ws.Projects {
				projects[slug][project] = true
			}
		}
	}
	// keep_workspaces are not declared: they are live workspaces the file does
	// not own, which must not be reported as undeclared (and must not be
	// reported as missing when they do not exist).
	return workspaces, projects
}

func diffTopology(cmd *cobra.Command, c *api.Client, topo *topologyFile, withTags bool) (driftReport, error) {
	declaredWs, declaredProjects := topo.declared()

	var wsEnv struct {
		Data []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/workspaces", nil, &wsEnv); err != nil {
		return driftReport{}, fmt.Errorf("listing workspaces: %w", err)
	}

	report := driftReport{Org: topo.MiosaOrg}
	liveBySlug := map[string]string{} // slug -> id

	for _, ws := range wsEnv.Data {
		liveBySlug[ws.Slug] = ws.ID
		if !declaredWs[ws.Slug] && !hasString(topo.KeepWorkspaces, ws.Slug) {
			report.UndeclaredWorkspaces = append(report.UndeclaredWorkspaces, ws.Slug)
		}
	}
	for slug := range declaredWs {
		if _, ok := liveBySlug[slug]; !ok {
			report.MissingWorkspaces = append(report.MissingWorkspaces, slug)
		}
	}

	for _, slug := range sortedKeys(liveBySlug) {
		id := liveBySlug[slug]
		liveProjects, err := listProjectSlugs(cmd, c, id)
		if err != nil {
			return driftReport{}, fmt.Errorf("listing projects in %s: %w", slug, err)
		}

		want := declaredProjects[slug]
		for _, project := range liveProjects {
			if !want[project] {
				report.UndeclaredProjects = append(report.UndeclaredProjects, slug+"/"+project)
			}
		}
		for project := range want {
			if !hasString(liveProjects, project) {
				report.MissingProjects = append(report.MissingProjects, slug+"/"+project)
			}
		}
	}

	if withTags {
		for _, slug := range sortedKeys(liveBySlug) {
			if !declaredWs[slug] {
				continue
			}
			untagged, err := countUntaggedSandboxes(cmd, c, liveBySlug[slug])
			if err != nil {
				return driftReport{}, fmt.Errorf("listing sandboxes in %s: %w", slug, err)
			}
			if untagged > 0 {
				report.UntaggedResources = append(report.UntaggedResources, untaggedResource{
					Workspace: slug,
					Kind:      "sandbox",
					Count:     untagged,
				})
			}
		}
	}

	sort.Strings(report.UndeclaredWorkspaces)
	sort.Strings(report.MissingWorkspaces)
	sort.Strings(report.UndeclaredProjects)
	sort.Strings(report.MissingProjects)

	return report, nil
}

func listProjectSlugs(cmd *cobra.Command, c *api.Client, workspaceID string) ([]string, error) {
	var env struct {
		Data []struct {
			Slug string `json:"slug"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("workspace_id", workspaceID)
	if err := c.Get(cmd.Context(), "/projects", q, &env); err != nil {
		return nil, err
	}
	slugs := make([]string, 0, len(env.Data))
	for _, project := range env.Data {
		slugs = append(slugs, project.Slug)
	}
	return slugs, nil
}

// countUntaggedSandboxes counts sandboxes in one workspace with no purpose
// tag. A resource without a purpose is the thing WO-37 wants visible: nobody
// can say whether it is production or disposable.
func countUntaggedSandboxes(cmd *cobra.Command, c *api.Client, workspaceID string) (int, error) {
	var env struct {
		Data []struct {
			Tags map[string]string `json:"tags"`
		} `json:"data"`
	}
	q := url.Values{}
	q.Set("workspace_id", workspaceID)
	q.Set("include_errored", "true")
	if err := c.Get(cmd.Context(), "/sandboxes", q, &env); err != nil {
		return 0, err
	}

	count := 0
	for _, sandbox := range env.Data {
		if strings.TrimSpace(sandbox.Tags["purpose"]) == "" {
			count++
		}
	}
	return count, nil
}

func printDriftReport(p *output.Printer, file string, report driftReport) {
	p.Line("Topology   %s", file)
	if report.Org != "" {
		p.Line("Org        %s", report.Org)
	}
	p.Line("")

	printGroup(p, "Workspaces undeclared (live, not in the file)", report.UndeclaredWorkspaces)
	printGroup(p, "Workspaces missing (in the file, not live)", report.MissingWorkspaces)
	printGroup(p, "Projects undeclared (live, not in the file)", report.UndeclaredProjects)
	printGroup(p, "Projects missing (in the file, not live)", report.MissingProjects)

	if len(report.UntaggedResources) == 0 {
		p.Line("Resources without a purpose tag: none")
	} else {
		p.Line("Resources without a purpose tag:")
		for _, entry := range report.UntaggedResources {
			p.Line("  %-24s %d %s(s)", entry.Workspace, entry.Count, entry.Kind)
		}
	}

	if report.findings() == 0 {
		p.Success("No drift.")
	}
}

func printGroup(p *output.Printer, title string, values []string) {
	if len(values) == 0 {
		return
	}
	p.Line("%s:", title)
	for _, value := range values {
		p.Line("  %s", value)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
