package commands

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A module is a named bundle of resources declared in one manifest and applied to a workspace.
//
//	module: wo28
//	resources:
//	  - {kind: bucket, name: wo28-assets, spec: {visibility: private}}
//	  - {kind: volume, name: wo28-data, spec: {size_gb: 1}}
//	  - {kind: database, name: wo28-db, spec: {engine: postgresql}}
//	  - {kind: schedule, name: wo28-nightly, spec: {schedule: "0 2 * * *", command: "true"}}
//	  - {kind: agent, name: wo28-reviewer, spec: {configuration: {instructions: "Review the queue."}}}
//	  - {kind: interface, name: wo28-web, spec: {project_id: <project>, kind: static}}
//
// plan and diff change nothing; apply creates what is missing and updates what drifted (schedules
// and interfaces update in place, the other kinds report drift); remove deletes exactly the
// manifest's resources.
type moduleManifest struct {
	Module    string           `yaml:"module"`
	Resources []moduleResource `yaml:"resources"`
}

type moduleResource struct {
	Kind string         `yaml:"kind"`
	Name string         `yaml:"name"`
	Spec map[string]any `yaml:"spec"`
}

// A kind names the API paths that list, create, update and delete it. Paths may carry
// {project_id} (from the resource's spec) and {id} (the id read back at apply time); when
// update or delete is empty they are derived from create plus the id.
type moduleKind struct {
	list, create, update, delete string
	// idPath is where a create response carries the new id; default data.id.
	idPath    string
	updatable bool
}

var moduleKinds = map[string]moduleKind{
	"bucket":   {list: "/storage/buckets", create: "/storage/buckets"},
	"volume":   {list: "/volumes", create: "/volumes"},
	"database": {list: "/databases", create: "/databases"},
	"schedule": {list: "/cron-jobs", create: "/cron-jobs", updatable: true},
	// An agent is created with its first version; its configuration is immutable per
	// version, so a changed spec is reported as drift rather than patched in place.
	"agent": {list: "/agents", create: "/agents", idPath: "data.definition.id"},
	// An interface is a node of a project's canvas: a static service in a project.
	// The project scopes the list, so the resource names it in spec.project_id.
	"interface": {
		list:      "/projects/{project_id}/services",
		create:    "/projects/{project_id}/services",
		update:    "/projects/{project_id}/services/{id}",
		delete:    "/projects/{project_id}/services/{id}",
		updatable: true,
	},
}

var moduleKindNames = []string{"agent", "bucket", "database", "interface", "schedule", "volume"}

// modulePath fills a path template from the resource's spec and an id.
func modulePath(tmpl string, r moduleResource, id string) string {
	path := strings.ReplaceAll(tmpl, "{project_id}", fmt.Sprint(r.Spec["project_id"]))
	return strings.ReplaceAll(path, "{id}", url.PathEscape(id))
}

// A value that is a reference, not a secret: ${NAME}, secret://..., vault:...
var secretRefRE = regexp.MustCompile(`^(\$\{[A-Za-z_][A-Za-z0-9_]*\}|secret://\S+|vault:\S+)$`)
var secretKeyRE = regexp.MustCompile(`(?i)(secret|token|password|passwd|api[_-]?key|private[_-]?key|credential)`)
var secretValueRE = regexp.MustCompile(`(sk_live_|sk_test_|whsec_|ghp_|xox[bap]-|AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

// parseModule reads and validates a manifest. A literal secret value is refused with its key named.
func parseModule(data []byte) (*moduleManifest, error) {
	var m moduleManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest is not valid YAML: %w", err)
	}
	if strings.TrimSpace(m.Module) == "" {
		return nil, fmt.Errorf("manifest needs a module: name")
	}
	seen := map[string]bool{}
	for i, r := range m.Resources {
		if _, ok := moduleKinds[r.Kind]; !ok {
			return nil, fmt.Errorf("resources[%d]: kind %q is not supported (%s)", i, r.Kind, strings.Join(moduleKindNames, ", "))
		}
		if r.Kind == "interface" {
			if project, _ := r.Spec["project_id"].(string); strings.TrimSpace(project) == "" {
				return nil, fmt.Errorf("resources[%d]: interface %q needs spec.project_id, the project whose canvas it belongs to", i, r.Name)
			}
		}
		if strings.TrimSpace(r.Name) == "" {
			return nil, fmt.Errorf("resources[%d]: name is required", i)
		}
		key := r.Kind + "/" + r.Name
		if seen[key] {
			return nil, fmt.Errorf("resources[%d]: %s is listed twice", i, key)
		}
		seen[key] = true
		if bad := literalSecret(fmt.Sprintf("resources[%d].spec", i), r.Spec); bad != "" {
			return nil, fmt.Errorf("%s holds a literal secret value; reference it as ${NAME}, secret://<name> or vault:<name>", bad)
		}
	}
	return &m, nil
}

// literalSecret returns the dotted key of the first spec value that looks like a secret, or "".
func literalSecret(path string, v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			child := path + "." + k
			if s, ok := t[k].(string); ok {
				if s != "" && !secretRefRE.MatchString(s) && (secretKeyRE.MatchString(k) || secretValueRE.MatchString(s)) {
					return child
				}
				continue
			}
			if bad := literalSecret(child, t[k]); bad != "" {
				return bad
			}
		}
	case []any:
		for i, e := range t {
			if bad := literalSecret(fmt.Sprintf("%s[%d]", path, i), e); bad != "" {
				return bad
			}
		}
	}
	return ""
}

type moduleChange struct {
	Action string `json:"action"` // create | update | unchanged | drift | missing
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	ID     string `json:"id,omitempty"`
	Detail string `json:"detail,omitempty"`
}

func moduleItems(resp any) []map[string]any {
	list, _ := lookupPath(resp, "data").([]any)
	if list == nil {
		list, _ = resp.([]any)
	}
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// planModule compares the manifest with the workspace and says what apply would do.
func planModule(cmd *cobra.Command, c *api.Client, m *moduleManifest, wsID string) ([]moduleChange, map[string]string, error) {
	existing := map[string]map[string]map[string]any{} // list path -> name -> item
	var changes []moduleChange
	ids := map[string]string{}
	for _, r := range m.Resources {
		listPath := modulePath(moduleKinds[r.Kind].list, r, "")
		if existing[listPath] == nil {
			q := url.Values{}
			q.Set("workspace_id", wsID)
			resp, err := c.JSONAny(cmd.Context(), api.Request{Method: "GET", Path: listPath, Query: q})
			if err != nil {
				return nil, nil, err
			}
			existing[listPath] = map[string]map[string]any{}
			for _, it := range moduleItems(resp) {
				if name, _ := it["name"].(string); name != "" {
					existing[listPath][name] = it
				}
			}
		}
		item, ok := existing[listPath][r.Name]
		if !ok {
			changes = append(changes, moduleChange{Action: "create", Kind: r.Kind, Name: r.Name})
			continue
		}
		id, _ := item["id"].(string)
		ids[r.Kind+"/"+r.Name] = id
		if drift := specDrift(r.Spec, item); drift != "" {
			action := "drift"
			if moduleKinds[r.Kind].updatable {
				action = "update"
			}
			changes = append(changes, moduleChange{Action: action, Kind: r.Kind, Name: r.Name, ID: id, Detail: drift})
			continue
		}
		changes = append(changes, moduleChange{Action: "unchanged", Kind: r.Kind, Name: r.Name, ID: id})
	}
	return changes, ids, nil
}

// specDrift names the spec fields whose value differs from what the server reports.
func specDrift(spec map[string]any, item map[string]any) string {
	keys := make([]string, 0, len(spec))
	for k := range spec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var diffs []string
	for _, k := range keys {
		got, present := item[k]
		if !present {
			continue // a field the server does not echo back cannot be compared
		}
		if fmt.Sprint(got) != fmt.Sprint(spec[k]) {
			diffs = append(diffs, fmt.Sprintf("%s: %v -> %v", k, got, spec[k]))
		}
	}
	return strings.Join(diffs, ", ")
}

func newModuleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "module",
		Short: "Apply a manifest of resources to a workspace",
		Long: `A module is a named set of buckets, volumes, databases, schedules, agents and interfaces
declared in one YAML file.

  module: wo28
  resources:
    - {kind: bucket, name: wo28-assets, spec: {visibility: private}}
    - {kind: schedule, name: wo28-nightly, spec: {schedule: "0 2 * * *", command: "true"}}
    - {kind: agent, name: wo28-reviewer, spec: {configuration: {instructions: "Review the queue."}}}
    - {kind: interface, name: wo28-web, spec: {project_id: <project>, kind: static}}

An interface is a node of a project's canvas, so it names its project in spec.project_id.
plan and diff change nothing. apply creates what is missing and updates what drifted; run twice it
changes nothing the second time. remove deletes exactly the manifest's resources. A literal secret in
a spec is refused: reference it as ${NAME}, secret://<name> or vault:<name>.`,
	}
	for _, verb := range []string{"plan", "diff", "apply", "remove"} {
		cmd.AddCommand(newModuleVerb(verb))
	}
	return cmd
}

func newModuleVerb(verb string) *cobra.Command {
	var workspace string
	var dryRun, yes bool
	short := map[string]string{
		"plan": "Show what apply would create or change", "diff": "Show where the workspace differs from the manifest",
		"apply": "Create and update the manifest's resources", "remove": "Delete the manifest's resources",
	}[verb]
	cmd := &cobra.Command{
		Use:   verb + " <manifest.yaml>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runModule(cmd, verb, args[0], workspace, dryRun, yes)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace slug or id (required)")
	if verb == "remove" {
		cmd.Flags().BoolVar(&dryRun, "dry-run", false, "List what would be removed and remove nothing")
		cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	}
	return cmd
}

func runModule(cmd *cobra.Command, verb, file, workspace string, dryRun, yes bool) error {
	if workspace == "" {
		return usagef("--workspace is required")
	}
	data, err := os.ReadFile(file)
	if err != nil && !strings.Contains(filepath.Base(file), ".") {
		// A module is usually named after its manifest: "remove wo28" reads wo28.yaml.
		for _, ext := range []string{".yaml", ".yml"} {
			if b, readErr := os.ReadFile(file + ext); readErr == nil {
				data, err = b, nil
				break
			}
		}
	}
	if err != nil {
		return usagef("%v", err)
	}
	m, err := parseModule(data)
	if err != nil {
		return usagef("%v", err)
	}
	bc, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	c := bc.API
	wsID, err := c.ResolveWorkspace(cmd.Context(), workspace)
	if err != nil {
		return die(err)
	}
	changes, ids, err := planModule(cmd, c, m, wsID)
	if err != nil {
		return die(err)
	}
	p := printerFor(cmd)

	switch verb {
	case "apply":
		for i, ch := range changes {
			switch ch.Action {
			case "create":
				body := map[string]any{"name": ch.Name, "workspace_id": wsID}
				for k, v := range m.Resources[i].Spec {
					body[k] = v
				}
				createPath := modulePath(moduleKinds[ch.Kind].create, m.Resources[i], "")
				resp, err := c.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: createPath, Body: body})
				if err != nil {
					return die(fmt.Errorf("%s %s: %w", ch.Kind, ch.Name, err))
				}
				changes[i].Action = "created"
				idPath := moduleKinds[ch.Kind].idPath
				if idPath == "" {
					idPath = "data.id"
				}
				if id, _ := lookupPath(resp, idPath).(string); id != "" {
					changes[i].ID = id
				}
			case "update":
				updatePath := moduleKinds[ch.Kind].update
				if updatePath == "" {
					updatePath = moduleKinds[ch.Kind].create + "/{id}"
				}
				if _, err := c.JSONAny(cmd.Context(), api.Request{Method: "PATCH", Path: modulePath(updatePath, m.Resources[i], ch.ID), Body: m.Resources[i].Spec}); err != nil {
					return die(fmt.Errorf("%s %s: %w", ch.Kind, ch.Name, err))
				}
				changes[i].Action = "updated"
			}
		}
	case "remove":
		for i, ch := range changes {
			if ch.Action == "create" {
				changes[i].Action = "missing" // nothing to remove
				continue
			}
			changes[i].Action = "remove"
		}
		if !dryRun {
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Delete the %d resources of module %q from workspace %q?", countAction(changes, "remove"), m.Module, workspace)); err != nil {
					return err
				}
			}
			for i, ch := range changes {
				if ch.Action != "remove" {
					continue
				}
				id := ids[ch.Kind+"/"+ch.Name]
				deletePath := moduleKinds[ch.Kind].delete
				if deletePath == "" {
					deletePath = moduleKinds[ch.Kind].create + "/{id}"
				}
				if _, err := c.JSONAny(cmd.Context(), api.Request{Method: "DELETE", Path: modulePath(deletePath, m.Resources[i], id)}); err != nil {
					return die(fmt.Errorf("%s %s: %w", ch.Kind, ch.Name, err))
				}
				changes[i].Action = "removed"
			}
		}
	}
	return renderModule(p, m, verb, changes)
}

func countAction(cs []moduleChange, a string) int {
	n := 0
	for _, c := range cs {
		if c.Action == a {
			n++
		}
	}
	return n
}

func renderModule(p interface {
	JSON(v interface{}) error
	Line(format string, args ...interface{})
}, m *moduleManifest, verb string, changes []moduleChange) error {
	if isJSON() {
		return p.JSON(map[string]any{"module": m.Module, "changes": changes})
	}
	for _, ch := range changes {
		line := fmt.Sprintf("%-9s %s %s", ch.Action, ch.Kind, ch.Name)
		if ch.ID != "" {
			line += "  " + ch.ID
		}
		if ch.Detail != "" {
			line += "  (" + ch.Detail + ")"
		}
		p.Line("%s", line)
	}
	counts := map[string]int{}
	for _, ch := range changes {
		counts[ch.Action]++
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
	p.Line("%s: %s", verb, strings.Join(parts, ", "))
	return nil
}
