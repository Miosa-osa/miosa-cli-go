package commands

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A deployed service (a project service) mounts a volume through its settings:
// settings.volumes is a list of {volume, mount_path, read_only}. mount and unmount
// read the service, change that list and write it back with the lock_version they
// read, so a concurrent edit is refused instead of overwritten. The change takes
// effect on the service's next deploy.

type serviceRow struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Slug        string         `json:"slug"`
	LockVersion int            `json:"lock_version"`
	Settings    map[string]any `json:"settings"`
}

func volumeMountOps(v Arg) []Op {
	projectFlag := Flag{Name: "project", Usage: "Project name or id (default: the workspace's Default project)", Type: "string", In: "local"}
	// A project name is not unique across an organization: every workspace has
	// its own Default. --workspace scopes the lookup; without it the profile's
	// default workspace, then the organization's Default workspace, is used.
	workspaceFlag := Flag{Name: "workspace", Usage: "Workspace whose project to use (default: the profile's, then the organization's Default workspace)", Type: "string", In: "local"}
	return []Op{
		{
			Use: "mount <volume> <service>", Short: "Mount a volume in a deployed service",
			Long: `Adds the volume to the service's settings so every replica mounts it at --mount
from its next deploy. A volume has one writer: a service with a writable volume
runs one replica, and a redeploy stops the previous version first. Use --read-only
to share it with several replicas. <service> is a name, slug or id.`,
			Method: "PATCH", Path: "/projects/{0}/services/{1}", Args: []Arg{v, {Name: "service"}},
			Flags: []Flag{
				{Name: "mount", Usage: "Absolute path in the service", Required: true, In: "local"},
				{Name: "read-only", Type: "bool", Usage: "Mount read-only", In: "local"},
				projectFlag,
				workspaceFlag,
			},
			Before: func(cmd *cobra.Command, c *api.Client) error {
				mount, _ := cmd.Flags().GetString("mount")
				ro, _ := cmd.Flags().GetBool("read-only")
				return changeServiceVolumes(cmd, c, func(vols []map[string]any, volume string) ([]map[string]any, error) {
					for _, e := range vols {
						if e["volume"] == volume {
							return nil, fmt.Errorf("%s is already mounted at %v", volume, e["mount_path"])
						}
					}
					entry := map[string]any{"volume": volume, "mount_path": mount}
					if ro {
						entry["read_only"] = true
					}
					return append(vols, entry), nil
				}, "Mounted %s in %s at "+strings.ReplaceAll(mount, "%", "%%")+" (takes effect on the next deploy)")
			},
		},
		{
			Use: "unmount <volume> <service>", Short: "Stop mounting a volume in a deployed service",
			Long:   "Removes the volume from the service's settings; the volume and its data stay. Takes effect on the next deploy.",
			Method: "PATCH", Path: "/projects/{0}/services/{1}", Args: []Arg{v, {Name: "service"}},
			Flags: []Flag{projectFlag, workspaceFlag},
			Before: func(cmd *cobra.Command, c *api.Client) error {
				return changeServiceVolumes(cmd, c, func(vols []map[string]any, volume string) ([]map[string]any, error) {
					kept := vols[:0:0]
					for _, e := range vols {
						if e["volume"] != volume {
							kept = append(kept, e)
						}
					}
					if len(kept) == len(vols) {
						return nil, fmt.Errorf("%s is not mounted in this service", volume)
					}
					return kept, nil
				}, "Unmounted %s from %s (takes effect on the next deploy)")
			},
		},
	}
}

// changeServiceVolumes does the whole command in the Before hook: it resolves the
// project and service, applies edit to the volume list and PATCHes the settings.
func changeServiceVolumes(cmd *cobra.Command, c *api.Client, edit func([]map[string]any, string) ([]map[string]any, error), done string) error {
	args := cmd.Flags().Args()
	if len(args) < 2 {
		return usagef("needs <volume> <service>")
	}
	ctx := cmd.Context()
	project, _ := cmd.Flags().GetString("project")
	workspace, _ := cmd.Flags().GetString("workspace")
	projectID, err := resolveProject(ctx, c, project, workspace)
	if err != nil {
		return die(err)
	}
	svc, err := resolveServiceRow(ctx, c, projectID, args[1])
	if err != nil {
		return die(err)
	}
	// The server matches a volume by name or id; keep what the person typed.
	volume := strings.TrimSpace(args[0])
	vols := currentVolumes(svc.Settings)
	next, err := edit(vols, volume)
	if err != nil {
		return die(err)
	}
	// The whole settings object goes back with only the volume list changed, so no
	// other setting depends on how the server merges a partial update.
	settings := map[string]any{}
	for k, val := range svc.Settings {
		settings[k] = val
	}
	if len(next) == 0 {
		delete(settings, "volumes")
	} else {
		settings["volumes"] = next
	}
	body := map[string]any{"lock_version": svc.LockVersion, "settings": settings}
	if err := c.Patch(ctx, "/projects/"+url.PathEscape(projectID)+"/services/"+url.PathEscape(svc.ID), body, nil); err != nil {
		return die(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), done+"\n", volume, svc.Name)
	return errRequestHandled
}

func currentVolumes(settings map[string]any) []map[string]any {
	out := []map[string]any{}
	list, _ := settings["volumes"].([]any)
	for _, raw := range list {
		if m, ok := raw.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// resolveProject returns the project id for a name or id; with no reference it
// is the Default project of the workspace in scope. Every workspace has its own
// Default project, so the lookup is scoped: --workspace, then the profile's
// default workspace, then the organization's Default workspace. An unscoped
// tenant-wide list is ordered newest first, so picking the first is_default
// there returns whichever workspace was created last - the bug this avoids.
func resolveProject(ctx context.Context, c *api.Client, ref, workspace string) (string, error) {
	ref = strings.TrimSpace(ref)
	if api.IsUUID(ref) {
		return ref, nil
	}
	wsID, err := projectWorkspaceID(ctx, c, workspace)
	if err != nil {
		return "", err
	}
	var out struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Slug      string `json:"slug"`
			IsDefault bool   `json:"is_default"`
		} `json:"data"`
	}
	q := url.Values{}
	if wsID != "" {
		q.Set("workspace_id", wsID)
	}
	if err := c.Get(ctx, "/projects", q, &out); err != nil {
		return "", err
	}
	var defaults []string
	for _, p := range out.Data {
		if ref == "" && p.IsDefault {
			defaults = append(defaults, p.ID)
			continue
		}
		if ref != "" && (p.Name == ref || p.Slug == ref) {
			return p.ID, nil
		}
	}
	switch {
	case ref == "" && len(defaults) == 1:
		return defaults[0], nil
	case ref == "" && len(defaults) > 1:
		// Only reachable when no workspace could be resolved; say so instead of
		// guessing which workspace's Default was meant.
		return "", fmt.Errorf("more than one Default project; pass --workspace or --project")
	case ref == "":
		return "", fmt.Errorf("no Default project in this workspace; pass --project")
	default:
		return "", fmt.Errorf("no project named %q in this workspace (see 'miosa project list')", ref)
	}
}

// projectWorkspaceID is the workspace whose projects a lookup may see:
// --workspace, then the profile's default workspace, then the organization's
// Default workspace (the one a create lands in without a workspace). It returns
// "" when none can be named, and the caller then refuses to guess.
func projectWorkspaceID(ctx context.Context, c *api.Client, workspace string) (string, error) {
	if ref := strings.TrimSpace(workspace); ref != "" {
		return resolveWorkspaceRef(ctx, c, ref)
	}
	if ref := defaultWorkspaceRef(); ref != "" {
		return resolveWorkspaceRef(ctx, c, ref)
	}
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/workspaces", nil, &out); err != nil {
		// An older server or a key that cannot list workspaces: leave the lookup
		// unscoped, and refuse below if that is ambiguous.
		return "", nil
	}
	for _, w := range out.Data {
		if w.Slug == "default" || w.Name == "default" {
			return w.ID, nil
		}
	}
	return "", nil
}

func resolveServiceRow(ctx context.Context, c *api.Client, projectID, ref string) (serviceRow, error) {
	ref = strings.TrimSpace(ref)
	var out struct {
		Data []serviceRow `json:"data"`
	}
	if err := c.Get(ctx, "/projects/"+url.PathEscape(projectID)+"/services", nil, &out); err != nil {
		return serviceRow{}, err
	}
	for _, s := range out.Data {
		if s.ID == ref || s.Name == ref || s.Slug == ref {
			return s, nil
		}
	}
	return serviceRow{}, fmt.Errorf("no service %q in this project", ref)
}
