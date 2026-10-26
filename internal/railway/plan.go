package railway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PlanVersion is the plan schema version. A plan from another version is
// refused rather than half-applied. Version 2 added environments (Action.Env),
// shared variables, provenance and the build command; a version 1 plan has no
// environments and still applies, into the project's default one.
const PlanVersion = 2

// OldestPlanVersion is the oldest plan this CLI still applies.
const OldestPlanVersion = 1

// RotationWarning is printed on every plan, because it is the one thing a
// Railway user must hear before trusting an import.
const RotationWarning = "Railway disclosed on 2026-10-21 that its secrets were exposed. " +
	"Rotate every secret you rely on; no literal secret is copied unless you pass --copy-variables."

// Plan is what the user reviews and what --apply executes.
type Plan struct {
	Version  int        `json:"version"`
	Source   PlanSource `json:"source"`
	Target   PlanTarget `json:"target"`
	Actions  []Action   `json:"actions"`
	Warnings []string   `json:"warnings"`
}

// PlanSource identifies the Railway read the plan was built from.
type PlanSource struct {
	Kind         string   `json:"kind"`
	ProjectID    string   `json:"project_id"`
	ProjectName  string   `json:"project_name"`
	Environment  string   `json:"environment"`
	Environments []string `json:"environments,omitempty"`
	FetchedAt    string   `json:"fetched_at"`
	Digest       string   `json:"digest"`
}

// PlanTarget is where the plan writes.
type PlanTarget struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	ProjectName string `json:"project_name"`
	ProjectID   string `json:"project_id,omitempty"`
	Into        string `json:"into,omitempty"`
}

// Action is one step. Fields are grouped by op; a field that does not belong to
// the action's op is empty, so the JSON stays readable and stable.
type Action struct {
	Op string `json:"op"`

	// Env is the Railway environment the action belongs to; the MIOSA Deploy
	// target of the same name. Empty means the project's default.
	Env string `json:"env,omitempty"`

	// project.create, target.create, service.create, database.create, and the
	// name inside variable.set and unmapped.
	Name string `json:"name,omitempty"`

	// service.create and database.service
	// Slug is the service's MIOSA slug (a-z 0-9 -, at most 40 characters).
	Slug string `json:"slug,omitempty"`
	// RailwayName is the service's name on Railway when MIOSA's had to differ.
	RailwayName string `json:"railway_name,omitempty"`

	// service.create
	Kind     string          `json:"kind,omitempty"`
	Source   *ServiceSource  `json:"source,omitempty"`
	Build    *BuildSetting   `json:"build,omitempty"`
	Settings *ServiceSetting `json:"settings,omitempty"`
	Address  *bool           `json:"address,omitempty"`
	// AddressLabel is the <label> of the service's <label>.<org>.miosa.app
	// address. Labels are unique across the organization, so the primary
	// environment's service takes its own slug and every other environment's
	// takes the slug with -<environment>: which one gets the plain name never
	// depends on which was created first.
	AddressLabel string `json:"address_label,omitempty"`
	// NeedsRegion is set when the Railway region has no MIOSA equivalent, so an
	// apply must be given --region rather than guessing.
	NeedsRegion bool `json:"needs_region,omitempty"`
	// RailwayRegion is the region the service ran in on Railway.
	RailwayRegion string `json:"railway_region,omitempty"`
	// Provenance names where each setting came from: the config file (its
	// path), the service settings, or the latest deployment.
	Provenance map[string]string `json:"provenance,omitempty"`

	// database.create
	Engine  string `json:"engine,omitempty"`
	Version string `json:"version,omitempty"`
	Size    string `json:"size,omitempty"`

	// variable.set and shared.set
	Service     string    `json:"service,omitempty"`
	Variable    *Variable `json:"variable,omitempty"`
	ValueTarget string    `json:"value_target,omitempty"` // the service whose value is needed

	// domain.report
	Domains []string `json:"domains,omitempty"`
	Custom  []string `json:"custom_domains,omitempty"`

	// unmapped
	Field string `json:"field,omitempty"`
	Why   string `json:"why,omitempty"`
	Next  string `json:"next,omitempty"`
}

// ServiceSource is a service's build input.
type ServiceSource struct {
	Type    string `json:"type"`
	Ref     string `json:"ref"`
	Branch  string `json:"branch,omitempty"`
	RootDir string `json:"root_dir,omitempty"`
	// Port is the port the service listens on: health checks and the public
	// address use it. Railway's target port.
	Port int `json:"port,omitempty"`
}

// BuildSetting is the build half of a service. The applier writes it onto the
// service source, which is what the builder reads.
type BuildSetting struct {
	// Dockerfile is relative to the source's root directory.
	Dockerfile   string `json:"dockerfile,omitempty"`
	BuildCommand string `json:"build_command,omitempty"`
}

// ServiceSetting is the run half of a service.
type ServiceSetting struct {
	StartCommand       string `json:"start_command,omitempty"`
	Replicas           int    `json:"replicas,omitempty"`
	Region             string `json:"region,omitempty"`
	CronSchedule       string `json:"cron_schedule,omitempty"`
	HealthcheckPath    string `json:"healthcheck_path,omitempty"`
	HealthcheckTimeout int    `json:"healthcheck_timeout_s,omitempty"`
	RestartPolicy      string `json:"restart_policy,omitempty"`
	MaxRetries         int    `json:"max_retries,omitempty"`
	PreDeployCommand   string `json:"pre_deploy_command,omitempty"`
}

// Options shape the plan: where it writes and how far it goes.
type Options struct {
	ProjectName    string
	WorkspaceID    string
	Into           string
	IncludeTargets bool
	CopyVariables  bool
	// Only keeps these services (case-insensitive). Empty keeps every one.
	Only []string
	// Exclude leaves these services out; it is applied after Only.
	Exclude []string
	// RuntimeOnlyVars creates every variable runtime only. By default an
	// import makes them build-visible, as on Railway.
	RuntimeOnlyVars bool
	// PrimaryEnvironment is the environment whose services get the plain
	// address labels; every other environment's labels end in -<environment>.
	// Empty means production.
	PrimaryEnvironment string
	// Suffix is appended to every service name, and inserted before the first
	// dot of every custom domain, so a shadow import cannot collide with the
	// project it mirrors.
	Suffix string
}

// Build turns a Railway read into a plan. It touches nothing: the same source
// always produces the same plan, which is what makes --plan reviewable.
func Build(src Source, opts Options) (Plan, error) {
	name := opts.ProjectName
	if name == "" {
		name = src.ProjectName
	}
	src, skipped := filterServices(src, opts.Only)
	src, excluded := excludeServices(src, opts.Exclude)
	src = applySuffix(src, opts.Suffix)
	src, renames := sanitizeNames(src)
	digest, err := digestOf(src)
	if err != nil {
		return Plan{}, err
	}

	plan := Plan{
		Version: PlanVersion,
		Source: PlanSource{
			Kind:         src.Kind,
			ProjectID:    src.ProjectID,
			ProjectName:  src.ProjectName,
			Environment:  src.Environment,
			Environments: src.Environments,
			FetchedAt:    src.FetchedAt.Format("2006-01-02T15:04:05Z"),
			Digest:       digest,
		},
		Target: PlanTarget{
			WorkspaceID: opts.WorkspaceID,
			ProjectName: name,
			Into:        opts.Into,
		},
		Warnings: []string{RotationWarning},
	}

	if opts.Into == "" {
		plan.Actions = append(plan.Actions, Action{Op: "project.create", Name: name})
	}
	read := readEnvironments(src)
	for _, env := range read {
		plan.Actions = append(plan.Actions, Action{Op: "target.create", Name: env})
	}
	if opts.IncludeTargets {
		for _, env := range src.Environments {
			if !containsFold(read, env) {
				plan.Actions = append(plan.Actions, Action{Op: "target.create", Name: env})
			}
		}
	}
	for _, plugin := range src.Plugins {
		if action, ok := databaseAction(plugin); ok {
			plan.Actions = append(plan.Actions, action)
			continue
		}
		plan.Actions = append(plan.Actions, Action{
			Op:    "unmapped",
			Name:  plugin.FriendlyName,
			Field: "plugin " + plugin.Name,
			Why:   "MIOSA has no managed " + plugin.Name + " engine today",
			Next:  "run it yourself as an image service",
		})
	}

	refs := refContext{engines: databaseEngines(src), renames: renames}
	needsValue, private, buildVisible := 0, 0, 0
	outsideUS := map[string]int{}
	for _, svc := range src.Services {
		env := svc.Environment
		if engine := databaseEngine(svc); engine != "" {
			// Railway runs databases as image services. MIOSA has a database
			// service that binds a managed database and provides DATABASE_URL /
			// REDIS_URL, which is what the incoming references expect.
			plan.Actions = append(plan.Actions, Action{Op: "database.service", Env: env, Name: svc.Name,
				Slug: ServiceSlug(svc.Name), RailwayName: svc.RailwayName, Engine: engine})
			plan.Actions = append(plan.Actions, withEnv(unmappedDatabase(svc, engine), env)...)
			continue
		}
		if svc.Region != "" && mapRegion(svc.Region) == "" {
			outsideUS[svc.Region]++
		}
		actions := withEnv(serviceActions(svc), env)
		for i := range actions {
			if actions[i].Op == "service.create" && actions[i].Address != nil && *actions[i].Address {
				actions[i].AddressLabel = AddressLabel(svc.Name, env, opts.PrimaryEnvironment)
			}
		}
		plan.Actions = append(plan.Actions, actions...)
		for _, v := range svc.Variables {
			carried, reports := carryVariable(v, svc.Name, env, refs)
			carried.Build = wantsBuild(carried.Name, opts)
			if carried.Build {
				buildVisible++
			}
			if strings.Contains(carried.Value, ".MIOSA_PRIVATE_DOMAIN}}") {
				private++
			}
			action := Action{Op: "variable.set", Env: env, Service: svc.Name, Variable: &carried}
			if !carried.HasValue {
				needsValue++
				action.ValueTarget = svc.Name
			}
			plan.Actions = append(plan.Actions, action)
			plan.Actions = append(plan.Actions, reports...)
		}
		if len(svc.ServiceDomains) > 0 || len(svc.CustomDomains) > 0 {
			plan.Actions = append(plan.Actions, Action{
				Op:      "domain.report",
				Env:     env,
				Service: svc.Name,
				Domains: svc.ServiceDomains,
			})
		}
		for _, d := range svc.CustomDomains {
			plan.Actions = append(plan.Actions, Action{Op: "domain.custom", Env: env, Service: svc.Name, Domains: []string{d}})
		}
		plan.Actions = append(plan.Actions, withEnv(unmappedServiceActions(svc), env)...)
	}
	for _, shared := range src.Shared {
		carried, reports := carryVariable(shared.Variable, "shared", shared.Environment, refs)
		carried.Build = wantsBuild(carried.Name, opts)
		if carried.Build {
			buildVisible++
		}
		plan.Actions = append(plan.Actions, Action{Op: "shared.set", Env: shared.Environment, Variable: &carried})
		if !carried.HasValue {
			needsValue++
		}
		plan.Actions = append(plan.Actions, reports...)
	}

	if len(outsideUS) > 0 {
		regions := make([]string, 0, len(outsideUS))
		total := 0
		for region, n := range outsideUS {
			regions = append(regions, fmt.Sprintf("%s (%d)", region, n))
			total += n
		}
		sort.Strings(regions)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%d service(s) ran outside the US on Railway (%s), and MIOSA runs in us-east, us-west and us-mia. "+
				"Apply needs --region; latency to their users and data residency change.",
			total, strings.Join(regions, ", ")))
	}
	if buildVisible > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%d variable(s) will be build-visible, because Railway exposes every service variable to the build "+
				"(NEXT_PUBLIC_*, VITE_* and PUBLIC_* are inlined then). On MIOSA a build-visible variable reaches the build as a "+
				"BuildKit secret, never stored in an image layer, unless the Dockerfile declares an ARG of that name, which gets it "+
				"as a build argument. One without a value becomes build-visible when you set it with --build. "+
				"Pass --runtime-only-vars to create them all runtime only.", buildVisible))
	}
	if private > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%d variable(s) now reference another service's MIOSA_PRIVATE_DOMAIN (<service>.miosa.internal); "+
				"they resolve only while private networking between services is on for the organization.", private))
	}
	if len(skipped) > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"not imported (--only): %s", strings.Join(skipped, ", ")))
	}
	if len(excluded) > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"not imported (--exclude): %s", strings.Join(excluded, ", ")))
	}
	if needsValue > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"%d variable(s) have no value: fill them in before the first deploy (--allow-missing-values creates them empty).",
			needsValue))
	}
	return plan, nil
}

func serviceActions(svc Service) []Action {
	kind, schedule := serviceKind(svc)
	setting := &ServiceSetting{
		StartCommand:       svc.StartCommand,
		Region:             mapRegion(svc.Region),
		CronSchedule:       schedule,
		HealthcheckPath:    svc.HealthcheckPath,
		HealthcheckTimeout: svc.HealthcheckTimeout,
		RestartPolicy:      mapRestartPolicy(svc.RestartPolicy),
		MaxRetries:         svc.RestartMaxRetries,
		PreDeployCommand:   svc.PreDeployCommand,
	}
	setting.Replicas = svc.NumReplicas
	if setting.Replicas > 50 {
		setting.Replicas = 50
	}
	if kind == "worker" {
		// A worker listens on no port, so it has no health check to run.
		setting.HealthcheckPath, setting.HealthcheckTimeout = "", 0
	}

	var source *ServiceSource
	switch {
	case svc.Repo != "":
		source = &ServiceSource{Type: "git", Ref: repoURL(svc.Repo), Branch: svc.Branch, RootDir: rootDir(svc.RootDirectory), Port: svc.Port}
	case svc.Image != "":
		source = &ServiceSource{Type: "image", Ref: svc.Image, Port: svc.Port}
	}

	address := len(svc.ServiceDomains) > 0
	var actions []Action
	action := Action{
		Op:          "service.create",
		Name:        svc.Name,
		Slug:        ServiceSlug(svc.Name),
		RailwayName: svc.RailwayName,
		Kind:        kind,
		Source:      source,
		Settings:    setting,
		Address:     &address,
		Provenance:  svc.Provenance,
	}
	if build := buildSetting(svc); build != nil && source != nil && source.Type == "git" {
		action.Build = build
	}
	actions = append(actions, action)

	if source == nil {
		actions = append(actions, Action{
			Op:    "unmapped",
			Name:  svc.Name,
			Field: "source",
			Why:   "Railway has no repository or image for this service (it was uploaded with the Railway CLI)",
			Next:  "it is created without a source; upload the code with miosa service deploy <project> " + svc.Name + " --dir <path>",
		})
	}
	if svc.NumReplicas > 50 {
		actions = append(actions, Action{
			Op:    "unmapped",
			Name:  svc.Name,
			Field: "numReplicas",
			Why:   fmt.Sprintf("Railway runs %d replicas; MIOSA allows 1 to 50", svc.NumReplicas),
			Next:  "raise it after the import if you need to",
		})
	}
	if svc.Region != "" && mapRegion(svc.Region) == "" {
		actions[0].NeedsRegion = true
		actions[0].RailwayRegion = svc.Region
		actions = append(actions, Action{
			Op:    "unmapped",
			Name:  svc.Name,
			Field: "region",
			Why:   fmt.Sprintf("Railway region %q has no MIOSA equivalent (us-east, us-west, us-mia)", svc.Region),
			Next:  fmt.Sprintf("closest MIOSA region is %s; pass --region %s to apply", closestRegion(svc.Region), closestRegion(svc.Region)),
		})
	}
	return actions
}

// buildReservedPrefixes are names the API refuses to make build-visible: the
// platform's own build settings.
var buildReservedPrefixes = []string{"MIOSA_", "RAILPACK_"}

// wantsBuild is whether an imported variable asks to be build-visible.
func wantsBuild(name string, opts Options) bool {
	if opts.RuntimeOnlyVars {
		return false
	}
	for _, prefix := range buildReservedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// AddressLabel is a service's public address label: its slug in the primary
// environment (production unless another is named), else its slug with
// -<environment>, kept within the 40-character label rule.
func AddressLabel(service, env, primary string) string {
	if primary == "" {
		primary = "production"
	}
	if env == "" || strings.EqualFold(env, primary) {
		return ServiceSlug(service)
	}
	return ServiceSlug(service + "-" + env)
}

// buildSetting is the Dockerfile path and build command, as the builder reads
// them: the Dockerfile relative to the root directory, without a leading slash
// (Railway writes "/Dockerfile").
func buildSetting(svc Service) *BuildSetting {
	dockerfile := strings.TrimLeft(strings.TrimSpace(svc.DockerfilePath), "/")
	if root := rootDir(svc.RootDirectory); root != "" {
		dockerfile = strings.TrimPrefix(dockerfile, root+"/")
	}
	b := &BuildSetting{Dockerfile: dockerfile, BuildCommand: strings.TrimSpace(svc.BuildCommand)}
	if b.Dockerfile == "" && b.BuildCommand == "" {
		return nil
	}
	return b
}

// rootDir is Railway's root directory as a relative path; the repository root
// is "".
func rootDir(dir string) string {
	return strings.Trim(strings.TrimSpace(dir), "/")
}

// withEnv tags actions with their environment.
func withEnv(actions []Action, env string) []Action {
	for i := range actions {
		actions[i].Env = env
	}
	return actions
}

// readEnvironments lists the environments the services came from, in read
// order. A source with no services still names the environments it read.
func readEnvironments(src Source) []string {
	var names []string
	for _, name := range strings.Split(src.Environment, ",") {
		if name = strings.TrimSpace(name); name != "" && !containsFold(names, name) {
			names = append(names, name)
		}
	}
	for _, svc := range src.Services {
		if svc.Environment != "" && !containsFold(names, svc.Environment) {
			names = append(names, svc.Environment)
		}
	}
	return names
}

// databaseEngines maps each database service's name (lower case) to its
// engine, for rewriting references to it.
func databaseEngines(src Source) map[string]string {
	engines := map[string]string{}
	for _, svc := range src.Services {
		if engine := databaseEngine(svc); engine != "" {
			engines[strings.ToLower(svc.Name)] = engine
		}
	}
	for _, plugin := range src.Plugins {
		if action, ok := databaseAction(plugin); ok {
			engines[strings.ToLower(plugin.FriendlyName)] = action.Engine
		}
	}
	return engines
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func unmappedServiceActions(svc Service) []Action {
	var actions []Action
	add := func(field, why, next string) {
		actions = append(actions, Action{Op: "unmapped", Name: svc.Name, Field: field, Why: why, Next: next})
	}
	if strings.EqualFold(svc.Builder, "NIXPACKS") {
		add("builder", "Railway built it with Nixpacks; MIOSA builds the Dockerfile if the repository has one, else detects the stack with Railpack",
			"check the first build")
	}
	if svc.BuildCommand != "" && svc.Repo == "" {
		add("buildCommand", "only a repository service builds on MIOSA", "none")
	}
	if svc.RestartPolicy != "" && mapRestartPolicy(svc.RestartPolicy) == "" {
		add("restartPolicyType", fmt.Sprintf("Railway policy %q has no MIOSA equivalent", svc.RestartPolicy),
			"set restart_policy to always, on_failure or never after the import")
	}
	for _, v := range svc.Volumes {
		add("volume "+v.MountPath, fmt.Sprintf("Railway mounted %d MB at %s; the data is not copied", v.SizeMB, v.MountPath),
			fmt.Sprintf("miosa volume create %s-data --size-gb %d, then attach it to the service at %s", svc.Name, volumeGB(v.SizeMB), v.MountPath))
	}
	return actions
}

func databaseAction(plugin PluginRef) (Action, bool) {
	engine := map[string]string{"postgresql": "postgres", "postgres": "postgres", "redis": "redis", "mysql": "mysql"}[plugin.Name]
	if engine == "" {
		return Action{}, false
	}
	return Action{Op: "database.create", Name: plugin.FriendlyName, Engine: engine}, true
}

func serviceKind(svc Service) (kind, schedule string) {
	if svc.CronSchedule != "" {
		return "cron", svc.CronSchedule
	}
	if isWorker(svc) {
		return "worker", ""
	}
	return "server", ""
}

// isWorker is an inference, because Railway has no service type: a service with
// no domain whose name or start command says worker is a Worker in MIOSA (no
// port, no address). It is reported like any other inference.
func isWorker(svc Service) bool {
	if len(svc.ServiceDomains) > 0 {
		return false
	}
	name := strings.ToLower(svc.Name)
	if strings.Contains(name, "worker") {
		return true
	}
	start := strings.ToLower(strings.TrimSpace(svc.StartCommand))
	for _, marker := range []string{"arq ", "celery", "rq worker", "sidekiq"} {
		if strings.HasPrefix(start, marker) || strings.Contains(start, " "+marker) {
			return true
		}
	}
	return false
}

// databaseEngine classifies a Railway database from its image.
func databaseEngine(svc Service) string {
	image := strings.ToLower(svc.Image)
	switch {
	case strings.Contains(image, "postgres"):
		return "postgres"
	case strings.Contains(image, "redis"), strings.Contains(image, "valkey"):
		return "redis"
	case strings.Contains(image, "mysql"), strings.Contains(image, "mariadb"):
		return "mysql"
	}
	return ""
}

// unmappedDatabase reports what a managed database does not take from Railway.
func unmappedDatabase(svc Service, engine string) []Action {
	var actions []Action
	add := func(field, why, next string) {
		actions = append(actions, Action{Op: "unmapped", Name: svc.Name, Field: field, Why: why, Next: next})
	}
	if svc.Image != "" {
		add("image", fmt.Sprintf("Railway ran %s as a service; MIOSA provisions a managed %s", svc.Image, engine),
			"check the engine version after the import")
	}
	if svc.StartCommand != "" {
		add("startCommand", "the managed database starts itself", "none")
	}
	for _, v := range svc.Volumes {
		add("volume "+v.MountPath, fmt.Sprintf("Railway mounted %d MB at %s", v.SizeMB, v.MountPath),
			fmt.Sprintf("MIOSA provisions %s at size small; confirm its storage covers %d MB, or resize it after the import", engine, v.SizeMB))
	}
	return actions
}

// mapRestartPolicy maps Railway's restart policy onto the service setting the
// engine enforces (always, on_failure, never). An unknown value is "" and the
// plan reports it instead of guessing.
func mapRestartPolicy(policy string) string {
	switch strings.ToUpper(strings.TrimSpace(policy)) {
	case "ALWAYS":
		return "always"
	case "ON_FAILURE", "ON-FAILURE":
		return "on_failure"
	case "NEVER":
		return "never"
	default:
		return ""
	}
}

// mapRegion turns Railway's region ids into MIOSA's three regions. An unknown
// region returns "", and the plan reports it instead of guessing.
func mapRegion(region string) string {
	switch {
	case region == "":
		return ""
	case strings.HasPrefix(region, "us-east"):
		return "us-east"
	case strings.HasPrefix(region, "us-west"), strings.HasPrefix(region, "us-central"):
		return "us-west"
	case strings.HasPrefix(region, "us-south"):
		return "us-mia"
	default:
		return ""
	}
}

// closestRegion names the MIOSA region nearest a Railway region MIOSA does not
// have. It is a suggestion the user confirms with --region, never a silent pick.
func closestRegion(region string) string {
	switch {
	case strings.HasPrefix(region, "us-west"), strings.HasPrefix(region, "asia"), strings.HasPrefix(region, "au-"):
		return "us-west"
	case strings.HasPrefix(region, "us-south"), strings.HasPrefix(region, "southamerica"):
		return "us-mia"
	default:
		return "us-east"
	}
}

// filterServices applies --only. A name that matches nothing is reported, not
// silently dropped.
func filterServices(src Source, only []string) (Source, []string) {
	if len(only) == 0 {
		return src, nil
	}
	wanted := map[string]bool{}
	for _, name := range only {
		wanted[strings.ToLower(strings.TrimSpace(name))] = true
	}
	kept := make([]Service, 0, len(src.Services))
	matched := map[string]bool{}
	var skipped []string
	for _, svc := range src.Services {
		if wanted[strings.ToLower(svc.Name)] {
			kept = append(kept, svc)
			matched[strings.ToLower(svc.Name)] = true
			continue
		}
		if !containsFold(skipped, svc.Name) {
			skipped = append(skipped, svc.Name)
		}
	}
	src.Services = kept
	for name := range wanted {
		if !matched[name] {
			skipped = append(skipped, name+" (not in this project)")
		}
	}
	sort.Strings(skipped)
	return src, skipped
}

// applySuffix renames services and custom domains so a shadow import never
// collides with the project it mirrors: `backend` becomes `backend2` and
// `app.example.com` becomes `app2.example.com`.
func applySuffix(src Source, suffix string) Source {
	suffix = strings.TrimSpace(suffix)
	if suffix == "" {
		return src
	}
	for i := range src.Services {
		svc := &src.Services[i]
		svc.Name += suffix
		for j, fqdn := range svc.CustomDomains {
			svc.CustomDomains[j] = suffixFirstLabel(fqdn, suffix)
		}
	}
	return src
}

// suffixFirstLabel inserts the suffix before the first dot, leaving the rest of
// the name alone: app.cliniciq.com with "2" is app2.cliniciq.com.
func suffixFirstLabel(fqdn, suffix string) string {
	if i := strings.IndexByte(fqdn, '.'); i > 0 {
		return fqdn[:i] + suffix + fqdn[i:]
	}
	return fqdn + suffix
}

// excludeServices drops the named services and reports them, including a name
// that matches nothing so a typo is visible.
func excludeServices(src Source, names []string) (Source, []string) {
	if len(names) == 0 {
		return src, nil
	}
	drop := map[string]bool{}
	for _, name := range names {
		drop[strings.ToLower(strings.TrimSpace(name))] = true
	}
	kept := make([]Service, 0, len(src.Services))
	matched := map[string]bool{}
	var removed []string
	for _, svc := range src.Services {
		if drop[strings.ToLower(svc.Name)] {
			if !matched[strings.ToLower(svc.Name)] {
				removed = append(removed, svc.Name)
			}
			matched[strings.ToLower(svc.Name)] = true
			continue
		}
		kept = append(kept, svc)
	}
	src.Services = kept
	for name := range drop {
		if !matched[name] {
			removed = append(removed, name+" (not in this project)")
		}
	}
	sort.Strings(removed)
	return src, removed
}

// repoURL expands Railway's "owner/repo" to a URL. Anything with a scheme or a
// host is kept as given.
func repoURL(repo string) string {
	if strings.Contains(repo, "://") || strings.HasPrefix(repo, "git@") {
		return repo
	}
	return "https://github.com/" + repo
}

func volumeGB(sizeMB int) int {
	gb := (sizeMB + 1023) / 1024
	if gb < 1 {
		gb = 1
	}
	return gb
}

// digestOf hashes the normalized read so an apply can refuse a project that
// changed after the plan was reviewed.
func digestOf(src Source) (string, error) {
	normalized := struct {
		ProjectID   string
		Environment string
		Services    []Service
		Plugins     []PluginRef
		Shared      []SharedVariable
	}{src.ProjectID, src.Environment, src.Services, src.Plugins, src.Shared}
	// Service and PluginRef hold only comparable/slice fields, but sort the
	// variables and volumes so the digest does not depend on read order.
	for i := range normalized.Services {
		s := normalized.Services[i]
		sort.Slice(s.Variables, func(a, b int) bool { return s.Variables[a].Name < s.Variables[b].Name })
		sort.Slice(s.Volumes, func(a, b int) bool { return s.Volumes[a].MountPath < s.Volumes[b].MountPath })
		sort.Strings(s.ServiceDomains)
		sort.Strings(s.CustomDomains)
		normalized.Services[i] = s
	}
	sort.Slice(normalized.Plugins, func(i, j int) bool { return normalized.Plugins[i].FriendlyName < normalized.Plugins[j].FriendlyName })
	sort.SliceStable(normalized.Shared, func(i, j int) bool {
		a, b := normalized.Shared[i], normalized.Shared[j]
		if a.Environment != b.Environment {
			return a.Environment < b.Environment
		}
		return a.Variable.Name < b.Variable.Name
	})

	body, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
