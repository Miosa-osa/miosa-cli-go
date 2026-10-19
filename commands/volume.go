package commands

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

const bytesPerGB = 1 << 30

var volumeCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"},
	{Head: "SIZE", Path: "size_bytes", Fmt: "bytes"}, {Head: "BACKEND", Path: "backend"},
}

var volumeSnapshotCols = []Col{
	{Head: "SNAPSHOT", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "KIND", Path: "kind"},
	{Head: "SIZE", Path: "size_bytes", Fmt: "bytes"}, {Head: "EXPIRES", Path: "expires_at"}, {Head: "CREATED", Path: "created_at", Fmt: "age"},
}

var volumeSnapshotDetail = []Col{
	{Head: "Snapshot", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Kind", Path: "kind"},
	{Head: "Size", Path: "size_bytes", Fmt: "bytes"}, {Head: "Expires", Path: "expires_at"},
}

var volumeAttachmentCols = []Col{
	{Head: "ATTACHMENT", Path: "id", Fmt: "short"}, {Head: "MACHINE", Path: "computer_id", Fmt: "short"},
	{Head: "MOUNT", Path: "mount_path"}, {Head: "READ-ONLY", Path: "read_only"}, {Head: "STATUS", Path: "status"},
}

func volumeCmd() *cobra.Command {
	v := Arg{Name: "volume"}
	return group("volume", "Persistent volumes shared between machines",
		`A volume outlives any one machine. It attaches to one machine at a time for
writing (several for reading), and keeps its data when detached.
<volume> is a name or an id.

  miosa volume create data --size-gb 5
  miosa volume attach data my-box --mount /data
  miosa volume attachments data
  miosa volume detach data my-box`, []string{"volumes"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List volumes", Method: "GET", Path: "/volumes", Cols: volumeCols,
			Flags: []Flag{{Name: "deleted", Type: "bool", Usage: "Include deleted volumes"}},
		},
		Op{
			Use: "create <name>", Short: "Create a volume", Method: "POST", Path: "/volumes", Args: []Arg{{Name: "name"}},
			Long: "Creates a volume of the given size on local disk (the only backend). A volume is formatted when it is first attached.",
			Flags: []Flag{
				{Name: "size-gb", Usage: "Size in GB (fractions allowed)", Required: true, Field: "size_gb"},
				{Name: "workspace", Usage: "Workspace slug or id", Field: "workspace_id"},
			},
			Before: func(cmd *cobra.Command, c *api.Client) error {
				ws, _ := cmd.Flags().GetString("workspace")
				if ws == "" {
					return nil
				}
				id, err := resolveWorkspaceRef(cmd.Context(), c, ws)
				if err != nil {
					return err
				}
				return cmd.Flags().Set("workspace", id)
			},
			// Bytes and the local backend, never the size_gb alias or an implied
			// backend: every server version understands these.
			Body: func(a []string, body map[string]any) (map[string]any, error) {
				bytes, err := volumeSizeBytes(fmt.Sprint(body["size_gb"]))
				if err != nil {
					return nil, err
				}
				delete(body, "size_gb")
				body["name"], body["size_bytes"], body["backend"] = a[0], bytes, "local"
				return body, nil
			},
			Cols: nil, Detail: []Col{
				{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Status", Path: "status"},
				{Head: "Size", Path: "size_bytes", Fmt: "bytes"}, {Head: "Backend", Path: "backend"},
			},
		},
		Op{
			Use: "get <volume>", Aliases: []string{"show"}, Short: "Show a volume", Method: "GET", Path: "/volumes/{0}",
			Args: []Arg{v}, ResolveArgs: resolveVolumeArgs,
		},
		Op{
			Use: "rm <volume>", Aliases: []string{"delete"}, Short: "Delete a volume", Method: "DELETE", Path: "/volumes/{0}",
			Args: []Arg{v}, ResolveArgs: resolveVolumeArgs,
			Confirm: "Permanently delete volume {0} and its data?", Done: "Deleted {0}",
		},
		Op{
			Use: "attach <volume> <machine>", Short: "Attach a volume to a machine", Method: "POST",
			Path: "/computers/{1}/volumes", PathFor: machineVolumesPath,
			Args:        []Arg{v, {Name: "machine", Complete: "sandbox"}},
			ResolveArgs: resolveVolumeAndMachine,
			Flags: []Flag{
				{Name: "mount", Usage: "Where to mount it in the machine", Required: true, Field: "mount_path"},
				{Name: "read-only", Type: "bool", Usage: "Mount read-only (several machines may share it)", Field: "read_only"},
			},
			Body: func(a []string, body map[string]any) (map[string]any, error) {
				body["volume_id"] = a[0]
				return body, nil
			},
			Done: "Attached {0} to {1}",
		},
		Op{
			Use: "detach <volume> <machine>", Short: "Detach a volume from a machine", Method: "DELETE",
			Path: "/computers/{1}/volumes/{2}", PathFor: machineVolumesPath,
			Args:        []Arg{v, {Name: "machine", Complete: "sandbox"}},
			ResolveArgs: resolveDetachArgs,
			Done:        "Detached {0} from {1}",
		},
		Op{
			Use: "snapshot <volume>", Short: "Take a snapshot of a volume now", Method: "POST",
			Path: "/volumes/{0}/snapshots", Args: []Arg{v}, ResolveArgs: resolveVolumeArgs,
			Long:   "Copies the volume's backing file on the host that holds it. A volume attached for writing is copied by an atomic clone, so the image is consistent. A volume that was never attached has nothing to snapshot.",
			Detail: volumeSnapshotDetail,
		},
		Op{
			Use: "snapshots <volume>", Short: "List a volume's snapshots", Method: "GET",
			Path: "/volumes/{0}/snapshots", Args: []Arg{v}, ResolveArgs: resolveVolumeArgs, Cols: volumeSnapshotCols,
		},
		Op{
			Use: "snapshot-rm <snapshot>", Short: "Delete a snapshot", Method: "DELETE",
			Path: "/volume-snapshots/{0}", Args: []Arg{{Name: "snapshot"}}, ResolveArgs: resolveSnapshotArgs,
			Confirm: "Permanently delete snapshot {0}?", Done: "Deleted {0}",
		},
		Op{
			Use: "fork <snapshot>", Short: "Create a new volume from a snapshot", Method: "POST",
			Path: "/volume-snapshots/{0}/restore", Args: []Arg{{Name: "snapshot"}}, ResolveArgs: resolveSnapshotArgs,
			Flags: []Flag{{Name: "to-new", Usage: "Name of the new volume", Required: true, Field: "name"}},
			Detail: []Col{
				{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Status", Path: "status"},
				{Head: "Size", Path: "size_bytes", Fmt: "bytes"},
			},
		},
		Op{
			Use: "attachments <volume>", Short: "Where a volume is attached", Method: "GET", Path: "/volumes/{0}/attachments",
			Args: []Arg{v}, ResolveArgs: resolveVolumeArgs, Cols: volumeAttachmentCols,
			Flags: []Flag{{Name: "all", Type: "bool", Usage: "Include past attachments"}},
		},
	)
}

// volumeSizeBytes reads --size-gb as whole bytes; 0.5 is half a gigabyte.
func volumeSizeBytes(raw string) (int64, error) {
	gb, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(gb) || math.IsInf(gb, 0) || gb <= 0 {
		return 0, fmt.Errorf("--size-gb must be a number above zero (got %q)", raw)
	}
	return int64(math.Round(gb * bytesPerGB)), nil
}

func resolveWorkspaceRef(ctx context.Context, c *api.Client, ref string) (string, error) {
	if api.IsUUID(ref) {
		return ref, nil
	}
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/workspaces", nil, &out); err != nil {
		return "", err
	}
	for _, w := range out.Data {
		if w.Slug == ref || w.Name == ref {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("workspace %q not found (see 'miosa workspace list')", ref)
}

type volumeRow struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// resolveVolume turns a volume name, id or the short id the CLI prints into the
// full id. Deleted volumes are never matched by name: the name is free again.
func resolveVolume(ctx context.Context, c *api.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if api.IsUUID(ref) {
		return ref, nil
	}
	var out struct {
		Data []volumeRow `json:"data"`
	}
	if err := c.Get(ctx, "/volumes", nil, &out); err != nil {
		return "", err
	}
	var prefix []string
	for _, v := range out.Data {
		if v.Name == ref {
			return v.ID, nil
		}
		if len(ref) >= 4 && strings.HasPrefix(v.ID, ref) {
			prefix = append(prefix, v.ID)
		}
	}
	switch len(prefix) {
	case 1:
		return prefix[0], nil
	case 0:
		return "", fmt.Errorf("no volume named %q (see 'miosa volume list')", ref)
	default:
		return "", fmt.Errorf("%q matches %d volumes; use a longer id", ref, len(prefix))
	}
}

func resolveVolumeArgs(ctx context.Context, c *api.Client, args []string) ([]string, error) {
	id, err := resolveVolume(ctx, c, args[0])
	if err != nil {
		return nil, err
	}
	return append([]string{id}, args[1:]...), nil
}

// machineKind prefixes a resolved machine id with the route family it lives in.
const (
	sandboxPrefix  = "sandboxes:"
	computerPrefix = "computers:"
)

// resolveMachine finds the sandbox or computer a person named. A sandbox name
// wins where both exist, as it does for exec.
func resolveMachine(ctx context.Context, c *api.Client, ref string) (string, error) {
	if api.IsUUID(ref) {
		if err := c.Get(ctx, "/computers/"+url.PathEscape(ref), nil, nil); err == nil {
			return computerPrefix + ref, nil
		}
		return sandboxPrefix + ref, nil
	}
	if id, err := c.ResolveSandbox(ctx, ref); err == nil && id != ref {
		return sandboxPrefix + id, nil
	}
	if id, err := c.ResolveComputer(ctx, ref); err == nil && id != ref {
		return computerPrefix + id, nil
	}
	return "", fmt.Errorf("no sandbox or computer named %q", ref)
}

func resolveVolumeAndMachine(ctx context.Context, c *api.Client, args []string) ([]string, error) {
	vid, err := resolveVolume(ctx, c, args[0])
	if err != nil {
		return nil, err
	}
	m, err := resolveMachine(ctx, c, args[1])
	if err != nil {
		return nil, err
	}
	return []string{vid, m}, nil
}

// resolveDetachArgs also finds the attachment id: detach is addressed by it.
func resolveDetachArgs(ctx context.Context, c *api.Client, args []string) ([]string, error) {
	resolved, err := resolveVolumeAndMachine(ctx, c, args)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID         string `json:"id"`
			ComputerID string `json:"computer_id"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/volumes/"+url.PathEscape(resolved[0])+"/attachments", nil, &out); err != nil {
		return nil, err
	}
	machine := strings.TrimPrefix(strings.TrimPrefix(resolved[1], sandboxPrefix), computerPrefix)
	for _, a := range out.Data {
		if a.ComputerID == machine {
			return append(resolved, a.ID), nil
		}
	}
	return nil, fmt.Errorf("volume %q is not attached to %q (see 'miosa volume attachments %s')", args[0], args[1], args[0])
}

// machineVolumesPath is the attach / detach route for the machine's family.
func machineVolumesPath(args []string) string {
	machine := ""
	if len(args) > 1 {
		machine = args[1]
	}
	family, id := "computers", machine
	switch {
	case strings.HasPrefix(machine, sandboxPrefix):
		family, id = "sandboxes", strings.TrimPrefix(machine, sandboxPrefix)
	case strings.HasPrefix(machine, computerPrefix):
		id = strings.TrimPrefix(machine, computerPrefix)
	}
	path := "/" + family + "/" + url.PathEscape(id) + "/volumes"
	if len(args) > 2 && args[2] != "" {
		path += "/" + url.PathEscape(args[2])
	}
	return path
}

// resolveSnapshot turns a snapshot id or the short id the CLI prints into the
// full id, searching the snapshots of every volume in the organization.
func resolveSnapshot(ctx context.Context, c *api.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if api.IsUUID(ref) {
		return ref, nil
	}
	if len(ref) < 4 {
		return "", fmt.Errorf("snapshot %q is too short; use at least 4 characters of its id", ref)
	}
	var vols struct {
		Data []volumeRow `json:"data"`
	}
	if err := c.Get(ctx, "/volumes", nil, &vols); err != nil {
		return "", err
	}
	var matches []string
	for _, v := range vols.Data {
		var snaps struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := c.Get(ctx, "/volumes/"+url.PathEscape(v.ID)+"/snapshots", nil, &snaps); err != nil {
			continue
		}
		for _, s := range snaps.Data {
			if strings.HasPrefix(s.ID, ref) {
				matches = append(matches, s.ID)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no snapshot starts with %q (see 'miosa volume snapshots <volume>')", ref)
	default:
		return "", fmt.Errorf("%q matches %d snapshots; use a longer id", ref, len(matches))
	}
}

func resolveSnapshotArgs(ctx context.Context, c *api.Client, args []string) ([]string, error) {
	id, err := resolveSnapshot(ctx, c, args[0])
	if err != nil {
		return nil, err
	}
	return append([]string{id}, args[1:]...), nil
}
