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

// Read resolves the project and environment and returns everything the plan
// needs. projectRef is an id, or a name when the token can see exactly one
// project with that name.
func (r Reader) Read(ctx context.Context, projectRef, environment string) (Source, error) {
	project, err := r.project(ctx, projectRef)
	if err != nil {
		return Source{}, err
	}
	env, err := pickEnvironment(project, environment)
	if err != nil {
		return Source{}, err
	}

	var envRead struct {
		Environment struct {
			ID               string                         `json:"id"`
			Name             string                         `json:"name"`
			ServiceInstances connection[wireInstance]       `json:"serviceInstances"`
			VolumeInstances  connection[wireVolumeInstance] `json:"volumeInstances"`
		} `json:"environment"`
	}
	vars := map[string]any{"id": env.ID}
	if err := r.Client.Query(ctx, queryEnvironment, vars, &envRead); err != nil {
		return Source{}, err
	}

	volumesByService := map[string][]VolumeRef{}
	for _, v := range envRead.Environment.VolumeInstances.nodes() {
		ref := VolumeRef{MountPath: v.MountPath, SizeMB: v.SizeMB}
		if v.ServiceID != "" {
			volumesByService[v.ServiceID] = append(volumesByService[v.ServiceID], ref)
		}
	}

	src := Source{
		Kind:        "railway",
		ProjectID:   project.ID,
		ProjectName: project.Name,
		Environment: env.Name,
		FetchedAt:   time.Now().UTC(),
	}
	for _, e := range project.Environments.nodes() {
		src.Environments = append(src.Environments, e.Name)
	}
	sort.Strings(src.Environments)

	for _, inst := range envRead.Environment.ServiceInstances.nodes() {
		svc, err := r.service(ctx, project.ID, env.ID, inst, volumesByService[inst.ServiceID])
		if err != nil {
			return Source{}, err
		}
		src.Services = append(src.Services, svc)
	}
	sort.Slice(src.Services, func(i, j int) bool { return src.Services[i].Name < src.Services[j].Name })

	plugins, err := r.plugins(ctx, project.ID)
	if err != nil {
		return Source{}, err
	}
	src.Plugins = plugins
	return src, nil
}

func (r Reader) service(ctx context.Context, projectID, environmentID string, inst wireInstance, volumes []VolumeRef) (Service, error) {
	svc := Service{
		Name:               inst.ServiceName,
		Repo:               inst.Source.Repo,
		Image:              inst.Source.Image,
		RootDirectory:      inst.RootDirectory,
		DockerfilePath:     inst.DockerfilePath,
		StartCommand:       inst.StartCommand,
		BuildCommand:       inst.BuildCommand,
		HealthcheckPath:    inst.HealthcheckPath,
		RestartPolicy:      inst.RestartPolicyType,
		Region:             inst.Region,
		CronSchedule:       inst.CronSchedule,
		Volumes:            volumes,
		HealthcheckTimeout: derefInt(inst.HealthcheckTimeout),
		NumReplicas:        derefInt(inst.NumReplicas),
	}

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
	vars := map[string]any{"projectId": projectID, "environmentId": environmentID, "serviceId": serviceID}
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
	}
	for _, d := range out.Domains.CustomDomains {
		lists.CustomDomains = append(lists.CustomDomains, d.Domain)
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

	var out struct {
		Projects connection[wireRef] `json:"projects"`
	}
	if err := r.Client.Query(ctx, queryProjects, nil, &out); err != nil {
		return wireProject{}, err
	}
	var matches []wireRef
	for _, p := range out.Projects.nodes() {
		if strings.EqualFold(p.Name, ref) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return wireProject{}, fmt.Errorf("%w: no Railway project named %q", ErrNotFound, ref)
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

func pickEnvironment(project wireProject, name string) (wireRef, error) {
	envs := project.Environments.nodes()
	if name == "" {
		name = "production"
	}
	var names []string
	for _, e := range envs {
		if strings.EqualFold(e.Name, name) {
			return e, nil
		}
		names = append(names, e.Name)
	}
	if len(envs) == 1 {
		return envs[0], nil
	}
	sort.Strings(names)
	return wireRef{}, fmt.Errorf("no Railway environment named %q in %s (it has %s)", name, project.Name, strings.Join(names, ", "))
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

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
