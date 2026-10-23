package commands

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func init() {
	rootCmd.AddCommand(
		newDeployCmd(), dbCmd(), storageCmd(), volumeCmd(), workflowCmd(), scheduleCmd(), legacyCronCmd(), functionCmd(),
		templateCmd(), projectCmd(), billingCmd(), auditCmd(), alertsCmd(), notificationsCmd(),
		memberCmd(), egressCmd(), regionsCmd(), cloudCmd(),
	)
}

func idArg(name string) Arg { return Arg{Name: name} }

// resolveWorkspaceQuery turns a workspace slug, name or id in the workspace_id query
// value into the id the list filters expect.
func resolveWorkspaceQuery(ctx context.Context, c *api.Client, q url.Values) error {
	ref := q.Get("workspace_id")
	if ref == "" {
		return nil
	}
	id, err := c.ResolveWorkspace(ctx, ref)
	if err != nil {
		return err
	}
	q.Set("workspace_id", id)
	return nil
}

// ─── deploy ───────────────────────────────────────────────────────────────────

var deployCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"},
	{Head: "URL", Path: "public_url"}, {Head: "SOURCE", Path: "source_type"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"},
}

// moveTargetFromArgsOrFlag lets the target be a second argument or --to. Both
// is only an error when they disagree, so a script that passes the flag and a
// person who types the argument both work.
func moveTargetFromArgsOrFlag(args []string, body map[string]any) (map[string]any, error) {
	positional := ""
	if len(args) > 1 {
		positional = strings.TrimSpace(args[1])
	}
	flagged, _ := body["to"].(string)
	flagged = strings.TrimSpace(flagged)

	switch {
	case positional != "" && flagged != "" && positional != flagged:
		return nil, usagef("the target is given twice and they differ: %q as an argument, %q with --to", positional, flagged)

	case positional != "":
		body["to"] = positional

	case flagged == "":
		return nil, usagef("give the deployment to move the domain to: miosa deploy domain-move <domain> <target> (or --to <target>)")
	}

	return body, nil
}

var deployDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "State", Path: "state"}, {Head: "URL", Path: "public_url"},
	{Head: "Source", Path: "source_type"}, {Head: "Repo", Path: "repo_url"}, {Head: "Branch", Path: "branch"},
	{Head: "Build", Path: "build_command"}, {Head: "Run", Path: "run_command"}, {Head: "Auto deploy", Path: "auto_deploy"}, {Head: "AI access", Path: "ai_access"},
	{Head: "Active version", Path: "active_version_id"}, {Head: "Active release", Path: "active_release_id"},
	{Head: "Workspace", Path: "workspace_id"}, {Head: "Created", Path: "created_at", Fmt: "age"},
}

// deployOps are the declarative subcommands of 'deploy' (list, get, logs, ...).
// 'miosa deploy' itself and its 'create' child are bespoke and live in
// deploy_create.go.
func deployOps() []Op {
	d := idArg("deployment")
	return []Op{
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List deployments",
			Long:    "Lists your deployments with their state and URL. Any of the name, short id, hostname or id shown here works as <deployment> in the other commands.",
			Example: "  miosa deploy list\n  miosa deploy ls --workspace-id <id> -n 20",
			Method:  "GET", Path: "/deployments", Cols: deployCols, AllOrgs: true,
			Flags: []Flag{{Name: "workspace-id", Usage: "Only this workspace", Field: "workspace_id"}, {Name: "limit", Short: "n", Type: "int", Usage: "How many"}},
		},
		Op{
			Use: "logs <deployment>", Short: "Show the running app's log",
			Long:    "Prints the most recent lines the deployment's process wrote. Static sites (uploaded files) run no process, so they have no runtime log.\n\n--follow then streams new lines until Ctrl-C: a build in progress is followed until it finishes, a running app's stdout and stderr until you stop.",
			Example: "  miosa deploy logs my-app\n  miosa deploy logs my-app -n 200\n  miosa deploy logs my-app --follow",
			After: func(cmd *cobra.Command, resp any) error {
				_ = showBuildFailure(cmd, resp)
				return staticEmptyNotice("static deployments have no runtime logs: they serve uploaded files and run no process")(cmd, resp)
			},
			Method: "GET", Path: "/deployments/{0}/logs", Args: []Arg{d}, Lines: "data.lines",
			Flags:  []Flag{{Name: "lines", Short: "n", Type: "int", Usage: "Number of lines"}},
			Follow: followDeployment,
		},
		Op{
			Use: "prove <deployment>", Aliases: []string{"proof"}, Short: "Check end to end that a deployment is live and serving",
			Long:    "Asks the platform to prove the deployment: it exists, is running, has a public URL, and (for App Engine apps) its host and runtime answer. The CLI then requests the public URL itself and checks its status and TLS certificate. Exits 1 when any check fails, so a script or CI step can gate on it.",
			Example: "  miosa deploy prove my-app\n  miosa deploy prove my-app --json",
			Method:  "GET", Path: "/deployments/{0}/proof", Args: []Arg{d},
			After: proveAfter, Human: renderProof,
		},
		Op{
			Use: "purge-cache <deployment>", Aliases: []string{"cache-purge"}, Short: "Drop the edge cache of a static deployment",
			Long:    "Drops every cached file of the deployment's versions, here and on every edge node. Promotes and rollbacks already serve new bytes (files are cached by content), so this is for forcing a refresh.",
			Example: "  miosa deploy purge-cache my-site",
			Method:  "POST", Path: "/deployments/{0}/purge-cache", Args: []Arg{d},
			Detail: []Col{{Head: "Versions", Path: "versions"}, {Head: "Purged here", Path: "local_purged"}, {Head: "Edge nodes reached", Compute: purgeNodesReached}, {Head: "Failed nodes", Path: "failed_nodes", Fmt: "list"}, {Head: "Entries purged on nodes", Path: "remote_purged"}},
		},
		Op{
			Use: "requests <deployment>", Short: "Show recent HTTP requests",
			Long:    "Lists recent HTTP requests the deployment served: time, method, path, status and duration. Requests to static sites are not logged yet; this says so instead of showing an empty table.",
			Example: "  miosa deploy requests my-app\n  miosa deploy requests my-app --json",
			After:   staticEmptyNotice("requests to static deployments are not logged yet, so this list is empty even when the site has traffic"),
			Method:  "GET", Path: "/deployments/{0}/request-logs", Args: []Arg{d}, ListKey: "logs",
			Cols: []Col{{Head: "TIME", Path: "timestamp", Fmt: "age"}, {Head: "METHOD", Path: "method"}, {Head: "PATH", Path: "path"}, {Head: "STATUS", Path: "status"}, {Head: "MS", Path: "duration_ms"}},
		},
		Op{
			Use: "observe <deployment>", Aliases: []string{"metrics"}, Short: "Invocations, errors and latency",
			Long:    "Summarizes the deployment's traffic: invocations, errors, error rate, p50/p95 latency and cold starts. Latency is not measured for static sites and shows n/a.",
			Example: "  miosa deploy observe my-app\n  miosa deploy observe my-app --json",
			After:   staticObserve,
			Method:  "GET", Path: "/deployments/{0}/observability", Args: []Arg{d},
			Detail: []Col{
				{Head: "Invocations", Path: "summary.invocations"}, {Head: "Errors", Path: "summary.error_count"}, {Head: "Error rate", Path: "summary.error_rate", Fmt: "pct"},
				{Head: "p50 (ms)", Path: "summary.duration_ms.p50"}, {Head: "p95 (ms)", Path: "summary.duration_ms.p95"}, {Head: "Cold start rate", Path: "summary.cold_start_rate", Fmt: "pct"},
			},
		},
		Op{
			Use: "releases <deployment>", Short: "List releases",
			Long:    "Lists the deployment's releases: each time a version was released to serve. 'miosa deploy versions' lists the uploaded versions and marks the live one.",
			Example: "  miosa deploy releases my-app\n  miosa deploy versions my-app",
			Method:  "GET", Path: "/deployments/{0}/releases", Args: []Arg{d},
			Cols:  []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "VERSION", Path: "deployment_version_id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "PORT", Path: "port"}, {Head: "READY", Path: "ready_at", Fmt: "age"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}, {Head: "LIVE", Path: "live"}},
			After: markLiveRelease,
		},
		Op{
			Use: "rollback <deployment>", Short: "Make an earlier version live again",
			Long: `Switches the live version back to the one you name with --to (a version
number, short id or full id from 'miosa deploy versions'). Before asking for
confirmation it prints the version live now and the exact version it will
switch to. Without --to it lists the versions and changes nothing: it never
guesses which one you meant.`,
			Example: "  miosa deploy versions my-app\n  miosa deploy rollback my-app --to 67cf439d\n  miosa deploy rollback my-app --to 3 --yes",
			Method:  "POST", Path: "/deployments/{0}/rollback", Args: []Arg{d}, Confirm: "Roll {0} back?", Done: "Rolled back {0}",
			Flags:  []Flag{{Name: "to", Usage: "Version to make live: number, short id or id (see 'miosa deploy versions')", Field: "version_id"}},
			Before: rollbackBefore,
			// The API refuses a rollback without an Idempotency-Key (found live
			// on 2.3.7's QA run: every CLI rollback had been refused).
			Idempotent: true,
		},
		Op{
			Use: "start <deployment>", Short: "Start a stopped deployment from its last release",
			Long:    "Brings a stopped deployment back from the release it was serving, at its current size. Nothing is built or uploaded, so it also works for an uploaded deployment, which has no source to redeploy from. A static site is live again at once; a server reads 'building' until its instance is healthy.",
			Example: "  miosa deploy start my-site",
			Method:  "POST", Path: "/deployments/{0}/start", Args: []Arg{d}, Done: "Starting {0}",
		},
		Op{
			Use: "stop <deployment>", Short: "Stop a deployment",
			Long:    "Stops serving traffic. The deployment, its versions and domains are kept; 'miosa deploy redeploy' or a new push brings it back.",
			Example: "  miosa deploy stop my-app\n  miosa deploy stop my-app --yes",
			Method:  "POST", Path: "/deployments/{0}/stop", Args: []Arg{d}, Confirm: "Stop deployment {0}? It stops serving traffic.", Done: "Stopped {0}",
		},
		Op{
			Use: "rm <deployment>", Aliases: []string{"delete"}, Short: "Delete a deployment",
			Long: `Deletes the deployment, its versions and its URLs. Custom domains attached
to it stop serving, so rm lists them and refuses while any are attached: move
them first with 'miosa deploy domain-move <domain> <other-deployment>', or pass
--with-domains to delete the deployment and detach them anyway.`,
			Example: "  miosa deploy rm old-site\n  miosa deploy domain-move example.com new-site && miosa deploy rm old-site",
			Method:  "DELETE", Path: "/deployments/{0}", Args: []Arg{d}, Confirm: "Permanently delete deployment {0}?", Done: "Deleted {0}",
			Flags:  []Flag{{Name: "with-domains", Type: "bool", Usage: "Delete even though custom domains are attached (they go offline)", Field: "with_domains"}},
			Query:  func(_ []string, q url.Values) error { q.Del("with_domains"); return nil },
			Before: rmBefore,
		},
		Op{
			Use: "builds <deployment>", Short: "List builds",
			Long:    "Lists the builds of a repository or sandbox deployment. Uploaded static sites are not built, so they have none.",
			Example: "  miosa deploy builds my-app",
			After:   staticEmptyNotice("uploaded sites have no builds: the files are served as uploaded"),
			Method:  "GET", Path: "/deployments/{0}/builds", Args: []Arg{d}, ListKey: "data",
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "COMMIT", Path: "commit_sha", Fmt: "short"}, {Head: "TRIGGER", Path: "triggered_by"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}, {Head: "ERROR", Path: "error_message", Fmt: "clip"}},
		},
		Op{
			Use: "env <deployment>", Short: "List the deployment's environment variable names",
			Long:    "Lists the names of the deployment's environment variables and when each changed. Values are encrypted at rest and never printed.",
			Example: "  miosa deploy env my-app",
			Method:  "GET", Path: "/deployments/{0}/env", Args: []Arg{d}, ListKey: "env_vars",
			Flags: []Flag{environmentFlag()}, Before: environmentSupported,
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ENVIRONMENT", Path: "environment"}, {Head: "UPDATED", Path: "updated_at", Fmt: "age"}},
		},
		Op{
			Use: "env-set <deployment>", Short: "Set the deployment's environment variables (values are encrypted at rest)",
			Long:    "Sets one or more environment variables. A production change cuts a new release on the server and prints it (id and state); the variables are live once that release is. Pass --no-release to store them without cutting one: they apply at the next release ('miosa deploy redeploy' or a new push).",
			Example: "  miosa deploy env-set my-app --set DATABASE_URL=postgres://... --set FEATURE=on\n  miosa deploy env-set my-app --set FEATURE=off --no-release",
			Method:  "POST", Path: "/deployments/{0}/env", Args: []Arg{d},
			Flags:  []Flag{{Name: "set", Usage: "Environment variable KEY=VALUE (repeatable)", Type: "kv", Field: "env", Required: true}, environmentFlag(), noReleaseFlag()},
			Body:   noReleaseBody,
			Before: environmentSupported,
			Human:  renderEnvSet,
		},
		Op{
			Use: "env-rm <deployment> <name>", Aliases: []string{"env-unset"}, Short: "Remove environment variables (comma-separated names)",
			Long:    "Removes the named variables. Like env-set, a production change cuts a new release on the server and prints it; pass --no-release to remove them without cutting one. A name that is not set is reported, not an error.",
			Example: "  miosa deploy env-rm my-app DEBUG\n  miosa deploy env-rm my-app OLD_TOKEN,LEGACY_URL",
			Method:  "DELETE", Path: "/deployments/{0}/env/{1}", Args: []Arg{d, {Name: "name"}},
			Flags: []Flag{environmentFlag(), noReleaseFlag()}, Before: environmentSupported,
			Query: noReleaseQuery,
			Human: renderEnvRemoval,
		},
		Op{
			Use: "domains <deployment>", Short: "List custom domains",
			Long:    "Lists the custom domains attached to the deployment, with their DNS verification and serving status.",
			Example: "  miosa deploy domains my-app\n  miosa deploy domains 7699e13d\n  miosa deploy domains example.com",
			Method:  "GET", Path: "/deployments/{0}/domains", Args: []Arg{d},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "HOSTNAME", Path: "hostname"}, {Head: "STATUS", Path: "status"}, {Head: "VERIFIED", Path: "verified"}},
		},
		Op{
			Use: "domain-add <deployment> <hostname>", Short: "Attach a custom domain (then add the DNS record it shows)",
			Long: `Attaches the hostname and prints the DNS records to add. --dns chooses what
to do with them:

  --dns zone        print them as DNS zone-file lines to paste (BIND syntax)
  --dns cloudflare  create them in your Cloudflare zone, using the value of
                    CLOUDFLARE_API_TOKEN (needs Zone:Read and DNS:Edit). It is
                    used by this command only and is never sent to MIOSA.

--redirect-to H makes this host answer with a permanent redirect to H, keeping
the path and query. H must be this name with or without "www." and must also be
attached to the deployment and verified, or nothing is redirected.

Registrars without a records API (Namecheap's replaces a whole zone and needs an
allow-listed IP) are not automated: use --dns zone and paste the lines.`,
			Example: "  miosa deploy domain-add my-app www.example.com --dns cloudflare\n  miosa deploy domain-add my-app www.example.com --redirect-to example.com",
			Method:  "POST", Path: "/deployments/{0}/domains", Args: []Arg{d, idArg("hostname")},
			Flags: []Flag{
				{Name: "dns", Usage: "What to do with the DNS records: zone | cloudflare", Enum: []string{"zone", "cloudflare"}},
				{Name: "redirect-to", Usage: "Redirect this host to the other form of the same name (www.example.com -> example.com or the reverse)", Field: "redirect_to"},
			},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				delete(body, "dns") // client-side only
				body["hostname"] = args[1]
				if target, _ := body["redirect_to"].(string); target != "" {
					policy, err := redirectPolicyFor(args[1], target)
					if err != nil {
						return nil, err
					}
					body["redirect_policy"] = policy
				}
				delete(body, "redirect_to")
				return body, nil
			},
			After: domainAddDNS,
		},
		Op{
			Use: "domain-verify <deployment> <domain-id>", Short: "Check a domain's DNS (--wait: until it is live)",
			Long: `Checks the domain's DNS once. With --wait it keeps checking every 10 seconds
until DNS and the TLS certificate are right and the site answers, then prints
"live https://...". On every check that fails it prints the exact record: type,
name, what is expected and what was observed. On timeout it prints what is still
missing and exits non-zero. <domain-id> may be the hostname when using --wait.`,
			Example: "  miosa deploy domain-verify my-app www.example.com --wait --timeout 15m",
			Method:  "POST", Path: "/deployments/{0}/domains/{1}/verify", Args: []Arg{d, idArg("domain")},
			Flags: []Flag{
				{Name: "wait", Type: "bool", Usage: "Wait until the domain is live (DNS + TLS + serving)"},
				{Name: "timeout", Default: "10m", Usage: "With --wait, give up after this long (for example 15m)"},
			},
			Body: func(args []string, body map[string]any) (map[string]any, error) {
				delete(body, "wait") // client-side only
				delete(body, "timeout")
				return body, nil
			},
			Before: waitDomainBefore,
		},
		Op{
			Use: "domain-move <domain> [target]", Short: "Move a custom domain to another deployment (no DNS change)",
			Long: `Repoints a domain you already verified at a different deployment. The
verification, TLS and redirect settings move with it, so you change no DNS
record and verify nothing again; the new deployment serves it within seconds.

<domain> is the hostname. The target deployment (id, slug or name) is the second
argument, or --to; give it one way or the other, not both.`,
			Example: "  miosa deploy domain-move lunivate.com lunivate-site-v2\n  miosa deploy domain-move lunivate.com --to lunivate-site-v2",
			Method:  "POST", Path: "/domains/{0}/move", Args: []Arg{idArg("domain"), {Name: "to", Optional: true}},
			Flags: []Flag{{Name: "to", Usage: "Deployment to move the domain to (id, slug or name)", Field: "to"}},
			Body:  moveTargetFromArgsOrFlag,
			Done:  "Moved {0}",
		},
		Op{
			Use: "domain-rm <deployment> <domain-id>", Short: "Detach a custom domain",
			Long:    "Detaches the domain: it stops serving this deployment. To serve it from another deployment without touching DNS, use 'miosa deploy domain-move' instead. <domain-id> is the id from 'miosa deploy domains'.",
			Example: "  miosa deploy domains my-app\n  miosa deploy domain-rm my-app 3f2a91c0",
			Method:  "DELETE", Path: "/deployments/{0}/domains/{1}", Args: []Arg{d, idArg("domain")}, Confirm: "Detach domain {1}?", Done: "Detached {1}",
		},
	}
}

// ─── db ───────────────────────────────────────────────────────────────────────

var dbCols = []Col{
	{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "ENGINE", Path: "engine"}, {Head: "STATE", Path: "state"},
	{Head: "MEMORY (MB)", Path: "memory_mb"}, {Head: "REGION", Path: "region"}, {Head: "CREATED", Path: "created_at", Fmt: "age"},
}

func dbCmd() *cobra.Command {
	d := idArg("database")
	cmd := group("db", "Managed databases",
		`Databases run on MIOSA and bill while running. <database> is a name or id.
'connection' prints the connection secret: treat the output like a password.`, []string{"database", "databases"},
		Op{Use: "list", Aliases: []string{"ls"}, Short: "List databases", Method: "GET", Path: "/databases", Cols: dbCols, AllOrgs: true,
			Flags:        []Flag{{Name: "workspace", Usage: "Only this workspace (slug, name or id)", Field: "workspace_id"}},
			ResolveQuery: resolveWorkspaceQuery},
		Op{
			Use: "get <database>", Aliases: []string{"show"}, Short: "Show a database", Method: "GET", Path: "/databases/{0}", Args: []Arg{d},
			Detail: []Col{{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Engine", Path: "engine"}, {Head: "State", Path: "state"}, {Head: "Host", Path: "host"}, {Head: "Port", Path: "port"}, {Head: "Memory (MB)", Path: "memory_mb"}, {Head: "Version", Path: "engine_version"}, {Head: "Extensions", Path: "extensions"}, {Head: "Workspace", Path: "workspace_id"}, {Head: "Error", Path: "error.reason"}, {Head: "Failed at", Path: "error.at"}},
		},
		Op{
			Use: "create <name>", Short: "Create a database", Method: "POST", Path: "/databases", Args: []Arg{idArg("name")},
			Long: `Create a managed database.

  miosa db create app-db --engine postgres --version 16 --size small
  miosa db create app-db --extensions vector,pg_trgm,pgcrypto --workspace tz
  miosa db create app-db --project shop

--extensions applies to PostgreSQL only; allowed: vector, pg_trgm, pgcrypto, uuid-ossp,
pg_stat_statements, citext, hstore, btree_gin, btree_gist. --version is a version with an
image (postgres 16, mysql 8, redis 7, mongodb 7). --size is one of tiny, xs, small, medium, large.

--project adds the database to a project, where its services can reference it; it is
created in the project's workspace unless --workspace says otherwise.

--engine optimal creates an Optimal Engine; 'miosa engine create' is the same call with
the Engine-specific flags and an idempotent name.`,
			Flags: []Flag{
				{Name: "engine", Usage: "Engine: postgres, mysql, redis, mongodb, qdrant or optimal", Default: "postgres", Enum: []string{"postgres", "postgresql", "mysql", "redis", "mongodb", "qdrant", "optimal"}},
				{Name: "version", Usage: "Engine version (default: the engine's current one)", Field: "engine_version"},
				{Name: "size", Usage: "Size: tiny, xs, small, medium or large", Enum: []string{"tiny", "xs", "small", "medium", "large"}},
				{Name: "extensions", Type: "strings", Usage: "PostgreSQL extensions to enable at create, comma separated"},
				{Name: "workspace", Usage: workspaceFlagUsage, Field: "workspace_id"},
				{Name: "workspace-id", Usage: "Workspace id", Field: "workspace_id", Hidden: true},
				{Name: "region", Usage: "Pin to a region"},
				{Name: "class", Usage: "Data classification: public, internal, confidential or restricted", Field: "classification", Enum: []string{"public", "internal", "confidential", "restricted"}},
				{Name: "project", Usage: "Add it to this project (name, slug or id), in the project's workspace", Field: "project_id"},
			},
			Before: settleDBProject,
			After:  reportWorkspaceLanding,
			Body:   func(a []string, body map[string]any) (map[string]any, error) { body["name"] = a[0]; return body, nil },
			Detail: []Col{{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Engine", Path: "engine"}, {Head: "Version", Path: "engine_version"}, {Head: "State", Path: "state"}, {Head: "Memory (MB)", Path: "memory_mb"}, {Head: "Workspace", Path: "workspace_id"}},
		},
		Op{Use: "start <database>", Short: "Start a stopped database", Method: "POST", Path: "/databases/{0}/start", Args: []Arg{d}, Done: "Starting {0}"},
		Op{Use: "stop <database>", Short: "Stop a database", Method: "POST", Path: "/databases/{0}/stop", Args: []Arg{d}, Confirm: "Stop database {0}?", Done: "Stopping {0}"},
		Op{Use: "restart <database>", Short: "Restart a database", Method: "POST", Path: "/databases/{0}/restart", Args: []Arg{d}, Done: "Restarting {0}"},
		Op{Use: "wake <database>", Short: "Wake a database that scaled to zero", Method: "POST", Path: "/databases/{0}/wake", Args: []Arg{d}, Done: "Waking {0}"},
		Op{Use: "connection <database>", Aliases: []string{"url"}, Short: "Show how to connect (a secret)", Method: "GET", Path: "/databases/{0}/credentials", Args: []Arg{d}},
		Op{
			Use: "logs <database>", Short: "Show the database log (the last run of one that failed)", Method: "GET", Path: "/databases/{0}/logs", Args: []Arg{d}, Lines: "logs",
			Example: "  miosa db logs my-db -n 200\n  miosa db logs my-db --follow",
			Flags:   []Flag{{Name: "tail", Short: "n", Type: "int", Usage: "How many lines", Field: "lines"}},
			Follow:  followRoute("database_id", "/databases/%s/logs/stream"),
		},
		Op{
			Use: "backups <database>", Short: "List backups", Method: "GET", Path: "/databases/{0}/backups", Args: []Arg{d},
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "STATE", Path: "state"}, {Head: "SIZE", Path: "size_bytes", Fmt: "bytes"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}},
		},
		Op{Use: "backup <database>", Short: "Take a backup now", Method: "POST", Path: "/databases/{0}/backups", Args: []Arg{d}},
		Op{Use: "rm <database>", Aliases: []string{"delete"}, Short: "Permanently delete a database", Method: "DELETE", Path: "/databases/{0}", Args: []Arg{d}, Confirm: "Permanently delete database {0} and its data?", Done: "Deleted {0}"},
	)
	cmd.AddCommand(newDBRestoreCmd(), newDBRestoreAliasCmd())
	cmd.AddCommand(newDBPublicCmd())
	return cmd
}

// ─── storage, volume ──────────────────────────────────────────────────────────

func storageCmd() *cobra.Command {
	b := idArg("bucket")
	g := group("storage", "Object storage buckets",
		"Buckets hold objects behind an S3-compatible endpoint. <bucket> is the bucket name or id.", []string{"bucket", "buckets"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List buckets", Method: "GET", Path: "/storage/buckets",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "OBJECTS", Path: "object_count"}, {Head: "USED", Path: "used_bytes", Fmt: "bytes"}, {Head: "VISIBILITY", Path: "visibility"}, {Head: "REGION", Path: "region"}},
		},
		Op{
			Use: "create <name>", Short: "Create a bucket", Method: "POST", Path: "/storage/buckets", Args: []Arg{idArg("name")},
			Flags:  []Flag{{Name: "visibility", Usage: "Who can read objects", Default: "private", Enum: []string{"private", "public"}}, {Name: "region", Usage: "Region"}, {Name: "workspace", Usage: "Workspace slug or id (default: $MIOSA_WORKSPACE, then the profile's default workspace)", Field: "workspace_id"}, {Name: "workspace-id", Usage: "Workspace id", Field: "workspace_id", Hidden: true}, {Name: "class", Usage: "Data classification: public, internal, confidential or restricted", Field: "classification", Enum: []string{"public", "internal", "confidential", "restricted"}}},
			Before: settleCreateWorkspace,
			After:  reportWorkspaceLanding,
			Body:   func(a []string, body map[string]any) (map[string]any, error) { body["name"] = a[0]; return body, nil },
		},
		Op{Use: "get <bucket>", Aliases: []string{"show"}, Short: "Show a bucket", Method: "GET", Path: "/storage/buckets/{0}", Args: []Arg{b}},
		Op{
			Use: "objects <bucket>", Aliases: []string{"ls-objects"}, Short: "List objects", Method: "GET", Path: "/storage/buckets/{0}/objects", Args: []Arg{b},
			Flags: []Flag{{Name: "prefix", Usage: "Only keys with this prefix"}, {Name: "limit", Short: "n", Type: "int", Usage: "How many"}},
			Query: func(a []string, q url.Values) error {
				if l := q.Get("limit"); l != "" {
					q.Set("max_keys", l)
				}
				return nil
			},
			After: func(cmd *cobra.Command, resp any) error { objectRows(resp); return nil },
			Cols:  []Col{{Head: "KEY", Path: "key"}, {Head: "SIZE", Path: "size", Fmt: "bytes"}, {Head: "MODIFIED", Path: "last_modified", Fmt: "age"}},
		},
		Op{Use: "rm <bucket>", Aliases: []string{"delete"}, Short: "Delete a bucket", Method: "DELETE", Path: "/storage/buckets/{0}", Args: []Arg{b}, Confirm: "Permanently delete bucket {0}?", Done: "Deleted {0}"},
		notionImportOp(b),
	)
	g.AddCommand(storageObjectCmds()...)
	extendStorageGet(g)
	g.AddCommand(storageKeysCmd())
	return g
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

// machineCell shows the machine a schedule runs on. The API sends machine_id
// for the target; older responses carry only computer_id or sandbox_id.
func machineCell(row map[string]any) string {
	for _, key := range []string{"machine_id", "computer_id", "sandbox_id"} {
		if id := strings.TrimSpace(fmt.Sprint(row[key])); id != "" && id != "<nil>" {
			return id[:min(8, len(id))]
		}
	}
	return "-"
}

func scheduleCmd() *cobra.Command {
	j := idArg("job")
	return group("schedule", "Scheduled jobs", `A schedule runs a command at set times. <job> is a name or id.

  miosa schedule create --name ingest --cron "*/10 * * * *" --tz America/Chicago \
      --on my-box --command "sh /workspace/ingest.sh" --overlap skip --timeout 600 \
      --env MODE=prod --secret TOKEN=secret://org/INGEST_TOKEN

With --on the command runs on that machine, waking it if it is paused, and never
starts a new one. Without it each run starts a fresh sandbox, runs the command and
destroys the sandbox.

With --agent each tick is a run of that agent, with the workspace's approval
policies and budgets, and a timeout of up to an hour:

  miosa schedule create --name drafts --cron "0 9 * * 1-5" --agent drafter \
      --task "Draft follow-up notes for yesterday's calls" --timeout 1800
  miosa schedule run drafts
  miosa agent runs drafter

An agent schedule is one of the agent's triggers; change it with 'miosa agent triggers'.`, []string{"schedules", "schedule-jobs"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List jobs", Method: "GET", Path: "/cron-jobs",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "SCHEDULE", Path: "schedule"}, {Head: "STATE", Path: "state"}, {Head: "LAST RUN", Path: "last_run_at", Fmt: "age"}, {Head: "LAST", Path: "last_status"}},
		},
		Op{
			Use: "create", Short: "Create a schedule", Method: "POST", Path: "/cron-jobs",
			Flags: []Flag{
				{Name: "name", Usage: "Name, unique in the organization", Required: true},
				{Name: "cron", Usage: "Five-field cron expression, for example \"*/10 * * * *\"", Field: "schedule", Required: true},
				{Name: "tz", Usage: "IANA time zone the expression is evaluated in (default UTC)", Field: "timezone"},
				{Name: "command", Usage: "Shell command to run"},
				{Name: "agent", Usage: "Run this agent (name or id) instead of a command"},
				{Name: "task", Usage: "With --agent: the instruction each run receives"},
				{Name: "on", Usage: "Run on this existing machine (name or id) instead of a fresh sandbox", Field: "computer_id"},
				{Name: "timeout", Usage: "Seconds a run may take, up to 3600 (default 300; at least 60 with --agent)", Type: "int", Field: "timeout_sec"},
				{Name: "env", Usage: "Environment variable NAME=value, stored encrypted (repeatable)", Type: "kv"},
				{Name: "secret", Usage: "Environment variable NAME=secret://org/NAME or secret://<workspace>/NAME (repeatable)", Type: "kv", Field: "secret"},
				{Name: "overlap", Usage: "When a run is due while the previous one is still going", Enum: []string{"skip", "queue", "allow"}},
				{Name: "workspace", Usage: "Workspace slug or id (default: $MIOSA_WORKSPACE, then the profile's default workspace)", Field: "workspace_id"},
			},
			Before: before(resolveAgentSchedule, settleWorkspace("workspace", true)),
			After:  reportWorkspaceLanding,
			Body:   scheduleCreateBody,
			Human:  printCreatedSchedule,
		},
		Op{
			Use: "get <job>", Aliases: []string{"show"}, Short: "Show a job", Method: "GET", Path: "/cron-jobs/{0}", Args: []Arg{j},
			Detail: []Col{{Head: "NAME", Path: "name"}, {Head: "ID", Path: "id"}, {Head: "SCHEDULE", Path: "schedule"}, {Head: "TZ", Path: "timezone"}, {Head: "COMMAND", Path: "command"}, {Head: "ON", Compute: machineCell}, {Head: "OVERLAP", Path: "overlap"}, {Head: "TIMEOUT", Path: "timeout_sec"}, {Head: "ENV", Path: "env_names", Fmt: "list"}, {Head: "STATUS", Path: "status"}, {Head: "NEXT", Path: "next_run_at"}, {Head: "LAST", Path: "last_status"}},
		},
		Op{Use: "pause <job>", Short: "Pause a job", Method: "POST", Path: "/cron-jobs/{0}/pause", Args: []Arg{j}, Done: "Paused {0}"},
		Op{Use: "resume <job>", Short: "Resume a job", Method: "POST", Path: "/cron-jobs/{0}/resume", Args: []Arg{j}, Done: "Resumed {0}"},
		Op{
			Use: "run <job>", Aliases: []string{"run-now"}, Short: "Run a job now", Method: "POST", Path: "/cron-jobs/{0}/run-now", Args: []Arg{j},
			Flags:   []Flag{{Name: "agent", Usage: "The agent, for a schedule created with --agent", In: "local"}},
			Before:  runAgentSchedule,
			OnError: agentScheduleHint,
		},
		Op{
			Use: "executions <job>", Aliases: []string{"runs"}, Short: "List past runs", Method: "GET", Path: "/cron-jobs/{0}/executions", Args: []Arg{j},
			Cols: []Col{{Head: "ID", Path: "id"}, {Head: "STATE", Path: "state"}, {Head: "EXIT", Path: "exit_code"}, {Head: "MS", Path: "duration_ms"}, {Head: "STARTED", Path: "started_at", Fmt: "age"}},
		},
		Op{
			Use: "output <job> <run>", Aliases: []string{"logs"}, Short: "Show a run's exit code and output", Method: "GET",
			Path: "/cron-jobs/{0}/executions/{1}", Args: []Arg{j, idArg("run")},
			Example: "miosa schedule runs nightly\nmiosa schedule output nightly <run-id>",
			Human:   printScheduleRun,
		},
		Op{Use: "rm <job>", Aliases: []string{"delete"}, Short: "Delete a job", Method: "DELETE", Path: "/cron-jobs/{0}", Args: []Arg{j}, Confirm: "Delete job {0}?", Done: "Deleted {0}"},
	)
}

// scheduleCreateBody folds --env and --secret into one env object, and refuses what
// the schedule API cannot honour before a request is made.
func scheduleCreateBody(_ []string, body map[string]any) (map[string]any, error) {
	if agent, _ := body["agent"].(string); agent != "" {
		return agentScheduleBody(agent, body)
	}
	delete(body, "agent")
	if task, _ := body["task"].(string); task != "" {
		return nil, fmt.Errorf("--task needs --agent: a command schedule runs --command")
	}
	if cmd, _ := body["command"].(string); strings.TrimSpace(cmd) == "" {
		return nil, fmt.Errorf("--command is required")
	}
	env := map[string]any{}
	for _, key := range []string{"env", "secret"} {
		m, _ := body[key].(map[string]any)
		delete(body, key)
		for name, value := range m {
			if _, dup := env[name]; dup {
				return nil, fmt.Errorf("%s is set twice: give it once with --env or --secret", name)
			}
			if key == "secret" {
				ref, _ := value.(string)
				if !strings.HasPrefix(ref, "secret://") {
					return nil, fmt.Errorf("--secret %s=%s: the value must be a vault reference like secret://org/%s", name, ref, name)
				}
			}
			env[name] = value
		}
	}
	if len(env) > 0 {
		body["env"] = env
	}
	if t, ok := body["timeout_sec"].(int); ok && (t < 1 || t > 3600) {
		return nil, fmt.Errorf("--timeout must be between 1 and 3600 seconds")
	}
	return body, nil
}

// functionFlags are the settings create and update share. The source of the code is --code-file (one
// file) or --dir (a project directory, uploaded as a .tar.gz; for an http function its dependencies are
// installed on a build cell). Exactly one is needed to create.
func functionFlags(create bool) []Flag {
	flags := []Flag{
		{Name: "code-file", Usage: "File holding the function source", Field: "code_file"},
		{Name: "dir", Usage: "Project directory to upload; its dependencies (package-lock.json, requirements.txt) are installed on a build cell. http functions only", Field: "dir"},
		{Name: "entrypoint", Usage: "Module file name inside the bundle (default index.js; for python module:attr, default main:app)"},
		{Name: "env", Usage: "Environment variable KEY=VALUE (repeatable)", Type: "kv"},
		{Name: "memory-mb", Usage: "Memory in MB; an http function runs only at the sizes a host serves (default 512)", Type: "int", Field: "memory_mb"},
		{Name: "timeout-sec", Usage: "Per-request time limit in seconds, 1-300", Type: "int", Field: "timeout_sec"},
	}
	if create {
		flags = append(flags,
			Flag{Name: "kind", Usage: "Kind: script (default) or http (host function pool)", Enum: []string{"script", "http"}},
			Flag{Name: "runtime", Usage: "Runtime (an http function supports node and python)"},
			Flag{Name: "workspace-id", Usage: "Workspace id", Field: "workspace_id"},
			Flag{Name: "project", Usage: "Project id to attach the function to", Field: "project_id"},
		)
	}
	return flags
}

// functionSource wires the two sources of code. Body turns --code-file into `code` (and, for create, the name
// argument into `name`), refuses a wrong combination before anything is sent, and adds the upload as `source`.
// Before uploads --dir once the client exists. Body runs twice (a usage check, then the real request), so the
// upload is kept between them; one command runs one function, so the closure is not shared.
func functionSource(create bool) (func(*cobra.Command, *api.Client) error, func([]string, map[string]any) (map[string]any, error)) {
	var source map[string]any
	before := func(cmd *cobra.Command, c *api.Client) error {
		source = nil
		dir, _ := cmd.Flags().GetString("dir")
		if dir == "" {
			return nil
		}
		name := dir
		if args := cmd.Flags().Args(); len(args) > 0 {
			name = args[0]
		}
		var err error
		source, err = uploadServiceSource(cmd.Context(), c, name, dir)
		return err
	}
	body := func(args []string, body map[string]any) (map[string]any, error) {
		if create {
			body["name"] = args[0]
		}
		file, hasFile := body["code_file"].(string)
		dir, hasDir := body["dir"].(string)
		delete(body, "code_file")
		delete(body, "dir")
		switch {
		case hasFile && hasDir:
			return nil, fmt.Errorf("give --code-file or --dir, not both")
		case !hasFile && !hasDir && create:
			return nil, fmt.Errorf("give the source of the code: --code-file for one file, or --dir for a project directory")
		case hasDir:
			if st, err := os.Stat(dir); err != nil || !st.IsDir() {
				return nil, fmt.Errorf("--dir: %q is not a directory", dir)
			}
			if source != nil {
				body["source"] = source
			}
		case hasFile:
			raw, err := os.ReadFile(file)
			if err != nil {
				return nil, fmt.Errorf("--code-file: %w", err)
			}
			body["code"] = string(raw)
		}
		return body, nil
	}
	return before, body
}

// functionLogsQuery turns --since (a duration) into the unix-millisecond cursor the API takes.
func functionLogsQuery(_ []string, q url.Values) error {
	raw := q.Get("since_ms")
	if raw == "" {
		return nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fmt.Errorf("--since must be a duration such as 90s, 15m or 2h (got %q)", raw)
	}
	q.Set("since_ms", strconv.FormatInt(time.Now().Add(-d).UnixMilli(), 10))
	return nil
}

func functionDetail() []Col {
	return []Col{
		{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Kind", Path: "kind"},
		{Head: "Runtime", Path: "runtime"}, {Head: "State", Path: "state"},
		{Head: "Serving", Path: "serving"}, {Head: "Why not", Path: "serving_reason"},
		{Head: "Version", Path: "pool_version"},
		{Head: "Build", Path: "build.state"}, {Head: "Build error", Path: "build.error.code"},
		{Head: "Build hint", Path: "build.error.hint"},
	}
}

func functionCmd() *cobra.Command {
	f := idArg("function")
	createBefore, createBody := functionSource(true)
	updateBefore, updateBody := functionSource(false)
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
		Op{
			Use: "create <name>", Short: "Create a function from a source file",
			Long: `Create a function. --kind http runs it on the host function pool: a Node module whose
default export is a web handler (Request in, Response out) or a (req, res) function. Without
--kind the server creates the original stdout-envelope script function.

  miosa function create hook --kind http --code-file index.js
  miosa function create hook --kind http --code-file index.js --env MODE=prod --memory-mb 512
  miosa function create api --kind http --dir ./api --runtime node

An http function answers 'serving: false' with a reason (no_fn_hosts, not_published) until a host
that has its runtime accepts it; invoke answers FUNCTION_NOT_PUBLISHED meanwhile.

--dir uploads a project directory instead of one file. Its dependencies are installed on a build
cell (npm ci with a package-lock.json, pip install with a requirements.txt), so the function reads
'serving_reason: building' until that finishes; 'miosa function get' shows the build and
'miosa function build-log' its output.`,
			Method: "POST", Path: "/functions", Args: []Arg{idArg("name")},
			Flags:  functionFlags(true),
			Before: createBefore,
			Body:   createBody,
			Detail: functionDetail(),
		},
		Op{
			Use: "logs <function>", Short: "Show an http function's recent output",
			Long: `Prints what the function's handler wrote to stdout and stderr, merged across the hosts that
serve it and oldest first. This is a recent window kept by the running instances, not a log
store: lines from instances that were replaced or stopped are gone.

  miosa function logs hook
  miosa function logs hook --since 15m -n 200`,
			Method: "GET", Path: "/functions/{0}/logs", Args: []Arg{f}, Lines: "data",
			Flags: []Flag{
				{Name: "since", Usage: "Only lines newer than this, such as 90s, 15m or 2h", Field: "since_ms"},
				{Name: "lines", Short: "n", Type: "int", Usage: "Most lines to show (default 500, at most 2000)", Field: "limit"},
			},
			Query: functionLogsQuery,
		},
		Op{
			Use: "update <function>", Short: "Change a function's code or settings",
			Long: `Change a function. Only the flags you pass are sent. A new --code-file or a changed setting
publishes a new version of an http function to its hosts.

  miosa function update hook --code-file index.js
  miosa function update hook --dir ./api
  miosa function update hook --state disabled

--dir builds the project first; the function keeps serving its current version until the build is
ready, and the other flags of that command are applied together with it.`,
			Method: "PATCH", Path: "/functions/{0}", Args: []Arg{f},
			Flags:  append(functionFlags(false), Flag{Name: "state", Usage: "State: active or disabled", Enum: []string{"active", "disabled"}}),
			Before: updateBefore,
			Body:   updateBody,
			Detail: functionDetail(),
		},
		Op{
			Use: "build-log <function>", Short: "Show the dependency install of an uploaded project",
			Long: `Prints the last lines of the newest install of a function made from --dir: what the package
manager printed on the build cell. 'miosa function get' shows its state and, for a failure, the reason.

  miosa function build-log hook`,
			Method: "GET", Path: "/functions/{0}", Args: []Arg{f}, Lines: "build.log_tail",
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
	cmd := group("project", "Projects group resources inside a workspace", "<project> is a name, slug or id.", []string{"projects"},
		Op{
			Use: "list", Aliases: []string{"ls"}, Short: "List projects", Method: "GET", Path: "/projects",
			Cols: []Col{{Head: "NAME", Path: "name"}, {Head: "SLUG", Path: "slug"}, {Head: "ID", Path: "id", Fmt: "short"}, {Head: "WORKSPACE", Path: "workspace_id", Fmt: "short"}, {Head: "DEFAULT", Path: "is_default"}},
		},
		Op{Use: "get <project>", Aliases: []string{"show"}, Short: "Show a project", Method: "GET", Path: "/projects/{0}", Args: []Arg{idArg("project")}},
		Op{
			Use: "create <name>", Short: "Create a project", Method: "POST", Path: "/projects", Args: []Arg{idArg("name")},
			Long: `Create a project in a workspace. The API requires one, so without --workspace
it goes where the console would put it: the profile's default workspace
('miosa workspace use'), else the workspace the organization marks default, else
the one whose slug is "default", else its only workspace.`,
			Example: `  miosa project create shop
  miosa project create shop --workspace staging`,
			Flags: []Flag{
				{Name: "workspace", Usage: "Workspace: slug, name or id (default: the default workspace)", Field: "workspace_id"},
				{Name: "workspace-id", Usage: "Workspace id (same as --workspace)", In: "local"},
				{Name: "description", Usage: "What it is for"},
			},
			Body:   func(a []string, body map[string]any) (map[string]any, error) { body["name"] = a[0]; return body, nil },
			Before: settleProjectWorkspace,
		},
		Op{
			Use: "rm <project>", Aliases: []string{"delete"}, Short: "Delete a project and everything in it",
			// The API answers /projects/<id> only; a name or slug is rewritten to the
			// id before the request (internal/api.ResolvePath), so `miosa project rm
			// shop` works the way `miosa project get shop` does.
			Long: `Delete a project. A workspace's default project cannot be deleted, and a
project that still owns databases, deployments or sandboxes answers 409: move or
delete those first. Its services are stopped and deleted with it; while one is
still stopping (just after 'service delete') the delete is accepted and
finishes on its own as soon as it has stopped.`,
			Method: "DELETE", Path: "/projects/{0}", Args: []Arg{idArg("project")},
			Confirm: "Permanently delete project {0} and everything in it?",
			Human:   renderProjectDelete, OnError: retryProjectDeleteWhileStopping,
		},
	)
	cmd.AddCommand(projectVarsCmd())
	cmd.AddCommand(projectEnvironmentCmd())
	cmd.AddCommand(projectChangesCmd())
	return cmd
}

// ─── billing, audit, alerts, notifications ────────────────────────────────────

func billingCmd() *cobra.Command {
	cmd := group("billing", "Credits, spend, invoices and plan",
		`What you have and what you spent. Credits are cents. 'billing add-credits' adds credits
(a Stripe checkout in your browser) and waits for the payment; 'billing portal'
opens the Stripe portal for cards and invoices; plans change in the console
('miosa open billing').`, nil,
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
			Flags: []Flag{{Name: "period", Usage: "Window to read (default 30d)", Enum: []string{"7d", "30d", "mtd"}}},
			// /usage groups by user when asked nothing; the one row it then returns is the whole
			// organization, which is not a per-workspace figure.
			Query: func(_ []string, q url.Values) error { q.Set("group_by", "workspace_id"); return nil },
			Cols:  []Col{{Head: "WORKSPACE", Path: "workspace_id", Fmt: "short"}, {Head: "SPEND", Path: "credit_cents", Fmt: "cents"}, {Head: "SANDBOX HOURS", Path: "sandbox_seconds", Fmt: "hours"}, {Head: "COMPUTER HOURS", Path: "computer_seconds", Fmt: "hours"}},
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
	cmd.AddCommand(billingCapCmd())
	cmd.AddCommand(autoReloadCmd())
	cmd.AddCommand(paymentMethodsCmd())
	cmd.AddCommand(billingTopupCmd(), billingPortalCmd())
	return cmd
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
	cmd := o.command("")
	cmd.AddCommand(newAuditExportCmd())
	return cmd
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
			Cols: []Col{{Head: "ID", Path: "id", Fmt: "short"}, {Head: "WHEN", Path: "inserted_at", Fmt: "age"}, {Head: "READ", Compute: readCell}, {Head: "TITLE", Compute: notificationTitleCell}},
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
			Use: "update <workspace>", Short: "Rename a workspace or change its description and labels",
			Long: `Changes the name, slug or description of a workspace, and sets labels on it
(--meta key=value, repeatable): the entity it belongs to, an owner, tags. The labels you
give replace the ones it has, so pass all of them. Machines, deployments and data stay
where they are.`,
			Example: "  miosa workspace update acme --name 'Acme Inc' --description 'Client delivery'\n  miosa workspace update acme --meta entity=lunivate --meta owner=roberto",
			Method:  "PATCH", Path: "/workspaces/{0}", Args: []Arg{w},
			Flags: []Flag{
				{Name: "name", Usage: "New name"},
				{Name: "slug", Usage: "New slug"},
				{Name: "description", Usage: "What the workspace is for"},
				{Name: "meta", Type: "kv", Usage: "Label key=value (repeatable)", Field: "metadata"},
			},
			Done: "Updated {0}",
		},
		{
			Use: "transfer <workspace>", Short: "Move machines to another workspace",
			Long: `Moves sandboxes and computers from <workspace> to the workspace whose id you give with --to.
Name each one with --id (repeatable). Deployments, databases and buckets cannot be
moved yet; the command only moves what the platform accepts and reports how many moved.`,
			Example: "  miosa workspace transfer old --to new --id <sandbox-id> --id <computer-id>",
			Method:  "POST", Path: "/workspaces/{0}/transfer", Args: []Arg{w},
			Flags: []Flag{
				{Name: "to", Usage: "Id of the workspace to move them to (miosa workspace list)", Field: "target_workspace_id", Required: true},
				{Name: "id", Type: "strings", Usage: "Id of a sandbox or computer to move (repeatable)", Field: "resource_ids", Required: true},
			},
			Human: func(cmd *cobra.Command, _ []string, resp any) error {
				printerFor(cmd).Line("Moved %v of the machines you named", lookupPath(resp, "data.moved_count"))
				return nil
			},
		},
		{
			Use: "stats <workspace>", Short: "Counts of machines, deployments and databases",
			Method: "GET", Path: "/workspaces/{0}/stats", Args: []Arg{w},
		},
		{
			Use: "usage <workspace>", Short: "Spend and runtime in a workspace",
			Method: "GET", Path: "/workspaces/{0}/usage", Args: []Arg{w}, ListKey: "by_product",
			Cols: []Col{{Head: "PRODUCT", Path: "product"}, {Head: "TYPE", Path: "resource_type"}, {Head: "SPEND", Path: "total_cost_cents", Fmt: "cents"}, {Head: "HOURS", Path: "total_seconds", Fmt: "hours"}, {Head: "RESOURCES", Path: "resources"}},
		},
		{
			Use: "activity <workspace>", Short: "Recent activity in a workspace",
			Method: "GET", Path: "/workspaces/{0}/activity", Args: []Arg{w},
			Cols: []Col{{Head: "WHEN", Path: "occurred_at", Fmt: "age"}, {Head: "TYPE", Path: "action"}, {Head: "RESOURCE", Path: "resource_type"}, {Head: "MESSAGE", Compute: activityMessageCell}},
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
			// One entry per organization: `workspace use` here must not change
			// what another organization creates into. The flat field stays for
			// a single-organization profile and for older config files.
			org, _ := effectiveOrg()
			if org == "" {
				org = orDefault(cfg.ActiveOrg, cfg.Tenant)
			}
			if org != "" {
				if cfg.DefaultWorkspaces == nil {
					cfg.DefaultWorkspaces = map[string]string{}
				}
				cfg.DefaultWorkspaces[org] = ws.Slug
			} else {
				cfg.DefaultWorkspace = ws.Slug
			}
			if err := saveCurrentConfig(cfg); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"default_workspace": ws.Slug, "org": org, "id": ws.ID})
			}
			if org != "" {
				p.Success("Default workspace for %s is now %q", org, ws.Slug)
			} else {
				p.Success("Default workspace is now %q", ws.Slug)
			}
			return nil
		},
	}
}

// printScheduleRun shows one run: its state and exit code, then what it
// printed. The runs list holds only the summary; the output is per run.
func printScheduleRun(cmd *cobra.Command, _ []string, resp any) error {
	run, _ := resp.(map[string]any)
	if data, ok := run["data"].(map[string]any); ok {
		run = data
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "state %v, exit %v, %v ms\n", run["state"], valueOr(run["exit_code"], "-"), valueOr(run["duration_ms"], "-"))
	for _, stream := range []string{"stdout", "stderr"} {
		if text, _ := run[stream].(string); text != "" {
			fmt.Fprintf(out, "--- %s ---\n%s", stream, text)
			if !strings.HasSuffix(text, "\n") {
				fmt.Fprintln(out)
			}
		}
	}
	return nil
}

func valueOr(v any, fallback string) any {
	if v == nil {
		return fallback
	}
	return v
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

// purgeNodesReached renders "8 of 12", the nodes that answered the purge out of
// the nodes asked. An older server sends neither field, so the cell is empty.
func purgeNodesReached(row map[string]any) string {
	reached, ok1 := row["nodes_reached"].(float64)
	total, ok2 := row["nodes_total"].(float64)
	if !ok1 || !ok2 {
		return ""
	}
	return fmt.Sprintf("%d of %d", int(reached), int(total))
}

// projectDeleteRetry bounds how long 'project rm' waits on a server that still
// refuses a delete while a service stops; tests shorten it.
var projectDeleteRetry = struct {
	every, within time.Duration
}{3 * time.Second, 60 * time.Second}

// renderProjectDelete says whether the project is gone (204) or the server
// accepted the delete and finishes it once its services stop (202).
func renderProjectDelete(cmd *cobra.Command, args []string, resp any) error {
	p := printerFor(cmd)
	if status, _ := lookupPath(resp, "data.status").(string); status == "deleting" {
		p.Success("Deleting %s: it finishes as soon as its services have stopped", args[0])
		return nil
	}
	p.Success("Deleted %s", args[0])
	return nil
}

// retryProjectDeleteWhileStopping retries a delete the server refused with
// SERVICES_NOT_STOPPED (a server older than the accepted, finish-later delete:
// a service still stopping, typically right after 'service delete', QA 10-23)
// until it goes through or the wait ends.
func retryProjectDeleteWhileStopping(cmd *cobra.Command, c *api.Client, args []string, err error) error {
	ae, ok := asAPIError(err)
	if !ok || ae.Code != "SERVICES_NOT_STOPPED" || len(args) == 0 {
		return err
	}
	if !isJSON() {
		fmt.Fprintln(cmd.ErrOrStderr(), "miosa: a service is still stopping; waiting to delete the project")
	}
	deadline := time.Now().Add(projectDeleteRetry.within)
	for time.Now().Before(deadline) {
		select {
		case <-cmd.Context().Done():
			return err
		case <-time.After(projectDeleteRetry.every):
		}
		resp, retryErr := c.JSONAny(cmd.Context(), api.Request{Method: "DELETE", Path: "/projects/" + url.PathEscape(args[0])})
		if retryErr == nil {
			if isJSON() {
				return printerFor(cmd).JSON(map[string]any{"ok": true, "data": resp})
			}
			return renderProjectDelete(cmd, args, resp)
		}
		if ae, ok := asAPIError(retryErr); !ok || ae.Code != "SERVICES_NOT_STOPPED" {
			return retryErr
		}
		err = retryErr
	}
	return err
}
