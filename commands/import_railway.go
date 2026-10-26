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
write a plan: the MIOSA project, a Deploy target per Railway environment, and
in each the services, databases, variable names and shared variables it would
create, and everything it cannot carry (volumes' data, Railway domains, regions
outside the US, references MIOSA has no equivalent for).

  miosa import railway --project acme-api                      # review the plan
  miosa import railway --project acme-api --environment all --out plan.json
  miosa import railway --apply --plan plan.json                # create it, stopped
  miosa import railway --apply --plan plan.json --deploy       # create and start it

Settings come from the repository's railway.json or railway.toml when it has
one (as on Railway, the file wins), else from the service settings, else from
what the latest deployment ran; the plan records which for each setting.

An apply creates every service stopped: nothing builds or runs until you deploy
it, or pass --deploy. A GitHub repository, private or public, is linked through
your GitHub connection (miosa connections add apps github): MIOSA copies it into
Forge and builds from the copy. Running apply again creates only what is missing.

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
	fs.StringSliceVar(&f.environments, "environment", []string{"production"}, "Railway environments to import, comma separated, or all")
	fs.StringVar(&f.fromFile, "from-file", "", "Read railway.json or railway.toml instead of the API")
	fs.StringVar(&f.serviceName, "service-name", "", "With --from-file: the service name")
	fs.StringVar(&f.workspace, "workspace", "", "MIOSA workspace (slug, name or id)")
	fs.StringVar(&f.name, "name", "", "MIOSA project name (default: the Railway project name)")
	fs.StringVar(&f.into, "into", "", "Import into an existing MIOSA project")
	fs.BoolVar(&f.includeTargets, "include-targets", false, "Also import other Railway environments as Deploy targets")
	fs.BoolVar(&f.copyVars, "copy-variables", false, "Copy variable values, not just names (prints a warning)")
	fs.StringVar(&f.out, "out", "", "Write the plan JSON here")
	fs.StringVar(&f.planPath, "plan", "", "With --apply: the plan to apply")
	fs.BoolVar(&f.apply, "apply", false, "Create what the plan says, with every service stopped")
	fs.BoolVar(&f.deploy, "deploy", false, "With --apply: also start every service that has a source")
	fs.BoolVar(&f.attachDomains, "attach-domains", false, "With --apply: attach the custom domains (default: list them, attach nothing)")
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
	environments   []string
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
	deploy         bool
	attachDomains  bool
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
	src, err := reader.ReadEnvironments(ctx, f.project, f.environments)
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
	if plan.Version < railway.OldestPlanVersion || plan.Version > railway.PlanVersion {
		return railway.Plan{}, fmt.Errorf("%s is a version %d plan; this CLI applies versions %d to %d", path, plan.Version, railway.OldestPlanVersion, railway.PlanVersion)
	}
	return plan, nil
}

// renderPlan is the review: what will be created where, where each setting came
// from, what needs a value, and what a person has to finish by hand.
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
	header := ""
	for _, a := range plan.Actions {
		in := envPrefix(a.Env)
		if a.Env != header && (a.Op == "service.create" || a.Op == "database.service") {
			header = a.Env
			p.Line("  in Deploy target %s:", a.Env)
		}
		switch a.Op {
		case "project.create":
			p.Line("  create project %s", a.Name)
			creates++
		case "target.create":
			p.Line("  Deploy target %s (found or created)", a.Name)
			creates++
		case "database.create":
			p.Line("  create database %s (%s)", a.Name, a.Engine)
			creates++
		case "database.service":
			p.Line("  %screate database service %s (%s, MIOSA provisions the database)", indent(a.Env), a.Name, a.Engine)
			creates++
		case "service.create":
			line := fmt.Sprintf("  %screate %s %s", indent(a.Env), a.Kind, a.Name)
			if a.RailwayName != "" {
				line += fmt.Sprintf(" (Railway: %s)", a.RailwayName)
			}
			switch {
			case a.Source == nil:
				line += ", no source (needs a source upload)"
			default:
				line += " from " + a.Source.Ref
				if a.Source.Branch != "" {
					line += " (" + a.Source.Branch + ")"
				}
				if a.Source.RootDir != "" {
					line += " in " + a.Source.RootDir
				}
			}
			if a.Build != nil && a.Build.Dockerfile != "" {
				line += " with " + a.Build.Dockerfile
			}
			if a.Source != nil && a.Source.Port > 0 {
				line += fmt.Sprintf(", port %d", a.Source.Port)
			}
			p.Line("%s", line)
			renderServiceSettings(p, a)
			creates++
		case "variable.set", "shared.set":
			if a.Variable == nil {
				continue
			}
			owner := in + a.Service
			if a.Op == "shared.set" {
				owner = in + "shared"
			}
			if a.Variable.HasValue {
				kind := "value"
				if strings.Contains(a.Variable.Value, "${{") {
					kind = "reference " + a.Variable.Value
				}
				variables = append(variables, fmt.Sprintf("%s.%s (%s)", owner, a.Variable.Name, kind))
			} else {
				needsValue = append(needsValue, fmt.Sprintf("%s.%s", owner, a.Variable.Name))
			}
		case "domain.report":
			if len(a.Domains) > 0 {
				manual = append(manual, [2]string{
					in + a.Service + " address",
					fmt.Sprintf("%s was %s; MIOSA assigns a new <label>.<org>.miosa.app", a.Service, strings.Join(a.Domains, ", ")),
				})
			}
		case "domain.custom":
			for _, d := range a.Domains {
				manual = append(manual, [2]string{
					in + a.Service + " custom domain " + d,
					"skipped (pass --attach-domains); attaching a live domain during the import would move its traffic",
				})
			}
		case "unmapped":
			next := a.Next
			if next == "" || next == "none" {
				next = "no action needed"
			}
			manual = append(manual, [2]string{in + a.Name + " " + a.Field, a.Why + " -> " + next})
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
	p.Line("Apply creates every service stopped; nothing builds or runs until you deploy it or pass --deploy.")
	if dest != "" {
		p.Line("Nothing was created. Plan written to %s.", dest)
		p.Line("Review it, then: miosa import railway --apply --plan %s", dest)
		return
	}
	p.Line("Nothing was created. Re-run with --out plan.json, review it, then --apply --plan plan.json.")
}

// envPrefix qualifies a name with its Deploy target in the review lists.
func envPrefix(env string) string {
	if env == "" {
		return ""
	}
	return env + "/"
}

// indent nests a create under its Deploy target's header.
func indent(env string) string {
	if env == "" {
		return ""
	}
	return "  "
}

// renderServiceSettings prints a service's carried settings, each with where it
// came from on Railway.
func renderServiceSettings(p printerLike, a railway.Action) {
	type row struct{ key, value string }
	var rows []row
	if s := a.Settings; s != nil {
		rows = append(rows,
			row{"start_command", s.StartCommand},
			row{"pre_deploy_command", s.PreDeployCommand},
			row{"healthcheck_path", s.HealthcheckPath},
			row{"cron_schedule", s.CronSchedule},
		)
		if s.Replicas > 1 {
			rows = append(rows, row{"replicas", fmt.Sprint(s.Replicas)})
		}
		region := s.Region
		if region == "" && a.RailwayRegion != "" {
			region = "--region (Railway: " + a.RailwayRegion + ")"
		}
		rows = append(rows, row{"region", region})
	}
	if b := a.Build; b != nil {
		rows = append(rows, row{"build_command", b.BuildCommand})
	}
	for _, r := range rows {
		if r.value == "" {
			continue
		}
		from := a.Provenance[r.key]
		if from == "" {
			p.Line("    %s  %s: %s", indent(a.Env), r.key, r.value)
			continue
		}
		p.Line("    %s  %s: %s  [%s]", indent(a.Env), r.key, r.value, from)
	}
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
		case "variable.set", sourceLinkOp:
			continue
		case "service.create", "database.service":
			err = c.API.Delete(ctx, servicePath(state.ProjectID, ref.ID), nil, nil)
		case repoDeploymentOp:
			err = c.API.Delete(ctx, "/deployments/"+url.PathEscape(ref.ID), nil, nil)
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
