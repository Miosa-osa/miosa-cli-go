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
// refused rather than half-applied.
const PlanVersion = 1

// RotationWarning is printed on every plan, because it is the one thing a
// Railway user must hear before trusting an import.
const RotationWarning = "Railway's secrets were exposed in the 2025 leak. " +
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

	// project.create, target.create, service.create, database.create, and the
	// name inside variable.set and unmapped.
	Name string `json:"name,omitempty"`

	// service.create
	Kind     string          `json:"kind,omitempty"`
	Source   *ServiceSource  `json:"source,omitempty"`
	Build    *BuildSetting   `json:"build,omitempty"`
	Settings *ServiceSetting `json:"settings,omitempty"`
	Address  *bool           `json:"address,omitempty"`
	// NeedsRegion is set when the Railway region has no MIOSA equivalent, so an
	// apply must be given --region rather than guessing.
	NeedsRegion bool `json:"needs_region,omitempty"`

	// database.create
	Engine  string `json:"engine,omitempty"`
	Version string `json:"version,omitempty"`
	Size    string `json:"size,omitempty"`

	// variable.set
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
}

// BuildSetting is the build half of a service.
type BuildSetting struct {
	Dockerfile string `json:"dockerfile,omitempty"`
}

// ServiceSetting is the run half of a service.
type ServiceSetting struct {
	StartCommand string `json:"start_command,omitempty"`
	Replicas     int    `json:"replicas,omitempty"`
	Region       string `json:"region,omitempty"`
	CronSchedule string `json:"cron_schedule,omitempty"`
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
}

// Build turns a Railway read into a plan. It touches nothing: the same source
// always produces the same plan, which is what makes --plan reviewable.
func Build(src Source, opts Options) (Plan, error) {
	name := opts.ProjectName
	if name == "" {
		name = src.ProjectName
	}
	src, skipped := filterServices(src, opts.Only)
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
	if opts.IncludeTargets {
		for _, env := range src.Environments {
			if !strings.EqualFold(env, src.Environment) {
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

	needsValue := 0
	for _, svc := range src.Services {
		if engine := databaseEngine(svc); engine != "" {
			// Railway runs databases as image services. MIOSA has a database
			// service that binds a managed database and provides DATABASE_URL /
			// REDIS_URL, which is what the incoming references expect.
			plan.Actions = append(plan.Actions, Action{Op: "database.service", Name: svc.Name, Engine: engine})
			plan.Actions = append(plan.Actions, unmappedDatabase(svc, engine)...)
			continue
		}
		plan.Actions = append(plan.Actions, serviceActions(svc)...)
		for _, v := range svc.Variables {
			action := Action{
				Op:       "variable.set",
				Service:  svc.Name,
				Variable: &Variable{Name: v.Name, Value: v.Value, HasValue: v.HasValue},
			}
			if !v.HasValue {
				needsValue++
				action.ValueTarget = svc.Name
			}
			plan.Actions = append(plan.Actions, action)
		}
		if len(svc.ServiceDomains) > 0 || len(svc.CustomDomains) > 0 {
			plan.Actions = append(plan.Actions, Action{
				Op:      "domain.report",
				Service: svc.Name,
				Domains: svc.ServiceDomains,
			})
		}
		for _, d := range svc.CustomDomains {
			plan.Actions = append(plan.Actions, Action{Op: "domain.custom", Service: svc.Name, Domains: []string{d}})
		}
		plan.Actions = append(plan.Actions, unmappedServiceActions(svc)...)
	}

	if len(skipped) > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf(
			"not imported (--only): %s", strings.Join(skipped, ", ")))
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
		StartCommand: svc.StartCommand,
		Region:       mapRegion(svc.Region),
		CronSchedule: schedule,
	}
	setting.Replicas = svc.NumReplicas
	if setting.Replicas > 50 {
		setting.Replicas = 50
	}

	var source *ServiceSource
	switch {
	case svc.Repo != "":
		source = &ServiceSource{Type: "git", Ref: repoURL(svc.Repo), Branch: svc.Branch, RootDir: svc.RootDirectory}
	case svc.Image != "":
		source = &ServiceSource{Type: "image", Ref: svc.Image}
	}

	address := len(svc.ServiceDomains) > 0
	var actions []Action
	if source == nil {
		actions = append(actions, Action{
			Op:    "unmapped",
			Name:  svc.Name,
			Field: "source",
			Why:   "Railway has no repository or image for this service (it was uploaded with the CLI)",
			Next:  "put the code in a repository, then set --repo on the plan's service before applying",
		})
	}
	action := Action{
		Op:       "service.create",
		Name:     svc.Name,
		Kind:     kind,
		Source:   source,
		Settings: setting,
		Address:  &address,
	}
	if svc.DockerfilePath != "" {
		action.Build = &BuildSetting{Dockerfile: svc.DockerfilePath}
	}
	actions = append(actions, action)

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

func unmappedServiceActions(svc Service) []Action {
	var actions []Action
	add := func(field, why, next string) {
		actions = append(actions, Action{Op: "unmapped", Name: svc.Name, Field: field, Why: why, Next: next})
	}
	if svc.BuildCommand != "" {
		add("buildCommand", "MIOSA builds from the Dockerfile or detects the stack; there is no build command setting",
			"move it into the Dockerfile or a build script")
	}
	if svc.HealthcheckPath != "" {
		add("healthcheckPath", "MIOSA checks readiness on the service's port", "none")
	}
	if svc.RestartPolicy != "" {
		add("restartPolicyType", "MIOSA restarts a failed release by policy", "none")
	}
	for _, v := range svc.Volumes {
		add("volume "+v.MountPath, fmt.Sprintf("the Services API has no service volumes; Railway mounted %d MB at %s", v.SizeMB, v.MountPath),
			fmt.Sprintf("miosa volume create %s-data --size-gb %d, then attach it where the data is needed", svc.Name, volumeGB(v.SizeMB)))
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
			"the managed database has its own storage")
	}
	return actions
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
	var skipped []string
	for _, svc := range src.Services {
		if wanted[strings.ToLower(svc.Name)] {
			kept = append(kept, svc)
			delete(wanted, strings.ToLower(svc.Name))
			continue
		}
		skipped = append(skipped, svc.Name)
	}
	src.Services = kept
	for missing := range wanted {
		skipped = append(skipped, missing+" (not in this project)")
	}
	sort.Strings(skipped)
	return src, skipped
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
	}{src.ProjectID, src.Environment, src.Services, src.Plugins}
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

	body, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
