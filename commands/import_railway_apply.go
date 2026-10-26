package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
)

// ── apply ────────────────────────────────────────────────────────────────────
//
// An apply creates; it does not start. Services are created stopped, with their
// source, settings and variables, and nothing builds or runs until the user
// deploys them, or passes --deploy. A repository service builds from a Forge
// copy of the GitHub repository, made through the user's GitHub connection, so
// a private repository builds without a credential on the service.
//
// Every create is looked up first (project, Deploy target, service in its
// Deploy target, database service), so a second run, or a run after an
// interrupted one, creates only what is missing.

// repoDeploymentOp is what an older CLI recorded when it made a repository
// service a legacy deployment; --cleanup still deletes those.
const repoDeploymentOp = "deployment.create"

// sourceLinkOp records a service's GitHub link, so --resume does not link twice.
const sourceLinkOp = "source.link"

type createdRef struct {
	Op   string `json:"op"`
	Env  string `json:"env,omitempty"`
	Name string `json:"name"`
	ID   string `json:"id"`
}

type applyStateFile struct {
	Version   int          `json:"version"`
	Source    string       `json:"source_digest"`
	ProjectID string       `json:"project_id"`
	Created   []createdRef `json:"created"`
}

func statePath(f *railwayImportFlags, plan railway.Plan) string {
	if f.state != "" {
		return f.state
	}
	name := strings.ReplaceAll(plan.Target.ProjectName, "/", "-")
	if name == "" {
		name = "railway"
	}
	return filepath.Join(".miosa", "import-railway-"+name+".json")
}

func loadState(path string) (applyStateFile, error) {
	var state applyStateFile
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return applyStateFile{Version: 1}, nil
		}
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return state, fmt.Errorf("%s is not a state file: %w", path, err)
	}
	return state, nil
}

func saveState(path string, state applyStateFile) error {
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(body, '\n'), 0o600)
}

// githubRepo is one repository the user's GitHub connection can read.
type githubRepo struct {
	ID       int64  `json:"id"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// applier holds one apply's state: the ids it found or created, and what the
// user still has to do.
type applier struct {
	ctx            context.Context
	api            *api.Client
	p              printerLike
	plan           railway.Plan
	f              *railwayImportFlags
	path           string
	state          applyStateFile
	created        map[string]string // op|env|name -> id
	projectID      string
	workspaceID    string
	envIDs         map[string]string // lower env name -> id
	services       map[string]string // env|name -> service id
	repos          map[string]githubRepo
	imports        map[string]string // env|name -> Forge import id
	sourceless     []string
	skippedDomains []string
	regions        []string
	byHand         []string
	started        []string
}

func createdKey(op, env, name string) string { return op + "|" + env + "|" + name }

func serviceKey(env, name string) string { return env + "|" + name }

func (a *applier) record(op, env, name, id string) error {
	if id == "" {
		return nil
	}
	a.created[createdKey(op, env, name)] = id
	a.state.Created = append(a.state.Created, createdRef{Op: op, Env: env, Name: name, ID: id})
	return saveState(a.path, a.state)
}

func (a *applier) failed(err error) error { return applyFailed(a.path, err) }

func railwayApply(ctx context.Context, cmd *cobra.Command, f *railwayImportFlags) error {
	plan, err := readPlan(f.planPath)
	if err != nil {
		return die(err)
	}
	missing := missingValues(plan)
	if missing > 0 && !f.allowMissing {
		return usagef("%d variable(s) have no value; fill them into the plan or pass --allow-missing-values to create them empty", missing)
	}
	if missing > 0 && f.deploy {
		return usagef("--deploy would start services with %d empty variable(s); apply without --deploy, set the values, then deploy", missing)
	}
	if needsRegion(plan) > 0 && f.region == "" {
		return usagef("%d service(s) come from a Railway region MIOSA does not have; pass --region us-east, us-west or us-mia", needsRegion(plan))
	}

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	a := &applier{
		ctx: ctx, api: c.API, p: printerFor(cmd), plan: plan, f: f,
		created: map[string]string{}, envIDs: map[string]string{}, services: map[string]string{},
		imports: map[string]string{},
	}

	// Nothing is created until every repository service can build here and
	// every repository is readable through the user's GitHub connection.
	if err := a.preflight(); err != nil {
		return err
	}
	// The plan keeps --workspace as typed (a slug, a name or an id); the API
	// takes only the id.
	if a.workspaceID, err = importWorkspaceID(ctx, c.API, plan.Target.WorkspaceID); err != nil {
		return err
	}

	a.path = statePath(f, plan)
	a.state, err = loadState(a.path)
	if err != nil {
		return die(err)
	}
	if a.state.Source == "" {
		a.state.Source = plan.Source.Digest
	}
	if a.state.Source != plan.Source.Digest {
		return usagef("%s was started from a different plan; use a new --state file or --cleanup first", a.path)
	}
	if f.resume && len(a.state.Created) == 0 {
		warnOnce("resume", "warning: nothing recorded in the state file yet; applying from the start")
	}
	for _, ref := range a.state.Created {
		a.created[createdKey(ref.Op, ref.Env, ref.Name)] = ref.ID
	}
	a.projectID = a.state.ProjectID

	steps := []func() error{
		a.applyProject, a.applyTargets, a.applyDatabaseServices, a.applyDatabases,
		a.applyServices, a.applyVariables, a.applySharedVariables, a.applyDomains,
	}
	if f.deploy {
		steps = append(steps, a.applyDeploy)
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	a.summary()
	return nil
}

// preflight refuses an apply that could only half work: a repository service
// where Services cannot build one, or a GitHub repository the user's
// connection cannot read. It answers before anything is created.
func (a *applier) preflight() error {
	var repoServices, github []string
	for _, act := range a.plan.Actions {
		if act.Op != "service.create" || act.Source == nil || (act.Source.Type != "git" && act.Source.Type != "forge") {
			continue
		}
		repoServices = append(repoServices, qualifiedName(act))
		if _, ok := githubFullName(act.Source.Ref); ok && act.Source.Type == "git" {
			github = append(github, act.Source.Ref)
		}
	}
	if len(repoServices) == 0 {
		return nil
	}
	if !serviceRepoBuilds(a.ctx, a.api) {
		return usagef("this organization cannot build services from a repository yet (GET /project-capabilities: source_builds is not true), "+
			"so nothing was created: %s.\nAsk MIOSA support to turn on service builds, or leave these services out with --exclude and import the rest",
			strings.Join(repoServices, ", "))
	}
	if len(github) == 0 {
		return nil
	}
	repos, err := listGitHubRepos(a.ctx, a.api)
	if err != nil {
		return err
	}
	a.repos = repos
	var unreadable []string
	for _, ref := range github {
		name, _ := githubFullName(ref)
		if _, ok := repos[strings.ToLower(name)]; !ok && !containsString(unreadable, name) {
			unreadable = append(unreadable, name)
		}
	}
	if len(unreadable) > 0 {
		sort.Strings(unreadable)
		return usagef("the connected GitHub account cannot read %s, so nothing was created. "+
			"Grant it access on GitHub (or connect the account that can: miosa connections add apps github), then apply again",
			strings.Join(unreadable, ", "))
	}
	return nil
}

// importWorkspaceID resolves --workspace (an id, a slug or a name, any case) to
// the workspace id the API takes. Empty is no workspace.
func importWorkspaceID(ctx context.Context, c *api.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || uuidRE.MatchString(ref) {
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
		return "", die(fmt.Errorf("resolving workspace %q: %w", ref, err))
	}
	var names []string
	for _, w := range out.Data {
		if strings.EqualFold(w.Slug, ref) || strings.EqualFold(w.Name, ref) || strings.EqualFold(w.ID, ref) {
			return w.ID, nil
		}
		names = append(names, firstNonEmpty(w.Slug, w.Name))
	}
	sort.Strings(names)
	return "", usagef("no workspace %q, so nothing was created; workspaces: %s", ref, orDashList(names))
}

// listGitHubRepos reads every repository the user's GitHub connection can
// read, by lower-case full name.
func listGitHubRepos(ctx context.Context, c *api.Client) (map[string]githubRepo, error) {
	repos := map[string]githubRepo{}
	for page := 1; page <= 50; page++ {
		var out struct {
			Data []githubRepo `json:"data"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
		if err := c.Get(ctx, "/integrations/github/repos", q, &out); err != nil {
			var apiErr *api.Error
			if errors.As(err, &apiErr) && (apiErr.Status == 412 || apiErr.Status == 404) {
				return nil, usagef("the plan builds from GitHub repositories and GitHub is not connected, so nothing was created.\n" +
					"Connect GitHub first: miosa connections add apps github")
			}
			return nil, die(err)
		}
		for _, r := range out.Data {
			repos[strings.ToLower(r.FullName)] = r
		}
		if len(out.Data) < 100 {
			break
		}
	}
	return repos, nil
}

// githubFullName reads owner/name from a GitHub clone URL.
func githubFullName(ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	for _, prefix := range []string{"https://github.com/", "http://github.com/", "git@github.com:"} {
		if strings.HasPrefix(strings.ToLower(ref), prefix) {
			name := strings.TrimSuffix(strings.Trim(ref[len(prefix):], "/"), ".git")
			if strings.Count(name, "/") == 1 {
				return name, true
			}
		}
	}
	return "", false
}

func (a *applier) applyProject() error {
	if a.plan.Target.Into != "" {
		id, err := resolveServiceProject(a.ctx, a.api, a.plan.Target.Into)
		if err != nil {
			return err
		}
		a.projectID = id
		a.state.ProjectID = id
		return saveState(a.path, a.state)
	}
	for _, act := range a.plan.Actions {
		if act.Op != "project.create" || a.projectID != "" {
			continue
		}
		id, err := findProjectByName(a.ctx, a.api, act.Name)
		if err != nil {
			return err
		}
		if id == "" {
			body := map[string]any{"name": act.Name}
			if a.workspaceID != "" {
				body["workspace_id"] = a.workspaceID
			}
			var out struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := a.api.Post(a.ctx, "/projects", body, &out); err != nil {
				return a.failed(err)
			}
			id = out.Data.ID
			a.p.Line("created project %s (%s)", act.Name, shortID(id))
		} else {
			a.p.Line("project %s already exists (%s)", act.Name, shortID(id))
		}
		a.projectID = id
		a.state.ProjectID = id
		if err := a.record(act.Op, "", act.Name, id); err != nil {
			return die(err)
		}
	}
	if a.projectID == "" {
		return usagef("the plan names no project to create and no --into project")
	}
	return nil
}

// applyTargets finds or creates every Deploy target the plan names, so each
// service lands in the one it came from.
func (a *applier) applyTargets() error {
	envs, err := listServiceEnvironments(a.ctx, a.api, a.projectID)
	if err != nil {
		return err
	}
	for _, e := range envs {
		a.envIDs[strings.ToLower(e.Name)] = e.ID
	}
	names := []string{}
	for _, act := range a.plan.Actions {
		if act.Op == "target.create" && !containsFold(names, act.Name) {
			names = append(names, act.Name)
		}
		if act.Env != "" && !containsFold(names, act.Env) {
			names = append(names, act.Env)
		}
	}
	for _, name := range names {
		if id := a.envIDs[strings.ToLower(name)]; id != "" {
			a.p.Line("Deploy target %s already exists (%s)", name, shortID(id))
			continue
		}
		var out struct {
			Data serviceEnvironment `json:"data"`
		}
		if err := a.api.Post(a.ctx, "/projects/"+url.PathEscape(a.projectID)+"/environments", map[string]any{"name": name}, &out); err != nil {
			return a.failed(err)
		}
		a.envIDs[strings.ToLower(name)] = out.Data.ID
		a.p.Line("created Deploy target %s (%s)", name, shortID(out.Data.ID))
		if err := a.record("target.create", "", name, out.Data.ID); err != nil {
			return die(err)
		}
	}
	return nil
}

// envID is the Deploy target id of an action, "" for the project's default.
func (a *applier) envID(env string) string {
	if env == "" {
		return ""
	}
	return a.envIDs[strings.ToLower(env)]
}

// envQuery is the ?env= of a Deploy target, "" for the default.
func (a *applier) envQuery(env string) string {
	if id := a.envID(env); id != "" {
		return "?" + url.Values{"env": {id}}.Encode()
	}
	return ""
}

// existingService finds a service by name in one Deploy target.
func (a *applier) existingService(env, name string) (*projectServiceRow, error) {
	services, err := listServices(a.ctx, a.api, a.projectID, a.envID(env))
	if err != nil {
		return nil, err
	}
	for i := range services {
		if strings.EqualFold(services[i].Name, name) {
			return &services[i], nil
		}
	}
	return nil, nil
}

// applyDatabaseServices creates each database service through the change-set
// path, the only one that provisions and binds a managed database in one step,
// with deploy off: the database is provisioned, no app is started.
func (a *applier) applyDatabaseServices() error {
	for _, act := range a.plan.Actions {
		if act.Op != "database.service" {
			continue
		}
		label := qualifiedName(act)
		if id := a.created[createdKey(act.Op, act.Env, act.Name)]; id != "" {
			a.services[serviceKey(act.Env, act.Name)] = id
			a.p.Line("database service %s already created (%s)", label, shortID(id))
			continue
		}
		existing, err := a.existingService(act.Env, act.Name)
		if err != nil {
			return err
		}
		if existing != nil {
			a.services[serviceKey(act.Env, act.Name)] = existing.ID
			a.p.Line("database service %s already exists (%s)", label, shortID(existing.ID))
			if err := a.record(act.Op, act.Env, act.Name, existing.ID); err != nil {
				return die(err)
			}
			continue
		}
		base := "/projects/" + url.PathEscape(a.projectID) + "/changes"
		query := a.envQuery(act.Env)
		var open struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := a.api.Get(a.ctx, base+query, nil, &open); err == nil && len(open.Data) > 0 {
			return usagef("Deploy target %s has %d staged change(s); apply or discard them before importing", envLabel(act.Env), len(open.Data))
		}
		stage := map[string]any{
			"op":      "service.create",
			"payload": databasePayload(act),
		}
		var staged struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := a.api.Post(a.ctx, base+query, stage, &staged); err != nil {
			return a.failed(err)
		}
		var applied struct {
			Data struct {
				Changes []struct {
					ID     string `json:"id"`
					Result struct {
						ServiceID string `json:"service_id"`
					} `json:"result"`
				} `json:"changes"`
			} `json:"data"`
		}
		if err := a.api.Post(a.ctx, base+"/apply"+query, map[string]any{"deploy": false}, &applied); err != nil {
			return a.failed(err)
		}
		// The service id is in the applied change's result; the staged id is the
		// change's, and --cleanup must never be handed that one.
		id := ""
		for _, change := range applied.Data.Changes {
			if change.ID == staged.Data.ID || len(applied.Data.Changes) == 1 {
				id = change.Result.ServiceID
			}
		}
		if id == "" {
			found, err := a.existingService(act.Env, act.Name)
			if err != nil {
				return err
			}
			if found == nil {
				return a.failed(fmt.Errorf("the database service %s was applied but its id was not returned or found", label))
			}
			id = found.ID
		}
		a.services[serviceKey(act.Env, act.Name)] = id
		a.p.Line("created database service %s (%s)", label, shortID(id))
		if err := a.record(act.Op, act.Env, act.Name, id); err != nil {
			return die(err)
		}
	}
	return nil
}

// applyDatabases creates the databases a legacy Railway plugin stood for.
func (a *applier) applyDatabases() error {
	for _, act := range a.plan.Actions {
		if act.Op != "database.create" {
			continue
		}
		if id := a.created[createdKey(act.Op, act.Env, act.Name)]; id != "" {
			a.p.Line("database %s already created (%s)", act.Name, shortID(id))
			continue
		}
		existing, err := findDatabaseByName(a.ctx, a.api, act.Name)
		if err != nil {
			return err
		}
		if existing != "" {
			a.p.Line("database %s already exists (%s)", act.Name, shortID(existing))
			if err := a.record(act.Op, act.Env, act.Name, existing); err != nil {
				return die(err)
			}
			continue
		}
		body := map[string]any{"name": act.Name, "engine": act.Engine}
		if act.Version != "" {
			body["engine_version"] = act.Version
		}
		if act.Size != "" {
			body["size"] = act.Size
		}
		if a.workspaceID != "" {
			body["workspace_id"] = a.workspaceID
		}
		var out struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := a.api.Post(a.ctx, "/databases", body, &out); err != nil {
			return a.failed(err)
		}
		a.p.Line("created database %s (%s)", act.Name, shortID(out.Data.ID))
		if err := a.record(act.Op, act.Env, act.Name, out.Data.ID); err != nil {
			return die(err)
		}
	}
	return nil
}

// applyServices creates each service, stopped, in its Deploy target, then
// points a GitHub service at a Forge copy of its repository.
func (a *applier) applyServices() error {
	for _, act := range a.plan.Actions {
		if act.Op != "service.create" {
			continue
		}
		label := qualifiedName(act)
		key := serviceKey(act.Env, act.Name)
		var existing *projectServiceRow
		if id := a.created[createdKey(act.Op, act.Env, act.Name)]; id != "" {
			a.services[key] = id
			a.p.Line("service %s already created (%s)", label, shortID(id))
		} else {
			found, err := a.existingService(act.Env, act.Name)
			if err != nil {
				return err
			}
			existing = found
		}
		if a.services[key] == "" && existing != nil {
			a.services[key] = existing.ID
			a.p.Line("service %s already exists (%s)", label, shortID(existing.ID))
			if err := a.record(act.Op, act.Env, act.Name, existing.ID); err != nil {
				return die(err)
			}
		}
		if a.services[key] == "" {
			if err := a.createService(act); err != nil {
				return err
			}
		}
		if act.Source == nil {
			a.sourceless = append(a.sourceless, act.Name+envSuffix(act.Env))
			continue
		}
		if name, ok := githubFullName(act.Source.Ref); ok && act.Source.Type == "git" {
			if err := a.linkGitHub(act, name, existing); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *applier) createService(act railway.Action) error {
	body := map[string]any{"name": act.Name, "kind": act.Kind, "slug": slugOf(act)}
	if source := a.sourceBody(act); source != nil {
		body["source"] = source
	}
	if settings := a.settingsBody(act); len(settings) > 0 {
		body["settings"] = settings
	}
	var out struct {
		Data projectServiceRow `json:"data"`
	}
	path := "/projects/" + url.PathEscape(a.projectID) + "/services" + a.envQuery(act.Env)
	if err := a.api.Post(a.ctx, path, body, &out); err != nil {
		return a.failed(err)
	}
	a.services[serviceKey(act.Env, act.Name)] = out.Data.ID
	a.p.Line("created %s %s (%s), not started", act.Kind, qualifiedName(act), shortID(out.Data.ID))
	if err := a.record(act.Op, act.Env, act.Name, out.Data.ID); err != nil {
		return die(err)
	}
	if act.Address != nil && *act.Address {
		if err := enableServiceAddress(a.ctx, a.api, a.projectID, out.Data.ID, act.AddressLabel); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s has no public address yet: %v\n", qualifiedName(act), err)
		}
	}
	return nil
}

// sourceBody is the source a service is created with. A GitHub repository gets
// none here: linkGitHub sets it, through the user's connection, right after.
func (a *applier) sourceBody(act railway.Action) map[string]any {
	if act.Source == nil {
		return nil
	}
	if _, ok := githubFullName(act.Source.Ref); ok && act.Source.Type == "git" {
		return nil
	}
	source := map[string]any{"type": act.Source.Type, "ref": act.Source.Ref}
	if act.Source.Branch != "" {
		source["branch"] = act.Source.Branch
	}
	if act.Source.RootDir != "" {
		source["root_dir"] = act.Source.RootDir
	}
	if act.Source.Type == "git" || act.Source.Type == "forge" {
		putBuild(source, act.Build)
	}
	if act.Source.Port > 0 {
		source["port"] = act.Source.Port
	}
	return source
}

// databasePayload is the change that creates a database service.
func databasePayload(act railway.Action) map[string]any {
	return map[string]any{"name": act.Name, "kind": "database", "managed": act.Engine, "slug": slugOf(act)}
}

// slugOf is the service's slug: the plan's, or, for a plan written before
// slugs, the one its name gives, so a long or unusual name never fails the
// slug rule halfway through an apply.
func slugOf(act railway.Action) string {
	if act.Slug != "" {
		return act.Slug
	}
	return railway.ServiceSlug(act.Name)
}

// putSource writes the Dockerfile path, the build command and the port onto a
// source, where the builder and the health check read them. It reports whether
// anything changed.
func putSource(source map[string]any, act railway.Action) bool {
	changed := putBuild(source, act.Build)
	if act.Source != nil && act.Source.Port > 0 && fmt.Sprint(source["port"]) != strconv.Itoa(act.Source.Port) {
		source["port"] = act.Source.Port
		changed = true
	}
	return changed
}

// putBuild writes the Dockerfile path and build command onto a source, where
// the builder reads them.
func putBuild(source map[string]any, build *railway.BuildSetting) bool {
	if build == nil {
		return false
	}
	changed := false
	if build.Dockerfile != "" && source["dockerfile"] != build.Dockerfile {
		source["dockerfile"] = build.Dockerfile
		changed = true
	}
	if build.BuildCommand != "" && source["build_command"] != build.BuildCommand {
		source["build_command"] = build.BuildCommand
		changed = true
	}
	return changed
}

func (a *applier) settingsBody(act railway.Action) map[string]any {
	settings := map[string]any{}
	s := act.Settings
	if s == nil {
		return settings
	}
	if s.StartCommand != "" {
		settings["start_command"] = s.StartCommand
	}
	if s.PreDeployCommand != "" {
		settings["pre_deploy_command"] = s.PreDeployCommand
	}
	if s.Replicas > 0 {
		settings["replicas"] = s.Replicas
	}
	switch {
	case s.Region != "":
		settings["region"] = s.Region
	case act.NeedsRegion && a.f.region != "":
		settings["region"] = a.f.region
		a.regions = append(a.regions, fmt.Sprintf("%s: Railway %s -> MIOSA %s", qualifiedName(act), act.RailwayRegion, a.f.region))
	}
	if s.CronSchedule != "" {
		settings["cron_schedule"] = s.CronSchedule
	}
	if s.HealthcheckPath != "" {
		settings["healthcheck_path"] = s.HealthcheckPath
	}
	if s.HealthcheckTimeout > 0 {
		settings["healthcheck_timeout_s"] = s.HealthcheckTimeout
	}
	if s.RestartPolicy != "" {
		settings["restart_policy"] = s.RestartPolicy
	}
	if s.MaxRetries > 0 {
		settings["max_retries"] = s.MaxRetries
	}
	return settings
}

// linkGitHub points a service at its GitHub repository through the user's
// connection (PUT .../source/github): MIOSA copy-imports the repository into
// Forge and the service builds from the copy, private or not. The link
// replaces the source, so the Dockerfile path and build command are written
// back onto it afterwards.
func (a *applier) linkGitHub(act railway.Action, fullName string, existing *projectServiceRow) error {
	label := qualifiedName(act)
	serviceID := a.services[serviceKey(act.Env, act.Name)]
	if existing == nil {
		row, err := a.getService(serviceID)
		if err != nil {
			return a.failed(err)
		}
		existing = row
	}
	source := map[string]any{}
	if existing != nil && existing.Source != nil {
		source = existing.Source
	}
	if t, _ := source["type"].(string); t != "forge" {
		repo := a.repos[strings.ToLower(fullName)]
		body := map[string]any{
			"github_repository_id": strconv.FormatInt(repo.ID, 10),
			"name":                 repoShortName(fullName),
		}
		if act.Source.Branch != "" {
			body["branch"] = act.Source.Branch
		}
		if act.Source.RootDir != "" {
			body["root_dir"] = act.Source.RootDir
		}
		var out struct {
			Data struct {
				Service projectServiceRow `json:"service"`
				Import  *struct {
					ID     string `json:"id"`
					Status string `json:"status"`
				} `json:"import"`
			} `json:"data"`
		}
		if err := a.api.Put(a.ctx, servicePath(a.projectID, serviceID)+"/source/github", body, &out); err != nil {
			return a.failed(fmt.Errorf("linking %s to github.com/%s: %w", label, fullName, err))
		}
		source = out.Data.Service.Source
		if source == nil {
			source = map[string]any{}
		}
		importID := ""
		if out.Data.Import != nil {
			importID = out.Data.Import.ID
			a.imports[serviceKey(act.Env, act.Name)] = importID
		}
		visibility := "public"
		if repo.Private {
			visibility = "private"
		}
		a.p.Line("linked %s to github.com/%s (%s; MIOSA copies it into Forge and builds from the copy)", label, fullName, visibility)
		if err := a.record(sourceLinkOp, act.Env, act.Name, firstNonEmpty(importID, serviceID)); err != nil {
			return die(err)
		}
	}
	if putSource(source, act) {
		if err := a.api.Patch(a.ctx, servicePath(a.projectID, serviceID), map[string]any{"source": source}, nil); err != nil {
			return a.failed(fmt.Errorf("setting the Dockerfile, build command and port of %s: %w", label, err))
		}
	}
	return nil
}

func (a *applier) getService(serviceID string) (*projectServiceRow, error) {
	var out struct {
		Data projectServiceRow `json:"data"`
	}
	if err := a.api.Get(a.ctx, servicePath(a.projectID, serviceID), nil, &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// variableEntry is one variable as the API's entry list takes it. A variable
// is build-visible only when the plan asks for it and it has a value: the API
// refuses an empty build-visible variable, and one created empty becomes
// build-visible when its value is set with --build.
func variableEntry(v *railway.Variable) map[string]any {
	value := ""
	if v.HasValue {
		value = v.Value
	}
	return map[string]any{"name": v.Name, "value": value, "build": v.Build && strings.TrimSpace(value) != ""}
}

// applyVariables writes each service's variables in one call per service, as
// the entry list, which is what carries each variable's build visibility.
func (a *applier) applyVariables() error {
	pending := map[string][]map[string]any{}
	var order []string
	for _, act := range a.plan.Actions {
		if act.Op != "variable.set" || act.Variable == nil {
			continue
		}
		key := serviceKey(act.Env, act.Service)
		if pending[key] == nil {
			order = append(order, key)
		}
		pending[key] = append(pending[key], variableEntry(act.Variable))
	}
	for _, key := range order {
		serviceID := a.services[key]
		if serviceID == "" {
			continue
		}
		body := map[string]any{"variables": pending[key]}
		if err := a.api.Put(a.ctx, servicePath(a.projectID, serviceID)+"/variables", body, nil); err != nil {
			return a.failed(err)
		}
		env, name, _ := strings.Cut(key, "|")
		a.p.Line("set %d variable(s) on %s%s%s", len(pending[key]), name, envSuffix(env), buildCount(pending[key]))
	}
	return nil
}

// applySharedVariables writes each Deploy target's shared variables.
func (a *applier) applySharedVariables() error {
	pending := map[string][]map[string]any{}
	var order []string
	for _, act := range a.plan.Actions {
		if act.Op != "shared.set" || act.Variable == nil {
			continue
		}
		if pending[act.Env] == nil {
			order = append(order, act.Env)
		}
		pending[act.Env] = append(pending[act.Env], variableEntry(act.Variable))
	}
	for _, env := range order {
		body := map[string]any{"variables": pending[env]}
		if id := a.envID(env); id != "" {
			body["env"] = id
		}
		if err := a.api.Put(a.ctx, "/projects/"+url.PathEscape(a.projectID)+"/shared-variables", body, nil); err != nil {
			return a.failed(err)
		}
		a.p.Line("set %d shared variable(s) in %s%s", len(pending[env]), envLabel(env), buildCount(pending[env]))
	}
	return nil
}

// buildCount says how many of a write's variables are build-visible.
func buildCount(entries []map[string]any) string {
	n := 0
	for _, e := range entries {
		if e["build"] == true {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", %d build-visible", n)
}

// applyDomains attaches custom domains, only with --attach-domains: a live
// production domain attached during an import would take its traffic and its
// certificate from Railway before anything here is ready. Without the flag
// each domain is listed with the command that attaches it later. A route that
// is not live falls back to the same command.
func (a *applier) applyDomains() error {
	live := true
	for _, act := range a.plan.Actions {
		if act.Op != "domain.custom" {
			continue
		}
		if !a.f.attachDomains {
			a.skippedDomains = append(a.skippedDomains, act.Domains...)
			a.domainByHand(act)
			continue
		}
		serviceID := a.services[serviceKey(act.Env, act.Service)]
		if serviceID == "" || !live {
			a.domainByHand(act)
			continue
		}
		err := a.api.Post(a.ctx, servicePath(a.projectID, serviceID)+"/domains", map[string]any{"domain": act.Domains[0]}, nil)
		if err != nil {
			live = false
			a.domainByHand(act)
			continue
		}
		a.p.Line("added custom domain %s to %s", act.Domains[0], act.Service+envSuffix(act.Env))
	}
	return nil
}

func (a *applier) domainByHand(act railway.Action) {
	for _, d := range act.Domains {
		a.byHand = append(a.byHand, fmt.Sprintf("miosa service domain enable %s %s --domain %s%s",
			shellArg(a.projectLabel()), shellArg(act.Service), d, targetFlag(act.Env)))
	}
}

// applyDeploy is --deploy: wait for the Forge copies, then queue one release
// per service that has a source.
func (a *applier) applyDeploy() error {
	for key, importID := range a.imports {
		if err := waitForImport(a.ctx, a.api, importID, 10*time.Minute); err != nil {
			env, name, _ := strings.Cut(key, "|")
			return a.failed(fmt.Errorf("the GitHub copy for %s%s is not ready: %w", name, envSuffix(env), err))
		}
	}
	for _, act := range a.plan.Actions {
		if act.Op != "service.create" || act.Source == nil {
			continue
		}
		serviceID := a.services[serviceKey(act.Env, act.Name)]
		if serviceID == "" {
			continue
		}
		body := map[string]any{"message": "imported from Railway"}
		if err := a.api.Post(a.ctx, servicePath(a.projectID, serviceID)+"/deployments", body, nil); err != nil {
			return a.failed(fmt.Errorf("deploying %s: %w", qualifiedName(act), err))
		}
		a.started = append(a.started, qualifiedName(act))
		a.p.Line("deploying %s", qualifiedName(act))
	}
	return nil
}

// waitForImport polls a Forge GitHub import until it has synced.
func waitForImport(ctx context.Context, c *api.Client, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var out struct {
			Data struct {
				Source struct {
					Status string `json:"status"`
					Error  string `json:"error"`
				} `json:"source"`
			} `json:"data"`
		}
		if err := c.Get(ctx, "/forge/github/imports/"+url.PathEscape(id), nil, &out); err != nil {
			return err
		}
		switch out.Data.Source.Status {
		case "synced":
			return nil
		case "error", "failed", "detached":
			return fmt.Errorf("import %s: %s", out.Data.Source.Status, out.Data.Source.Error)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("import still %s after %s", out.Data.Source.Status, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(importPollInterval):
		}
	}
}

// importPollInterval is how often --deploy checks a GitHub copy.
var importPollInterval = 3 * time.Second

// summary is the one screen a user reads after an apply: what exists, that
// nothing was started, what still needs a value, and the exact next commands.
func (a *applier) summary() {
	var services, databases, targets, needs []string
	type need struct {
		env, service, name string
		build              bool
	}
	var needList []need
	for _, act := range a.plan.Actions {
		switch act.Op {
		case "service.create":
			services = append(services, qualifiedName(act))
		case "database.create":
			databases = append(databases, act.Name)
		case "database.service":
			databases = append(databases, qualifiedName(act)+" (service)")
		case "target.create":
			if !containsString(targets, act.Name) {
				targets = append(targets, act.Name)
			}
		case "variable.set":
			if act.Variable != nil && !act.Variable.HasValue {
				needs = append(needs, act.Service+envSuffix(act.Env)+"."+act.Variable.Name)
				needList = append(needList, need{act.Env, act.Service, act.Variable.Name, act.Variable.Build})
			}
		case "shared.set":
			if act.Variable != nil && !act.Variable.HasValue {
				needs = append(needs, "shared"+envSuffix(act.Env)+"."+act.Variable.Name)
				needList = append(needList, need{act.Env, "", act.Variable.Name, act.Variable.Build})
			}
		}
	}
	p, project := a.p, shellArg(a.projectLabel())
	p.Line("")
	p.Line("Summary")
	if len(targets) > 0 {
		p.Line("  Deploy targets: %s", strings.Join(targets, ", "))
	}
	if len(services) > 0 {
		p.Line("  services:  %s", strings.Join(services, ", "))
	}
	if len(databases) > 0 {
		p.Line("  databases: %s", strings.Join(databases, ", "))
	}
	if len(a.started) > 0 {
		p.Line("  deploying (--deploy): %s", strings.Join(a.started, ", "))
	} else {
		p.Line("  nothing was started: every service is created and stopped")
	}
	for _, line := range a.regions {
		p.Line("  region %s", line)
	}
	if len(a.sourceless) > 0 {
		p.Line("  need a source upload: %s", strings.Join(a.sourceless, ", "))
	}
	if len(a.skippedDomains) > 0 {
		p.Line("  custom domains not attached (pass --attach-domains when DNS is ready to move): %s", strings.Join(a.skippedDomains, ", "))
	}
	if len(needs) > 0 {
		p.Line("  need a value (%d): %s", len(needs), strings.Join(needs, ", "))
	}
	p.Line("")
	p.Line("Next:")
	if len(needList) > 0 {
		p.Line("  set the new secrets (never reuse the Railway value):")
		for _, n := range needList {
			flags := targetFlag(n.env)
			if n.build {
				// As on Railway, the build sees it once it has a value.
				flags += " --build"
			}
			if n.service == "" {
				p.Line("    miosa project vars set %s %s=<new value>%s", project, n.name, flags)
				continue
			}
			p.Line("    miosa service vars set %s %s %s=<new value>%s", project, shellArg(n.service), n.name, flags)
		}
	}
	for _, act := range a.plan.Actions {
		if act.Op == "service.create" && act.Source == nil {
			p.Line("  miosa service deploy %s %s --dir <path to its code>%s", project, shellArg(act.Name), targetFlag(act.Env))
		}
	}
	for _, line := range a.byHand {
		p.Line("  %s", line)
	}
	if len(a.started) == 0 {
		for _, act := range a.plan.Actions {
			if act.Op == "service.create" && act.Source != nil {
				p.Line("  miosa service deploy %s %s%s", project, shellArg(act.Name), targetFlag(act.Env))
			}
		}
	}
	p.Line("  state: %s (undo with --cleanup --state %s)", a.path, a.path)
}

func needsRegion(plan railway.Plan) int {
	n := 0
	for _, a := range plan.Actions {
		if a.Op == "service.create" && a.NeedsRegion {
			n++
		}
	}
	return n
}

func applyFailed(statePath string, err error) error {
	fmt.Fprintf(os.Stderr, "stopped. What was created is recorded in %s; continue with --resume or undo with --cleanup.\n", statePath)
	return die(err)
}

func missingValues(plan railway.Plan) int {
	n := 0
	for _, a := range plan.Actions {
		if (a.Op == "variable.set" || a.Op == "shared.set") && a.Variable != nil && !a.Variable.HasValue {
			n++
		}
	}
	return n
}

func findProjectByName(ctx context.Context, c *api.Client, name string) (string, error) {
	var out struct {
		Data []serviceProject `json:"data"`
	}
	if err := c.Get(ctx, "/projects", nil, &out); err != nil {
		return "", die(err)
	}
	for _, p := range out.Data {
		if strings.EqualFold(p.Name, name) || strings.EqualFold(p.Slug, name) {
			return p.ID, nil
		}
	}
	return "", nil
}

func findDatabaseByName(ctx context.Context, c *api.Client, name string) (string, error) {
	var out struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/databases", nil, &out); err != nil {
		return "", die(err)
	}
	for _, d := range out.Data {
		if strings.EqualFold(d.Name, name) {
			return d.ID, nil
		}
	}
	return "", nil
}

// projectLabel is the MIOSA project the user names in a command: the --into
// project, else the one the plan created.
func (a *applier) projectLabel() string {
	if a.plan.Target.Into != "" {
		return a.plan.Target.Into
	}
	return a.plan.Target.ProjectName
}

// qualifiedName is a service name with its Deploy target, when it has one.
func qualifiedName(a railway.Action) string {
	return a.Name + envSuffix(a.Env)
}

func envSuffix(env string) string {
	if env == "" {
		return ""
	}
	return " (" + env + ")"
}

func envLabel(env string) string {
	if env == "" {
		return "the default Deploy target"
	}
	return "Deploy target " + env
}

func targetFlag(env string) string {
	if env == "" {
		return ""
	}
	return " --deploy-target " + shellArg(env)
}

func repoShortName(fullName string) string {
	if i := strings.LastIndexByte(fullName, '/'); i >= 0 {
		return fullName[i+1:]
	}
	return fullName
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
