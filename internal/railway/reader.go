package railway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Querier is the read transport: the token client or the Railway CLI session.
type Querier interface {
	Query(ctx context.Context, query string, variables map[string]any, out any) error
}

// Reader reads one Railway project. CopyVariables decides whether literal
// variable values are read; references (${{Service.NAME}}) are always kept,
// because a reference is not a secret and it is what makes an import portable.
type Reader struct {
	Client        Querier
	CopyVariables bool
}

// Read reads one environment. It is ReadEnvironments with one name.
func (r Reader) Read(ctx context.Context, projectRef, environment string) (Source, error) {
	return r.ReadEnvironments(ctx, projectRef, []string{environment})
}

// AllEnvironments, given as the environment name, reads every environment of
// the project.
const AllEnvironments = "all"

// ReadEnvironments resolves the project and the named environments and returns
// everything the plan needs: every service instance of every environment (each
// tagged with its environment), and each environment's shared variables.
// projectRef is an id, or a name when the token can see exactly one project with
// that name.
func (r Reader) ReadEnvironments(ctx context.Context, projectRef string, environments []string) (Source, error) {
	project, err := r.project(ctx, projectRef)
	if err != nil {
		return Source{}, err
	}
	envs, err := pickEnvironments(project, environments)
	if err != nil {
		return Source{}, err
	}

	src := Source{
		Kind:        "railway",
		ProjectID:   project.ID,
		ProjectName: project.Name,
		FetchedAt:   time.Now().UTC(),
	}
	for _, e := range project.Environments.nodes() {
		src.Environments = append(src.Environments, e.Name)
	}
	sort.Strings(src.Environments)

	names := make([]string, 0, len(envs))
	for _, env := range envs {
		names = append(names, env.Name)
		if err := r.readEnvironment(ctx, project.ID, env, &src); err != nil {
			return Source{}, err
		}
	}
	src.Environment = strings.Join(names, ",")
	sort.SliceStable(src.Services, func(i, j int) bool {
		a, b := src.Services[i], src.Services[j]
		if a.Environment != b.Environment {
			return a.Environment < b.Environment
		}
		return a.Name < b.Name
	})

	plugins, err := r.plugins(ctx, project.ID)
	if err != nil {
		return Source{}, err
	}
	src.Plugins = plugins
	return src, nil
}

// readEnvironment adds one environment's service instances and shared
// variables to src.
func (r Reader) readEnvironment(ctx context.Context, projectID string, env wireRef, src *Source) error {
	var envRead struct {
		Environment struct {
			ID               string                         `json:"id"`
			Name             string                         `json:"name"`
			ServiceInstances connection[wireInstance]       `json:"serviceInstances"`
			VolumeInstances  connection[wireVolumeInstance] `json:"volumeInstances"`
		} `json:"environment"`
	}
	if err := r.Client.Query(ctx, queryEnvironment, map[string]any{"id": env.ID}, &envRead); err != nil {
		return err
	}

	volumesByService := map[string][]VolumeRef{}
	for _, v := range envRead.Environment.VolumeInstances.nodes() {
		ref := VolumeRef{MountPath: v.MountPath, SizeMB: v.SizeMB}
		if v.ServiceID != "" {
			volumesByService[v.ServiceID] = append(volumesByService[v.ServiceID], ref)
		}
	}
	for _, inst := range envRead.Environment.ServiceInstances.nodes() {
		svc, err := r.service(ctx, projectID, env.ID, inst, volumesByService[inst.ServiceID])
		if err != nil {
			return err
		}
		svc.Environment = env.Name
		src.Services = append(src.Services, svc)
	}

	shared, err := r.variables(ctx, projectID, env.ID, "")
	if err != nil {
		return err
	}
	for _, v := range shared {
		src.Shared = append(src.Shared, SharedVariable{Environment: env.Name, Variable: v})
	}
	return nil
}

func (r Reader) service(ctx context.Context, projectID, environmentID string, inst wireInstance, volumes []VolumeRef) (Service, error) {
	svc := Service{
		Name:          inst.ServiceName,
		Repo:          inst.Source.Repo,
		Image:         inst.Source.Image,
		RootDirectory: inst.RootDirectory,
		Volumes:       volumes,
	}
	resolveSettings(&svc, inst)

	branch, err := r.branch(ctx, projectID, environmentID, inst.ServiceID)
	if err != nil {
		return Service{}, err
	}
	svc.Branch = branch

	domains, err := r.domains(ctx, projectID, environmentID, inst.ServiceID)
	if err != nil {
		return Service{}, err
	}
	svc.ServiceDomains = domains.ServiceDomains
	svc.CustomDomains = domains.CustomDomains
	svc.Port = domains.Port

	vars, err := r.variables(ctx, projectID, environmentID, inst.ServiceID)
	if err != nil {
		return Service{}, err
	}
	svc.Variables = vars
	return svc, nil
}

func (r Reader) variables(ctx context.Context, projectID, environmentID, serviceID string) ([]Variable, error) {
	var out struct {
		Variables map[string]string `json:"variables"`
	}
	vars := map[string]any{"projectId": projectID, "environmentId": environmentID}
	if serviceID != "" {
		vars["serviceId"] = serviceID
	}
	// variables() answers a JSON scalar; decoding it into a map keeps the order
	// problem in one place, below.
	if err := r.Client.Query(ctx, queryVariables, vars, &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Variables))
	for name := range out.Variables {
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]Variable, 0, len(names))
	for _, name := range names {
		if IsRailwayBuiltin(name) {
			// Railway injects these itself; MIOSA injects its own (MIOSA_*).
			continue
		}
		value := out.Variables[name]
		sealed := value == "" || value == "*****"
		isReference := strings.Contains(value, "${{")
		keep := r.CopyVariables || isReference
		v := Variable{Name: name}
		if keep && !sealed {
			v.Value = value
			v.HasValue = true
		}
		result = append(result, v)
	}
	return result, nil
}

type domainLists struct {
	ServiceDomains []string
	CustomDomains  []string
	// Port is the target port of the service's domains: Railway's own domain
	// first, else a custom domain's.
	Port int
}

func (r Reader) domains(ctx context.Context, projectID, environmentID, serviceID string) (domainLists, error) {
	var out struct {
		Domains wireDomains `json:"domains"`
	}
	vars := map[string]any{"projectId": projectID, "environmentId": environmentID, "serviceId": serviceID}
	if err := r.Client.Query(ctx, queryDomainsFull, vars, &out); err != nil {
		return domainLists{}, err
	}
	var lists domainLists
	for _, d := range out.Domains.ServiceDomains {
		lists.ServiceDomains = append(lists.ServiceDomains, d.Domain)
		if lists.Port == 0 && d.TargetPort != nil {
			lists.Port = *d.TargetPort
		}
	}
	for _, d := range out.Domains.CustomDomains {
		lists.CustomDomains = append(lists.CustomDomains, d.Domain)
		if lists.Port == 0 && d.TargetPort != nil {
			lists.Port = *d.TargetPort
		}
	}
	sort.Strings(lists.ServiceDomains)
	sort.Strings(lists.CustomDomains)
	return lists, nil
}

func (r Reader) branch(ctx context.Context, projectID, environmentID, serviceID string) (string, error) {
	var out struct {
		DeploymentTriggers connection[wireTrigger] `json:"deploymentTriggers"`
	}
	vars := map[string]any{"projectId": projectID, "environmentId": environmentID, "serviceId": serviceID}
	if err := r.Client.Query(ctx, queryTriggers, vars, &out); err != nil {
		return "", err
	}
	triggers := out.DeploymentTriggers.nodes()
	if len(triggers) == 0 {
		return "", nil
	}
	sort.Slice(triggers, func(i, j int) bool { return triggers[i].Branch < triggers[j].Branch })
	return triggers[0].Branch, nil
}

// plugins reads the legacy plugin list. Railway removed plugins from the
// current model, so a failure here is not an import failure: the plan
// classifies services instead.
func (r Reader) plugins(ctx context.Context, projectID string) ([]PluginRef, error) {
	var out struct {
		Project wireProject `json:"project"`
	}
	if err := r.Client.Query(ctx, queryPlugins, map[string]any{"id": projectID}, &out); err != nil {
		return nil, nil
	}
	var refs []PluginRef
	for _, p := range out.Project.Plugins.nodes() {
		refs = append(refs, PluginRef{Name: strings.ToLower(p.Name), FriendlyName: p.FriendlyName})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].FriendlyName < refs[j].FriendlyName })
	return refs, nil
}

func (r Reader) project(ctx context.Context, ref string) (wireProject, error) {
	if looksLikeID(ref) {
		var out struct {
			Project wireProject `json:"project"`
		}
		if err := r.Client.Query(ctx, queryProject, map[string]any{"id": ref}, &out); err != nil {
			return wireProject{}, err
		}
		if out.Project.ID == "" {
			return wireProject{}, fmt.Errorf("%w: no Railway project with id %s", ErrNotFound, ref)
		}
		return out.Project, nil
	}

	projects, err := r.visibleProjects(ctx)
	if err != nil {
		return wireProject{}, fmt.Errorf("%w; pass the project id instead: --project <id> (Railway shows it in the project's Settings)", err)
	}
	var matches []wireRef
	for _, p := range projects {
		if strings.EqualFold(p.Name, ref) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return wireProject{}, fmt.Errorf("%w: no Railway project named %q that this login can see; pass the project id: --project <id>", ErrNotFound, ref)
	case 1:
	default:
		return wireProject{}, fmt.Errorf("more than one Railway project is named %q; pass the project id", ref)
	}

	var one struct {
		Project wireProject `json:"project"`
	}
	if err := r.Client.Query(ctx, queryProject, map[string]any{"id": matches[0].ID}, &one); err != nil {
		return wireProject{}, err
	}
	return one.Project, nil
}

// visibleProjects lists every project the login can see: through its
// workspaces first (what a workspace or account login may read), else through
// the top-level projects query. When both fail it answers Railway's messages.
func (r Reader) visibleProjects(ctx context.Context) ([]wireRef, error) {
	var me struct {
		Me struct {
			Workspaces []wireRef `json:"workspaces"`
		} `json:"me"`
	}
	workspacesErr := r.Client.Query(ctx, queryWorkspaces, nil, &me)
	if workspacesErr == nil && len(me.Me.Workspaces) > 0 {
		var projects []wireRef
		for _, w := range me.Me.Workspaces {
			var out struct {
				Workspace struct {
					Projects connection[wireRef] `json:"projects"`
				} `json:"workspace"`
			}
			if err := r.Client.Query(ctx, queryWorkspaceProjects, map[string]any{"id": w.ID}, &out); err != nil {
				return nil, err
			}
			projects = append(projects, out.Workspace.Projects.nodes()...)
		}
		return projects, nil
	}
	var out struct {
		Projects connection[wireRef] `json:"projects"`
	}
	if err := r.Client.Query(ctx, queryProjects, nil, &out); err != nil {
		if workspacesErr != nil {
			return nil, fmt.Errorf("Railway would not list projects (%v; %v)", workspacesErr, err)
		}
		return nil, fmt.Errorf("Railway would not list projects (%v)", err)
	}
	return out.Projects.nodes(), nil
}

// pickEnvironments resolves the requested names, "all" for every environment.
// With no name, or "production" in a project that has no production but only
// one environment, it is that one environment.
func pickEnvironments(project wireProject, names []string) ([]wireRef, error) {
	envs := project.Environments.nodes()
	var cleaned []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			cleaned = append(cleaned, n)
		}
	}
	if len(cleaned) == 0 {
		cleaned = []string{"production"}
	}
	for _, n := range cleaned {
		if strings.EqualFold(n, AllEnvironments) {
			if len(envs) == 0 {
				return nil, fmt.Errorf("%s has no environments", project.Name)
			}
			sorted := append([]wireRef(nil), envs...)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
			return sorted, nil
		}
	}
	var picked []wireRef
	seen := map[string]bool{}
	for _, n := range cleaned {
		env, err := pickEnvironment(project, n, len(cleaned) == 1)
		if err != nil {
			return nil, err
		}
		if !seen[env.ID] {
			seen[env.ID] = true
			picked = append(picked, env)
		}
	}
	return picked, nil
}

func pickEnvironment(project wireProject, name string, onlyOne bool) (wireRef, error) {
	envs := project.Environments.nodes()
	var names []string
	for _, e := range envs {
		if strings.EqualFold(e.Name, name) {
			return e, nil
		}
		names = append(names, e.Name)
	}
	if onlyOne && len(envs) == 1 {
		return envs[0], nil
	}
	sort.Strings(names)
	return wireRef{}, fmt.Errorf("no Railway environment named %q in %s (it has %s; --environment all reads every one)",
		name, project.Name, strings.Join(names, ", "))
}

func looksLikeID(ref string) bool {
	if len(ref) != 36 {
		return false
	}
	for i, c := range ref {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
