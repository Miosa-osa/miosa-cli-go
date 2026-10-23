package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// Services are the Railway-shaped unit: a web service, a worker, a private
// service or a static site inside a project environment, with releases,
// variables, a public address and cron runs. The API calls them
// `/projects/:id/services`; the flat `/services/:id` routes do the same.
//
// This group is `miosa service` (singular). `miosa services` (plural) is the
// different, older product: named systemd services inside a sandbox.

// serviceTypes maps the product type to the API's kind and whether the service
// gets a public address by default.
var serviceTypes = map[string]struct {
	kind   string
	public bool
}{
	"web":      {"server", true},
	"private":  {"server", false},
	"worker":   {"worker", false},
	"static":   {"static", true},
	"cron":     {"cron", false},
	"schedule": {"cron", false},
	"database": {"database", false},
	// The API kinds are accepted too, so a user who read the reference is not
	// stopped by the product names.
	"server": {"server", true},
}

var serviceTypeNames = "web, worker, private, static, schedule, database"

// ── API shapes ───────────────────────────────────────────────────────────────

type serviceProject struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	IsDefault bool   `json:"is_default"`
}

type serviceAddress struct {
	Label *string `json:"label"`
	Host  *string `json:"host"`
	URL   *string `json:"url"`
}

type projectServiceRow struct {
	ID                 string                 `json:"id"`
	ProjectID          string                 `json:"project_id"`
	EnvironmentID      string                 `json:"environment_id"`
	Name               string                 `json:"name"`
	Slug               string                 `json:"slug"`
	Kind               string                 `json:"kind"`
	Source             map[string]any         `json:"source"`
	Build              map[string]any         `json:"build"`
	Settings           map[string]any         `json:"settings"`
	ActiveDeploymentID string                 `json:"active_deployment_id"`
	PendingChanges     *servicePendingChanges `json:"pending_changes"`
	PrivateDomain      string                 `json:"private_domain"`
	PublicAddress      *serviceAddress        `json:"public_address"`
	LockVersion        int                    `json:"lock_version"`
	CreatedAt          string                 `json:"created_at"`
	UpdatedAt          string                 `json:"updated_at"`
}

// servicePendingChanges is a variable write that made the running release (or
// the built artifact) stale. It is consumed when a new release starts.
type servicePendingChanges struct {
	Needs     string   `json:"needs"` // rebuild or redeploy
	Variables []string `json:"variables"`
	Because   string   `json:"because"` // own, shared, or "Web.DATABASE_URL"
	At        string   `json:"at"`
}

type serviceReason struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type serviceRelease struct {
	ID            string         `json:"id"`
	ServiceID     string         `json:"service_id"`
	EnvironmentID string         `json:"environment_id"`
	Number        int            `json:"number"`
	State         string         `json:"state"`
	Reason        *serviceReason `json:"reason"`
	DeadlineAt    *string        `json:"deadline_at"`
	Trigger       string         `json:"trigger"`
	ImageRef      string         `json:"image_ref"`
	Message       string         `json:"message"`
	RollbackOfID  *string        `json:"rollback_of_id"`
	Source        map[string]any `json:"source"`
	StartedAt     *string        `json:"started_at"`
	FinishedAt    *string        `json:"finished_at"`
	CreatedAt     string         `json:"created_at"`
}

type serviceEvent struct {
	Seq        int            `json:"seq"`
	FromState  *string        `json:"from_state"`
	ToState    string         `json:"to_state"`
	ReasonCode *string        `json:"reason_code"`
	Detail     map[string]any `json:"detail"`
	Actor      string         `json:"actor"`
	At         string         `json:"at"`
}

// UnmarshalJSON reads both event shapes: the API's release events route
// answers {from, to, reason: {code, detail}}, which the CLI printed as
// "- -> " because it knew only from_state/to_state/reason_code.
func (e *serviceEvent) UnmarshalJSON(b []byte) error {
	type plain serviceEvent
	var raw struct {
		plain
		From   *string `json:"from"`
		To     string  `json:"to"`
		Reason *struct {
			Code   string `json:"code"`
			Detail any    `json:"detail"`
		} `json:"reason"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*e = serviceEvent(raw.plain)
	if e.FromState == nil {
		e.FromState = raw.From
	}
	if e.ToState == "" {
		e.ToState = raw.To
	}
	if r := raw.Reason; r != nil && e.ReasonCode == nil && r.Code != "" {
		code := r.Code
		e.ReasonCode = &code
		if d, ok := r.Detail.(string); ok && d != "" && detailMessage(e.Detail) == "" {
			e.Detail = map[string]any{"detail": d}
		}
	}
	return nil
}

type serviceVariable struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	Source       string  `json:"source"`
	Sealed       bool    `json:"sealed"`
	Build        bool    `json:"build"`
	Expression   *string `json:"expression"`
	ValuePreview *string `json:"value_preview"`
	UpdatedAt    string  `json:"updated_at"`
}

// ── group ────────────────────────────────────────────────────────────────────

func serviceCmd() *cobra.Command {
	cmd := group("service", "Create and operate web services, workers, private services and static sites",
		`Services are the apps a project runs: a web service, a worker, a private service or
a static site, each with releases, variables and an optional public address.

<project> is a name, slug or id; <service> is a name, slug or id. The API only
resolves services by id, so the CLI looks the name up first.

  miosa service create cliniciq API --type web --repo https://github.com/acme/api
  miosa service deploy cliniciq API --wait
  miosa service logs cliniciq API
  miosa service scale cliniciq API --replicas 4
  miosa service vars set cliniciq API DATABASE_URL='${{Postgres.DATABASE_URL}}'
  miosa service domains cliniciq API

'miosa services' and 'miosa svc' are aliases of this group. A named background
process inside a sandbox is 'miosa daemon', a different product.`, []string{"services", "svc"})
	cmd.AddCommand(
		serviceCreateCmd(),
		serviceListCmd(),
		serviceGetCmd(),
		serviceUpdateCmd(),
		serviceDeployCmd(),
		serviceLogsCmd(),
		serviceScaleCmd(),
		serviceActionCmd("start", "Start a stopped service", false,
			"Start a stopped service and queue a release. A service that is already running is a conflict."),
		serviceActionCmd("stop", "Stop a service now", true,
			"Stop every active release now. Fails if a replica cannot be stopped, so the service is never reported stopped while it still runs."),
		serviceActionCmd("restart", "Restart a service", false,
			"Queue a release that restarts the service."),
		serviceDeleteCmd(),
		serviceReleasesCmd(),
		serviceReleaseCmd(),
		serviceRollbackCmd("rollback", "Serve this release's built image again"),
		serviceRollbackCmd("promote", "Serve this release again (the API's rollback)"),
		serviceCancelCmd(),
		serviceDomainsCmd(),
		serviceVarsCmd(),
		serviceRunsCmd(),
		serviceConnectionCmd(),
		serviceMetricsCmd(),
		serviceSettingsCmd(),
		serviceTemplatesCmd(),
		serviceSelectCmd(),
	)
	return cmd
}

// ── create ───────────────────────────────────────────────────────────────────

type serviceCreateFlags struct {
	name         string
	typ          string
	slug         string
	repo         string
	branch       string
	rootDir      string
	image        string
	dockerfile   string
	port         int
	startCommand string
	buildCommand string
	outputDir    string
	replicas     int
	region       string
	size         string
	cron         string
	envs         []string
	noPublic     bool
	env          string
}

func serviceCreateCmd() *cobra.Command {
	f := &serviceCreateFlags{}
	cmd := &cobra.Command{
		Use:   "create <project> <name>",
		Short: "Create a service",
		Long: `Create a web service, worker, private service, static site, Schedule or database
service. A web or static service gets a public address unless --no-public is given.

  miosa service create cliniciq API --type web --repo https://github.com/acme/api
  miosa service create cliniciq Jobs --type worker --repo https://github.com/acme/api --start-command "node worker.js"
  miosa service create cliniciq Cache --type private --image redis:7
  miosa service create cliniciq Nightly --type schedule --repo https://github.com/acme/api --schedule "0 3 * * *"
  miosa service create cliniciq API --repo https://github.com/acme/api --size dedicated-4`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			name := args[1]
			if strings.TrimSpace(name) == "" {
				return usagef("give the service a name")
			}
			typ, ok := serviceTypes[strings.ToLower(f.typ)]
			if !ok {
				return usagef("--type must be one of %s (got %q)", serviceTypeNames, f.typ)
			}
			if f.replicas != 0 && (f.replicas < 1 || f.replicas > 50) {
				return usagef("--replicas must be 1 to 50")
			}
			if err := checkServiceSize(f.size); err != nil {
				return err
			}
			if typ.kind == "cron" && strings.TrimSpace(f.cron) == "" {
				return usagef("a Schedule needs --schedule \"<5 field expression>\"")
			}
			if typ.kind != "cron" && f.cron != "" {
				return usagef("--schedule only applies to --type schedule")
			}
			if f.repo == "" && f.image == "" {
				return usagef("give a source: --repo <git url> or --image <ref>")
			}
			if f.repo != "" && f.image != "" {
				return usagef("give one source: --repo or --image, not both")
			}
			if f.dockerfile != "" && f.repo == "" {
				return usagef("--dockerfile applies to a --repo source")
			}
			if f.port != 0 && f.image == "" {
				return usagef("--port applies to an --image source")
			}

			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}

			body := map[string]any{"name": name, "kind": typ.kind}
			if f.slug != "" {
				body["slug"] = f.slug
			}
			source := map[string]any{}
			if f.repo != "" {
				source["type"] = "git"
				source["ref"] = f.repo
				if f.branch != "" {
					source["branch"] = f.branch
				}
				if f.rootDir != "" {
					source["root_dir"] = f.rootDir
				}
			} else {
				source["type"] = "image"
				source["ref"] = f.image
				if f.port != 0 {
					source["port"] = f.port
				}
			}
			body["source"] = source
			if f.dockerfile != "" {
				body["build"] = map[string]any{"dockerfile": f.dockerfile}
			}
			settings := map[string]any{}
			if f.startCommand != "" {
				settings["start_command"] = f.startCommand
			}
			if f.buildCommand != "" {
				settings["build_command"] = f.buildCommand
			}
			if f.outputDir != "" {
				settings["output_dir"] = f.outputDir
			}
			if f.replicas != 0 {
				settings["replicas"] = f.replicas
			}
			if f.region != "" {
				settings["region"] = f.region
			}
			if f.size != "" {
				settings["shape"] = f.size
			}
			if f.cron != "" {
				settings["cron_schedule"] = f.cron
			}
			if len(settings) > 0 {
				body["settings"] = settings
			}

			path := "/projects/" + url.PathEscape(projectID) + "/services"
			if f.env != "" {
				path += "?" + url.Values{"env": {f.env}}.Encode()
			}
			var created struct {
				Data projectServiceRow `json:"data"`
			}
			if err := c.API.Post(ctx, path, body, &created); err != nil {
				return die(err)
			}
			svc := created.Data

			if typ.public && !f.noPublic {
				if err := enableServiceAddress(ctx, c.API, projectID, svc.ID, ""); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: created, but the public address was not enabled: %v\n", err)
				}
			}
			if len(f.envs) > 0 {
				vars, err := serviceVars(f.envs)
				if err != nil {
					return usagef("%v", err)
				}
				if err := putServiceVariables(ctx, c.API, projectID, svc.ID, vars); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: created, but the variables were not saved: %v\n", err)
				}
			}

			if isJSON() {
				return p.JSON(svc)
			}
			p.Success("Created %s service %q", typ.kind, svc.Name)
			renderService(p, svc)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.typ, "type", "web", "Service type: "+serviceTypeNames)
	fs.StringVar(&f.slug, "slug", "", "URL label (default: from the name)")
	fs.StringVar(&f.repo, "repo", "", "Git repository URL")
	fs.StringVar(&f.branch, "branch", "", "Git branch (default: the repository default)")
	fs.StringVar(&f.rootDir, "root-dir", "", "Directory inside the repository to build")
	fs.StringVar(&f.image, "image", "", "Container image reference")
	fs.StringVar(&f.dockerfile, "dockerfile", "", "Dockerfile path (needs Services build cells)")
	fs.IntVar(&f.port, "port", 0, "Port the container listens on (--image only)")
	fs.StringVar(&f.startCommand, "start-command", "", "Command the service starts with")
	fs.StringVar(&f.buildCommand, "build-command", "", "Static services: build command (Railpack builds only)")
	fs.StringVar(&f.outputDir, "output-dir", "", "Static services: directory to serve (default: detected)")
	fs.IntVar(&f.replicas, "replicas", 0, "Replicas to run (1-50)")
	fs.StringVar(&f.region, "region", "", "Region: us-west, us-east or us-mia")
	fs.StringVar(&f.size, "size", "", "Dedicated vCPU size: "+serviceSizeNames)
	fs.StringVar(&f.cron, "schedule", "", "Five field schedule expression (with --type schedule)")
	fs.StringArrayVar(&f.envs, "env", nil, "Variable KEY=VALUE (repeatable); a value may be a ${{...}} reference")
	fs.BoolVar(&f.noPublic, "no-public", false, "Do not give a web or static service a public address")
	fs.StringVar(&f.env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

// ── list, get, update ────────────────────────────────────────────────────────

func serviceListCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:     "list <project>",
		Aliases: []string{"ls"},
		Short:   "List a project's services",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			services, err := listServices(ctx, c.API, projectID, env)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(services)
			}
			rows := make([][]string, 0, len(services))
			for _, s := range services {
				rows = append(rows, []string{s.Name, s.Slug, s.Kind, addressURL(s), shortID(s.ID), s.UpdatedAt})
			}
			p.Table([]string{"NAME", "SLUG", "TYPE", "URL", "ID", "UPDATED"}, rows)
			if len(rows) == 0 {
				p.Line("No services. Create one with 'miosa service create %s <name> --type web --repo <url>'.", args[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func serviceGetCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:     "get <project> <service>",
		Aliases: []string{"show"},
		Short:   "Show one service",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			_, svc, err := openService(cmd.Context(), c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(svc)
			}
			renderService(p, *svc)
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func serviceUpdateCmd() *cobra.Command {
	var env, name, startCommand, region, spread, strategy, size string
	var replicas, rolloutStepS, terminateGrace int
	var buildCommand, outputDir string
	var rolloutSteps []int
	cmd := &cobra.Command{
		Use:   "update <project> <service>",
		Short: "Change a service's name or settings",
		Long: `Change a service's name or settings. Settings are merged: only the flags you
pass change.

  miosa service update cliniciq API --replicas 3
  miosa service update cliniciq API --name backend --start-command "node dist/server.js"
  miosa service update cliniciq API --strategy rolling --rollout-step 10 --rollout-step 50 --rollout-step 100
  miosa service update cliniciq API --spread single_host
  miosa service update cliniciq API --size dedicated-4
  miosa service update cliniciq site --build-command "npm run build" --output-dir dist

--spread is auto (replicas land on different hosts where the network allows) or
single_host. --strategy is replace or rolling; rolling starts the new version
beside the old one, shifts traffic in steps and rolls back automatically if the
new replica is unhealthy. --rollout-step must ascend and end at 100.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			settings := map[string]any{}
			if cmd.Flags().Changed("start-command") {
				settings["start_command"] = startCommand
			}
			if cmd.Flags().Changed("replicas") {
				if replicas < 1 || replicas > 50 {
					return usagef("--replicas must be 1 to 50")
				}
				settings["replicas"] = replicas
			}
			if cmd.Flags().Changed("region") {
				settings["region"] = region
			}
			if cmd.Flags().Changed("build-command") {
				settings["build_command"] = buildCommand
			}
			if cmd.Flags().Changed("output-dir") {
				settings["output_dir"] = outputDir
			}
			if cmd.Flags().Changed("spread") {
				settings["spread"] = spread
			}
			if cmd.Flags().Changed("strategy") {
				settings["strategy"] = strategy
			}
			if cmd.Flags().Changed("rollout-step") {
				for i := 1; i < len(rolloutSteps); i++ {
					if rolloutSteps[i] <= rolloutSteps[i-1] {
						return usagef("--rollout-step must ascend, got %v", rolloutSteps)
					}
				}
				if last := rolloutSteps[len(rolloutSteps)-1]; last != 100 {
					return usagef("--rollout-step must end at 100, got %d", last)
				}
				settings["rollout_steps"] = rolloutSteps
			}
			if cmd.Flags().Changed("rollout-step-s") {
				settings["rollout_step_s"] = rolloutStepS
			}
			if cmd.Flags().Changed("terminate-grace") {
				settings["terminate_grace_s"] = terminateGrace
			}
			if cmd.Flags().Changed("size") {
				if size == "" {
					return usagef("--size must be one of %s", serviceSizeNames)
				}
				if err := checkServiceSize(size); err != nil {
					return err
				}
				settings["shape"] = size
			}
			if !cmd.Flags().Changed("name") && len(settings) == 0 {
				return usagef("nothing to change: pass --name, --start-command, --replicas, --region, --spread, --strategy, --size or a rollout flag")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			body := map[string]any{"lock_version": svc.LockVersion}
			if cmd.Flags().Changed("name") {
				body["name"] = name
			}
			if len(settings) > 0 {
				body["settings"] = settings
			}
			var updated struct {
				Data projectServiceRow `json:"data"`
			}
			if err := c.API.Patch(ctx, servicePath(projectID, svc.ID), body, &updated); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(updated.Data)
			}
			p.Success("Updated %q", updated.Data.Name)
			renderService(p, updated.Data)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	fs.StringVar(&name, "name", "", "New name")
	fs.StringVar(&startCommand, "start-command", "", "Command the service starts with")
	fs.IntVar(&replicas, "replicas", 0, "Replicas to run (1-50)")
	fs.StringVar(&buildCommand, "build-command", "", "Static services: build command (Railpack builds only)")
	fs.StringVar(&outputDir, "output-dir", "", "Static services: directory to serve (default: detected)")
	fs.StringVar(&region, "region", "", "Region: us-west, us-east or us-mia")
	fs.StringVar(&spread, "spread", "", "Replica placement: auto or single_host")
	fs.StringVar(&strategy, "strategy", "", "Release strategy: replace or rolling")
	fs.IntSliceVar(&rolloutSteps, "rollout-step", nil, "Traffic step in percent (repeatable, ascending, ends at 100)")
	fs.IntVar(&rolloutStepS, "rollout-step-s", 0, "Seconds to wait between steps (10-3600)")
	fs.IntVar(&terminateGrace, "terminate-grace", 0, "Seconds the old version drains during a rolling deploy (>=10)")
	fs.StringVar(&size, "size", "", "Dedicated vCPU size: "+serviceSizeNames)
	return cmd
}

// serviceSizes are the dedicated vCPU sizes (one hardware thread per vCPU, never shared, 4 GiB per vCPU),
// the values of the service setting "shape". They are available only where the platform has switched the
// class on; the API refuses the setting otherwise.
var serviceSizes = []string{"dedicated-2", "dedicated-4", "dedicated-8", "dedicated-16"}

var serviceSizeNames = strings.Join(serviceSizes, ", ")

// checkServiceSize accepts an empty size (not given) or one of serviceSizes.
func checkServiceSize(size string) error {
	if size == "" {
		return nil
	}
	for _, s := range serviceSizes {
		if s == size {
			return nil
		}
	}
	return usagef("--size must be one of %s (got %q)", serviceSizeNames, size)
}

// ── deploy ───────────────────────────────────────────────────────────────────

type serviceDeployFlags struct {
	message string
	dir     string
	repo    string
	branch  string
	rootDir string
	image   string
	port    int
	env     string
	wait    bool
	timeout time.Duration
}

func serviceDeployCmd() *cobra.Command {
	f := &serviceDeployFlags{}
	cmd := &cobra.Command{
		Use:   "deploy <project> <service>",
		Short: "Build and release a service",
		Long: `Queue a release. With no source flag the service's own source is rebuilt. With
--dir a local directory is uploaded as the source.

  miosa service deploy cliniciq API
  miosa service deploy cliniciq API --dir . --message "fix the header" --wait
  miosa service deploy cliniciq API --repo https://github.com/acme/api --branch main`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			given := 0
			for _, v := range []string{f.dir, f.repo, f.image} {
				if v != "" {
					given++
				}
			}
			if given > 1 {
				return usagef("give one source: --dir, --repo or --image")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], f.env)
			if err != nil {
				return err
			}

			body := map[string]any{}
			if f.message != "" {
				body["message"] = f.message
			}
			switch {
			case f.dir != "":
				source, err := uploadServiceSource(ctx, c.API, svc.Name, f.dir)
				if err != nil {
					return err
				}
				body["source"] = source
			case f.repo != "":
				source := map[string]any{"type": "git", "ref": f.repo}
				if f.branch != "" {
					source["branch"] = f.branch
				}
				if f.rootDir != "" {
					source["root_dir"] = f.rootDir
				}
				body["source"] = source
			case f.image != "":
				source := map[string]any{"type": "image", "ref": f.image}
				if f.port != 0 {
					source["port"] = f.port
				}
				body["source"] = source
			}

			var queued struct {
				Data serviceRelease `json:"data"`
			}
			path := servicePath(projectID, svc.ID) + "/deployments"
			if err := c.API.Post(ctx, path, body, &queued); err != nil {
				return die(err)
			}
			release := queued.Data
			if f.wait {
				release, err = waitForRelease(ctx, c.API, projectID, svc.ID, release.ID, f.timeout, cmd)
				if err != nil {
					return err
				}
			}
			return renderServiceRelease(p, release)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.message, "message", "", "Release message")
	fs.StringVar(&f.dir, "dir", "", "Upload this local directory as the source")
	fs.StringVar(&f.repo, "repo", "", "Build this git repository URL")
	fs.StringVar(&f.branch, "branch", "", "Git branch")
	fs.StringVar(&f.rootDir, "root-dir", "", "Directory inside the repository to build")
	fs.StringVar(&f.image, "image", "", "Deploy this container image")
	fs.IntVar(&f.port, "port", 0, "Port the container listens on (--image only)")
	fs.StringVar(&f.env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	fs.BoolVar(&f.wait, "wait", false, "Wait until the release is live or fails")
	fs.DurationVar(&f.timeout, "timeout", 10*time.Minute, "How long --wait may take")
	return cmd
}

// ── logs ─────────────────────────────────────────────────────────────────────

func serviceLogsCmd() *cobra.Command {
	var env, releaseRef, buildID, replica string
	var follow, build, events bool
	var tail int
	cmd := &cobra.Command{
		Use:   "logs <project> <service>",
		Short: "Show a service's output, a release's events, or a build's log",
		Long: `Show what the service's process writes (stdout and stderr), the tail of each
replica's log. With several replicas each line names its replica; --replica reads
one. --follow keeps printing new lines until Ctrl-C.

With --events this shows what happened to a release instead: every state
transition, and the reason a release failed. The newest release is used unless
--release names one (a release number or id).

With --build this shows the build log: the build the release used, or the build
named by --build-id. --follow keeps reading until the build finishes. A service
built from an image has no build log.

--tail is the number of lines from the end of the log (runtime 1-1000, build
1-2000).`,
		Example: `  miosa service logs shop api
  miosa service logs shop api --follow
  miosa service logs shop api --events
  miosa service logs shop api --build --follow`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			events = events || releaseRef != ""
			if events && (build || buildID != "") {
				return usagef("--events and --build show different logs; pick one")
			}
			if follow && events {
				return usagef("--follow does not apply to --events; release events are a finished history")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}

			if build || buildID != "" {
				target := buildID
				if target == "" {
					target = "latest"
				}
				return serviceBuildLogs(ctx, cmd, c.API, projectID, svc, target, tail, follow)
			}
			if !events {
				if err := printServiceRuntimeLogs(cmd, c.API, projectID, svc.ID, replica, tail, follow); err != nil {
					return die(err)
				}
				return nil
			}

			release, err := latestOrNamedRelease(ctx, c.API, projectID, svc.ID, releaseRef)
			if err != nil {
				return err
			}
			evs, err := serviceEvents(ctx, c.API, projectID, svc.ID, release.ID)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(map[string]any{"release": release, "events": evs})
			}
			p.Line("Release %d (%s) %s", release.Number, release.State, release.ID)
			if release.Reason != nil {
				p.Line("Reason: %s %s", release.Reason.Code, release.Reason.Detail)
			}
			for _, e := range evs {
				from := "-"
				if e.FromState != nil {
					from = *e.FromState
				}
				line := fmt.Sprintf("%s  %s -> %s", e.At, from, e.ToState)
				if e.ReasonCode != nil {
					line += "  " + *e.ReasonCode
				}
				if detail := detailMessage(e.Detail); detail != "" {
					line += ": " + detail
				}
				p.Line("%s", line)
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	fs.BoolVar(&events, "events", false, "Show a release's events instead of the service's output")
	fs.StringVar(&releaseRef, "release", "", "With --events: release number or id (default: the newest)")
	fs.BoolVar(&build, "build", false, "Show the build log instead of the service's output")
	fs.StringVar(&buildID, "build-id", "", "Build id to read with --build (default: latest)")
	fs.StringVar(&replica, "replica", "", "Read one replica's log (its id)")
	fs.IntVar(&tail, "tail", 200, "Lines from the end of the log (runtime 1-1000, build 1-2000)")
	fs.BoolVarP(&follow, "follow", "f", false, "Keep printing new lines (with --build: until the build finishes)")
	return cmd
}

// serviceBuildLog is one page of a build log.
type serviceBuildLog struct {
	Build    serviceBuild `json:"build"`
	Text     string       `json:"text"`
	Offset   int64        `json:"offset"`
	Complete bool         `json:"complete"`
}

// serviceBuild is the build a log page belongs to. SourceCommit is the commit the
// build packed, and DurationMS how long it ran (still counting while it builds);
// both are absent on a build that has not started or that packed no commit.
type serviceBuild struct {
	ID           string         `json:"id"`
	DeploymentID *string        `json:"deployment_id"`
	State        string         `json:"state"`
	Attempt      int            `json:"attempt"`
	Builder      string         `json:"builder"`
	SourceCommit *string        `json:"source_commit"`
	DurationMS   *int64         `json:"duration_ms"`
	Reason       *serviceReason `json:"reason"`
	StartedAt    *string        `json:"started_at"`
	FinishedAt   *string        `json:"finished_at"`
}

// serviceBuildLogs reads a build log, and with follow keeps reading from the
// offset the server reports until the build is complete.
func serviceBuildLogs(ctx context.Context, cmd *cobra.Command, c *api.Client, projectID string, svc *projectServiceRow, buildID string, tail int, follow bool) error {
	p := printerFor(cmd)
	path := servicePath(projectID, svc.ID) + "/builds/" + url.PathEscape(buildID) + "/logs"
	after := int64(-1)
	shownState := ""

	for {
		q := url.Values{}
		if after >= 0 {
			q.Set("after", strconv.FormatInt(after, 10))
		} else if tail > 0 {
			q.Set("tail", strconv.Itoa(tail))
		}

		var page struct {
			Data serviceBuildLog `json:"data"`
		}
		if err := c.Get(ctx, path, q, &page); err != nil {
			return serviceBuildLogError(err, svc, buildID)
		}

		if isJSON() {
			if err := p.JSONLine(page.Data); err != nil {
				return err
			}
		} else {
			// The log text is the output and goes to stdout; the build it belongs to
			// goes to stderr, so `... --build | grep` still reads only the log.
			if page.Data.Build.State != shownState {
				fmt.Fprintln(cmd.ErrOrStderr(), buildLine(page.Data.Build))
				shownState = page.Data.Build.State
			}
			if page.Data.Text != "" {
				fmt.Fprint(p.Writer(), page.Data.Text)
			}
		}

		if !follow || page.Data.Complete {
			return nil
		}
		after = page.Data.Offset

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// buildLine is the one-line summary of the build a log came from: which build it
// is, what state it is in, the commit it packed, and how long it has run.
func buildLine(b serviceBuild) string {
	commit := "-"
	if b.SourceCommit != nil {
		if len(*b.SourceCommit) >= 7 {
			commit = (*b.SourceCommit)[:7]
		} else if *b.SourceCommit != "" {
			commit = *b.SourceCommit
		}
	}
	return fmt.Sprintf("Build %s %s %s %s", b.ID, b.State, commit, buildDuration(b.DurationMS))
}

func buildDuration(ms *int64) string {
	if ms == nil {
		return "-"
	}
	d := time.Duration(*ms) * time.Millisecond
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return d.Round(time.Second).String()
	}
}

// serviceBuildLogError names the condition a reader can act on instead of passing
// the raw status through. A 404 covers three cases, so it reads the service to tell
// an image-built service (which has no build log) from a build id that is not this
// service's.
func serviceBuildLogError(err error, svc *projectServiceRow, buildID string) error {
	var ae *api.Error
	if errors.As(err, &ae) {
		switch {
		case ae.Status == http.StatusNotFound:
			message := "this service has no builds yet"
			switch {
			case buildID != "latest":
				// An explicit id that is not this service's is the more useful answer,
				// even when the service also happens to be built from an image.
				message = "no such build for this service"
			case svc.Source["type"] == "image":
				message = "no build log for this service; a service built from an image has none"
			}
			return die(&api.Error{
				Status:  http.StatusNotFound,
				Code:    "BUILD_LOG_NOT_FOUND",
				Message: message,
			})
		case ae.Code == "BUILD_LOG_UNAVAILABLE":
			return die(&api.Error{
				Status:    ae.Status,
				Code:      ae.Code,
				Message:   "the build log store could not be read; try again",
				RequestID: ae.RequestID,
			})
		}
	}
	return die(err)
}

// ── scale, start, stop, restart ──────────────────────────────────────────────

func serviceScaleCmd() *cobra.Command {
	var env, region string
	var replicas int
	cmd := &cobra.Command{
		Use:   "scale <project> <service>",
		Short: "Change replicas or region",
		Long: `Change the replica count or the region and queue a release that applies it.

  miosa service scale cliniciq API --replicas 4
  miosa service scale cliniciq API --region us-east`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if !cmd.Flags().Changed("replicas") && region == "" {
				return usagef("nothing to scale: pass --replicas or --region")
			}
			body := map[string]any{}
			if cmd.Flags().Changed("replicas") {
				if replicas < 1 || replicas > 50 {
					return usagef("--replicas must be 1 to 50")
				}
				body["replicas"] = replicas
			}
			if region != "" {
				body["region"] = region
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			var out struct {
				Data serviceRelease `json:"data"`
			}
			if err := c.API.Post(ctx, servicePath(projectID, svc.ID)+"/scale", body, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Queued release %d to scale %q", out.Data.Number, svc.Name)
			return renderServiceRelease(p, out.Data)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	fs.IntVar(&replicas, "replicas", 0, "Replicas to run (1-50)")
	fs.StringVar(&region, "region", "", "Region: us-west, us-east or us-mia")
	return cmd
}

var serviceActionLabels = map[string]string{"start": "Start", "stop": "Stop", "restart": "Restart"}

func serviceActionCmd(action, short string, confirm bool, long string) *cobra.Command {
	var env string
	var yes bool
	cmd := &cobra.Command{
		Use:   action + " <project> <service>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if confirm && !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("%s service %q?", action, args[1])); err != nil {
					return err
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			var out struct {
				Data serviceRelease `json:"data"`
			}
			if err := c.API.Post(ctx, servicePath(projectID, svc.ID)+"/"+action, nil, &out); err != nil {
				return die(err)
			}
			if action == "stop" {
				// stop answers {stopped: n}, not a release.
				if isJSON() {
					return p.JSON(map[string]any{"service": svc.Name, "status": "stopped"})
				}
				p.Success("Stopped %q", svc.Name)
				return nil
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("%s queued release %d for %q", serviceActionLabels[action], out.Data.Number, svc.Name)
			return renderServiceRelease(p, out.Data)
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	if confirm {
		cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation")
	}
	return cmd
}

// ── delete ───────────────────────────────────────────────────────────────────

func serviceDeleteCmd() *cobra.Command {
	var env string
	var yes bool
	cmd := &cobra.Command{
		Use:     "delete <project> <service>",
		Aliases: []string{"rm"},
		Short:   "Delete a service",
		Long:    "Delete a service and stop its replicas. The service disappears from reads at once.",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Permanently delete service %q?", args[1])); err != nil {
					return err
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			if err := c.API.Delete(ctx, servicePath(projectID, svc.ID), nil, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"id": svc.ID, "state": "deleting"})
			}
			p.Success("Deleted %q", svc.Name)
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation")
	return cmd
}

// ── releases ─────────────────────────────────────────────────────────────────

func serviceReleasesCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:     "releases <project> <service>",
		Aliases: []string{"history"},
		Short:   "List a service's releases",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			releases, err := listReleases(ctx, c.API, projectID, svc.ID)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(releases)
			}
			rows := make([][]string, 0, len(releases))
			for _, r := range releases {
				reason := ""
				if r.Reason != nil {
					reason = r.Reason.Code
				}
				rows = append(rows, []string{strconv.Itoa(r.Number), r.State, reason, r.Trigger, clip(r.Message, 40), shortID(r.ID), r.CreatedAt})
			}
			p.Table([]string{"#", "STATE", "REASON", "TRIGGER", "MESSAGE", "ID", "CREATED"}, rows)
			if len(rows) == 0 {
				p.Line("No releases yet. Run 'miosa service deploy %s %s'.", args[0], args[1])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func serviceReleaseCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   "release <project> <service> <release>",
		Short: "Show one release",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			release, err := latestOrNamedRelease(ctx, c.API, projectID, svc.ID, args[2])
			if err != nil {
				return err
			}
			return renderServiceRelease(p, *release)
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func serviceRollbackCmd(use, short string) *cobra.Command {
	var env string
	var yes bool
	cmd := &cobra.Command{
		Use:   use + " <project> <service> <release>",
		Short: short,
		Long: `Queue a release that serves an earlier release's built image with its frozen
source and settings. 'promote' is the same operation; the Services API calls it
rollback.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Serve release %s of %q again?", args[2], args[1])); err != nil {
					return err
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			target, err := latestOrNamedRelease(ctx, c.API, projectID, svc.ID, args[2])
			if err != nil {
				return err
			}
			var out struct {
				Data serviceRelease `json:"data"`
			}
			path := "/service-deployments/" + url.PathEscape(target.ID) + "/rollback"
			if err := c.API.Post(ctx, path, nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Queued release %d serving release %d", out.Data.Number, target.Number)
			return renderServiceRelease(p, out.Data)
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation")
	return cmd
}

func serviceCancelCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   "cancel <project> <service> <release>",
		Short: "Cancel a queued or building release",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			target, err := latestOrNamedRelease(ctx, c.API, projectID, svc.ID, args[2])
			if err != nil {
				return err
			}
			var out struct {
				Data serviceRelease `json:"data"`
			}
			path := "/service-deployments/" + url.PathEscape(target.ID) + "/cancel"
			if err := c.API.Post(ctx, path, nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Canceled release %d", target.Number)
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

// ── domains ──────────────────────────────────────────────────────────────────

func serviceDomainsCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:     "domains <project> <service>",
		Aliases: []string{"domain"},
		Short:   "Show a service's addresses",
		Long: `Show the private address every service has and the public address it may have.

  miosa service domains cliniciq API
  miosa service domains enable cliniciq API --label api
  miosa service domains disable cliniciq API`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			_, svc, err := openService(cmd.Context(), c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(map[string]any{"private_domain": svc.PrivateDomain, "public_address": svc.PublicAddress})
			}
			p.Fields([][2]string{
				{"Service", svc.Name},
				{"Private", svc.PrivateDomain},
				{"Public", addressURL(*svc)},
				{"Label", addressLabel(*svc)},
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	var enableEnv string
	var label string
	enable := &cobra.Command{
		Use:   "enable <project> <service>",
		Short: "Give a service a public address",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], enableEnv)
			if err != nil {
				return err
			}
			if err := enableServiceAddress(ctx, c.API, projectID, svc.ID, label); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "enabled"})
			}
			p.Success("Enabled a public address for %q", svc.Name)
			return nil
		},
	}
	enable.Flags().StringVar(&enableEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	enable.Flags().StringVar(&label, "label", "", "Host label (default: the service slug)")

	var disableEnv string
	disable := &cobra.Command{
		Use:   "disable <project> <service>",
		Short: "Remove a service's public address",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], disableEnv)
			if err != nil {
				return err
			}
			if err := c.API.Delete(ctx, servicePath(projectID, svc.ID)+"/public-address", nil, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "disabled"})
			}
			p.Success("Removed the public address of %q", svc.Name)
			return nil
		},
	}
	disable.Flags().StringVar(&disableEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	cmd.AddCommand(enable, disable)
	return cmd
}

// ── variables ────────────────────────────────────────────────────────────────

func serviceVarsCmd() *cobra.Command {
	cmd := group("vars", "Variables of a service",
		`Variables are sealed at rest and never printed except by 'reveal'. A value may be a
reference: ${{Postgres.DATABASE_URL}} (another service), ${{shared.API_BASE}} (an
environment shared variable), ${{NAME}} (another variable of this service) or
${{secret(32)}} (a generated secret).

  miosa service vars list cliniciq API
  miosa service vars set cliniciq API DATABASE_URL='${{Postgres.DATABASE_URL}}' NODE_ENV=production
  miosa service vars effective cliniciq API
  miosa service vars reveal cliniciq API DATABASE_URL`, nil)

	var listEnv string
	list := &cobra.Command{
		Use:   "list <project> <service>",
		Short: "List a service's variables (values masked)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], listEnv)
			if err != nil {
				return err
			}
			vars, err := listServiceVariables(ctx, c.API, projectID, svc.ID)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(vars)
			}
			rows := make([][]string, 0, len(vars))
			for _, v := range vars {
				rows = append(rows, []string{v.Name, v.Kind, v.Source, yesNo(v.Build), deref(v.ValuePreview), deref(v.Expression)})
			}
			p.Table([]string{"NAME", "KIND", "SOURCE", "BUILD", "PREVIEW", "EXPRESSION"}, rows)
			if len(rows) == 0 {
				p.Line("No variables. Set one with 'miosa service vars set %s %s KEY=VALUE'.", args[0], args[1])
			}
			return nil
		},
	}
	list.Flags().StringVar(&listEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	var setEnv string
	set := &cobra.Command{
		Use:   "set <project> <service> KEY=VALUE [KEY=VALUE...]",
		Short: "Set variables",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			vars, err := serviceVars(args[2:])
			if err != nil {
				return usagef("%v", err)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], setEnv)
			if err != nil {
				return err
			}
			if err := putServiceVariables(ctx, c.API, projectID, svc.ID, vars); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"service": svc.Name, "variables": keys(vars)})
			}
			p.Success("Set %s on %q", strings.Join(keys(vars), ", "), svc.Name)
			p.Line("Deploy for the change to take effect: miosa service deploy %s %s", args[0], args[1])
			return nil
		},
	}
	set.Flags().StringVar(&setEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	var unsetEnv string
	unset := &cobra.Command{
		Use:   "unset <project> <service> <NAME>[,NAME...]",
		Short: "Delete variables",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], unsetEnv)
			if err != nil {
				return err
			}
			path := servicePath(projectID, svc.ID) + "/variables/" + url.PathEscape(args[2])
			if err := c.API.Delete(ctx, path, nil, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"service": svc.Name, "deleted": args[2]})
			}
			p.Success("Deleted %s from %q", args[2], svc.Name)
			return nil
		},
	}
	unset.Flags().StringVar(&unsetEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	var effectiveEnv string
	effective := &cobra.Command{
		Use:   "effective <project> <service>",
		Short: "Show the variables a deploy would receive, references resolved",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], effectiveEnv)
			if err != nil {
				return err
			}
			var out struct {
				Data struct {
					Variables []struct {
						Name         string   `json:"name"`
						Source       string   `json:"source"`
						Layer        string   `json:"layer"`
						Sealed       bool     `json:"sealed"`
						Build        bool     `json:"build"`
						ValuePreview *string  `json:"value_preview"`
						References   []string `json:"references"`
					} `json:"variables"`
					Problems []any `json:"problems"`
				} `json:"data"`
			}
			path := servicePath(projectID, svc.ID) + "/variables/effective"
			if err := c.API.Get(ctx, path, nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			rows := make([][]string, 0, len(out.Data.Variables))
			for _, v := range out.Data.Variables {
				rows = append(rows, []string{v.Name, v.Source, v.Layer, yesNo(v.Build), deref(v.ValuePreview)})
			}
			p.Table([]string{"NAME", "SOURCE", "LAYER", "BUILD", "VALUE"}, rows)
			if len(out.Data.Problems) > 0 {
				p.Line("")
				p.Line("%d problem(s) would block a deploy:", len(out.Data.Problems))
				for _, problem := range out.Data.Problems {
					p.Line("  %v", problem)
				}
			}
			return nil
		},
	}
	effective.Flags().StringVar(&effectiveEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	var revealEnv string
	reveal := &cobra.Command{
		Use:   "reveal <project> <service> <NAME>",
		Short: "Print one variable's value",
		Long: `Print one variable's plaintext. Needs the env:reveal scope and a person acting
as themselves; a sealed variable is never revealed.`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], revealEnv)
			if err != nil {
				return err
			}
			var out struct {
				Data struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"data"`
			}
			path := servicePath(projectID, svc.ID) + "/variables/" + url.PathEscape(args[2]) + "/reveal"
			if err := c.API.Get(ctx, path, nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Line("%s", out.Data.Value)
			return nil
		},
	}
	reveal.Flags().StringVar(&revealEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	cmd.AddCommand(list, set, unset, effective, reveal)
	return cmd
}

// projectVarsCmd is the project-level (shared) variables surface. It joins the
// existing 'miosa project' group.
func projectVarsCmd() *cobra.Command {
	cmd := group("vars", "Shared variables of a Deploy target",
		`Shared variables are visible to every service in an environment. A service reads
one as ${{shared.NAME}}.

  miosa project vars list cliniciq
  miosa project vars set cliniciq API_BASE=https://api.acme.dev
  miosa project vars unset cliniciq API_BASE`, nil)

	var listEnv string
	list := &cobra.Command{
		Use:     "list <project>",
		Aliases: []string{"ls"},
		Short:   "List shared variables (values masked)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			q := url.Values{}
			if listEnv != "" {
				q.Set("env", listEnv)
			}
			var out struct {
				Data []serviceVariable `json:"data"`
			}
			if err := c.API.Get(ctx, "/projects/"+url.PathEscape(projectID)+"/shared-variables", q, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			rows := make([][]string, 0, len(out.Data))
			for _, v := range out.Data {
				rows = append(rows, []string{v.Name, v.Kind, yesNo(v.Build), deref(v.ValuePreview), deref(v.Expression)})
			}
			p.Table([]string{"NAME", "KIND", "BUILD", "PREVIEW", "EXPRESSION"}, rows)
			if len(rows) == 0 {
				p.Line("No shared variables.")
			}
			return nil
		},
	}
	list.Flags().StringVar(&listEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	var setEnv string
	set := &cobra.Command{
		Use:   "set <project> KEY=VALUE [KEY=VALUE...]",
		Short: "Set shared variables",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			vars, err := serviceVars(args[1:])
			if err != nil {
				return usagef("%v", err)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			body := map[string]any{"variables": vars}
			if setEnv != "" {
				body["env"] = setEnv
			}
			if err := c.API.Put(ctx, "/projects/"+url.PathEscape(projectID)+"/shared-variables", body, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"variables": keys(vars)})
			}
			p.Success("Set shared %s", strings.Join(keys(vars), ", "))
			return nil
		},
	}
	set.Flags().StringVar(&setEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	var unsetEnv string
	unset := &cobra.Command{
		Use:   "unset <project> <NAME>[,NAME...]",
		Short: "Delete shared variables",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			q := url.Values{}
			if unsetEnv != "" {
				q.Set("env", unsetEnv)
			}
			path := "/projects/" + url.PathEscape(projectID) + "/shared-variables/" + url.PathEscape(args[1])
			if err := c.API.Delete(ctx, path, q, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"deleted": args[1]})
			}
			p.Success("Deleted shared %s", args[1])
			return nil
		},
	}
	unset.Flags().StringVar(&unsetEnv, "deploy-target", "", "Deploy target name or id (default: the project's default)")

	cmd.AddCommand(list, set, unset)
	return cmd
}

// ── project environments ─────────────────────────────────────────────────────

type serviceEnvironment struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"project_id"`
	Name      string  `json:"name"`
	Slug      string  `json:"slug"`
	Kind      *string `json:"kind"`
	IsDefault bool    `json:"is_default"`
	CreatedAt string  `json:"created_at"`
}

// projectEnvironmentCmd is the Deploy target surface of a project. The API
// resolves an environment by slug on the service routes, but the update and
// delete routes take an id, so the CLI resolves a name either way.
func projectEnvironmentCmd() *cobra.Command {
	cmd := group("target", "Deploy targets of a project (production, staging, ...)",
		`Every project has a default Deploy target; the first read creates 'production'.
Services, variables and releases live inside one Deploy target.

  miosa project target list shop
  miosa project target create shop staging
  miosa project target duplicate shop production --name staging
  miosa project target rm shop staging --yes`, []string{"targets"})

	list := &cobra.Command{
		Use:     "list <project>",
		Aliases: []string{"ls"},
		Short:   "List a project's Deploy targets",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			envs, err := listServiceEnvironments(ctx, c.API, projectID)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(envs)
			}
			rows := make([][]string, 0, len(envs))
			for _, e := range envs {
				rows = append(rows, []string{e.Name, e.Slug, deref(e.Kind), yesNo(e.IsDefault), shortID(e.ID)})
			}
			p.Table([]string{"NAME", "SLUG", "KIND", "DEFAULT", "ID"}, rows)
			return nil
		},
	}

	var slug, kind string
	create := &cobra.Command{
		Use:   "create <project> <name>",
		Short: "Create a Deploy target",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			body := map[string]any{"name": args[1]}
			if slug != "" {
				body["slug"] = slug
			}
			if kind != "" {
				body["kind"] = kind
			}
			var out struct {
				Data serviceEnvironment `json:"data"`
			}
			if err := c.API.Post(ctx, "/projects/"+url.PathEscape(projectID)+"/environments", body, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Created Deploy target %q", out.Data.Name)
			return nil
		},
	}
	create.Flags().StringVar(&slug, "slug", "", "URL slug (default: from the name)")
	create.Flags().StringVar(&kind, "kind", "", "Environment kind")

	var duplicateName string
	duplicate := &cobra.Command{
		Use:   "duplicate <project> <environment>",
		Short: "Copy an environment's services and variables",
		Long: `Copy a Deploy target's services and variables into a new one. Secrets are
generated again and no data or managed database is copied.

  miosa project target duplicate shop production --name staging`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if strings.TrimSpace(duplicateName) == "" {
				return usagef("--name is required: the new Deploy target's name")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			env, err := resolveServiceEnvironment(ctx, c.API, projectID, args[1])
			if err != nil {
				return err
			}
			var out struct {
				Data any `json:"data"`
			}
			path := "/projects/" + url.PathEscape(projectID) + "/environments/" + url.PathEscape(env.ID) + "/duplicate"
			if err := c.API.Post(ctx, path, map[string]any{"name": duplicateName}, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Duplicated %q as %q", env.Name, duplicateName)
			return nil
		},
	}
	duplicate.Flags().StringVar(&duplicateName, "name", "", "Name for the new environment")

	var yes bool
	rm := &cobra.Command{
		Use:     "rm <project> <environment>",
		Aliases: []string{"delete"},
		Short:   "Delete an environment",
		Long:    "Delete a non-default Deploy target. Its services stop.",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if !yes {
				if err := confirmDestructive(cmd, fmt.Sprintf("Delete Deploy target %q and its services?", args[1])); err != nil {
					return err
				}
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			env, err := resolveServiceEnvironment(ctx, c.API, projectID, args[1])
			if err != nil {
				return err
			}
			path := "/projects/" + url.PathEscape(projectID) + "/environments/" + url.PathEscape(env.ID)
			if err := c.API.Delete(ctx, path, nil, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"deleted": env.Name})
			}
			p.Success("Deleted Deploy target %q", env.Name)
			return nil
		},
	}
	rm.Flags().BoolVar(&yes, "yes", false, "Do not ask for confirmation")

	cmd.AddCommand(list, create, duplicate, rm)
	return cmd
}

func listServiceEnvironments(ctx context.Context, c *api.Client, projectID string) ([]serviceEnvironment, error) {
	var out struct {
		Data []serviceEnvironment `json:"data"`
	}
	if err := c.Get(ctx, "/projects/"+url.PathEscape(projectID)+"/environments", nil, &out); err != nil {
		return nil, die(err)
	}
	return out.Data, nil
}

// resolveServiceEnvironment turns an environment name, slug or id into the id
// the update and delete routes need.
func resolveServiceEnvironment(ctx context.Context, c *api.Client, projectID, ref string) (*serviceEnvironment, error) {
	envs, err := listServiceEnvironments(ctx, c, projectID)
	if err != nil {
		return nil, err
	}
	var matches []serviceEnvironment
	for _, e := range envs {
		if strings.EqualFold(e.ID, ref) || strings.EqualFold(e.Slug, ref) || strings.EqualFold(e.Name, ref) {
			matches = append(matches, e)
		}
	}
	switch len(matches) {
	case 1:
		return &matches[0], nil
	case 0:
		names := make([]string, 0, len(envs))
		for _, e := range envs {
			names = append(names, e.Name)
		}
		return nil, die(&api.Error{Status: 404, Code: "ENVIRONMENT_NOT_FOUND",
			Message: fmt.Sprintf("no environment %q; environments: %s", ref, orDashList(names))})
	default:
		return nil, die(&api.Error{Status: 409, Code: "ENVIRONMENT_AMBIGUOUS",
			Message: fmt.Sprintf("%q matches more than one environment; use the environment id", ref)})
	}
}

// ── runs, connection, settings, templates ────────────────────────────────────

func serviceRunsCmd() *cobra.Command {
	var env string
	var withOutput bool
	cmd := &cobra.Command{
		Use:   "runs <project> <service>",
		Short: "Show a Schedule's recent runs",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			var out struct {
				Data []struct {
					ID          string  `json:"id"`
					State       string  `json:"state"`
					ExitCode    *int    `json:"exit_code"`
					StartedAt   *string `json:"started_at"`
					CompletedAt *string `json:"completed_at"`
					Output      *string `json:"output"`
				} `json:"data"`
			}
			if err := c.API.Get(ctx, servicePath(projectID, svc.ID)+"/runs", nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			rows := make([][]string, 0, len(out.Data))
			for _, r := range out.Data {
				exit := "-"
				if r.ExitCode != nil {
					exit = strconv.Itoa(*r.ExitCode)
				}
				rows = append(rows, []string{r.State, exit, deref(r.StartedAt), deref(r.CompletedAt), shortID(r.ID)})
			}
			p.Table([]string{"STATE", "EXIT", "STARTED", "COMPLETED", "ID"}, rows)
			if len(rows) == 0 {
				p.Line("No runs yet.")
			}
			if withOutput {
				for _, r := range out.Data {
					if r.Output != nil {
						p.Line("")
						p.Line("%s:", shortID(r.ID))
						p.Line("%s", clip(*r.Output, 4000))
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	cmd.Flags().BoolVar(&withOutput, "output", false, "Also print each run's output")
	return cmd
}

func serviceConnectionCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   "connection <project> <service>",
		Short: "Show a database service's connection URLs",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}
			var out struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Get(ctx, servicePath(projectID, svc.ID)+"/connection", nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Fields([][2]string{
				{"Private URL", stringOf(out.Data["private_url"])},
				{"Public URL", stringOf(out.Data["public_url"])},
				{"Engine", stringOf(out.Data["engine"])},
			})
			return nil
		},
	}
	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	return cmd
}

func serviceMetricsCmd() *cobra.Command {
	var (
		env    string
		range_ string
	)

	cmd := &cobra.Command{
		Use:   "metrics <project> <service>",
		Short: "Requests, errors and latency for a service",
		Long: `The service's own metric series: requests, errors, error rate and response
latency (p50 and p95). CPU, memory and network are not collected for services,
so those fields are null and the footer says which columns are real.

  miosa service metrics cliniciq API
  miosa service metrics cliniciq API --range 24h --json`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, svc, err := openService(ctx, c.API, args[0], args[1], env)
			if err != nil {
				return err
			}

			q := url.Values{}
			q.Set("range", range_)

			var out struct {
				Data struct {
					Series    []map[string]any `json:"series"`
					Available []string         `json:"available"`
					Source    string           `json:"source"`
				} `json:"data"`
			}

			path := servicePath(projectID, svc.ID) + "/metrics/series"
			if err := c.API.Get(ctx, path, q, &out); err != nil {
				return die(err)
			}

			if isJSON() {
				return p.JSON(out.Data)
			}

			if len(out.Data.Series) == 0 {
				p.Line("%s: no traffic in the last %s", svc.Name, range_)
			} else {
				rows := make([][]string, 0, len(out.Data.Series))
				for _, point := range out.Data.Series {
					rows = append(rows, []string{
						metricTime(point["t"]),
						metricValue(point["requests"], "%.0f"),
						metricValue(point["errors"], "%.0f"),
						metricPercent(point["error_rate"]),
						metricValue(point["response_ms_p50"], "%.0f"),
						metricValue(point["response_ms_p95"], "%.0f"),
					})
				}
				p.Table([]string{"TIME", "REQUESTS", "ERRORS", "ERROR%", "P50", "P95"}, rows)
			}

			p.Line("")
			p.Line("%s, last %s. Measured: %s. Not collected for services: cpu, memory, network.",
				svc.Name, range_, strings.Join(out.Data.Available, ", "))

			return nil
		},
	}

	cmd.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	cmd.Flags().StringVar(&range_, "range", "1h", "Window: 15m, 1h, 6h, 24h or 7d")
	return cmd
}

func metricTime(value any) string {
	if seconds, ok := number(value); ok {
		return time.Unix(int64(seconds), 0).UTC().Format("15:04")
	}
	return "-"
}

func metricValue(value any, format string) string {
	if seconds, ok := number(value); ok {
		return fmt.Sprintf(format, seconds)
	}
	return "-"
}

func metricPercent(value any) string {
	if seconds, ok := number(value); ok {
		return fmt.Sprintf("%.1f%%", seconds*100)
	}
	return "-"
}

func number(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

func serviceSettingsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "settings <project>",
		Short: "Show what each service setting does today",
		Long: `Every setting is 'enforced' (it takes effect), 'stored' (saved but not applied
yet) or 'unsupported'. Read this before relying on a setting.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			var out struct {
				Data map[string]struct {
					State  string `json:"state"`
					Reason string `json:"reason"`
				} `json:"data"`
			}
			path := "/projects/" + url.PathEscape(projectID) + "/services/setting-capabilities"
			if err := c.API.Get(ctx, path, nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			names := make([]string, 0, len(out.Data))
			for name := range out.Data {
				names = append(names, name)
			}
			sortStrings(names)
			rows := make([][]string, 0, len(names))
			for _, name := range names {
				rows = append(rows, []string{name, out.Data[name].State, out.Data[name].Reason})
			}
			p.Table([]string{"SETTING", "STATE", "REASON"}, rows)
			return nil
		},
	}
	return cmd
}

func serviceTemplatesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "templates",
		Short: "List project templates (add Postgres, Redis, a web app)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			var out struct {
				Data []struct {
					Slug        string   `json:"slug"`
					Name        string   `json:"name"`
					Description string   `json:"description"`
					Tags        []string `json:"tags"`
				} `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/project-templates", nil, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			rows := make([][]string, 0, len(out.Data))
			for _, t := range out.Data {
				rows = append(rows, []string{t.Slug, t.Name, strings.Join(t.Tags, ","), clip(t.Description, 60)})
			}
			p.Table([]string{"SLUG", "NAME", "TAGS", "DESCRIPTION"}, rows)
			p.Line("")
			p.Line("Apply one with: miosa service templates apply <project> <slug>")
			return nil
		},
	}

	var env string
	var noDeploy bool
	apply := &cobra.Command{
		Use:   "apply <project> <slug>",
		Short: "Apply a template to a project",
		Long: `Create the template's services and variables in a Deploy target.

  miosa service templates apply cliniciq nextjs-postgres
  miosa service templates apply cliniciq redis`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			projectID, err := resolveServiceProject(ctx, c.API, args[0])
			if err != nil {
				return err
			}
			q := url.Values{}
			if env != "" {
				q.Set("env", env)
			}
			if noDeploy {
				q.Set("deploy", "false")
			}
			path := "/projects/" + url.PathEscape(projectID) + "/templates/" + url.PathEscape(args[1]) + "/apply"
			var out struct {
				Data any `json:"data"`
			}
			if err := c.API.JSON(ctx, api.Request{Method: http.MethodPost, Path: path, Query: q}, &out); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(out.Data)
			}
			p.Success("Applied template %q", args[1])
			return nil
		},
	}
	apply.Flags().StringVar(&env, "deploy-target", "", "Deploy target name or id (default: the project's default)")
	apply.Flags().BoolVar(&noDeploy, "no-deploy", false, "Create the services without deploying them")
	cmd.AddCommand(apply)
	return cmd
}

// ── resolution and shared calls ──────────────────────────────────────────────

// resolveServiceProject turns a project name, slug or id into the UUID the
// Services routes require. An empty ref uses the default project.
func resolveServiceProject(ctx context.Context, c *api.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "default"
	}
	var out struct {
		Data []serviceProject `json:"data"`
	}
	if err := c.Get(ctx, "/projects", nil, &out); err != nil {
		return "", die(err)
	}
	var matches []serviceProject
	for _, p := range out.Data {
		switch {
		case strings.EqualFold(p.ID, ref), strings.EqualFold(p.Slug, ref), strings.EqualFold(p.Name, ref):
			matches = append(matches, p)
		case ref == "default" && p.IsDefault:
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].ID, nil
	case 0:
		return "", die(&api.Error{Status: 404, Code: "PROJECT_NOT_FOUND",
			Message: fmt.Sprintf("no project %q; projects: %s", ref, projectNames(out.Data))})
	default:
		return "", die(&api.Error{Status: 409, Code: "PROJECT_AMBIGUOUS",
			Message: fmt.Sprintf("%q matches more than one project; use the project id", ref)})
	}
}

func projectNames(projects []serviceProject) string {
	if len(projects) == 0 {
		return "none (create one with 'miosa project create <name> --workspace-id <id>')"
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Slug)
	}
	sortStrings(names)
	return strings.Join(names, ", ")
}

// openService resolves the project and the service. The API only resolves a
// service by id, so the list is the name lookup.
func openService(ctx context.Context, c *api.Client, projectRef, serviceRef, env string) (string, *projectServiceRow, error) {
	projectID, err := resolveServiceProject(ctx, c, projectRef)
	if err != nil {
		return "", nil, err
	}
	services, err := listServices(ctx, c, projectID, env)
	if err != nil {
		return "", nil, err
	}
	var matches []projectServiceRow
	for _, s := range services {
		if strings.EqualFold(s.ID, serviceRef) || strings.EqualFold(s.Slug, serviceRef) || strings.EqualFold(s.Name, serviceRef) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 1:
		return projectID, &matches[0], nil
	case 0:
		names := make([]string, 0, len(services))
		for _, s := range services {
			if s.Name != "" {
				names = append(names, s.Name)
			}
		}
		return "", nil, die(&api.Error{Status: 404, Code: "SERVICE_NOT_FOUND",
			Message: fmt.Sprintf("no service %q in project %s; services: %s", serviceRef, projectRef, orDashList(names))})
	default:
		return "", nil, die(&api.Error{Status: 409, Code: "SERVICE_AMBIGUOUS",
			Message: fmt.Sprintf("%q matches more than one service; use the service id", serviceRef)})
	}
}

func listServices(ctx context.Context, c *api.Client, projectID, env string) ([]projectServiceRow, error) {
	q := url.Values{}
	if env != "" {
		q.Set("env", env)
	}
	var out struct {
		Data []projectServiceRow `json:"data"`
	}
	if err := c.Get(ctx, "/projects/"+url.PathEscape(projectID)+"/services", q, &out); err != nil {
		return nil, die(err)
	}
	return out.Data, nil
}

func servicePath(projectID, serviceID string) string {
	return "/projects/" + url.PathEscape(projectID) + "/services/" + url.PathEscape(serviceID)
}

func enableServiceAddress(ctx context.Context, c *api.Client, projectID, serviceID, label string) error {
	body := map[string]any{}
	if label != "" {
		body["label"] = label
	}
	return c.Put(ctx, servicePath(projectID, serviceID)+"/public-address", body, nil)
}

func putServiceVariables(ctx context.Context, c *api.Client, projectID, serviceID string, vars map[string]any) error {
	return c.Put(ctx, servicePath(projectID, serviceID)+"/variables", map[string]any{"variables": vars}, nil)
}

func listServiceVariables(ctx context.Context, c *api.Client, projectID, serviceID string) ([]serviceVariable, error) {
	var out struct {
		Data []serviceVariable `json:"data"`
	}
	if err := c.Get(ctx, servicePath(projectID, serviceID)+"/variables", nil, &out); err != nil {
		return nil, die(err)
	}
	return out.Data, nil
}

// serviceVars turns KEY=VALUE arguments into the API's variables map. The value
// is passed through untouched, so a ${{...}} reference stays a reference.
func serviceVars(args []string) (map[string]any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("give at least one KEY=VALUE")
	}
	vars := make(map[string]any, len(args))
	for _, arg := range args {
		name, value, ok := strings.Cut(arg, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("expected KEY=VALUE (got %q)", arg)
		}
		vars[name] = value
	}
	return vars, nil
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// sortStrings is a small insertion sort so this file does not import sort for
// the handful of names it orders.
func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func listReleases(ctx context.Context, c *api.Client, projectID, serviceID string) ([]serviceRelease, error) {
	var out struct {
		Data []serviceRelease `json:"data"`
	}
	if err := c.Get(ctx, servicePath(projectID, serviceID)+"/deployments", nil, &out); err != nil {
		return nil, die(err)
	}
	return out.Data, nil
}

// latestOrNamedRelease takes a release number, a release id or "" (the newest).
func latestOrNamedRelease(ctx context.Context, c *api.Client, projectID, serviceID, ref string) (*serviceRelease, error) {
	ref = strings.TrimSpace(ref)
	if isUUID(ref) {
		var out struct {
			Data serviceRelease `json:"data"`
		}
		path := servicePath(projectID, serviceID) + "/deployments/" + url.PathEscape(ref)
		if err := c.Get(ctx, path, nil, &out); err != nil {
			return nil, die(err)
		}
		return &out.Data, nil
	}
	releases, err := listReleases(ctx, c, projectID, serviceID)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		if len(releases) == 0 {
			return nil, die(&api.Error{Status: 404, Code: "NO_RELEASES",
				Message: "this service has no releases yet; run 'miosa service deploy <project> <service>'"})
		}
		return &releases[0], nil
	}
	number, convErr := strconv.Atoi(ref)
	if convErr != nil {
		return nil, die(&api.Error{Status: 404, Code: "RELEASE_NOT_FOUND",
			Message: fmt.Sprintf("no release %q; give a release number or id", ref)})
	}
	for i := range releases {
		if releases[i].Number == number {
			return &releases[i], nil
		}
	}
	return nil, die(&api.Error{Status: 404, Code: "RELEASE_NOT_FOUND",
		Message: fmt.Sprintf("no release %d on this service", number)})
}

func serviceEvents(ctx context.Context, c *api.Client, projectID, serviceID, releaseID string) ([]serviceEvent, error) {
	var out struct {
		Data []serviceEvent `json:"data"`
	}
	path := servicePath(projectID, serviceID) + "/deployments/" + url.PathEscape(releaseID) + "/events"
	if err := c.Get(ctx, path, nil, &out); err != nil {
		return nil, die(err)
	}
	return out.Data, nil
}

// waitForRelease polls a release until it is live, fails, or the timeout ends.
func waitForRelease(ctx context.Context, c *api.Client, projectID, serviceID, releaseID string, timeout time.Duration, cmd *cobra.Command) (serviceRelease, error) {
	deadline := time.Now().Add(timeout)
	path := servicePath(projectID, serviceID) + "/deployments/" + url.PathEscape(releaseID)
	last := ""
	for {
		var out struct {
			Data serviceRelease `json:"data"`
		}
		if err := c.Get(ctx, path, nil, &out); err != nil {
			return out.Data, die(err)
		}
		state := out.Data.State
		if state != last {
			fmt.Fprintf(cmd.ErrOrStderr(), "release %d: %s\n", out.Data.Number, state)
			last = state
		}
		switch state {
		case "active", "sleeping":
			return out.Data, nil
		case "failed", "canceled", "stopped", "removed":
			reason := state
			if out.Data.Reason != nil {
				reason = out.Data.Reason.Code
				if out.Data.Reason.Detail != "" {
					reason += ": " + out.Data.Reason.Detail
				}
			}
			return out.Data, die(&api.Error{Status: 409, Code: "RELEASE_FAILED",
				Message: fmt.Sprintf("release %d ended %s (%s)", out.Data.Number, state, reason)})
		}
		if time.Now().After(deadline) {
			return out.Data, die(&api.Error{Status: 408, Code: "RELEASE_TIMEOUT",
				Message: fmt.Sprintf("release %d is still %s after %s; check 'miosa service logs'", out.Data.Number, state, timeout)})
		}
		select {
		case <-ctx.Done():
			return out.Data, die(ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}

// uploadServiceSource packages a directory as .tar.gz, records it with
// POST /uploads, PUTs the archive to the presigned URL and returns the source
// object a release takes.
func uploadServiceSource(ctx context.Context, c *api.Client, name, dir string) (map[string]any, error) {
	archive, sum, err := serviceSourceArchive(dir)
	if err != nil {
		return nil, die(err)
	}
	filename := sanitizeFileName(name) + ".tar.gz"
	body := map[string]any{"filename": filename, "size": len(archive), "sha256": sum}
	var out struct {
		Data struct {
			UploadID string `json:"upload_id"`
			URL      string `json:"url"`
		} `json:"data"`
	}
	if err := c.Post(ctx, "/uploads", body, &out); err != nil {
		return nil, die(err)
	}
	if out.Data.UploadID == "" || out.Data.URL == "" {
		return nil, die(fmt.Errorf("the upload request returned no upload_id or url"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, out.Data.URL, bytes.NewReader(archive))
	if err != nil {
		return nil, die(err)
	}
	req.Header.Set("Content-Type", "application/gzip")
	putClient := &http.Client{Timeout: 5 * time.Minute}
	resp, err := putClient.Do(req)
	if err != nil {
		return nil, die(fmt.Errorf("uploading %s: %w", filename, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, die(fmt.Errorf("uploading %s: HTTP %d: %s", filename, resp.StatusCode, strings.TrimSpace(string(detail))))
	}
	return map[string]any{"type": "upload", "upload_id": out.Data.UploadID}, nil
}

// serviceSourceArchive archives a directory (relative paths, no absolute prefix) and
// returns the bytes and the SHA-256 hex of the archive.
func serviceSourceArchive(dir string) ([]byte, string, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, "", err
	}
	if !fi.IsDir() {
		return nil, "", fmt.Errorf("%s is not a directory", dir)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			if skipUploadPath(rel) {
				return filepath.SkipDir
			}
			return tw.WriteHeader(&tar.Header{Name: rel + "/", Mode: 0o755, Typeflag: tar.TypeDir, ModTime: info.ModTime()})
		}
		if skipUploadPath(rel) || !info.Mode().IsRegular() {
			return nil
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = rel
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		return nil, "", walkErr
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := gz.Close(); err != nil {
		return nil, "", err
	}
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

// skipUploadPath keeps the archive small and reproducible: no VCS metadata, no
// dependency caches, no local env files.
func skipUploadPath(rel string) bool {
	base := filepath.Base(rel)
	switch base {
	case ".git", "node_modules", ".next", ".svelte-kit", "target", "__pycache__", ".venv", "venv":
		return true
	}
	return strings.HasPrefix(base, ".env")
}

func sanitizeFileName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	if name == "" {
		name = "source"
	}
	return name
}

// ── rendering ────────────────────────────────────────────────────────────────

func renderService(p *output.Printer, s projectServiceRow) {
	p.Fields([][2]string{
		{"Name", s.Name},
		{"Slug", s.Slug},
		{"Type", s.Kind},
		{"Project", s.ProjectID},
		{"Environment", s.EnvironmentID},
		{"Private", orDash(s.PrivateDomain)},
		{"Public", addressURL(s)},
		{"Replicas", settingString(s.Settings, "replicas")},
		{"Start", settingString(s.Settings, "start_command")},
		{"Active release", orDash(s.ActiveDeploymentID)},
		{"Pending changes", pendingChangesLine(s.PendingChanges)},
		{"Id", s.ID},
		{"Created", s.CreatedAt},
		{"Updated", s.UpdatedAt},
	})
}

func renderServiceRelease(p *output.Printer, r serviceRelease) error {
	if p.JSONMode() {
		return p.JSON(r)
	}
	reason := "-"
	if r.Reason != nil {
		reason = r.Reason.Code
		if r.Reason.Detail != "" {
			reason += ": " + r.Reason.Detail
		}
	}
	p.Fields([][2]string{
		{"Release", strconv.Itoa(r.Number)},
		{"State", r.State},
		{"Reason", reason},
		{"Trigger", r.Trigger},
		{"Image", orDash(r.ImageRef)},
		{"Message", orDash(r.Message)},
		{"Started", deref(r.StartedAt)},
		{"Finished", deref(r.FinishedAt)},
		{"Id", r.ID},
	})
	return nil
}

// pendingChangesLine renders a variable write that needs a release, and names
// the action: a redeploy rebuilds and serves, and clears the mark.
func pendingChangesLine(p *servicePendingChanges) string {
	if p == nil || p.Needs == "" {
		return "-"
	}
	line := p.Needs + " needed"
	if len(p.Variables) > 0 {
		line += " for " + strings.Join(p.Variables, ", ")
	}
	if p.Because == "shared" {
		line += " (a shared variable changed)"
	} else if p.Because != "" && p.Because != "own" {
		line += " (via " + p.Because + ")"
	}
	return line + "; run: miosa service deploy <project> <service>"
}

func settingString(settings map[string]any, key string) string {
	if v, ok := settings[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return "-"
}

func addressURL(s projectServiceRow) string {
	if s.PublicAddress == nil || s.PublicAddress.URL == nil {
		return "-"
	}
	return *s.PublicAddress.URL
}

func addressLabel(s projectServiceRow) string {
	if s.PublicAddress == nil || s.PublicAddress.Label == nil {
		return "-"
	}
	return *s.PublicAddress.Label
}

func detailMessage(detail map[string]any) string {
	if detail == nil {
		return ""
	}
	for _, key := range []string{"message", "detail", "reason", "error"} {
		if v, ok := detail[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func deref(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func orDashList(v []string) string {
	if len(v) == 0 {
		return "-"
	}
	sortStrings(v)
	return strings.Join(v, ", ")
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func stringOf(v any) string {
	if v == nil {
		return "-"
	}
	if s, ok := v.(string); ok {
		if s == "" {
			return "-"
		}
		return s
	}
	return fmt.Sprintf("%v", v)
}
