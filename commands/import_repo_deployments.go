package commands

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
)

// Where Services cannot build a repository yet (serviceRepoBuilds), an imported
// repository service becomes a deployment in the project instead: the path
// + Add > GitHub repository and 'miosa deploy create --git' take, which builds
// and runs it today. A repository service created through Services would sit
// queued until its deadline.

// repoDeploymentOp records a deployment made for a repository service, so
// --resume skips it and --cleanup deletes it.
const repoDeploymentOp = "deployment.create"

// importedRepo is what the applier did with one repository service.
type importedRepo struct {
	deployments map[string]string // service name -> deployment id
	skipped     []string          // "name: why"
	byHand      []string          // commands the user still has to run
}

func newImportedRepo() *importedRepo {
	return &importedRepo{deployments: map[string]string{}}
}

// goesToDeployment reports whether a service.create becomes a deployment.
func goesToDeployment(a railway.Action, repoServices bool) bool {
	return !repoServices && a.Source != nil && (a.Source.Type == "git" || a.Source.Type == "forge")
}

// deploymentBlocker says why a repository service cannot become a deployment
// without changing what it runs, or "" when it can.
func deploymentBlocker(a railway.Action) string {
	switch {
	case a.Source.Type == "forge":
		return "a Forge source imports as a service only, which cannot build yet; deploy it with 'miosa deploy create --forge'"
	case a.Kind != "server":
		return fmt.Sprintf("a %s runs as a service only, and services cannot build from a repository yet", a.Kind)
	case a.Source.RootDir != "" && a.Source.RootDir != "." && a.Source.RootDir != "/":
		return fmt.Sprintf("it builds from %s inside the repository, and a deployment builds the repository root", a.Source.RootDir)
	}
	return ""
}

// droppedSettings lists what a deployment does not carry from the service.
func droppedSettings(a railway.Action) []string {
	var dropped []string
	if a.Build != nil && a.Build.Dockerfile != "" && a.Build.Dockerfile != "Dockerfile" {
		dropped = append(dropped, "Dockerfile path "+a.Build.Dockerfile)
	}
	if s := a.Settings; s != nil {
		if s.Replicas > 1 {
			dropped = append(dropped, fmt.Sprintf("%d replicas", s.Replicas))
		}
		if s.HealthcheckPath != "" {
			dropped = append(dropped, "healthcheck "+s.HealthcheckPath)
		}
		if s.PreDeployCommand != "" {
			dropped = append(dropped, "pre-deploy command")
		}
		if s.RestartPolicy != "" {
			dropped = append(dropped, "restart policy "+s.RestartPolicy)
		}
	}
	return dropped
}

var wholeReference = regexp.MustCompile(`^\$\{\{\s*([^.}\s]+)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}$`)

// splitReference reads a value that is one whole ${{Service.KEY}} reference.
func splitReference(value string) (service, key string, ok bool) {
	m := wholeReference.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// deploymentEnv splits a service's variables into literal values for the
// deployment, database connection variables to attach, and references a
// deployment cannot resolve.
func deploymentEnv(vars map[string]any, databases map[string]bool) (env map[string]any, attach map[string]string, unresolved []string) {
	env, attach = map[string]any{}, map[string]string{}
	for name, raw := range vars {
		value, _ := raw.(string)
		if !strings.Contains(value, "${{") {
			env[name] = value
			continue
		}
		if service, _, ok := splitReference(value); ok && databases[strings.ToLower(service)] {
			attach[name] = service
			continue
		}
		unresolved = append(unresolved, name)
	}
	return env, attach, unresolved
}

// createRepoDeployment makes the deployment for one repository service and
// attaches the databases its variables point at. It returns the deployment id.
func createRepoDeployment(ctx context.Context, c *client.Client, p printerLike, projectID, workspaceID string, a railway.Action, env map[string]any) (string, error) {
	if id, found, ok, err := existingDeploymentByName(ctx, c, a.Name); err != nil {
		return "", err
	} else if ok {
		return "", usagef("a deployment named %q already exists (%s); import with --suffix, or rename the service in the plan", found, shortID(id))
	}
	body := map[string]any{
		"name":        a.Name,
		"repo_url":    a.Source.Ref,
		"auto_deploy": true,
		"project_id":  projectID,
	}
	if a.Source.Branch != "" {
		body["branch"] = a.Source.Branch
	}
	if a.Settings != nil && a.Settings.StartCommand != "" {
		body["run_command"] = a.Settings.StartCommand
	}
	if workspaceID != "" {
		body["workspace_id"] = workspaceID
	}
	if len(env) > 0 {
		body["env"] = env
	}
	var resp createDeployResponse
	if err := c.API.Post(ctx, "/deployments", body, &resp); err != nil {
		return "", err
	}
	if resp.Data.ID == "" {
		return "", fmt.Errorf("the server created the deployment for %s but returned no id", a.Name)
	}
	// The server builds a connected repository on create; one it left pending
	// needs the first build queued, as 'miosa deploy create --git' does.
	if resp.Data.State == "" || resp.Data.State == "pending" {
		if err := c.API.Post(ctx, "/deployments/"+url.PathEscape(resp.Data.ID)+"/redeploy", nil, nil); err != nil {
			p.Line("warning: %s was created but its first build did not start: %v", a.Name, err)
		}
	}
	return resp.Data.ID, nil
}

// importedDatabaseID finds the managed database a plan database stands for:
// the id recorded for a database.create, or, for a database.service, the
// database the apply made for it in this project (named after the service).
func importedDatabaseID(ctx context.Context, c *api.Client, created map[string]string, projectID, name string) (string, error) {
	if id := created["database.create|"+name]; id != "" {
		return id, nil
	}
	var out struct {
		Data []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			ProjectID string `json:"project_id"`
		} `json:"data"`
	}
	if err := c.Get(ctx, "/databases", nil, &out); err != nil {
		return "", err
	}
	slug := serviceSlug(name)
	for _, d := range out.Data {
		if (d.ProjectID == "" || d.ProjectID == projectID) && (strings.EqualFold(d.Name, slug) || strings.EqualFold(d.Name, name)) {
			return d.ID, nil
		}
	}
	return "", nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// serviceSlug is the database name an apply gives a database service.
func serviceSlug(name string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

// attachDatabase points one connection variable of a deployment at a database.
func attachDatabase(ctx context.Context, c *api.Client, deploymentID, databaseID, envName string) error {
	body := map[string]any{"database_id": databaseID, "env": envName}
	return c.Post(ctx, "/deployments/"+url.PathEscape(deploymentID)+"/database", body, nil)
}

// planDatabases is every database a plan makes, by lower-case name.
func planDatabases(plan railway.Plan) map[string]bool {
	names := map[string]bool{}
	for _, a := range plan.Actions {
		if a.Op == "database.create" || a.Op == "database.service" {
			names[strings.ToLower(a.Name)] = true
		}
	}
	return names
}

// planName finds the plan's own spelling of a database name.
func planName(plan railway.Plan, lower string) string {
	for _, a := range plan.Actions {
		if (a.Op == "database.create" || a.Op == "database.service") && strings.ToLower(a.Name) == lower {
			return a.Name
		}
	}
	return lower
}

func (r *importedRepo) wasSkipped(name string) bool {
	for _, why := range r.skipped {
		if strings.HasPrefix(why, name+": ") {
			return true
		}
	}
	return false
}

// applyRepoDeployment turns one repository service.create into a deployment in
// the project: created once (resume skips it), with its literal variables, its
// database connection variables attached, and what it cannot carry said.
func applyRepoDeployment(ctx context.Context, c *client.Client, p printerLike, plan railway.Plan, projectID string, a railway.Action,
	vars map[string]any, created map[string]string, repos *importedRepo, record func(op, name, id string) error) error {
	if id := created[repoDeploymentOp+"|"+a.Name]; id != "" {
		repos.deployments[a.Name] = id
		p.Line("deployment %s already created (%s)", a.Name, shortID(id))
		return nil
	}
	if why := deploymentBlocker(a); why != "" {
		repos.skipped = append(repos.skipped, a.Name+": "+why)
		p.Line("service %s not created: %s", a.Name, why)
		return nil
	}

	env, attach, unresolved := deploymentEnv(vars, planDatabases(plan))
	id, err := createRepoDeployment(ctx, c, p, projectID, plan.Target.WorkspaceID, a, env)
	if err != nil {
		return err
	}
	repos.deployments[a.Name] = id
	p.Line("created deployment %s from %s (%s); services cannot build a repository yet, so it deploys the way + Add > GitHub repository does", a.Name, a.Source.Ref, shortID(id))
	if err := record(repoDeploymentOp, a.Name, id); err != nil {
		return die(err)
	}

	names := make([]string, 0, len(attach))
	for envName := range attach {
		names = append(names, envName)
	}
	sort.Strings(names)
	sort.Strings(unresolved)
	for _, envName := range names {
		name := planName(plan, strings.ToLower(attach[envName]))
		dbID, err := importedDatabaseID(ctx, c.API, created, projectID, name)
		if err == nil && dbID == "" {
			err = fmt.Errorf("its database was not found")
		}
		if err == nil {
			err = attachDatabase(ctx, c.API, id, dbID, envName)
		}
		if err != nil {
			unresolved = append(unresolved, envName)
			continue
		}
		p.Line("  %s connects to %s", envName, name)
	}
	for _, envName := range unresolved {
		repos.byHand = append(repos.byHand, fmt.Sprintf("miosa deploy env-set %s --set %s=<value>   (was %v on Railway)", shellArg(a.Name), envName, vars[envName]))
	}
	if dropped := droppedSettings(a); len(dropped) > 0 {
		p.Line("  not carried to the deployment: %s", strings.Join(dropped, ", "))
	}
	return nil
}
