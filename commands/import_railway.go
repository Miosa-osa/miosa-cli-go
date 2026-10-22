package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
)

// railwayImportCmd is `miosa import railway`: read a Railway project and create
// the same services and databases here, after a review step.
func railwayImportCmd() *cobra.Command {
	f := &railwayImportFlags{}
	cmd := &cobra.Command{
		Use:   "railway",
		Short: "Import a Railway project (dry run unless --apply)",
		Long: `Read one Railway project through Railway's public API with your own token and
write a plan: the MIOSA project, Deploy targets, services, databases and variable
names it would create, and everything it cannot carry (build commands, health
checks, volumes, Railway domains, custom domains).

  miosa import railway --project acme-api                      # review the plan
  miosa import railway --project acme-api --out plan.json      # keep it
  miosa import railway --apply --plan plan.json                # create it

The token is read from RAILWAY_TOKEN, --token-file, or a prompt. It is never
stored. Variable values are not copied unless you pass --copy-variables, which
prints a warning: Railway disclosed on 2026-10-21 that its secrets were exposed, so rotate them.

With --from-file, a railway.json or railway.toml is read locally and no token
or network call is needed.

RAILWAY_API_URL overrides the Railway endpoint (for a proxy or recorded
fixtures).

Guide: https://miosa.ai/docs/migrate/railway`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRailwayImport(cmd, f)
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&f.project, "project", "", "Railway project id or name")
	fs.StringVar(&f.environment, "environment", "production", "Railway environment to import")
	fs.StringVar(&f.fromFile, "from-file", "", "Read railway.json or railway.toml instead of the API")
	fs.StringVar(&f.serviceName, "service-name", "", "With --from-file: the service name")
	fs.StringVar(&f.workspace, "workspace", "", "MIOSA workspace (slug, name or id)")
	fs.StringVar(&f.name, "name", "", "MIOSA project name (default: the Railway project name)")
	fs.StringVar(&f.into, "into", "", "Import into an existing MIOSA project")
	fs.BoolVar(&f.includeTargets, "include-targets", false, "Also import other Railway environments as Deploy targets")
	fs.BoolVar(&f.copyVars, "copy-variables", false, "Copy variable values, not just names (prints a warning)")
	fs.StringVar(&f.out, "out", "", "Write the plan JSON here")
	fs.StringVar(&f.planPath, "plan", "", "With --apply: the plan to apply")
	fs.BoolVar(&f.apply, "apply", false, "Create what the plan says")
	fs.StringVar(&f.state, "state", "", "Where created ids are recorded (default .miosa/import-railway-<project>.json)")
	fs.BoolVar(&f.resume, "resume", false, "Continue an interrupted apply")
	fs.BoolVar(&f.cleanup, "cleanup", false, "Delete exactly the ids recorded in --state")
	fs.BoolVar(&f.allowMissing, "allow-missing-values", false, "Create variables with no value as empty strings")
	fs.StringVar(&f.token, "token", "", "Railway token (prefer RAILWAY_TOKEN or --token-file)")
	fs.StringVar(&f.tokenFile, "token-file", "", "File holding the Railway token")
	fs.StringSliceVar(&f.only, "only", nil, "Import only these services (comma separated)")
	fs.StringSliceVar(&f.exclude, "exclude", nil, "Leave these services out (comma separated)")
	fs.StringVar(&f.suffix, "suffix", "", "Append this to every service name and insert it before each custom domain's first dot")
	fs.StringVar(&f.via, "via", "", "How to read Railway: token (default) or railway-cli (its logged-in session)")
	fs.StringVar(&f.region, "region", "", "With --apply: MIOSA region for services whose Railway region has no equivalent")
	fs.StringVar(&f.dumpDir, "dump-graphql", "", "Write every Railway exchange to this directory (fixtures)")
	fs.BoolVar(&f.yes, "yes", false, "With --cleanup: do not ask")
	_ = fs.MarkHidden("dump-graphql")
	return cmd
}

type railwayImportFlags struct {
	project        string
	environment    string
	fromFile       string
	serviceName    string
	workspace      string
	name           string
	into           string
	includeTargets bool
	copyVars       bool
	out            string
	planPath       string
	apply          bool
	state          string
	resume         bool
	cleanup        bool
	allowMissing   bool
	token          string
	tokenFile      string
	dumpDir        string
	only           []string
	exclude        []string
	suffix         string
	via            string
	region         string
	yes            bool
}

func runRailwayImport(cmd *cobra.Command, f *railwayImportFlags) error {
	ctx := cmd.Context()
	switch {
	case f.cleanup:
		return railwayCleanup(ctx, cmd, f)
	case f.apply:
		if f.planPath == "" {
			return usagef("--apply needs --plan <file>: review the plan first, then apply it")
		}
		return railwayApply(ctx, cmd, f)
	}
	if f.project == "" && f.fromFile == "" {
		return usagef("name a project: --project <id or name>, or --from-file <railway.json|railway.toml>")
	}
	if f.project != "" && f.fromFile != "" {
		return usagef("give --project or --from-file, not both")
	}

	plan, err := buildImportPlan(ctx, f)
	if err != nil {
		return err
	}
	dest := f.out
	if dest == "" {
		dest = f.planPath
	}
	if dest != "" {
		if err := writePlan(dest, plan); err != nil {
			return die(err)
		}
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(plan)
	}
	renderPlan(p, plan, dest)
	return nil
}

// buildImportPlan reads the source and turns it into a plan.
func buildImportPlan(ctx context.Context, f *railwayImportFlags) (railway.Plan, error) {
	opts := railway.Options{
		ProjectName:    f.name,
		WorkspaceID:    f.workspace,
		Into:           f.into,
		IncludeTargets: f.includeTargets,
		CopyVariables:  f.copyVars,
		Only:           f.only,
		Exclude:        f.exclude,
		Suffix:         f.suffix,
	}
	if f.fromFile != "" {
		cfg, err := railway.ReadFile(f.fromFile)
		if err != nil {
			return railway.Plan{}, die(err)
		}
		return railway.BuildFile(cfg, f.serviceName, opts), nil
	}

	reader := railway.Reader{CopyVariables: f.copyVars}
	if f.via == "railway-cli" {
		// The Railway CLI's own session: no token enters this process.
		reader.Client = &railway.CLIClient{DumpDir: f.dumpDir}
	} else {
		token, err := railwayToken(f)
		if err != nil {
			return railway.Plan{}, err
		}
		client := railway.NewClient(token)
		client.DumpDir = f.dumpDir
		if endpoint := strings.TrimSpace(os.Getenv("RAILWAY_API_URL")); endpoint != "" {
			client.Endpoint = endpoint
		}
		reader.Client = client
	}
	src, err := reader.Read(ctx, f.project, f.environment)
	if err != nil {
		return railway.Plan{}, die(err)
	}
	plan, err := railway.Build(src, opts)
	if err != nil {
		return railway.Plan{}, die(err)
	}
	return plan, nil
}

// railwayToken resolves the Railway token without ever storing it. A token on
// the command line is allowed but warned about, because the process list is
// readable by other users on the machine.
func railwayToken(f *railwayImportFlags) (string, error) {
	if f.tokenFile != "" {
		body, err := os.ReadFile(f.tokenFile)
		if err != nil {
			return "", die(err)
		}
		token := strings.TrimSpace(string(body))
		if token == "" {
			return "", usagef("%s is empty", f.tokenFile)
		}
		return token, nil
	}
	if f.token != "" {
		warnOnce("railway-token-argv", "warning: --token is visible in the process list; prefer RAILWAY_TOKEN or --token-file")
		return f.token, nil
	}
	if token := strings.TrimSpace(os.Getenv("RAILWAY_TOKEN")); token != "" {
		return token, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", usagef("set RAILWAY_TOKEN or pass --token-file")
	}
	fmt.Fprint(os.Stderr, "Railway token: ")
	body, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", die(err)
	}
	token := strings.TrimSpace(string(body))
	if token == "" {
		return "", usagef("no token given")
	}
	return token, nil
}

func writePlan(path string, plan railway.Plan) error {
	body, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func readPlan(path string) (railway.Plan, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return railway.Plan{}, err
	}
	var plan railway.Plan
	if err := json.Unmarshal(body, &plan); err != nil {
		return railway.Plan{}, fmt.Errorf("%s is not a plan: %w", path, err)
	}
	if plan.Version != railway.PlanVersion {
		return railway.Plan{}, fmt.Errorf("%s is a version %d plan; this CLI applies version %d", path, plan.Version, railway.PlanVersion)
	}
	return plan, nil
}

// renderPlan is the review: what will be created, what needs a value, and what a
// person has to finish by hand.
func renderPlan(p printerLike, plan railway.Plan, dest string) {
	where := plan.Target.ProjectName
	if plan.Target.Into != "" {
		where = plan.Target.Into
	}
	from := plan.Source.ProjectName
	if plan.Source.Kind == "file" {
		from = "config file"
	}
	p.Line("%s (%s) -> MIOSA project %s", from, plan.Source.Environment, where)
	p.Line("")

	creates := 0
	var variables, needsValue []string
	var manual [][2]string
	for _, a := range plan.Actions {
		switch a.Op {
		case "project.create":
			p.Line("  create project %s", a.Name)
			creates++
		case "target.create":
			p.Line("  create Deploy target %s", a.Name)
			creates++
		case "database.create":
			p.Line("  create database %s (%s)", a.Name, a.Engine)
			creates++
		case "database.service":
			p.Line("  create database service %s (%s, MIOSA provisions the database)", a.Name, a.Engine)
			creates++
		case "service.create":
			line := fmt.Sprintf("  create %s %s", a.Kind, a.Name)
			if a.Source != nil {
				line += " from " + a.Source.Ref
				if a.Source.Branch != "" {
					line += " (" + a.Source.Branch + ")"
				}
			}
			if a.Build != nil && a.Build.Dockerfile != "" {
				line += " with " + a.Build.Dockerfile
			}
			p.Line("%s", line)
			creates++
		case "variable.set":
			if a.Variable == nil {
				continue
			}
			if a.Variable.HasValue {
				kind := "value"
				if strings.Contains(a.Variable.Value, "${{") {
					kind = "reference, kept"
				}
				variables = append(variables, fmt.Sprintf("%s.%s (%s)", a.Service, a.Variable.Name, kind))
			} else {
				needsValue = append(needsValue, fmt.Sprintf("%s.%s", a.Service, a.Variable.Name))
			}
		case "domain.report":
			if len(a.Domains) > 0 {
				manual = append(manual, [2]string{
					a.Service + " address",
					fmt.Sprintf("%s was %s; MIOSA assigns a new <label>.<org>.miosa.app", a.Service, strings.Join(a.Domains, ", ")),
				})
			}
		case "domain.custom":
			for _, d := range a.Domains {
				manual = append(manual, [2]string{
					a.Service + " custom domain " + d,
					"added at apply when the custom-domain API is live, else: miosa service domain enable <project> " + a.Service + " --domain " + d,
				})
			}
		case "unmapped":
			next := a.Next
			if next == "" || next == "none" {
				next = "no action needed"
			}
			manual = append(manual, [2]string{a.Name + " " + a.Field, a.Why + " -> " + next})
		}
	}
	if creates == 0 {
		p.Line("  (nothing to create)")
	}
	if len(variables) > 0 {
		p.Line("")
		p.Line("Variables carried:")
		for _, v := range variables {
			p.Line("  %s", v)
		}
	}
	if len(needsValue) > 0 {
		p.Line("")
		p.Line("Variables that need a value (%d):", len(needsValue))
		for _, v := range needsValue {
			p.Line("  %s", v)
		}
	}
	if len(manual) > 0 {
		p.Line("")
		p.Line("Finish by hand:")
		for _, m := range manual {
			p.Line("  %s: %s", m[0], m[1])
		}
	}
	p.Line("")
	for _, w := range plan.Warnings {
		p.Line("warning: %s", w)
	}
	p.Line("")
	if dest != "" {
		p.Line("Nothing was created. Plan written to %s.", dest)
		p.Line("Review it, then: miosa import railway --apply --plan %s", dest)
		return
	}
	p.Line("Nothing was created. Re-run with --out plan.json, review it, then --apply --plan plan.json.")
}

// warnOnce prints a warning once per process key.
func warnOnce(key, message string) {
	if warned[key] {
		return
	}
	warned[key] = true
	fmt.Fprintln(os.Stderr, message)
}

var warned = map[string]bool{}

// ── apply ────────────────────────────────────────────────────────────────────

type createdRef struct {
	Op   string `json:"op"`
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

func railwayApply(ctx context.Context, cmd *cobra.Command, f *railwayImportFlags) error {
	plan, err := readPlan(f.planPath)
	if err != nil {
		return die(err)
	}
	if !f.allowMissing {
		if n := missingValues(plan); n > 0 {
			return usagef("%d variable(s) have no value; fill them into the plan or pass --allow-missing-values to create them empty", n)
		}
	}
	if needsRegion(plan) > 0 && f.region == "" {
		return usagef("%d service(s) come from a Railway region MIOSA does not have; pass --region us-east, us-west or us-mia", needsRegion(plan))
	}

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	path := statePath(f, plan)
	state, err := loadState(path)
	if err != nil {
		return die(err)
	}
	if state.Source == "" {
		state.Source = plan.Source.Digest
	}
	if state.Source != plan.Source.Digest {
		return usagef("%s was started from a different plan; use a new --state file or --cleanup first", path)
	}
	if f.resume && len(state.Created) == 0 {
		warnOnce("resume", "warning: nothing recorded in the state file yet; applying from the start")
	}

	p := printerFor(cmd)
	created := map[string]string{} // op|name -> id
	for _, ref := range state.Created {
		created[ref.Op+"|"+ref.Name] = ref.ID
	}
	record := func(op, name, id string) error {
		if id == "" {
			return nil
		}
		created[op+"|"+name] = id
		state.Created = append(state.Created, createdRef{Op: op, Name: name, ID: id})
		return saveState(path, state)
	}

	projectID := state.ProjectID
	if plan.Target.Into != "" {
		projectID, err = resolveServiceProject(ctx, c.API, plan.Target.Into)
		if err != nil {
			return err
		}
	}

	// A database service binds a managed database and provides DATABASE_URL /
	// REDIS_URL, so incoming references resolve. It goes through the change-set
	// path, which is the only one that provisions and binds in one step.
	for _, a := range plan.Actions {
		if a.Op != "database.service" {
			continue
		}
		if projectID == "" {
			return usagef("the plan creates a database service before its project")
		}
		if id := created[a.Op+"|"+a.Name]; id != "" {
			p.Line("database service %s already created (%s)", a.Name, shortID(id))
			continue
		}
		var open struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := c.API.Get(ctx, "/projects/"+url.PathEscape(projectID)+"/changes", nil, &open); err == nil && len(open.Data) > 0 {
			return usagef("this project has %d staged change(s); apply or discard them before importing", len(open.Data))
		}
		stage := map[string]any{
			"op": "service.create",
			"payload": map[string]any{
				"name":    a.Name,
				"kind":    "database",
				"managed": a.Engine,
			},
		}
		var staged struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		base := "/projects/" + url.PathEscape(projectID) + "/changes"
		if err := c.API.Post(ctx, base, stage, &staged); err != nil {
			return applyFailed(path, err)
		}
		var applied struct {
			Data struct {
				Results []struct {
					ServiceID string `json:"service_id"`
				} `json:"results"`
			} `json:"data"`
		}
		if err := c.API.Post(ctx, base+"/apply", map[string]any{"deploy": false}, &applied); err != nil {
			return applyFailed(path, err)
		}
		id := staged.Data.ID
		if len(applied.Data.Results) > 0 {
			id = applied.Data.Results[0].ServiceID
		}
		p.Line("created database service %s (%s)", a.Name, shortID(id))
		if err := record(a.Op, a.Name, id); err != nil {
			return die(err)
		}
	}

	// Databases first, so a reference resolves when the first release builds.
	for _, a := range plan.Actions {
		if a.Op != "database.create" {
			continue
		}
		if id := created[a.Op+"|"+a.Name]; id != "" {
			p.Line("database %s already created (%s)", a.Name, shortID(id))
			continue
		}
		body := map[string]any{"name": a.Name, "engine": a.Engine}
		if a.Version != "" {
			body["engine_version"] = a.Version
		}
		if a.Size != "" {
			body["size"] = a.Size
		}
		if plan.Target.WorkspaceID != "" {
			body["workspace_id"] = plan.Target.WorkspaceID
		}
		existing, err := findDatabaseByName(ctx, c.API, a.Name)
		if err != nil {
			return err
		}
		if existing != "" {
			p.Line("database %s already exists (%s)", a.Name, shortID(existing))
			if err := record(a.Op, a.Name, existing); err != nil {
				return die(err)
			}
			continue
		}
		var out struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := c.API.Post(ctx, "/databases", body, &out); err != nil {
			return applyFailed(path, err)
		}
		p.Line("created database %s (%s)", a.Name, shortID(out.Data.ID))
		if err := record(a.Op, a.Name, out.Data.ID); err != nil {
			return die(err)
		}
	}

	for _, a := range plan.Actions {
		switch a.Op {
		case "project.create":
			if projectID != "" {
				continue
			}
			id, err := findProjectByName(ctx, c.API, a.Name)
			if err != nil {
				return err
			}
			if id == "" {
				body := map[string]any{"name": a.Name}
				if plan.Target.WorkspaceID != "" {
					body["workspace_id"] = plan.Target.WorkspaceID
				}
				var out struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := c.API.Post(ctx, "/projects", body, &out); err != nil {
					return applyFailed(path, err)
				}
				id = out.Data.ID
				p.Line("created project %s (%s)", a.Name, shortID(id))
			} else {
				p.Line("project %s already exists (%s)", a.Name, shortID(id))
			}
			projectID = id
			state.ProjectID = id
			if err := record(a.Op, a.Name, id); err != nil {
				return die(err)
			}
		case "target.create":
			if projectID == "" {
				return usagef("the plan creates a target before its project")
			}
			envs, err := listServiceEnvironments(ctx, c.API, projectID)
			if err != nil {
				return err
			}
			found := ""
			for _, e := range envs {
				if strings.EqualFold(e.Name, a.Name) {
					found = e.ID
				}
			}
			if found != "" {
				p.Line("Deploy target %s already exists (%s)", a.Name, shortID(found))
				if err := record(a.Op, a.Name, found); err != nil {
					return die(err)
				}
				continue
			}
			var out struct {
				Data serviceEnvironment `json:"data"`
			}
			body := map[string]any{"name": a.Name}
			if err := c.API.Post(ctx, "/projects/"+url.PathEscape(projectID)+"/environments", body, &out); err != nil {
				return applyFailed(path, err)
			}
			p.Line("created Deploy target %s (%s)", a.Name, shortID(out.Data.ID))
			if err := record(a.Op, a.Name, out.Data.ID); err != nil {
				return die(err)
			}
		}
	}

	serviceIDs := map[string]string{}
	for _, a := range plan.Actions {
		if a.Op != "service.create" {
			continue
		}
		if projectID == "" {
			return usagef("the plan creates a service before its project")
		}
		if id := created[a.Op+"|"+a.Name]; id != "" {
			serviceIDs[a.Name] = id
			p.Line("service %s already created (%s)", a.Name, shortID(id))
			continue
		}
		if a.Source == nil {
			return usagef("service %q has no source in the plan; put its code in a repository and add --repo before applying", a.Name)
		}
		body := map[string]any{"name": a.Name, "kind": a.Kind}
		if a.Source != nil {
			source := map[string]any{"type": a.Source.Type, "ref": a.Source.Ref}
			if a.Source.Branch != "" {
				source["branch"] = a.Source.Branch
			}
			if a.Source.RootDir != "" {
				source["root_dir"] = a.Source.RootDir
			}
			body["source"] = source
		}
		if a.Build != nil && a.Build.Dockerfile != "" {
			body["build"] = map[string]any{"dockerfile": a.Build.Dockerfile}
		}
		if a.Settings != nil {
			settings := map[string]any{}
			if a.Settings.StartCommand != "" {
				settings["start_command"] = a.Settings.StartCommand
			}
			if a.Settings.Replicas > 0 {
				settings["replicas"] = a.Settings.Replicas
			}
			switch {
			case a.Settings.Region != "":
				settings["region"] = a.Settings.Region
			case a.NeedsRegion && f.region != "":
				settings["region"] = f.region
			}
			if a.Settings.CronSchedule != "" {
				settings["cron_schedule"] = a.Settings.CronSchedule
			}
			if a.Settings.HealthcheckPath != "" {
				settings["healthcheck_path"] = a.Settings.HealthcheckPath
			}
			if a.Settings.HealthcheckTimeout > 0 {
				settings["healthcheck_timeout_s"] = a.Settings.HealthcheckTimeout
			}
			if a.Settings.RestartPolicy != "" {
				settings["restart_policy"] = a.Settings.RestartPolicy
			}
			if a.Settings.MaxRetries > 0 {
				settings["max_retries"] = a.Settings.MaxRetries
			}
			if a.Settings.PreDeployCommand != "" {
				settings["pre_deploy_command"] = a.Settings.PreDeployCommand
			}
			if len(settings) > 0 {
				body["settings"] = settings
			}
		}

		existing, err := findServiceByName(ctx, c.API, projectID, a.Name)
		if err != nil {
			return err
		}
		if existing != "" {
			serviceIDs[a.Name] = existing
			p.Line("service %s already exists (%s)", a.Name, shortID(existing))
			if err := record(a.Op, a.Name, existing); err != nil {
				return die(err)
			}
			continue
		}

		var out struct {
			Data projectServiceRow `json:"data"`
		}
		if err := c.API.Post(ctx, "/projects/"+url.PathEscape(projectID)+"/services", body, &out); err != nil {
			return applyFailed(path, err)
		}
		serviceIDs[a.Name] = out.Data.ID
		p.Line("created %s %s (%s)", a.Kind, a.Name, shortID(out.Data.ID))
		if err := record(a.Op, a.Name, out.Data.ID); err != nil {
			return die(err)
		}
		if a.Address != nil && *a.Address {
			if err := enableServiceAddress(ctx, c.API, projectID, out.Data.ID, ""); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s has no public address yet: %v\n", a.Name, err)
			}
		}
	}

	// Variables go in one write per service.
	pending := map[string]map[string]any{}
	order := []string{}
	for _, a := range plan.Actions {
		if a.Op != "variable.set" || a.Variable == nil {
			continue
		}
		if pending[a.Service] == nil {
			pending[a.Service] = map[string]any{}
			order = append(order, a.Service)
		}
		value := ""
		if a.Variable.HasValue {
			value = a.Variable.Value
		}
		pending[a.Service][a.Variable.Name] = value
	}
	for _, name := range order {
		serviceID := serviceIDs[name]
		if serviceID == "" || projectID == "" {
			continue
		}
		if err := putServiceVariables(ctx, c.API, projectID, serviceID, pending[name]); err != nil {
			return applyFailed(path, err)
		}
		p.Line("set %d variable(s) on %s", len(pending[name]), name)
	}

	// Custom domains: the Services domain API is landing with services-runtime.
	// Try it, and fall back to the by-hand command when it is not live yet.
	domainsLive := true
	for _, a := range plan.Actions {
		if a.Op != "domain.custom" {
			continue
		}
		serviceID := serviceIDs[a.Service]
		if serviceID == "" || projectID == "" || !domainsLive {
			printDomainByHand(p, plan, a)
			continue
		}
		body := map[string]any{"domain": a.Domains[0]}
		err := c.API.Post(ctx, "/projects/"+url.PathEscape(projectID)+"/services/"+url.PathEscape(serviceID)+"/domains", body, nil)
		if err != nil {
			domainsLive = false
			printDomainByHand(p, plan, a)
			continue
		}
		p.Line("added custom domain %s to %s", a.Domains[0], a.Service)
	}

	renderApplySummary(p, plan, path)
	return nil
}

// printDomainByHand is the seam for a route that is not live yet.
func printDomainByHand(p printerLike, plan railway.Plan, a railway.Action) {
	for _, d := range a.Domains {
		p.Line("custom domain %s: add it when the Services domain API is live, or now with: miosa service domain enable %s %s --domain %s",
			d, plan.Target.ProjectName, a.Service, d)
	}
}

// renderApplySummary is the one screen a user reads after an apply: what exists,
// what still needs a value, and the exact next commands.
func renderApplySummary(p printerLike, plan railway.Plan, statePath string) {
	var services, databases, targets []string
	var needs []string
	for _, a := range plan.Actions {
		switch a.Op {
		case "service.create":
			services = append(services, a.Name)
		case "database.create":
			databases = append(databases, a.Name)
		case "database.service":
			databases = append(databases, a.Name+" (service)")
		case "target.create":
			targets = append(targets, a.Name)
		case "variable.set":
			if a.Variable != nil && !a.Variable.HasValue {
				needs = append(needs, a.Service+"."+a.Variable.Name)
			}
		}
	}
	p.Line("")
	p.Line("Summary")
	if len(services) > 0 {
		p.Line("  services:  %s", strings.Join(services, ", "))
	}
	if len(databases) > 0 {
		p.Line("  databases: %s", strings.Join(databases, ", "))
	}
	if len(targets) > 0 {
		p.Line("  targets:   %s", strings.Join(targets, ", "))
	}
	if len(needs) > 0 {
		p.Line("  need a value (%d): %s", len(needs), strings.Join(needs, ", "))
	}
	p.Line("")
	p.Line("Next:")
	if len(needs) > 0 {
		p.Line("  set the new secrets (never reuse the Railway value):")
		for _, n := range needs {
			if i := strings.IndexByte(n, '.'); i > 0 {
				p.Line("    miosa service vars set %s %s %s=<new value>", plan.Target.ProjectName, n[:i], n[i+1:])
			}
		}
	}
	for _, name := range services {
		p.Line("  miosa service deploy %s %s", plan.Target.ProjectName, name)
	}
	for _, a := range plan.Actions {
		if a.Op == "domain.custom" {
			p.Line("  miosa service domain enable %s %s --domain %s", plan.Target.ProjectName, a.Service, a.Domains[0])
		}
	}
	p.Line("  state: %s (undo with --cleanup --state %s)", statePath, statePath)
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
		if a.Op == "variable.set" && a.Variable != nil && !a.Variable.HasValue {
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

func findServiceByName(ctx context.Context, c *api.Client, projectID, name string) (string, error) {
	var out struct {
		Data []projectServiceRow `json:"data"`
	}
	if err := c.Get(ctx, "/projects/"+url.PathEscape(projectID)+"/services", nil, &out); err != nil {
		return "", die(err)
	}
	for _, s := range out.Data {
		if strings.EqualFold(s.Name, name) {
			return s.ID, nil
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

// ── cleanup ─────────────────────────────────────────────────────────────────

func railwayCleanup(ctx context.Context, cmd *cobra.Command, f *railwayImportFlags) error {
	if f.state == "" {
		return usagef("--cleanup needs --state <file>")
	}
	state, err := loadState(f.state)
	if err != nil {
		return die(err)
	}
	if len(state.Created) == 0 {
		return usagef("%s records nothing to delete", f.state)
	}
	p := printerFor(cmd)
	p.Line("This deletes exactly the ids recorded in %s:", f.state)
	for _, ref := range state.Created {
		p.Line("  %s %s (%s)", ref.Op, ref.Name, shortID(ref.ID))
	}
	if !f.yes {
		return usagef("nothing was deleted; pass --yes to do it")
	}

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	for i := len(state.Created) - 1; i >= 0; i-- {
		ref := state.Created[i]
		var err error
		switch ref.Op {
		case "variable.set":
			continue
		case "service.create":
			err = c.API.Delete(ctx, servicePath(state.ProjectID, ref.ID), nil, nil)
		case "target.create":
			err = c.API.Delete(ctx, "/projects/"+url.PathEscape(state.ProjectID)+"/environments/"+url.PathEscape(ref.ID), nil, nil)
		case "database.create":
			err = c.API.Delete(ctx, "/databases/"+url.PathEscape(ref.ID), nil, nil)
		case "project.create":
			err = c.API.Delete(ctx, "/projects/"+url.PathEscape(ref.ID), nil, nil)
		}
		if err != nil {
			return die(err)
		}
	}
	state.Created = nil
	if err := saveState(f.state, state); err != nil {
		return die(err)
	}
	p.Line("Deleted. %s is now empty.", f.state)
	return nil
}
