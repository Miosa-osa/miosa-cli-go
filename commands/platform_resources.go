package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(
		deployCmd(), dbCmd(), storageCmd(), volumeCmd(), workflowCmd(), scheduleCmd(), legacyCronCmd(), functionCmd(),
		templateCmd(), projectCmd(), billingCmd(), auditCmd(), alertsCmd(), notificationsCmd(),
		memberCmd(), egressCmd(), regionsCmd(), cloudCmd(),
	)
}

func idArg(name string) Arg { return Arg{Name: name} }

// ─── deploy ───────────────────────────────────────────────────────────────────

var deployCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"},
	{Head: "URL", Path: "public_url"}, {Head: "SOURCE", Path: "source_type"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"},
}

var deployDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "State", Path: "state"}, {Head: "URL", Path: "public_url"},
	{Head: "Source", Path: "source_type"}, {Head: "Repo", Path: "repo_url"}, {Head: "Branch", Path: "branch"},
	{Head: "Build", Path: "build_command"}, {Head: "Run", Path: "run_command"}, {Head: "Auto deploy", Path: "auto_deploy"},
	{Head: "Active version", Path: "active_version_id"}, {Head: "Active release", Path: "active_release_id"},
	{Head: "Workspace", Path: "workspace_id"}, {Head: "Created", Path: "created_at", Fmt: "age"},
}

func deployCmd() *cobra.Command {
	d := idArg("deployment")
	return group("deploy", "Inspect and operate deployments",
		`Deployments are the running, versioned apps published from sandboxes, git or
uploads. <deployment> is a name, slug or id.

  miosa deploy list
  miosa deploy get my-app
  miosa deploy logs my-app -n 100
  miosa deploy rollback my-app --to <version-id>
  miosa deploy domains my-app`, []string{"deployments", "deployment"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List deployments",
			Method: "GET", Path: "/deployments", Cols: deployCols,
			Flags: []Flag{{Name: "workspace-id", Usage: "Only this workspace", Field: "workspace_id"}, {Name: "limit", Short: "n", Type: "int", Usage: "How many"}},
		},
		Op{
			Use: "get <deployment>", Aliases: []string{"show"}, Short: "Show a deployment",
			Method: "GET", Path: "/deployments/{0}", Args: []Arg{d}, Detail: deployDetail,
		},
		Op{
			Use: "logs <deployment>", Short: "Show the running app's log",
			Method: "GET", Path: "/deployments/{0}/logs", Args: []Arg{d}, Lines: "data.lines",
			Flags: []Flag{{Name: "lines", Short: "n", Type: "int", Usage: "Number of lines"}},
		},
		Op{
			Use: "requests <deployment>", Short: "Show recent HTTP requests",
			Method: "GET", Path: "/deployments/{0}/request-logs", Args: []Arg{d}, ListKey: "logs",
			Cols: []Col{{Head: "TIME", Path: "timestamp", Fmt: "age"}, {Head: "METHOD", Path: "method"}, {Head: "PATH", Path: "path"}, {Head: "STATUS", Path: "status"}, {Head: "MS", Path: "duration_ms"}},
		},
		Op{
			Use: "observe <deployment>", Aliases: []string{"metrics"}, Short: "Invocations, errors and latency",
			Method: "GET", Path: "/deployments/{0}/observability", Args: []Arg{d},
			Detail: []Col{
				{Head: "Invocations", Path: "summary.invocations"}, {Head: "Errors", Path: "summary.error_count"}, {Head: "Error rate", Path: "summary.error_rate"},
				{Head: "p50 (ms)", Path: "summary.duration_ms.p50"}, {Head: "p95 (ms)", Path: "summary.duration_ms.p95"}, {Head: "Cold start rate", Path: "summary.cold_start_rate"},
			},
		},
		Op{
			Use: "versions <deployment>", Short: "List versions",
			Method: "GET", Path: "/deployments/{0}/versions", Args: []Arg{d},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "SOURCE", Path: "metadata.publish_source"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}},
		},
		Op{
			Use: "releases <deployment>", Short: "List releases",
			Method: "GET", Path: "/deployments/{0}/releases", Args: []Arg{d},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "PORT", Path: "port"}, {Head: "READY", Path: "ready_at", Fmt: "age"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}},
		},
		Op{
			Use: "promote <deployment> <release>", Short: "Make a release the live one",
			Method: "POST", Path: "/deployments/{0}/releases/{1}/promote", Args: []Arg{d, idArg("release")}, Done: "Promoted release {1}",
		},
		Op{
			Use: "rollback <deployment>", Short: "Return to an earlier version",
			Method: "POST", Path: "/deployments/{0}/rollback", Args: []Arg{d}, Confirm: "Roll {0} back?", Done: "Rolled back {0}",
			Flags: []Flag{{Name: "to", Usage: "Version id to return to (default: the previous one)", Field: "version_id"}},
		},
		Op{
			Use: "redeploy <deployment>", Short: "Build and release again from the same source",
			Method: "POST", Path: "/deployments/{0}/redeploy", Args: []Arg{d}, Done: "Redeploying {0}",
		},
		Op{
			Use: "stop <deployment>", Short: "Stop a deployment",
			Method: "POST", Path: "/deployments/{0}/stop", Args: []Arg{d}, Confirm: "Stop deployment {0}? It stops serving traffic.", Done: "Stopped {0}",
		},
		Op{
			Use: "rm <deployment>", Aliases: []string{"delete"}, Short: "Delete a deployment",
			Method: "DELETE", Path: "/deployments/{0}", Args: []Arg{d}, Confirm: "Permanently delete deployment {0}?", Done: "Deleted {0}",
		},
		Op{
			Use: "builds <deployment>", Short: "List builds",
			Method: "GET", Path: "/deployments/{0}/builds", Args: []Arg{d}, ListKey: "data",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "CREATED", Path: "inserted_at", Fmt: "age"}},
		},
		Op{
			Use: "env <deployment>", Short: "List the deployment's environment variable names",
			Method: "GET", Path: "/deployments/{0}/env", Args: []Arg{d}, ListKey: "env_vars",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"}},
		},
		Op{
			Use: "domains <deployment>", Short: "List custom domains",
			Method: "GET", Path: "/deployments/{0}/domains", Args: []Arg{d},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "HOSTNAME", Path: "hostname"}, {Head: "STATUS", Path: "status"}, {Head: "VERIFIED", Path: "verified"}},
		},
		Op{
			Use: "domain-add <deployment> <hostname>", Short: "Attach a custom domain (then add the DNS record it shows)",
			Method: "POST", Path: "/deployments/{0}/domains", Args: []Arg{d, idArg("hostname")},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				body["hostname"] = args[1]
				return body, nil
			},
		},
		Op{
			Use: "domain-verify <deployment> <domain-id>", Short: "Check a domain's DNS",
			Method: "POST", Path: "/deployments/{0}/domains/{1}/verify", Args: []Arg{d, idArg("domain")},
		},
		Op{
			Use: "domain-rm <deployment> <domain-id>", Short: "Detach a custom domain",
			Method: "DELETE", Path: "/deployments/{0}/domains/{1}", Args: []Arg{d, idArg("domain")}, Confirm: "Detach domain {1}?", Done: "Detached {1}",
		},
	)
}

// ─── db ───────────────────────────────────────────────────────────────────────

var dbCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "ENGINE", Path: "engine"}, {Head: "STATE", Path: "state"},
	{Head: "MEMORY (MB)", Path: "memory_mb"}, {Head: "REGION", Path: "region"}, {Head: "CREATED", Path: "created_at", Fmt: "age"},
}

func dbCmd() *cobra.Command {
	d := idArg("database")
	return group("db", "Managed databases",
		`Databases run on MIOSA and bill while running. <database> is a name or id.
'connection' prints the connection secret: treat the output like a password.`, []string{"database", "databases"},
		Op{Use: "list", Aliases: []string{"ls"}, Short: "List databases", Method: "GET", Path: "/databases", Cols: dbCols},
		Op{
			Use: "get <database>", Aliases: []string{"show"}, Short: "Show a database", Method: "GET", Path: "/databases/{0}", Args: []Arg{d},
			Detail: []Col{{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Engine", Path: "engine"}, {Head: "State", Path: "state"}, {Head: "Host", Path: "host"}, {Head: "Port", Path: "port"}, {Head: "Memory (MB)", Path: "memory_mb"}, {Head: "Error", Path: "last_error"}},
		},
		Op{Use: "start <database>", Short: "Start a stopped database", Method: "POST", Path: "/databases/{0}/start", Args: []Arg{d}, Done: "Starting {0}"},
		Op{Use: "stop <database>", Short: "Stop a database", Method: "POST", Path: "/databases/{0}/stop", Args: []Arg{d}, Confirm: "Stop database {0}?", Done: "Stopping {0}"},
		Op{Use: "restart <database>", Short: "Restart a database", Method: "POST", Path: "/databases/{0}/restart", Args: []Arg{d}, Done: "Restarting {0}"},
		Op{Use: "wake <database>", Short: "Wake a database that scaled to zero", Method: "POST", Path: "/databases/{0}/wake", Args: []Arg{d}, Done: "Waking {0}"},
		Op{Use: "connection <database>", Aliases: []string{"url"}, Short: "Show how to connect (a secret)", Method: "GET", Path: "/databases/{0}/credentials", Args: []Arg{d}},
		Op{Use: "logs <database>", Short: "Show the database log", Method: "GET", Path: "/databases/{0}/logs", Args: []Arg{d}, Lines: "lines"},
		Op{
			Use: "backups <database>", Short: "List backups", Method: "GET", Path: "/databases/{0}/backups", Args: []Arg{d},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "SIZE", Path: "size_bytes", Fmt: "bytes"}, {Head: "CREATED", Path: "inserted_at", Fmt: "age"}},
		},
		Op{Use: "backup <database>", Short: "Take a backup now", Method: "POST", Path: "/databases/{0}/backups", Args: []Arg{d}},
		Op{Use: "rm <database>", Aliases: []string{"delete"}, Short: "Permanently delete a database", Method: "DELETE", Path: "/databases/{0}", Args: []Arg{d}, Confirm: "Permanently delete database {0} and its data?", Done: "Deleted {0}"},
	)
}

// ─── storage, volume ──────────────────────────────────────────────────────────

func storageCmd() *cobra.Command {
	b := idArg("bucket")
	return group("storage", "Object storage buckets",
		"Buckets hold objects behind an S3-compatible endpoint. <bucket> is the bucket id.", []string{"bucket", "buckets"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List buckets", Method: "GET", Path: "/storage/buckets",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "OBJECTS", Path: "object_count"}, {Head: "USED", Path: "used_bytes", Fmt: "bytes"}, {Head: "VISIBILITY", Path: "visibility"}, {Head: "REGION", Path: "region"}},
		},
		Op{
			Use: "create <name>", Short: "Create a bucket", Method: "POST", Path: "/storage/buckets", Args: []Arg{idArg("name")},
			Flags: []Flag{{Name: "visibility", Usage: "Who can read objects", Default: "private", Enum: []string{"private", "public"}}, {Name: "region", Usage: "Region"}, {Name: "workspace-id", Usage: "Workspace", Field: "workspace_id"}},
			Body:  func(a []string, body map[string]any) (map[string]any, error) { body["name"] = a[0]; return body, nil },
		},
		Op{Use: "get <bucket>", Aliases: []string{"show"}, Short: "Show a bucket", Method: "GET", Path: "/storage/buckets/{0}", Args: []Arg{b}},
		Op{
			Use: "objects <bucket>", Aliases: []string{"ls-objects"}, Short: "List objects", Method: "GET", Path: "/storage/buckets/{0}/objects", Args: []Arg{b},
			Flags: []Flag{{Name: "prefix", Usage: "Only keys with this prefix"}, {Name: "limit", Short: "n", Type: "int", Usage: "How many"}},
			Cols:  []Col{{Head: "KEY", Path: "key"}, {Head: "SIZE", Path: "size", Fmt: "bytes"}, {Head: "MODIFIED", Path: "last_modified", Fmt: "age"}},
		},
		Op{Use: "rm <bucket>", Aliases: []string{"delete"}, Short: "Delete a bucket", Method: "DELETE", Path: "/storage/buckets/{0}", Args: []Arg{b}, Confirm: "Permanently delete bucket {0}?", Done: "Deleted {0}"},
	)
}

func volumeCmd() *cobra.Command {
	v := idArg("volume")
	return group("volume", "Persistent volumes shared between machines", "Volumes outlive any one machine and can be attached to computers.", []string{"volumes"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List volumes", Method: "GET", Path: "/volumes",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "SIZE", Path: "size_bytes", Fmt: "bytes"}, {Head: "BACKEND", Path: "backend"}},
		},
		Op{
			Use: "create <name>", Short: "Create a volume", Method: "POST", Path: "/volumes", Args: []Arg{idArg("name")},
			Flags: []Flag{{Name: "size-gb", Type: "int", Usage: "Size in GB", Field: "size_gb"}},
			Body:  func(a []string, body map[string]any) (map[string]any, error) { body["name"] = a[0]; return body, nil },
		},
		Op{Use: "get <volume>", Aliases: []string{"show"}, Short: "Show a volume", Method: "GET", Path: "/volumes/{0}", Args: []Arg{v}},
		Op{Use: "rm <volume>", Aliases: []string{"delete"}, Short: "Delete a volume", Method: "DELETE", Path: "/volumes/{0}", Args: []Arg{v}, Confirm: "Permanently delete volume {0} and its data?", Done: "Deleted {0}"},
	)
}

// ─── workflow, cron, function ─────────────────────────────────────────────────

func workflowCmd() *cobra.Command {
	w := idArg("workflow")
	return group("workflow", "Automations made of steps, agents and tools", "<workflow> is a name or id.", []string{"workflows"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List workflows", Method: "GET", Path: "/workflows",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"}},
		},
		Op{Use: "get <workflow>", Aliases: []string{"show"}, Short: "Show a workflow", Method: "GET", Path: "/workflows/{0}", Args: []Arg{w}},
		Op{
			Use: "run <workflow>", Short: "Start a run", Method: "POST", Path: "/workflows/{0}/run", Args: []Arg{w},
			Flags: []Flag{{Name: "input", Usage: "Input as JSON", Field: "input"}},
			Body: func(_ []string, body map[string]any) (map[string]any, error) {
				if s, ok := body["input"].(string); ok {
					v := rawOrString(s)
					body["input"] = v
				}
				return body, nil
			},
		},
		Op{Use: "pause <workflow>", Short: "Pause triggers", Method: "POST", Path: "/workflows/{0}/pause", Args: []Arg{w}, Done: "Paused {0}"},
		Op{Use: "resume <workflow>", Short: "Resume triggers", Method: "POST", Path: "/workflows/{0}/resume", Args: []Arg{w}, Done: "Resumed {0}"},
		Op{
			Use: "runs", Short: "List runs of all workflows", Method: "GET", Path: "/workflows/runs",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "WORKFLOW", Path: "workflow_id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "STARTED", Path: "started_at", Fmt: "age"}},
		},
		Op{Use: "run-get <run>", Short: "Show a workflow run", Method: "GET", Path: "/workflows/runs/{0}", Args: []Arg{idArg("run")}},
		Op{Use: "run-cancel <run>", Short: "Cancel a workflow run", Method: "POST", Path: "/workflows/runs/{0}/cancel", Args: []Arg{idArg("run")}, Done: "Canceled {0}"},
		Op{Use: "rm <workflow>", Aliases: []string{"delete"}, Short: "Delete a workflow", Method: "DELETE", Path: "/workflows/{0}", Args: []Arg{w}, Confirm: "Delete workflow {0}?", Done: "Deleted {0}"},
	)
}

func scheduleCmd() *cobra.Command {
	j := idArg("job")
	return group("schedule", "Scheduled jobs", "A schedule runs a command or function at set times. <job> is a name or id.", []string{"schedules", "schedule-jobs"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List jobs", Method: "GET", Path: "/cron-jobs",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "SCHEDULE", Path: "schedule"}, {Head: "STATUS", Path: "status"}, {Head: "NEXT", Path: "next_run_at"}},
		},
		Op{Use: "get <job>", Aliases: []string{"show"}, Short: "Show a job", Method: "GET", Path: "/cron-jobs/{0}", Args: []Arg{j}},
		Op{Use: "pause <job>", Short: "Pause a job", Method: "POST", Path: "/cron-jobs/{0}/pause", Args: []Arg{j}, Done: "Paused {0}"},
		Op{Use: "resume <job>", Short: "Resume a job", Method: "POST", Path: "/cron-jobs/{0}/resume", Args: []Arg{j}, Done: "Resumed {0}"},
		Op{Use: "run <job>", Aliases: []string{"run-now"}, Short: "Run a job now", Method: "POST", Path: "/cron-jobs/{0}/run-now", Args: []Arg{j}},
		Op{
			Use: "executions <job>", Short: "List past executions", Method: "GET", Path: "/cron-jobs/{0}/executions", Args: []Arg{j},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "STARTED", Path: "started_at", Fmt: "age"}},
		},
		Op{Use: "rm <job>", Aliases: []string{"delete"}, Short: "Delete a job", Method: "DELETE", Path: "/cron-jobs/{0}", Args: []Arg{j}, Confirm: "Delete job {0}?", Done: "Deleted {0}"},
	)
}

func functionCmd() *cobra.Command {
	f := idArg("function")
	return group("function", "Edge functions", "<function> is a name or id.", []string{"functions", "fn"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List functions", Method: "GET", Path: "/functions",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "RUNTIME", Path: "runtime"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"}},
		},
		Op{Use: "get <function>", Aliases: []string{"show"}, Short: "Show a function", Method: "GET", Path: "/functions/{0}", Args: []Arg{f}},
		Op{
			Use: "invoke <function>", Short: "Call a function", Method: "POST", Path: "/functions/{0}/invoke", Args: []Arg{f},
			Flags: []Flag{{Name: "data", Short: "d", Usage: "Payload as JSON", Field: "payload"}},
			Body: func(_ []string, body map[string]any) (map[string]any, error) {
				if s, ok := body["payload"].(string); ok {
					body["payload"] = rawOrString(s)
				}
				return body, nil
			},
		},
		Op{Use: "rm <function>", Aliases: []string{"delete"}, Short: "Delete a function", Method: "DELETE", Path: "/functions/{0}", Args: []Arg{f}, Confirm: "Delete function {0}?", Done: "Deleted {0}"},
	)
}

// ─── template, project ────────────────────────────────────────────────────────

func templateCmd() *cobra.Command {
	t := idArg("template")
	return group("template", "Sandbox templates",
		`Templates are the images sandboxes start from. 'list' shows the catalog you can
create from; 'custom' lists the templates your organization built.

  miosa template list
  miosa template custom
  miosa template builds my-template`, []string{"templates"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List templates you can create sandboxes from", Method: "GET", Path: "/templates",
			Cols: []Col{{Head: "ID", Path: "id"}, {Head: "NAME", Path: "name"}, {Head: "READINESS", Path: "readiness"}, {Head: "DEFAULT SIZE", Path: "default_size"}, {Head: "DESCRIPTION", Path: "description", Fmt: "clip"}},
		},
		Op{
			Use: "custom", Short: "List the templates your organization built", Method: "GET", Path: "/sandbox-templates",
			Cols: []Col{{Head: "ID", Path: "id"}, {Head: "NAME", Path: "name"}, {Head: "STATUS", Path: "status"}, {Head: "PUBLIC", Path: "public"}},
		},
		Op{Use: "get <template>", Aliases: []string{"show"}, Short: "Show a template", Method: "GET", Path: "/sandbox-templates/{0}", Args: []Arg{t}},
		Op{
			Use: "builds <template>", Short: "List a template's builds", Method: "GET", Path: "/sandbox-templates/{0}/builds", Args: []Arg{t},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "CREATED", Path: "inserted_at", Fmt: "age"}},
		},
		Op{Use: "build <template>", Short: "Start a build", Method: "POST", Path: "/sandbox-templates/{0}/builds", Args: []Arg{t}},
		Op{Use: "build-logs <build>", Short: "Show a build's log", Method: "GET", Path: "/sandbox-template-builds/{0}/logs", Args: []Arg{idArg("build")}, Lines: "lines"},
		Op{Use: "promote <template>", Short: "Make the latest build the live one", Method: "POST", Path: "/sandbox-templates/{0}/promote", Args: []Arg{t}, Done: "Promoted {0}"},
		Op{Use: "rollback <template>", Short: "Return to the previous version", Method: "POST", Path: "/sandbox-templates/{0}/rollback", Args: []Arg{t}, Confirm: "Roll template {0} back?", Done: "Rolled back {0}"},
		Op{Use: "warm-pool <template>", Short: "Show how many warm sandboxes are kept ready", Method: "GET", Path: "/sandbox-templates/{0}/warm-pool", Args: []Arg{t}},
		Op{Use: "rm <template>", Aliases: []string{"delete"}, Short: "Delete a custom template", Method: "DELETE", Path: "/sandbox-templates/{0}", Args: []Arg{t}, Confirm: "Delete template {0}?", Done: "Deleted {0}"},
	)
}

func projectCmd() *cobra.Command {
	return group("project", "Projects group resources inside a workspace", "<project> is a name, slug or id.", []string{"projects"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List projects", Method: "GET", Path: "/projects",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "SLUG", Path: "slug"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "WORKSPACE", Path: "workspace_id", Fmt: "short"}, {Head: "DEFAULT", Path: "is_default"}},
		},
		Op{Use: "get <project>", Aliases: []string{"show"}, Short: "Show a project", Method: "GET", Path: "/projects/{0}", Args: []Arg{idArg("project")}},
		Op{
			Use: "create <name>", Short: "Create a project", Method: "POST", Path: "/projects", Args: []Arg{idArg("name")},
			Flags: []Flag{{Name: "workspace-id", Usage: "Workspace", Field: "workspace_id"}, {Name: "description", Usage: "What it is for"}},
			Body:  func(a []string, body map[string]any) (map[string]any, error) { body["name"] = a[0]; return body, nil },
		},
	)
}

// ─── billing, audit, alerts, notifications ────────────────────────────────────

func billingCmd() *cobra.Command {
	return group("billing", "Credits, spend, invoices and plan",
		`Read-only views of what you have and what you spent. Credits are cents. Changing
plans and payment methods happens in the console ('miosa open billing').`, nil,
		Op{
			Use: "balance", Short: "Show the credit balance", Method: "GET", Path: "/credits/balance",
			Detail: []Col{{Head: "Balance (credits)", Path: "balance_credits"}, {Head: "Lifetime earned", Path: "lifetime_earned"}, {Head: "Lifetime spent", Path: "lifetime_spent"}, {Head: "Expires", Path: "credit_expiry_at"}},
		},
		Op{
			Use: "transactions", Aliases: []string{"tx"}, Short: "List credit transactions", Method: "GET", Path: "/credits/transactions", ListKey: "transactions",
			Flags: []Flag{{Name: "per-page", Short: "n", Type: "int", Usage: "How many", Field: "per_page"}, {Name: "page", Type: "int", Usage: "Page number"}},
			Cols:  []Col{{Head: "WHEN", Path: "inserted_at", Fmt: "age"}, {Head: "TYPE", Path: "type"}, {Head: "CREDITS", Path: "amount_credits"}, {Head: "RESOURCE", Path: "resource_type"}, {Head: "DESCRIPTION", Path: "description", Fmt: "clip"}},
		},
		Op{
			Use: "usage", Short: "Model usage by day", Method: "GET", Path: "/credits/usage", ListKey: "usage",
			Cols: []Col{{Head: "DAY", Path: "day"}, {Head: "MODEL", Path: "model"}, {Head: "CALLS", Path: "call_count"}, {Head: "INPUT", Path: "input_tokens"}, {Head: "OUTPUT", Path: "output_tokens"}},
		},
		Op{Use: "breakdown", Short: "Spend by resource", Method: "GET", Path: "/credits/breakdown"},
		Op{
			Use: "spend", Short: "Compute spend per workspace", Method: "GET", Path: "/usage", ListKey: "results",
			Cols: []Col{{Head: "WORKSPACE", Path: "workspace_id", Fmt: "short"}, {Head: "SPEND", Path: "credit_cents", Fmt: "cents"}, {Head: "SANDBOX HOURS", Path: "sandbox_seconds", Fmt: "hours"}, {Head: "COMPUTER HOURS", Path: "computer_seconds", Fmt: "hours"}},
		},
		Op{
			Use: "invoices", Short: "List invoices", Method: "GET", Path: "/billing/invoices",
			Cols: []Col{{Head: "ID", Path: "id"}, {Head: "STATUS", Path: "status"}, {Head: "TOTAL", Path: "total"}, {Head: "DATE", Path: "created"}},
		},
		Op{
			Use: "upcoming", Short: "Show the upcoming invoice", Method: "GET", Path: "/billing/upcoming",
			Detail: []Col{{Head: "Amount due", Path: "amount_due_cents", Fmt: "cents"}, {Head: "Currency", Path: "currency"}, {Head: "Period start", Path: "period_start"}, {Head: "Period end", Path: "period_end"}},
		},
		Op{
			Use: "plan", Short: "Show your plan and current usage against its limits", Method: "GET", Path: "/tenant/plan",
			Detail: []Col{
				{Head: "Plan", Path: "plan.name"}, {Head: "Interval", Path: "plan.interval"}, {Head: "Sandboxes", Path: "usage.sandboxes"}, {Head: "Active sandboxes", Path: "usage.sandboxes_active"},
				{Head: "Computers", Path: "usage.computers"}, {Head: "Deployments", Path: "usage.deployments"}, {Head: "API keys", Path: "usage.api_keys"},
				{Head: "Credits this month", Path: "usage.credits_used_this_month"}, {Head: "Credit balance", Path: "usage.credits_balance"},
			},
		},
	)
}

func auditCmd() *cobra.Command {
	o := Op{
		Use: "audit", Aliases: []string{"activity"}, Short: "Show the audit log of API activity",
		Long:   "Who did what, when, from where. Newest first.",
		Method: "GET", Path: "/audit-log",
		Flags: []Flag{
			{Name: "limit", Short: "n", Type: "int", Usage: "How many entries", Default: "50"},
			{Name: "type", Usage: "Only this event type"},
			{Name: "workspace-id", Usage: "Only this workspace", Field: "workspace_id"},
		},
		Cols: []Col{{Head: "WHEN", Path: "ts", Fmt: "age"}, {Head: "TYPE", Path: "type"}, {Head: "STATUS", Path: "status"}, {Head: "ACTOR", Path: "actor.id", Fmt: "short"}, {Head: "IP", Path: "ip_address"}, {Head: "RESOURCE", Path: "resource.type"}, {Head: "RESOURCE ID", Path: "resource.id", Fmt: "short"}},
	}
	return o.command("")
}

func alertsCmd() *cobra.Command {
	return group("alerts", "Platform alerts for your organization", "", nil,
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List alerts", Method: "GET", Path: "/alerts",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "SEVERITY", Path: "severity"}, {Head: "STATUS", Path: "status"}, {Head: "WHEN", Path: "inserted_at", Fmt: "age"}, {Head: "MESSAGE", Path: "message", Fmt: "clip"}},
		},
		Op{Use: "resolve <id>", Short: "Mark an alert resolved", Method: "POST", Path: "/alerts/{0}/resolve", Args: []Arg{idArg("alert")}, Done: "Resolved {0}"},
	)
}

func notificationsCmd() *cobra.Command {
	return group("notifications", "Your notifications", "", []string{"notification", "inbox"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List notifications", Method: "GET", Path: "/notifications",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "WHEN", Path: "inserted_at", Fmt: "age"}, {Head: "READ", Path: "read"}, {Head: "TITLE", Path: "title", Fmt: "clip"}},
		},
		Op{Use: "unread", Short: "Count unread notifications", Method: "GET", Path: "/notifications/unread-count", Detail: []Col{{Head: "Unread", Path: "count"}}},
		Op{Use: "read <id>", Short: "Mark one read", Method: "POST", Path: "/notifications/{0}/read", Args: []Arg{idArg("notification")}, Done: "Marked {0} read"},
		Op{Use: "read-all", Short: "Mark all read", Method: "POST", Path: "/notifications/read-all", Done: "Marked all read"},
	)
}

// ─── member, egress, regions ──────────────────────────────────────────────────

func memberCmd() *cobra.Command {
	return group("member", "People in your organization",
		"Roles: owner, admin, member, viewer. Only owners and admins can change membership.", []string{"members", "team"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List members", Method: "GET", Path: "/tenant/members",
			Cols: []Col{{Head: "EMAIL", Path: "email"}, {Head: "ROLE", Path: "role"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "JOINED", Path: "created_at", Fmt: "age"}},
		},
		Op{
			Use: "add <email>", Short: "Add a member", Method: "POST", Path: "/tenant/members", Args: []Arg{idArg("email")},
			Flags: []Flag{{Name: "role", Usage: "Role", Default: "member", Enum: []string{"admin", "member", "viewer"}}},
			Body:  func(a []string, body map[string]any) (map[string]any, error) { body["email"] = a[0]; return body, nil },
		},
		Op{
			Use: "role <member-id>", Short: "Change a member's role", Method: "PATCH", Path: "/tenant/members/{0}/role", Args: []Arg{idArg("member")},
			Flags: []Flag{{Name: "role", Usage: "New role", Required: true, Enum: []string{"admin", "member", "viewer"}}},
		},
		Op{
			Use: "rm <member-id>", Aliases: []string{"remove"}, Short: "Remove a member", Method: "DELETE", Path: "/tenant/members/{0}", Args: []Arg{idArg("member")},
			Confirm: "Remove member {0}?", Done: "Removed {0}",
		},
	)
}

func egressCmd() *cobra.Command {
	return group("egress", "Outbound network policy for sandboxes",
		`Egress policies decide which hosts sandboxes may call. 'audit' shows what was
contacted; a policy in audit_only mode records without blocking.`, []string{"network"},
		Op{
			Use: "policies", Short: "List egress policies", Method: "GET", Path: "/egress/policies",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "MODE", Path: "mode"}, {Head: "DEFAULT", Path: "default_action"}, {Head: "SCOPE", Path: "resource_type"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"}},
		},
		Op{
			Use: "allowlist", Short: "List allowed hosts of a policy", Method: "GET", Path: "/egress/allowlist",
			Flags: []Flag{{Name: "policy", Usage: "Policy id", Field: "policy_id", Required: true}},
			Cols:  []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "HOST", Path: "host"}, {Head: "PORT", Path: "port"}},
		},
		Op{
			Use: "audit", Short: "Show recent outbound connections", Method: "GET", Path: "/egress/audit",
			Flags: []Flag{{Name: "limit", Short: "n", Type: "int", Usage: "How many"}},
			Cols:  []Col{{Head: "WHEN", Path: "inserted_at", Fmt: "age"}, {Head: "HOST", Path: "host"}, {Head: "ACTION", Path: "action"}, {Head: "SANDBOX", Path: "sandbox_id", Fmt: "short"}},
		},
		Op{
			Use: "suggestions", Short: "Suggest allowlist entries from observed traffic", Method: "GET", Path: "/egress/audit/suggestions",
		},
	)
}

func regionsCmd() *cobra.Command {
	o := Op{
		Use: "regions", Short: "List regions and whether they have capacity",
		Method: "GET", Path: "/regions", ListKey: "regions",
		Cols: []Col{{Head: "ID", Path: "id"}, {Head: "NAME", Path: "label"}, {Head: "AVAILABLE", Path: "available"}},
	}
	return o.command("")
}

// ─── workspace extras ─────────────────────────────────────────────────────────

// workspaceOps extend 'miosa workspace' beyond create, list and delete.
// <workspace> is a slug, name or id.
func workspaceOps() []*cobra.Command {
	w := Arg{Name: "workspace"}
	ops := []Op{
		{
			Use: "get <workspace>", Aliases: []string{"show"}, Short: "Show a workspace",
			Method: "GET", Path: "/workspaces/{0}", Args: []Arg{w},
			Detail: []Col{{Head: "Name", Path: "name"}, {Head: "Slug", Path: "slug"}, {Head: "Id", Path: "id"}, {Head: "Default", Path: "is_default"}, {Head: "Description", Path: "description"}, {Head: "Created", Path: "created_at", Fmt: "age"}},
		},
		{
			Use: "stats <workspace>", Short: "Counts of machines, deployments and databases",
			Method: "GET", Path: "/workspaces/{0}/stats", Args: []Arg{w},
		},
		{
			Use: "usage <workspace>", Short: "Spend and runtime in a workspace",
			Method: "GET", Path: "/workspaces/{0}/usage", Args: []Arg{w},
		},
		{
			Use: "activity <workspace>", Short: "Recent activity in a workspace",
			Method: "GET", Path: "/workspaces/{0}/activity", Args: []Arg{w},
			Cols: []Col{{Head: "WHEN", Path: "inserted_at", Fmt: "age"}, {Head: "TYPE", Path: "type"}, {Head: "MESSAGE", Path: "message", Fmt: "clip"}},
		},
		{
			Use: "inventory <workspace>", Short: "Everything a workspace contains",
			Method: "GET", Path: "/workspaces/{0}/inventory", Args: []Arg{w},
		},
		{
			Use: "members <workspace>", Short: "List the people in a workspace",
			Method: "GET", Path: "/workspaces/{0}/members", Args: []Arg{w},
			Cols: []Col{{Head: "EMAIL", Path: "email"}, {Head: "ROLE", Path: "role"}, {Head: "USER", Path: "user_id", Fmt: "short"}},
		},
	}
	cmds := make([]*cobra.Command, 0, len(ops)+1)
	for _, o := range ops {
		cmds = append(cmds, o.command("workspace"))
	}
	return append(cmds, newWorkspaceUseCmd())
}

func newWorkspaceUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <workspace>",
		Short: "Make a workspace the default for this profile",
		Long:  "Stores the workspace slug as default_workspace in the active profile. The workspace must exist.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			var ws struct {
				Slug string `json:"slug"`
				Name string `json:"name"`
				ID   string `json:"id"`
			}
			var env struct {
				Data struct {
					Slug string `json:"slug"`
					Name string `json:"name"`
					ID   string `json:"id"`
				} `json:"data"`
			}
			id, err := c.API.ResolveWorkspace(cmd.Context(), args[0])
			if err != nil {
				return die(err)
			}
			if err := c.API.Get(cmd.Context(), "/workspaces/"+id, nil, &env); err != nil {
				return die(err)
			}
			ws.Slug, ws.Name, ws.ID = env.Data.Slug, env.Data.Name, env.Data.ID
			if ws.Slug == "" {
				ws.Slug = args[0]
			}
			cfg.DefaultWorkspace = ws.Slug
			if err := saveCurrentConfig(cfg); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"default_workspace": ws.Slug, "id": ws.ID})
			}
			p.Success("Default workspace is now %q", ws.Slug)
			return nil
		},
	}
}

// legacyCronCmd keeps the old spelling working; the glossary calls these
// schedules.
func legacyCronCmd() *cobra.Command {
	c := scheduleCmd()
	c.Use, c.Aliases = "cron", nil
	c.Hidden = true
	c.Deprecated = "use 'miosa schedule'"
	return c
}

// cloudCmd covers bring-your-own-cloud: the accounts you connected, the regions
// and pools built on them, and the hosts that came up. Connecting and
// provisioning follow the same preview gate as the console: host provisioning
// commands refuse to run until the server says it is activated for you.
func cloudCmd() *cobra.Command {
	account := Arg{Name: "account"}
	pool := Arg{Name: "pool"}
	cmd := group("cloud", "Your own cloud accounts, regions and host pools (bring your own cloud)",
		`Shows the cloud accounts connected to your organization, the regions and host
pools built on them, and the hosts in each pool, and sets them up. Bring your
own cloud is in private preview: connecting an account and creating regions and
pools are accepted when your organization is enrolled, but starting hosts
('pool provision') and creating pools need host provisioning to be activated
for you, exactly as in the console. 'miosa cloud capabilities' shows what is.

Cloud changes need an owner or admin and a key with the cloud:write scope.

  miosa cloud account new prod --provider aws
  miosa cloud account attach-aws <account> --role-arn arn:aws:iam::123456789012:role/MiosaAccess --region us-east-1
  miosa cloud preflight run --account <account>
  miosa cloud pool new --region <region> --instance-type m7i.8xlarge --target-nodes 1 --max-nodes 4
  miosa cloud pool provision <pool> --count 1`, []string{"byoc"},
		Op{
			Use: "capabilities", Short: "Show what each provider can do for your organization", Method: "GET", Path: "/cloud/capabilities",
		},
		Op{
			Use: "accounts", Short: "List connected cloud accounts", Method: "GET", Path: "/cloud/accounts",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "NAME", Path: "display_name"}, {Head: "PROVIDER", Path: "provider"}, {Head: "REGION", Path: "default_region"}, {Head: "STATUS", Path: "status"}, {Head: "MODE", Path: "mode"}},
		},
		Op{
			Use: "regions", Short: "List the regions built on your accounts", Method: "GET", Path: "/cloud/regions",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "NAME", Path: "display_name"}, {Head: "PROVIDER", Path: "provider"}, {Head: "REGION", Path: "provider_region"}, {Head: "STATUS", Path: "status"}},
		},
		Op{
			Use: "pools", Short: "List host pools", Method: "GET", Path: "/cloud/pools",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "KIND", Path: "pool_kind"}, {Head: "TYPE", Path: "instance_type"}, {Head: "TARGET", Path: "target_nodes"}, {Head: "MAX", Path: "max_nodes"}, {Head: "STATUS", Path: "status"}},
		},
		Op{
			Use: "nodes", Short: "List the hosts in your pools", Method: "GET", Path: "/cloud/nodes",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "HOST", Path: "hostname"}, {Head: "PROVIDER", Path: "provider"}, {Head: "STATUS", Path: "status"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}},
		},
		Op{
			Use: "preflights", Short: "List preflight checks run against your accounts", Method: "GET", Path: "/cloud/preflights",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATUS", Path: "status"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}},
		},
	)
	cloudDetail := []Col{{Head: "Id", Path: "id"}, {Head: "Name", Path: "display_name"}, {Head: "Provider", Path: "provider"}, {Head: "Status", Path: "status"}, {Head: "Region", Path: "default_region"}}
	cmd.AddCommand(
		group("account", "Connect a cloud account", "An account is the AWS or Google Cloud account MIOSA starts hosts in. Creating one is the first step; attach the access role or identity next.", nil,
			Op{
				Use: "new <name>", Aliases: []string{"create"}, Short: "Create a cloud account record", Method: "POST", Path: "/cloud/accounts", Args: []Arg{{Name: "name"}}, Detail: cloudDetail,
				Flags: []Flag{{Name: "provider", Usage: "Cloud provider", Required: true, Enum: []string{"aws", "gcp"}}},
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					body["display_name"] = args[0]
					body["mode"] = "customer_byoc"
					return body, nil
				},
			},
			Op{
				Use: "attach-aws <account>", Short: "Attach the AWS role MIOSA assumes", Method: "POST", Path: "/cloud/accounts/{0}/aws-role", Args: []Arg{account}, Detail: cloudDetail,
				Flags: []Flag{{Name: "role-arn", Usage: "ARN of the role in your AWS account", Required: true}, {Name: "region", Usage: "Default AWS region", Field: "default_region", Required: true}},
			},
			Op{
				Use: "attach-gcp <account>", Short: "Attach the Google Cloud identity MIOSA uses", Method: "POST", Path: "/cloud/accounts/{0}/gcp-workload-identity", Args: []Arg{account}, Detail: cloudDetail,
				Flags: []Flag{
					{Name: "project-id", Usage: "Google Cloud project id", Required: true},
					{Name: "actuator-service-account", Usage: "Email of the service account that launches hosts", Field: "actuator_service_account_email", Required: true},
					{Name: "worker-service-account", Usage: "Email of the service account hosts run as", Field: "worker_service_account_email", Required: true},
					{Name: "region", Usage: "Default region", Field: "default_region", Required: true},
					{Name: "project-number", Usage: "Google Cloud project number"},
					{Name: "identity-provider", Usage: "Workload identity provider resource name", Field: "workload_identity_provider"},
				},
			},
		),
		group("preflight", "Check that an account or region is ready", "A preflight tests access and configuration without starting any host.", nil,
			Op{
				Use: "run", Short: "Run a preflight check", Method: "POST", Path: "/cloud/preflights", Detail: []Col{{Head: "Id", Path: "id"}, {Head: "Status", Path: "status"}, {Head: "Checks", Path: "checks", Fmt: "json"}},
				Flags: []Flag{{Name: "account", Usage: "Account id to check", Field: "cloud_account_id"}, {Name: "region", Usage: "Region id to check", Field: "cloud_region_id"}},
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					if body["cloud_account_id"] == nil && body["cloud_region_id"] == nil {
						return nil, fmt.Errorf("give --account or --region")
					}
					return body, nil
				},
			},
		),
		group("region", "Regions built on a cloud account", "A region ties an account to a network (subnet, security groups) and the host image MIOSA starts.", nil,
			Op{
				Use: "new", Aliases: []string{"create"}, Short: "Create a region on an account", Method: "POST", Path: "/cloud/regions", FileBody: true,
				Long: "The core fields are flags. For anything else (Google Cloud networks, host image details) put a JSON object in --file; flags override its fields.",
				Flags: []Flag{
					{Name: "account", Usage: "Account id", Field: "cloud_account_id"},
					{Name: "name", Usage: "Region name", Field: "display_name"},
					{Name: "provider-region", Usage: "Provider region, for example us-east-1"},
					{Name: "zone", Usage: "Provider zone", Field: "provider_zone"},
					{Name: "subnet", Usage: "Subnet id", Field: "subnet_ref"},
					{Name: "security-group", Usage: "Security group id (repeatable)", Type: "strings", Field: "security_group_refs"},
					{Name: "instance-profile", Usage: "Instance profile reference", Field: "instance_profile_ref"},
					{Name: "artifact-manifest-uri", Usage: "Location of the host artifact manifest"},
					{Name: "meta", Usage: "Region metadata KEY=VALUE, for example host_image_id=ami-123 (repeatable)", Type: "kv", Field: "metadata"},
				},
			},
		),
		group("pool", "Pools of hosts in a region", "A pool says how many hosts of one type to keep in a region. Creating and starting them needs host provisioning activated for your organization.", nil,
			Op{
				Use: "new", Aliases: []string{"create"}, Short: "Create a host pool", Method: "POST", Path: "/cloud/pools", Before: cloudProvisioningGate,
				Flags: []Flag{
					{Name: "region", Usage: "Region id", Field: "cloud_region_id", Required: true},
					{Name: "instance-type", Usage: "Instance type, for example m7i.8xlarge", Required: true},
					{Name: "target-nodes", Usage: "How many hosts to keep", Type: "int", Required: true},
					{Name: "max-nodes", Usage: "Most hosts the pool may reach", Type: "int", Required: true},
					{Name: "max-hourly-cents", Usage: "Spending cap per hour, in cents", Type: "int"},
					{Name: "ttl-seconds", Usage: "Retire hosts after this many seconds", Type: "int"},
					{Name: "scope", Usage: "Workload the pool may serve: sandbox, computer or deployment (repeatable)", Type: "strings", Field: "placement_scope"},
				},
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					body["pool_kind"] = "standing_byoc"
					body["node_type"] = "byoc"
					return body, nil
				},
			},
			Op{
				Use: "update <pool>", Short: "Change a pool's size or limits", Method: "PATCH", Path: "/cloud/pools/{0}", Args: []Arg{pool}, Before: cloudProvisioningGate,
				Flags: []Flag{
					{Name: "target-nodes", Usage: "How many hosts to keep", Type: "int"},
					{Name: "max-nodes", Usage: "Most hosts the pool may reach", Type: "int"},
					{Name: "max-hourly-cents", Usage: "Spending cap per hour, in cents", Type: "int"},
					{Name: "ttl-seconds", Usage: "Retire hosts after this many seconds", Type: "int"},
					{Name: "scope", Usage: "Workload the pool may serve (repeatable)", Type: "strings", Field: "placement_scope"},
				},
			},
			Op{
				Use: "provision <pool>", Short: "Start hosts in a pool", Method: "POST", Path: "/cloud/pools/{0}/provision", Args: []Arg{pool}, Before: cloudProvisioningGate,
				Flags:   []Flag{{Name: "count", Usage: "How many hosts to start", Type: "int", Default: "1"}},
				Confirm: "Start hosts in pool {0}? They bill your cloud account while they run.",
			},
		),
		group("node", "Hosts in a pool", "", nil,
			Op{
				Use: "certify <node>", Short: "Check that a host can run workloads", Method: "POST", Path: "/cloud/nodes/{0}/certify", Args: []Arg{{Name: "node"}}, Before: cloudProvisioningGate,
				Flags: []Flag{{Name: "workload", Usage: "Workload to certify", Default: "sandbox"}},
				Body: func(args []string, body map[string]any) (map[string]any, error) {
					body["idempotency_key"] = fmt.Sprintf("cli-%d", time.Now().UnixNano())
					return body, nil
				},
			},
		),
	)
	return cmd
}

// cloudProvisioningGate is the console's preview gate: host provisioning is
// only offered once the server reports it activated for the organization.
func cloudProvisioningGate(cmd *cobra.Command, c *api.Client) error {
	var out struct {
		Data struct {
			ProvisioningEnabled bool `json:"provisioning_enabled"`
			WireguardConfigured bool `json:"wireguard_configured"`
		} `json:"data"`
	}
	if err := c.Get(cmd.Context(), "/cloud/capabilities", nil, &out); err != nil {
		return die(err)
	}
	var blockers []string
	if !out.Data.ProvisioningEnabled {
		blockers = append(blockers, "host provisioning is not activated for your organization")
	}
	if !out.Data.WireguardConfigured {
		blockers = append(blockers, "WireGuard enrollment is not configured")
	}
	if len(blockers) > 0 {
		return die(fmt.Errorf("bring your own cloud is in private preview: %s (see 'miosa cloud capabilities')", strings.Join(blockers, "; ")))
	}
	return nil
}
