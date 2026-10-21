package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// Attaching a workspace to an Optimal Engine is the MIOSA-side half of WO-36:
// the server records which Engine serves the workspace, mirrors the workspace
// into that Engine with the same slug, and mints one key scoped to it. This
// command never sees the key; every resource in the workspace receives it
// through the platform's secret injection.

type engineAttachment struct {
	WorkspaceID         string                  `json:"workspace_id"`
	DatabaseID          string                  `json:"database_id"`
	Scope               string                  `json:"scope"`
	Status              string                  `json:"status"`
	EngineWorkspaceID   string                  `json:"engine_workspace_id"`
	EngineWorkspaceSlug string                  `json:"engine_workspace_slug"`
	KeyID               string                  `json:"key_id"`
	KeyMintedAt         string                  `json:"key_minted_at"`
	Engine              *engineAttachmentEngine `json:"engine,omitempty"`
}

type engineAttachmentEngine struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
	Scope string `json:"scope"`
	URL   string `json:"url"`
}

// engineAPI is the slice of the API client these commands use.
type engineAPI interface {
	Get(ctx context.Context, path string, q url.Values, out any) error
	ResolveWorkspace(ctx context.Context, ref string) (string, error)
}

func newEngineAttachCmd() *cobra.Command {
	var to string
	var replace bool

	cmd := &cobra.Command{
		Use:   "attach <workspace> --to <org|engine>",
		Short: "Attach a workspace to an Optimal Engine",
		Long: `Attaches a workspace to an Optimal Engine and gives every sandbox, computer,
deployment and agent run in that workspace the Engine URL and a workspace-scoped
key, through the platform's secret injection. There is nothing to copy into the
workspace.

--to org uses the organization's Engine. When no Engine is the organization's
yet and exactly one managed Engine exists, that Engine becomes the
organization's, which is the default for a new organization.

A workspace is served by one Engine at a time. Attaching it to a different one
is refused with the Engine it is already on; pass --replace to move it, which
only an organization owner or admin may do.

  miosa engine attach kii --to org
  miosa engine attach kii --to qa-brain
  miosa engine attach kii --to qa-brain --replace
  miosa engine attach kii --to org --json | jq '{scope, engine_workspace_slug}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineAttach(cmd, args[0], to, replace)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Engine name or id, or 'org' for the organization's Engine (required)")
	cmd.Flags().BoolVar(&replace, "replace", false, "Move a workspace already attached to another Engine (owners and admins only)")
	return cmd
}

func runEngineAttach(cmd *cobra.Command, workspaceRef, to string, replace bool) error {
	if strings.TrimSpace(to) == "" {
		return usagef("--to is required: an Engine name, its id, or 'org'")
	}

	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()

	workspaceID, err := c.API.ResolveWorkspace(ctx, workspaceRef)
	if err != nil {
		return die(err)
	}

	engines, err := listOptimalEngines(ctx, c.API.Get)
	if err != nil {
		return die(err)
	}

	engine, declareOrg, err := pickAttachEngine(engines, workspaceRef, to)
	if err != nil {
		return die(err)
	}

	body := map[string]any{"workspace_id": workspaceID}
	if declareOrg {
		body["declare_org"] = true
	}
	if replace {
		body["replace"] = true
	}

	var out struct {
		Data engineAttachment `json:"data"`
	}
	path := "/databases/" + url.PathEscape(engine.ID) + "/attachments"
	if err := c.API.Post(ctx, path, body, &out); err != nil {
		return die(attachError(workspaceRef, err))
	}

	if isJSON() {
		return p.JSON(out.Data)
	}
	if declareOrg {
		p.Line("%s is now the organization's Engine", engine.Name)
	}
	p.Success("Attached %s to %s (%s)", workspaceRef, engine.Name, shortRef(engine.ID))
	p.Line("Scope: %s", out.Data.Scope)
	p.Line("Engine workspace: %s", out.Data.EngineWorkspaceSlug)
	return nil
}

// attachError turns the binding's refusals into what the user can act on. The
// 409 names the Engine that already serves the workspace.
func attachError(workspaceRef string, err error) error {
	if api.IsCode(err, "ALREADY_ATTACHED") {
		if name := attachedEngineRef(err); name != "" {
			return fmt.Errorf("workspace %q is already attached to Engine %q; pass --replace to move it (owners and admins only)", workspaceRef, name)
		}
		return fmt.Errorf("workspace %q is already attached to another Engine; pass --replace to move it (owners and admins only)", workspaceRef)
	}
	if api.IsCode(err, "REPLACE_REQUIRES_ADMIN") {
		return errors.New("only an organization owner or admin can move a workspace to another Engine")
	}
	return err
}

// attachedEngineRef reads the Engine from the 409 body; the error object
// carries its name and id beside code and message. The name may be null, so
// the id stands in.
func attachedEngineRef(err error) string {
	var e *api.Error
	if !errors.As(err, &e) || len(e.Body) == 0 {
		return ""
	}
	var raw struct {
		Error struct {
			EngineName string `json:"engine_name"`
			EngineID   string `json:"engine_id"`
		} `json:"error"`
	}
	if json.Unmarshal(e.Body, &raw) != nil {
		return ""
	}
	if name := strings.TrimSpace(raw.Error.EngineName); name != "" {
		return name
	}
	return strings.TrimSpace(raw.Error.EngineID)
}

func newEngineDetachCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "detach <workspace>",
		Short: "Detach a workspace and revoke its Engine key",
		Long: `Detaches a workspace from the Engine that serves it and revokes the workspace
key on that Engine. Resources that are already running keep the variables they
were started with until they are recreated.

A revocation that cannot reach the Engine fails the detach, because the key
would otherwise stay valid. --force marks the attachment detached anyway and
records that the key is still to be revoked; use it only when the Engine or the
workspace is being destroyed.

  miosa engine detach kii
  miosa engine detach kii --force --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineDetach(cmd, args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Detach even when the key cannot be revoked on the Engine")
	return cmd
}

func runEngineDetach(cmd *cobra.Command, workspaceRef string, force bool) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()

	workspaceID, err := c.API.ResolveWorkspace(ctx, workspaceRef)
	if err != nil {
		return die(err)
	}

	var current struct {
		Data engineAttachment `json:"data"`
	}
	if err := c.API.Get(ctx, "/workspaces/"+url.PathEscape(workspaceID)+"/engine", nil, &current); err != nil {
		if api.IsStatus(err, 404) {
			return die(fmt.Errorf("%s is not attached to an Optimal Engine", workspaceRef))
		}
		return die(err)
	}

	query := url.Values{}
	if force {
		query.Set("force", "true")
	}
	path := "/databases/" + url.PathEscape(current.Data.DatabaseID) +
		"/attachments/" + url.PathEscape(workspaceID)

	var out struct {
		Data engineAttachment `json:"data"`
	}
	if err := c.API.Delete(ctx, path, query, &out); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(out.Data)
	}
	p.Success("Detached %s from %s", workspaceRef, current.Data.DatabaseID)
	return nil
}

func newEngineStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [workspace]",
		Short: "Show which Engine serves a workspace, or every Engine and its workspaces",
		Long: `With a workspace, shows the Engine that serves it, its scope, the Engine
workspace it maps to and the key the workspace's resources use (the key id, not
the key). A workspace that is not attached prints 'not attached' and exits 0.

Without an argument it lists every managed Engine with the workspaces attached
to it.

  miosa engine status kii
  miosa engine status
  miosa engine status --json | jq '.data[] | {engine: .engine.name, workspaces: [.attachments[].workspace_id]}'`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := ""
			if len(args) == 1 {
				ref = args[0]
			}
			return runEngineStatus(cmd, ref)
		},
	}
}

func runEngineStatus(cmd *cobra.Command, workspaceRef string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()

	if workspaceRef != "" {
		return engineStatusOne(ctx, p, c.API, workspaceRef)
	}
	return engineStatusAll(ctx, p, c.API)
}

func engineStatusOne(ctx context.Context, p *output.Printer, c engineAPI, workspaceRef string) error {
	workspaceID, err := c.ResolveWorkspace(ctx, workspaceRef)
	if err != nil {
		return die(err)
	}

	var out struct {
		Data engineAttachment `json:"data"`
	}
	err = c.Get(ctx, "/workspaces/"+url.PathEscape(workspaceID)+"/engine", nil, &out)
	if err != nil {
		if api.IsStatus(err, 404) {
			if isJSON() {
				return p.JSON(map[string]any{"workspace_id": workspaceID, "attached": false})
			}
			p.Line("%s is not attached to an Optimal Engine", workspaceRef)
			return nil
		}
		return die(err)
	}

	if isJSON() {
		return p.JSON(out.Data)
	}

	engine := ""
	if out.Data.Engine != nil {
		engine = out.Data.Engine.Name
	}
	p.Fields([][2]string{
		{"Workspace", workspaceRef},
		{"Engine", engine},
		{"Engine id", out.Data.DatabaseID},
		{"Scope", out.Data.Scope},
		{"Engine workspace", out.Data.EngineWorkspaceSlug},
		{"Key id", out.Data.KeyID},
		{"Key minted", out.Data.KeyMintedAt},
	})
	return nil
}

func engineStatusAll(ctx context.Context, p *output.Printer, c engineAPI) error {
	engines, err := listOptimalEngines(ctx, c.Get)
	if err != nil {
		return die(err)
	}
	if len(engines) == 0 {
		if isJSON() {
			return p.JSON([]any{})
		}
		p.Line("No managed Optimal Engine yet. Create one with 'miosa engine create <name> --workspace <workspace>'.")
		return nil
	}

	type engineWithAttachments struct {
		Engine      engineRef          `json:"engine"`
		Attachments []engineAttachment `json:"attachments"`
	}

	out := make([]engineWithAttachments, 0, len(engines))
	for _, engine := range engines {
		var list struct {
			Data []engineAttachment `json:"data"`
		}
		path := "/databases/" + url.PathEscape(engine.ID) + "/attachments"
		if err := c.Get(ctx, path, nil, &list); err != nil {
			return die(err)
		}
		out = append(out, engineWithAttachments{Engine: engine, Attachments: list.Data})
	}

	if isJSON() {
		return p.JSON(out)
	}

	rows := make([][]string, 0, len(out))
	for _, item := range out {
		workspaces := make([]string, 0, len(item.Attachments))
		for _, attachment := range item.Attachments {
			workspaces = append(workspaces, attachment.EngineWorkspaceSlug)
		}
		scope := engineScope(item.Engine)
		if scope == "" {
			scope = "workspace"
		}
		rows = append(rows, []string{
			item.Engine.Name,
			shortRef(item.Engine.ID),
			scope,
			item.Engine.State,
			strings.Join(workspaces, ", "),
		})
	}
	p.Table([]string{"ENGINE", "ID", "SCOPE", "STATE", "WORKSPACES"}, rows)
	return nil
}

// pickAttachEngine resolves --to against the managed Engines. `org` means the
// organization's Engine; when none is declared yet and exactly one Engine
// exists, that Engine is declared the organization's in the same call.
func pickAttachEngine(engines []engineRef, workspaceRef, to string) (engineRef, bool, error) {
	if strings.EqualFold(strings.TrimSpace(to), "org") {
		for _, engine := range engines {
			if engineScope(engine) == "org" {
				return engine, false, nil
			}
		}
		switch len(engines) {
		case 0:
			return engineRef{}, false, fmt.Errorf(
				"no managed Optimal Engine yet: create one with 'miosa engine create <name> --workspace %s'",
				workspaceRef)
		case 1:
			return engines[0], true, nil
		default:
			names := make([]string, 0, len(engines))
			for _, engine := range engines {
				names = append(names, engine.Name)
			}
			return engineRef{}, false, fmt.Errorf(
				"no Engine is the organization's yet; name one: miosa engine attach %s --to <%s>",
				workspaceRef, strings.Join(names, "|"))
		}
	}

	for _, engine := range engines {
		if engine.ID == to || engine.Name == to {
			return engine, false, nil
		}
	}

	names := make([]string, 0, len(engines))
	for _, engine := range engines {
		names = append(names, engine.Name+" ("+shortRef(engine.ID)+")")
	}
	if len(names) == 0 {
		return engineRef{}, false, fmt.Errorf("no managed Optimal Engine named %q exists yet", to)
	}
	return engineRef{}, false, fmt.Errorf("no managed Optimal Engine named %q (have: %s)", to, strings.Join(names, ", "))
}

// listOptimalEngines lists the managed Engines (managed databases with engine
// "optimal"); other database engines are left alone.
func listOptimalEngines(ctx context.Context, get apiGetter) ([]engineRef, error) {
	query := url.Values{"limit": {"200"}}
	var list struct {
		Data []engineRef `json:"data"`
	}
	if err := get(ctx, "/databases", query, &list); err != nil {
		return nil, err
	}
	engines := make([]engineRef, 0, len(list.Data))
	for _, database := range list.Data {
		if database.Engine == "optimal" {
			engines = append(engines, database)
		}
	}
	return engines, nil
}

// engineScope reads metadata["scope"]; an Engine without it is workspace-
// scoped unless the server says otherwise.
func engineScope(engine engineRef) string {
	if engine.Metadata != nil {
		if scope, ok := engine.Metadata["scope"].(string); ok {
			return scope
		}
	}
	return ""
}
