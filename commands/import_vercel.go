package commands

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	"github.com/Miosa-osa/miosa-cli-go/internal/railway"
	"github.com/Miosa-osa/miosa-cli-go/internal/vercel"
	"github.com/spf13/cobra"
)

type vercelImportFlags struct {
	fromFile     string
	name         string
	service      string
	repo         string
	region       string
	workspace    string
	into         string
	out          string
	planPath     string
	apply        bool
	state        string
	resume       bool
	allowMissing bool
}

func vercelImportCmd() *cobra.Command {
	f := &vercelImportFlags{}

	cmd := &cobra.Command{
		Use:   "vercel --from-file vercel.json",
		Short: "Read a vercel.json and write a reviewable plan",
		Long: `Read a Vercel project's vercel.json and write a plan: the MIOSA project,
the production Deploy target, one service, and the environment variable names
it declares. Review the plan, then --apply it.

What maps: the app, its region (when the file names one MIOSA has), and the
environment variable names. What does not, and is listed with the reason:
rewrites, redirects, headers, builds, the functions block, cleanUrls,
trailingSlash, buildCommand, installCommand, outputDirectory and crons.

  miosa import vercel --from-file vercel.json --name shop --repo https://github.com/acme/shop
  miosa import vercel --from-file vercel.json --out plan.json
  miosa import vercel --apply --plan plan.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runVercelImport(cmd, f)
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&f.fromFile, "from-file", "", "The vercel.json to read")
	fs.StringVar(&f.name, "name", "", "MIOSA project name (default: vercel-app)")
	fs.StringVar(&f.service, "service", "web", "Service name inside the project")
	fs.StringVar(&f.repo, "repo", "", "Git URL for the service's source")
	fs.StringVar(&f.region, "region", "", "Region: us-east, us-west or us-mia (overrides the file)")
	fs.StringVar(&f.workspace, "workspace", "", "MIOSA workspace (slug, name or id)")
	fs.StringVar(&f.into, "into", "", "Import into an existing MIOSA project")
	fs.StringVar(&f.out, "out", "", "Write the plan JSON here")
	fs.StringVar(&f.planPath, "plan", "", "With --apply: the plan to apply")
	fs.BoolVar(&f.apply, "apply", false, "Create what the plan says")
	fs.StringVar(&f.state, "state", "", "Where created ids are recorded (default .miosa/import-vercel-<project>.json)")
	fs.BoolVar(&f.resume, "resume", false, "Continue an interrupted apply")
	fs.BoolVar(&f.allowMissing, "allow-missing-values", false, "Create variables with no value as empty strings")

	return cmd
}

func runVercelImport(cmd *cobra.Command, f *vercelImportFlags) error {
	if f.apply {
		if f.planPath == "" {
			return usagef("--apply needs --plan <file> written by an earlier run")
		}
		// The applier is the same one the Railway import uses: one plan
		// vocabulary, one code path to create project, target, service and
		// variables, and one state file for cleanup.
		return railwayApply(cmd.Context(), cmd, &railwayImportFlags{
			planPath:     f.planPath,
			allowMissing: f.allowMissing,
			region:       f.region,
			into:         f.into,
			state:        f.state,
			resume:       f.resume,
		})
	}

	if strings.TrimSpace(f.fromFile) == "" {
		return usagef("--from-file is required (the vercel.json to read)")
	}

	body, err := os.ReadFile(f.fromFile)
	if err != nil {
		return die(err)
	}

	cfg, err := vercel.Parse(body)
	if err != nil {
		return die(err)
	}

	plan := vercel.Plan(cfg, vercel.Options{
		ProjectName: f.name,
		ServiceName: f.service,
		Repository:  f.repo,
		Region:      f.region,
		WorkspaceID: f.workspace,
		FetchedAt:   time.Now(),
	})

	if f.out != "" {
		encoded, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return die(err)
		}
		if err := os.WriteFile(f.out, encoded, 0o600); err != nil {
			return die(err)
		}
	}

	renderVercelPlan(printerFor(cmd), plan, f.out)
	return nil
}

func renderVercelPlan(p *output.Printer, plan railway.Plan, dest string) {
	p.Line("Vercel import: %s -> MIOSA project %q", plan.Source.Kind, plan.Target.ProjectName)
	p.Line("")

	var create, variables, unmapped []railway.Action
	for _, a := range plan.Actions {
		switch a.Op {
		case "target.create", "service.create":
			create = append(create, a)
		case "variable.set":
			variables = append(variables, a)
		case "unmapped":
			unmapped = append(unmapped, a)
		}
	}

	p.Line("Will create:")
	for _, a := range create {
		if a.Op == "service.create" {
			region := ""
			if a.Settings != nil && a.Settings.Region != "" {
				region = ", region " + a.Settings.Region
			}
			p.Line("  service %s (server%s)", a.Name, region)
			continue
		}
		p.Line("  Deploy target %s", a.Name)
	}

	if len(variables) > 0 {
		names := make([]string, 0, len(variables))
		for _, a := range variables {
			names = append(names, a.Variable.Name)
		}
		p.Line("")
		p.Line("%d variable name(s), with no values: %s", len(names), strings.Join(names, ", "))
		p.Line("  values are not read from Vercel; set them with 'miosa variables set' after the import")
	}

	if len(unmapped) > 0 {
		p.Line("")
		p.Line("Not carried:")
		for _, a := range unmapped {
			p.Line("  %s: %s", a.Field, a.Why)
		}
	}

	for _, w := range plan.Warnings {
		p.Line("")
		p.Line("warning: %s", w)
	}

	p.Line("")
	if dest != "" {
		p.Line("Nothing was created. Plan written to %s.", dest)
		p.Line("Apply it with: miosa import vercel --apply --plan %s", dest)
	} else {
		p.Line("Nothing was created. Add --out plan.json to keep the plan, then --apply --plan plan.json.")
	}
}
