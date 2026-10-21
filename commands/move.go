package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// movable is one kind of resource `miosa move` can re-scope, with the route
// that does it and how a name or id is turned into the id that route takes.
type movable struct {
	kind  string
	route string // collection path, "/databases"
	// bulk is the type name the workspace move route (POST /workspaces/:id/move)
	// knows this kind by; it checks dependencies, projects and busy states. The
	// other kinds have their own /move route.
	bulk    string
	resolve func(ctx context.Context, c *api.Client, ref string) (string, error)
}

func resolveByRewrite(route string) func(context.Context, *api.Client, string) (string, error) {
	return func(ctx context.Context, c *api.Client, ref string) (string, error) {
		path, err := c.RewritePath(ctx, route+"/"+url.PathEscape(ref))
		if err != nil {
			return "", err
		}
		return strings.TrimPrefix(path, route+"/"), nil
	}
}

var movables = []movable{
	{"sandbox", "/sandboxes", "", func(ctx context.Context, c *api.Client, ref string) (string, error) {
		return c.ResolveSandbox(ctx, ref)
	}},
	{"computer", "/computers", "", func(ctx context.Context, c *api.Client, ref string) (string, error) {
		return c.ResolveComputer(ctx, ref)
	}},
	{"deployment", "/deployments", "deployment", resolveByRewrite("/deployments")},
	{"database", "/databases", "database", resolveByRewrite("/databases")},
	{"bucket", "/storage/buckets", "bucket", resolveByRewrite("/storage/buckets")},
	{"volume", "/volumes", "", resolveByRewrite("/volumes")},
	{"schedule", "/cron-jobs", "", resolveByRewrite("/cron-jobs")},
	{"agent", "/agents", "", func(ctx context.Context, c *api.Client, ref string) (string, error) { return c.ResolveAgent(ctx, ref) }},
	{"webhook", "/webhooks", "", func(ctx context.Context, c *api.Client, ref string) (string, error) {
		if api.IsUUID(ref) {
			return ref, nil
		}
		// Webhooks are not a named resource the resolver knows; match the name
		// against the list, and leave an unknown reference to the probe.
		var out struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := c.Get(ctx, "/webhooks", nil, &out); err != nil {
			return "", err
		}
		for _, hook := range out.Data {
			if hook.Name == ref {
				return hook.ID, nil
			}
		}
		return ref, nil
	}},
	{"project", "/projects", "", resolveByRewrite("/projects")},
}

func movableKinds() []string {
	kinds := make([]string, 0, len(movables))
	for _, m := range movables {
		kinds = append(kinds, m.kind)
	}
	return kinds
}

func newMoveCmd() *cobra.Command {
	var toWorkspace, toOrg, confirm, kind, project string
	var detach, acceptEnv, dryRun bool
	cmd := &cobra.Command{
		Use:   "move <resource>",
		Short: "Move a resource to another workspace",
		Long: `Move a resource to another workspace of the same organization.

  miosa move app-db --to-workspace staging
  miosa move assets --type bucket --to-workspace staging

<resource> is a name or id. Without --type the resource is looked up among
` + strings.Join(movableKinds(), ", ") + `; a name that matches more than one kind
needs --type. A move changes ownership and scope only: names, hostnames,
connection strings and object URLs keep working, and the data is untouched. A
running database, deployment or machine must be stopped first. Moving needs
admin rights in both workspaces.

Cross-organization transfer (deployments today) takes the target organization
and the resource's name, and bills the target organization from the transfer:

  miosa move my-app --to-org acme-eu --to-workspace production --confirm my-app`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(toWorkspace) == "" {
				return usagef("say where to move it: --to-workspace <workspace>")
			}
			return runMove(cmd, args[0], kind, toWorkspace, moveOptions{
				project: project, detach: detach, acceptEnv: acceptEnv, dryRun: dryRun,
				toOrg: toOrg, confirm: confirm,
			})
		},
	}
	cmd.Flags().StringVar(&toWorkspace, "to-workspace", "", "The workspace to move it to (slug, name or id)")
	cmd.Flags().StringVar(&project, "project", "", "Project of the target workspace to put it in (deployments, databases, buckets)")
	cmd.Flags().BoolVar(&detach, "detach-from-project", false, "Leave its project behind (deployments, databases, buckets)")
	cmd.Flags().BoolVar(&acceptEnv, "accept-env-changes", false, "Move a deployment even though the target's workspace variables differ")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Run every check and show the plan without moving anything (deployments, databases, buckets)")
	cmd.Flags().StringVar(&toOrg, "to-org", "", "Transfer to this organization (id or slug); needs --to-workspace and --confirm")
	cmd.Flags().StringVar(&confirm, "confirm", "", "The resource's name, required for a cross-organization transfer")
	cmd.Flags().StringVar(&kind, "type", "", "Kind of resource: "+strings.Join(movableKinds(), ", "))
	return cmd
}

type moveOptions struct {
	project                   string
	toOrg, confirm            string
	detach, acceptEnv, dryRun bool
}

func runMove(cmd *cobra.Command, ref, kind, toWorkspace string, opts moveOptions) error {
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()

	if kind != "" {
		known := false
		for _, m := range movables {
			known = known || m.kind == kind
		}
		if !known {
			return usagef("--type %q is not movable; choose one of: %s", kind, strings.Join(movableKinds(), ", "))
		}
	}
	target, err := c.API.ResolveWorkspace(ctx, toWorkspace)
	if err != nil {
		return usagef("workspace %q: %v", toWorkspace, err)
	}

	found, err := findMovable(ctx, c.API, ref, kind)
	if err != nil {
		return err
	}

	if opts.toOrg != "" {
		return runTransfer(cmd, c.API, found, target, opts)
	}

	var resp map[string]any
	path := found.route + "/" + url.PathEscape(found.id) + "/move"
	body := map[string]any{"target_workspace_id": target}
	if found.bulk != "" {
		if found.workspaceID == "" {
			return die(fmt.Errorf("could not tell which workspace %s %s is in", found.kind, ref))
		}
		path = "/workspaces/" + url.PathEscape(found.workspaceID) + "/move"
		body["resources"] = []map[string]any{{"type": found.bulk, "id": found.id}}
		if opts.project != "" {
			body["project_id"] = opts.project
		}
		if opts.detach {
			body["detach_from_project"] = true
		}
		if opts.acceptEnv {
			body["accept_inherited_env_changes"] = true
		}
		if opts.dryRun {
			body["dry_run"] = true
		}
	} else if opts.dryRun || opts.detach || opts.project != "" || opts.acceptEnv {
		return usagef("--dry-run, --project, --detach-from-project and --accept-env-changes apply to deployments, databases and buckets; a %s has no such options", found.kind)
	}
	if err := c.API.Post(ctx, path, body, &resp); err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(resp)
	}
	if opts.dryRun {
		p.Success("Dry run: %s %s can move to workspace %s", found.kind, ref, toWorkspace)
		return nil
	}
	p.Success("Moved %s %s to workspace %s", found.kind, ref, toWorkspace)
	return nil
}

type movableMatch struct {
	kind, route, id, bulk, workspaceID, name string
}

// findMovable looks the reference up among the movable kinds (or only `kind`)
// by asking the API for each; a reference that exists as exactly one kind is
// the one to move.
func findMovable(ctx context.Context, c *api.Client, ref, kind string) (movableMatch, error) {
	var matches []movableMatch
	for _, m := range movables {
		if kind != "" && m.kind != kind {
			continue
		}
		id, err := m.resolve(ctx, c, ref)
		if err != nil || id == "" {
			continue
		}
		var probe map[string]any
		if err := c.Get(ctx, m.route+"/"+url.PathEscape(id), nil, &probe); err != nil {
			continue
		}
		matches = append(matches, movableMatch{
			kind: m.kind, route: m.route, id: id, bulk: m.bulk,
			workspaceID: workspaceIDOf(probe), name: nameOf(probe),
		})
	}
	switch len(matches) {
	case 0:
		where := "among " + strings.Join(movableKinds(), ", ")
		if kind != "" {
			where = "as a " + kind
		}
		return movableMatch{}, die(fmt.Errorf("%q was not found %s in this organization", ref, where))
	case 1:
		return matches[0], nil
	}
	kinds := make([]string, 0, len(matches))
	for _, m := range matches {
		kinds = append(kinds, m.kind)
	}
	sort.Strings(kinds)
	return movableMatch{}, usagef("%q is more than one kind of resource (%s): add --type", ref, strings.Join(kinds, ", "))
}

// nameOf reads a resource's name off a probe response, bare or under data.
func nameOf(resp any) string {
	m, ok := resp.(map[string]any)
	if !ok {
		return ""
	}
	if d, ok := m["data"].(map[string]any); ok {
		m = d
	}
	name, _ := m["name"].(string)
	return name
}

// runTransfer posts a cross-organization transfer for one resource. The target
// organization and workspace are resolved by the API; the caller confirms by the
// resource's name, and the response carries when billing switched.
func runTransfer(cmd *cobra.Command, c *api.Client, found movableMatch, target string, opts moveOptions) error {
	if found.kind != "deployment" {
		return usagef("a cross-organization transfer supports deployments today; %s is not transferable yet", found.kind)
	}
	name := orDefault(found.name, opts.confirm)
	if strings.TrimSpace(opts.confirm) == "" {
		return usagef("confirm the transfer by the resource's name: --confirm %s", name)
	}
	if name != "" && strings.TrimSpace(opts.confirm) != name {
		return usagef("--confirm %q does not match the resource's name %q", opts.confirm, name)
	}
	if current, _ := effectiveOrg(); current != "" && strings.EqualFold(current, opts.toOrg) {
		return usagef("--to-org %q is the current organization; use --to-workspace for a move inside it", opts.toOrg)
	}
	if found.workspaceID == "" {
		return die(fmt.Errorf("could not tell which workspace %s %s is in", found.kind, found.name))
	}

	body := map[string]any{
		"target_org_id":       opts.toOrg,
		"target_workspace_id": target,
		"resources":           []map[string]any{{"type": found.kind, "id": found.id}},
		"confirm":             opts.confirm,
	}
	var resp map[string]any
	if err := c.Post(cmd.Context(), "/workspaces/"+url.PathEscape(found.workspaceID)+"/cross-org-transfer", body, &resp); err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(resp)
	}
	switched := ""
	if data, ok := resp["data"].(map[string]any); ok {
		switched, _ = data["billing_switched_at"].(string)
	}
	p.Success("Transferred %s %s to %s; billing switched at %s", found.kind, orDefault(found.name, found.id), opts.toOrg, orDefault(switched, "the transfer"))
	return nil
}

// workspaceIDOf reads the workspace_id off a resource response, bare or under
// data. A typed struct (an SDK result handed straight to a create's landing
// line) is read by round-tripping it through JSON.
func workspaceIDOf(resp any) string {
	m, ok := resp.(map[string]any)
	if !ok {
		b, err := json.Marshal(resp)
		if err != nil {
			return ""
		}
		if err := json.Unmarshal(b, &m); err != nil {
			return ""
		}
	}
	if id, _ := m["workspace_id"].(string); id != "" {
		return id
	}
	if d, ok := m["data"].(map[string]any); ok {
		if id, _ := d["workspace_id"].(string); id != "" {
			return id
		}
	}
	// An agent schedule answers with the trigger under "schedule".
	if s, ok := m["schedule"].(map[string]any); ok {
		if id, _ := s["workspace_id"].(string); id != "" {
			return id
		}
	}
	return ""
}
