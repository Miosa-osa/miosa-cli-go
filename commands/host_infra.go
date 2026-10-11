package commands

import (
	"github.com/spf13/cobra"
)

// hostExtraGroups returns the OpenComputers (bring-your-own-host) resource
// groups that hang off `miosa host`. They are separate from hosts.go only to
// keep that file readable.
func hostExtraGroups(host Arg) []*cobra.Command {
	return []*cobra.Command{
		hostGhaRunnerGroup(host),
		hostMeshGroup(),
		hostBackupGroup(host),
	}
}

// ─── gha-runner ───────────────────────────────────────────────────────────────

var ghaRunnerCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"},
	{Head: "REPO", Path: "repo_url"}, {Head: "LABELS", Path: "labels", Fmt: "list"}, {Head: "EPHEMERAL", Path: "ephemeral"},
	{Head: "JOB", Path: "current_job"}, {Head: "SEEN", Path: "last_activity_at", Fmt: "age"},
}

var ghaRunnerDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "State", Path: "state"}, {Head: "Repo", Path: "repo_url"},
	{Head: "Labels", Path: "labels", Fmt: "list"}, {Head: "Ephemeral", Path: "ephemeral"},
	{Head: "GitHub runner id", Path: "gh_runner_id"}, {Head: "Version", Path: "installed_version"},
	{Head: "Current job", Path: "current_job"}, {Head: "Host", Path: "host_id"},
	{Head: "Last activity", Path: "last_activity_at", Fmt: "age"}, {Head: "Created", Path: "created_at", Fmt: "age"},
}

// hostGhaRunnerGroup registers self-hosted GitHub Actions runners on a host.
func hostGhaRunnerGroup(host Arg) *cobra.Command {
	runner := Arg{Name: "runner"}
	g := group("gha-runner", "Self-hosted GitHub Actions runners on a host",
		`A runner is a GitHub Actions runner the host keeps registered for one
repository. Create one with a token; MIOSA stores it and starts the runner.

  miosa host gha-runner list <host>
  miosa host gha-runner new <host> --repo https://github.com/acme/app --token <token>
  miosa host gha-runner events <host> <runner>`, []string{"gha-runners", "runner", "runners"},
		Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List runners on a host", Method: "GET", Path: "/opencomputers/hosts/{0}/gha-runners", Args: []Arg{host}, ListKey: "runners", Cols: ghaRunnerCols},
		Op{Use: "get <host> <runner>", Aliases: []string{"show"}, Short: "Show one runner", Method: "GET", Path: "/opencomputers/hosts/{0}/gha-runners/{1}", Args: []Arg{host, runner}, Detail: ghaRunnerDetail},
		Op{
			Use: "new <host>", Aliases: []string{"create", "add"}, Short: "Register a runner for a repository", Method: "POST", Path: "/opencomputers/hosts/{0}/gha-runners", Args: []Arg{host},
			Flags: []Flag{
				{Name: "repo", Usage: "Repository URL, for example https://github.com/acme/app", Field: "repo_url", Required: true},
				{Name: "name", Usage: "Runner name (default: generated)"},
				{Name: "label", Usage: "Runner label (repeatable)", Type: "strings", Field: "labels"},
				{Name: "ephemeral", Type: "bool", Usage: "Deregister the runner after one job"},
				{Name: "token", Usage: "GitHub registration token (sent to the host, not stored)", Field: "pat_value"},
				{Name: "token-secret", Usage: "Name of a host secret holding the token", Field: "pat_secret_id"},
			},
		},
		Op{Use: "rm <host> <runner>", Aliases: []string{"delete"}, Short: "Deregister a runner", Method: "DELETE", Path: "/opencomputers/hosts/{0}/gha-runners/{1}", Args: []Arg{host, runner}, Confirm: "Deregister runner {1}?", Done: "Deregistered {1}"},
		Op{Use: "refresh <host> <runner>", Short: "Reinstall and restart a runner", Method: "POST", Path: "/opencomputers/hosts/{0}/gha-runners/{1}/refresh", Args: []Arg{host, runner}, Done: "Refreshed {1}"},
	)
	g.AddCommand(newGhaRunnerEventsCmd(host, runner))
	return g
}

func newGhaRunnerEventsCmd(host, runner Arg) *cobra.Command {
	return &cobra.Command{
		Use:   "events <host> <runner>",
		Short: "Stream a runner's activity events",
		Long: `Stream events as the runner picks up and finishes jobs. Ctrl-C stops.
With --json each event is one JSON object per line.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return streamHostEvents(cmd, "/opencomputers/hosts/"+args[0]+"/gha-runners/"+args[1]+"/events")
		},
	}
}

// ─── mesh ─────────────────────────────────────────────────────────────────────

var meshCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"},
	{Head: "CIDR", Path: "cidr"}, {Head: "PORT BASE", Path: "listen_port_base"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"},
}

var meshDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "State", Path: "state"}, {Head: "CIDR", Path: "cidr"},
	{Head: "Listen port base", Path: "listen_port_base"}, {Head: "PSK rotated", Path: "preshared_key_rotated_at", Fmt: "age"},
	{Head: "Hosts", Path: "peers", Fmt: "count"}, {Head: "Created", Path: "inserted_at", Fmt: "age"},
}

// hostMeshGroup registers WireGuard mesh networks: private encrypted
// host-to-host links between your hosts.
func hostMeshGroup() *cobra.Command {
	mesh := Arg{Name: "mesh"}
	g := group("mesh", "Private encrypted host-to-host networks (WireGuard)",
		`A mesh links several of your hosts on a private encrypted network so they can
reach each other without going over the public internet.

  miosa host mesh list
  miosa host mesh new lab --host <host> --host <host>
  miosa host mesh get lab`, []string{"meshes"},
		Op{Use: "list", Aliases: []string{"ls"}, Short: "List meshes", Method: "GET", Path: "/opencomputers/meshes", ListKey: "meshes", Cols: meshCols},
		Op{Use: "get <mesh>", Aliases: []string{"show"}, Short: "Show one mesh and its hosts", Method: "GET", Path: "/opencomputers/meshes/{0}", Args: []Arg{mesh}, Detail: meshDetail},
		Op{
			Use: "new <name>", Aliases: []string{"create"}, Short: "Create a mesh", Method: "POST", Path: "/opencomputers/meshes", Args: []Arg{{Name: "name"}},
			Flags: []Flag{{Name: "host", Usage: "Host to add to the mesh (repeatable)", Type: "strings", Field: "host_ids"}},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				body["name"] = args[0]
				return body, nil
			},
			Detail: meshDetail,
		},
		Op{Use: "add-host <mesh> <host>", Short: "Add a host to a mesh", Method: "POST", Path: "/opencomputers/meshes/{0}/hosts/{1}", Args: []Arg{mesh, {Name: "host"}}, Done: "Added {1} to {0}"},
		Op{Use: "remove-host <mesh> <host>", Short: "Remove a host from a mesh", Method: "DELETE", Path: "/opencomputers/meshes/{0}/hosts/{1}", Args: []Arg{mesh, {Name: "host"}}, Confirm: "Remove host {1} from mesh {0}?", Done: "Removed {1} from {0}"},
		Op{Use: "rm <mesh>", Aliases: []string{"delete"}, Short: "Delete a mesh", Method: "DELETE", Path: "/opencomputers/meshes/{0}", Args: []Arg{mesh}, Confirm: "Delete mesh {0}?", Done: "Deleted {0}"},
	)
	g.AddCommand(newMeshEventsCmd(mesh))
	return g
}

func newMeshEventsCmd(mesh Arg) *cobra.Command {
	return &cobra.Command{
		Use:   "events <mesh>",
		Short: "Stream a mesh's events",
		Long:  `Stream events (host joins, key rotation, state changes) as they happen. Ctrl-C stops.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return streamHostEvents(cmd, "/opencomputers/meshes/"+args[0]+"/events")
		},
	}
}

// ─── backup ───────────────────────────────────────────────────────────────────

var backupConfigCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"},
	{Head: "PATH", Path: "path"}, {Head: "SCHEDULE", Path: "schedule_cron"}, {Head: "KEEP", Path: "retention_count"},
	{Head: "LAST", Path: "last_snapshot_at", Fmt: "age"},
}

var backupConfigDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "State", Path: "state"}, {Head: "Path", Path: "path"},
	{Head: "Schedule", Path: "schedule_cron"}, {Head: "Timezone", Path: "timezone"},
	{Head: "Keep (count)", Path: "retention_count"}, {Head: "Keep (days)", Path: "retention_days"},
	{Head: "Exclude", Path: "exclude_patterns", Fmt: "list"}, {Head: "Compression", Path: "compression_level"},
	{Head: "Last snapshot", Path: "last_snapshot_at", Fmt: "age"}, {Head: "Last size", Path: "last_snapshot_size_bytes", Fmt: "bytes"},
	{Head: "Host", Path: "host_id"}, {Head: "Created", Path: "created_at", Fmt: "age"},
}

var backupSnapshotCols = []Col{
	{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "SIZE", Path: "compressed_bytes", Fmt: "bytes"},
	{Head: "CHUNKS", Path: "chunk_count"}, {Head: "STARTED", Path: "started_at", Fmt: "age"}, {Head: "FINISHED", Path: "completed_at", Fmt: "age"},
	{Head: "ERROR", Path: "error"},
}

var backupSnapshotDetail = []Col{
	{Head: "Id", Path: "id"}, {Head: "State", Path: "state"}, {Head: "Config", Path: "config_id"}, {Head: "Host", Path: "host_id"},
	{Head: "Total size", Path: "total_bytes", Fmt: "bytes"}, {Head: "Compressed", Path: "compressed_bytes", Fmt: "bytes"},
	{Head: "Chunks", Path: "chunk_count"}, {Head: "Started", Path: "started_at", Fmt: "age"},
	{Head: "Finished", Path: "completed_at", Fmt: "age"}, {Head: "Error", Path: "error"},
}

// hostBackupGroup registers host backup configurations and their snapshots.
func hostBackupGroup(host Arg) *cobra.Command {
	config := Arg{Name: "config"}
	snapshot := Arg{Name: "snapshot"}
	g := group("backup", "Scheduled backups of directories on a host",
		`A backup configuration names a directory on a host, a schedule and how many
snapshots to keep. A snapshot can be forked onto a host.

  miosa host backup list <host>
  miosa host backup new <host> --name nightly --path /data --schedule "0 3 * * *"
  miosa host backup trigger <host> nightly
  miosa host backup snapshot list <host> nightly`, []string{"backups"},
		Op{Use: "list <host>", Aliases: []string{"ls"}, Short: "List backup configurations", Method: "GET", Path: "/opencomputers/hosts/{0}/backup-configs", Args: []Arg{host}, ListKey: "backup_configs", Cols: backupConfigCols},
		Op{
			Use: "new <host>", Aliases: []string{"create"}, Short: "Create a backup configuration", Method: "POST", Path: "/opencomputers/hosts/{0}/backup-configs", Args: []Arg{host},
			Flags: []Flag{
				{Name: "name", Usage: "A name for the configuration", Required: true},
				{Name: "path", Usage: "Directory on the host to back up", Required: true},
				{Name: "schedule", Usage: "Schedule expression, for example \"0 3 * * *\"", Field: "schedule_cron"},
				{Name: "timezone", Usage: "Timezone the schedule is read in"},
				{Name: "keep", Usage: "How many snapshots to keep", Type: "int", Field: "retention_count"},
				{Name: "keep-days", Usage: "How many days to keep snapshots", Type: "int", Field: "retention_days"},
				{Name: "exclude", Usage: "Glob to exclude (repeatable)", Type: "strings", Field: "exclude_patterns"},
				{Name: "compression", Usage: "Compression level 0-9", Type: "int", Field: "compression_level"},
			},
			Detail: backupConfigDetail,
		},
		Op{
			Use: "update <host> <config>", Short: "Change a backup configuration", Method: "PATCH", Path: "/opencomputers/hosts/{0}/backup-configs/{1}", Args: []Arg{host, config},
			Flags: []Flag{
				{Name: "name", Usage: "A name for the configuration"},
				{Name: "path", Usage: "Directory on the host to back up"},
				{Name: "schedule", Usage: "Schedule expression", Field: "schedule_cron"},
				{Name: "timezone", Usage: "Timezone the schedule is read in"},
				{Name: "keep", Usage: "How many snapshots to keep", Type: "int", Field: "retention_count"},
				{Name: "keep-days", Usage: "How many days to keep snapshots", Type: "int", Field: "retention_days"},
				{Name: "exclude", Usage: "Glob to exclude (repeatable)", Type: "strings", Field: "exclude_patterns"},
				{Name: "compression", Usage: "Compression level 0-9", Type: "int", Field: "compression_level"},
			},
			Detail: backupConfigDetail,
		},
		Op{Use: "pause <host> <config>", Short: "Pause a backup's schedule", Method: "POST", Path: "/opencomputers/hosts/{0}/backup-configs/{1}/pause", Args: []Arg{host, config}, Done: "Paused {1}"},
		Op{Use: "resume <host> <config>", Short: "Resume a backup's schedule", Method: "POST", Path: "/opencomputers/hosts/{0}/backup-configs/{1}/resume", Args: []Arg{host, config}, Done: "Resumed {1}"},
		Op{Use: "trigger <host> <config>", Short: "Take a snapshot now", Method: "POST", Path: "/opencomputers/hosts/{0}/backup-configs/{1}/trigger", Args: []Arg{host, config}, Done: "Started a snapshot of {1}"},
		Op{Use: "rm <host> <config>", Aliases: []string{"delete"}, Short: "Delete a backup configuration", Method: "DELETE", Path: "/opencomputers/hosts/{0}/backup-configs/{1}", Args: []Arg{host, config}, Confirm: "Delete backup configuration {1}? Its snapshots stay.", Done: "Deleted {1}"},
	)
	snapshots := group("snapshot", "Snapshots taken by a backup configuration", "", []string{"snapshots"},
		Op{
			Use: "list <host> <config>", Aliases: []string{"ls"}, Short: "List a configuration's snapshots", Method: "GET", Path: "/opencomputers/hosts/{0}/backup-configs/{1}/snapshots", Args: []Arg{host, config},
			ListKey: "snapshots", Cols: backupSnapshotCols, Flags: []Flag{{Name: "limit", Type: "int", Usage: "How many"}, {Name: "before", Usage: "Only snapshots started before this time"}},
		},
		Op{Use: "get <snapshot>", Aliases: []string{"show"}, Short: "Show one snapshot", Method: "GET", Path: "/opencomputers/snapshots/{0}", Args: []Arg{snapshot}, Detail: backupSnapshotDetail},
		Op{Use: "rm <snapshot>", Aliases: []string{"delete"}, Short: "Delete a snapshot", Method: "DELETE", Path: "/opencomputers/snapshots/{0}", Args: []Arg{snapshot}, Confirm: "Delete snapshot {0}?", Done: "Deleted {0}"},
		forkSnapshotOp(snapshot),
	)
	snapshots.AddCommand(legacyForkSnapshotCmd(snapshot))
	g.AddCommand(snapshots)
	return g
}

// forkSnapshotOp writes a snapshot's files onto a host: the "fork" of a backup.
func forkSnapshotOp(snapshot Arg) Op {
	return Op{
		Use: "fork <snapshot>", Short: "Write a snapshot's files onto a host", Method: "POST", Path: "/opencomputers/snapshots/{0}/restore", Args: []Arg{snapshot}, FileBody: true,
		Flags:   []Flag{{Name: "to-host", Usage: "Host to write onto (default: the original)", Field: "target_host_id"}, {Name: "to-path", Usage: "Path to write into", Field: "target_path"}, {Name: "overwrite", Type: "bool", Usage: "Overwrite files that already exist"}},
		Confirm: "Fork snapshot {0}? It writes files onto the target host.",
	}
}

// legacyForkSnapshotCmd keeps the retired `restore` name working, hidden from
// help and the docs, for anyone used to the 1.x command.
func legacyForkSnapshotCmd(snapshot Arg) *cobra.Command {
	legacy := forkSnapshotOp(snapshot).command("snapshot")
	legacy.Use = "restore <snapshot>"
	legacy.Aliases = nil
	legacy.Hidden = true
	legacy.Deprecated = "use 'miosa host backup snapshot fork'"
	legacy.Short = "Write a snapshot's files onto a host"
	return legacy
}
