package commands

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// newVariablesCmd is `miosa variables` (alias `vars`): the Railway-shaped way to
// read and write variables for the linked project, Deploy target or service,
// without naming them on every call.
func newVariablesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "variables",
		Aliases: []string{"vars"},
		Short:   "Read and write variables of the linked project, target or service",
		Long: `Read and write variables without repeating the project and service.

  miosa variables list                     # the linked service
  miosa variables list --shared            # the Deploy target's shared variables
  miosa variables set API_BASE=https://example.test
  miosa variables set CALLBACK='${{API_BASE}}/cb'
  miosa variables unset OLD_KEY
  miosa variables import .env
  miosa variables export > .env

A value may be a reference: ${{Service.NAME}}, ${{shared.NAME}}, ${{NAME}} (this
service) or ${{secret(32)}}. References are checked before the write.

--skip-deploys is accepted for Railway compatibility: a variable write never
redeploys here; the service reports a pending rebuild or redeploy instead.`,
	}
	cmd.AddCommand(
		variablesListCmd(),
		variablesSetCmd(),
		variablesUnsetCmd(),
		variablesImportCmd(),
		variablesExportCmd(),
	)
	return cmd
}

// varsTarget is where a variables command reads or writes.
type varsTarget struct {
	projectID   string
	serviceID   string
	environment string
	shared      bool
	label       string
}

// resolveVarsTarget takes the flags, then the link file, and finally an
// optional positional project (and service) so a one-off call needs no link.
func resolveVarsTarget(ctx context.Context, c *api.Client, args []string, serviceFlag, envFlag string, shared bool) (varsTarget, error) {
	link, linkErr := readLink()

	projectRef := ""
	if len(args) > 0 {
		projectRef = args[0]
	} else if linkErr == nil {
		projectRef = link.ProjectID
	}
	if projectRef == "" {
		if linkErr != nil {
			return varsTarget{}, usagef("%v", linkErr)
		}
		return varsTarget{}, usagef("name a project, or run `miosa link <project>` first")
	}
	projectID, err := resolveServiceProject(ctx, c, projectRef)
	if err != nil {
		return varsTarget{}, err
	}

	env := envFlag
	if env == "" && len(args) == 0 && linkErr == nil {
		env = link.Environment
	}

	serviceRef := serviceFlag
	if serviceRef == "" && len(args) > 1 {
		serviceRef = args[1]
	}
	if shared && serviceRef != "" {
		return varsTarget{}, usagef("--shared is the Deploy target's; drop --service")
	}
	if serviceRef == "" && !shared && len(args) == 0 && linkErr == nil {
		if link.Service == "" {
			return varsTarget{}, usagef("the link names no service; pass --service or --shared")
		}
		serviceRef = link.ServiceID
	}
	if serviceRef == "" && !shared {
		return varsTarget{}, usagef("pass --service, --shared, or run `miosa link <project> --service <name>` first")
	}

	envID, err := deployTargetID(ctx, c, projectID, env)
	if err != nil {
		return varsTarget{}, err
	}
	target := varsTarget{projectID: projectID, environment: envID, shared: shared}
	if shared {
		target.label = "shared"
		return target, nil
	}

	services, err := listServices(ctx, c, projectID, envID)
	if err != nil {
		return varsTarget{}, err
	}
	for _, s := range services {
		if strings.EqualFold(s.ID, serviceRef) || strings.EqualFold(s.Name, serviceRef) || strings.EqualFold(s.Slug, serviceRef) {
			target.serviceID, target.label = s.ID, s.Name
			return target, nil
		}
	}
	return varsTarget{}, usagef("no service %q in this project", serviceRef)
}

func (t varsTarget) path() string {
	if t.shared {
		return "/projects/" + url.PathEscape(t.projectID) + "/shared-variables"
	}
	return servicePath(t.projectID, t.serviceID) + "/variables"
}

func (t varsTarget) envQuery() string {
	if t.environment == "" {
		return ""
	}
	return url.Values{"env": {t.environment}}.Encode()
}

// variableReference matches every accepted reference form.
var variableReference = regexp.MustCompile(`\$\{\{\s*([^{}]*?)\s*\}\}`)
var referenceService = regexp.MustCompile(`\A[A-Za-z0-9][A-Za-z0-9 _-]{0,62}\.[A-Za-z_][A-Za-z0-9_]*\z`)
var referenceName = regexp.MustCompile(`\A[A-Za-z_][A-Za-z0-9_]*\z`)
var referenceSecret = regexp.MustCompile(`\Asecret\((\d{1,3})\)\z`)

// validateVariableValue checks every reference in a value before it is sent, so
// a typo is a clear message instead of a server error.
func validateVariableValue(value string) error {
	for _, match := range variableReference.FindAllStringSubmatch(value, -1) {
		inner := match[1]
		switch {
		case referenceSecret.MatchString(inner):
			var n int
			fmt.Sscanf(referenceSecret.FindStringSubmatch(inner)[1], "%d", &n)
			if n < 8 || n > 128 {
				return fmt.Errorf("${{%s}}: secret(n) needs 8 to 128 characters", inner)
			}
		case strings.HasPrefix(inner, "shared."):
			if !referenceName.MatchString(strings.TrimPrefix(inner, "shared.")) {
				return fmt.Errorf("${{%s}}: a shared reference is ${{shared.NAME}}", inner)
			}
		case referenceService.MatchString(inner), referenceName.MatchString(inner):
			// a service reference or another variable of this service
		default:
			return fmt.Errorf("${{%s}}: use ${{Service.NAME}}, ${{shared.NAME}}, ${{NAME}} or ${{secret(32)}}", inner)
		}
	}
	if strings.Contains(value, "${{") && !strings.Contains(value, "}}") {
		return fmt.Errorf("unclosed reference in %q", value)
	}
	return nil
}

func parsePairs(pairs []string) (map[string]any, error) {
	vars := map[string]any{}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, usagef("give KEY=VALUE, got %q", pair)
		}
		if !referenceName.MatchString(key) {
			return nil, usagef("%q is not a variable name", key)
		}
		if err := validateVariableValue(value); err != nil {
			return nil, usagef("%s: %v", key, err)
		}
		vars[key] = value
	}
	return vars, nil
}

func variablesListCmd() *cobra.Command {
	var serviceFlag, envFlag string
	var shared, kv bool
	cmd := &cobra.Command{
		Use:   "list [project] [service]",
		Short: "List variables of the linked service or the shared set",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			target, err := resolveVarsTarget(ctx, c.API, args, serviceFlag, envFlag, shared)
			if err != nil {
				return err
			}
			rows, err := readVariables(ctx, c.API, target)
			if err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(rows)
			}
			if kv {
				for _, row := range rows {
					p.Line("%s=%s", row.Name, row.Shown)
				}
				return nil
			}
			if len(rows) == 0 {
				p.Line("No variables.")
				return nil
			}
			table := make([][]string, 0, len(rows))
			for _, row := range rows {
				table = append(table, []string{row.Name, row.Shown})
			}
			p.Table([]string{"NAME", "VALUE"}, table)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&serviceFlag, "service", "", "Service (default: the linked one)")
	fs.StringVar(&envFlag, "environment", "", "Deploy target (default: the linked one)")
	fs.StringVar(&envFlag, "deploy-target", "", "Alias of --environment")
	fs.BoolVar(&shared, "shared", false, "The Deploy target's shared variables")
	fs.BoolVar(&kv, "kv", false, "Print NAME=VALUE lines")
	return cmd
}

type variableRow struct {
	Name  string `json:"name"`
	Shown string `json:"value"`
}

// readVariables reads the masked list for a service or the shared set.
func readVariables(ctx context.Context, c *api.Client, target varsTarget) ([]variableRow, error) {
	var out struct {
		Data []serviceVariable `json:"data"`
	}
	var q url.Values
	if target.envQuery() != "" {
		q, _ = url.ParseQuery(target.envQuery())
	}
	if err := c.Get(ctx, target.path(), q, &out); err != nil {
		return nil, die(err)
	}
	rows := make([]variableRow, 0, len(out.Data))
	for _, v := range out.Data {
		shown := "(sealed)"
		switch {
		case v.Expression != nil:
			shown = *v.Expression
		case v.ValuePreview != nil:
			shown = *v.ValuePreview
		}
		rows = append(rows, variableRow{Name: v.Name, Shown: shown})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func variablesSetCmd() *cobra.Command {
	var serviceFlag, envFlag string
	var shared, skipDeploys bool
	var build *buildVisibility
	cmd := &cobra.Command{
		Use:   "set KEY=VALUE [KEY=VALUE...]",
		Short: "Set variables on the linked service or the shared set",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			target, err := resolveVarsTarget(ctx, c.API, nil, serviceFlag, envFlag, shared)
			if err != nil {
				return err
			}
			vars, err := parsePairs(args)
			if err != nil {
				return err
			}
			if err := writeVariables(ctx, c.API, target, vars, build.value()); err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(map[string]any{"target": target.label, "set": keysOfMap(vars)})
			}
			p.Success("Set %d variable(s) on %s", len(vars), target.label)
			if note := buildNote(build.value()); note != "" {
				p.Line("  %s", note)
			}
			if skipDeploys {
				p.Line("  --skip-deploys: a variable write never redeploys here; the service shows a pending change.")
			} else {
				p.Line("  The service now shows a pending rebuild or redeploy; `miosa service deploy` applies it.")
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&serviceFlag, "service", "", "Service (default: the linked one)")
	fs.StringVar(&envFlag, "environment", "", "Deploy target (default: the linked one)")
	fs.StringVar(&envFlag, "deploy-target", "", "Alias of --environment")
	fs.BoolVar(&shared, "shared", false, "Write the Deploy target's shared variables")
	fs.BoolVar(&skipDeploys, "skip-deploys", false, "Railway compatibility: do not redeploy (never does)")
	build = addBuildFlags(cmd)
	return cmd
}

func writeVariables(ctx context.Context, c *api.Client, target varsTarget, vars map[string]any, build *bool) error {
	payload, err := variablesBody(vars, build)
	if err != nil {
		return err
	}
	body := map[string]any{"variables": payload}
	path := target.path()
	if q := target.envQuery(); q != "" {
		path += "?" + q
	}
	if err := c.Put(ctx, path, body, nil); err != nil {
		return die(err)
	}
	return nil
}

func variablesUnsetCmd() *cobra.Command {
	var serviceFlag, envFlag string
	var shared bool
	cmd := &cobra.Command{
		Use:   "unset NAME [NAME...]",
		Short: "Delete variables from the linked service or the shared set",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			target, err := resolveVarsTarget(ctx, c.API, nil, serviceFlag, envFlag, shared)
			if err != nil {
				return err
			}
			for _, name := range args {
				path := target.path() + "/" + url.PathEscape(name)
				if q := target.envQuery(); q != "" {
					path += "?" + q
				}
				if err := c.API.Delete(ctx, path, nil, nil); err != nil {
					return die(err)
				}
			}
			p.Success("Deleted %d variable(s) from %s", len(args), target.label)
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&serviceFlag, "service", "", "Service (default: the linked one)")
	fs.StringVar(&envFlag, "environment", "", "Deploy target (default: the linked one)")
	fs.StringVar(&envFlag, "deploy-target", "", "Alias of --environment")
	fs.BoolVar(&shared, "shared", false, "Write the Deploy target's shared variables")
	return cmd
}

func variablesImportCmd() *cobra.Command {
	var serviceFlag, envFlag string
	var shared bool
	var build *buildVisibility
	cmd := &cobra.Command{
		Use:   "import <file|->",
		Short: "Set variables from a .env file (or stdin)",
		Long: `Read KEY=VALUE lines and set them on the target. Blank lines, comments and a
leading "export " are ignored; quotes are stripped. "-" reads stdin, so a pasted
block works:

  miosa variables import -
  miosa variables import .env
  miosa variables import build.env --build    # every variable in the file build-visible`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			var reader io.Reader
			if args[0] == "-" {
				reader = os.Stdin
			} else {
				file, err := os.Open(args[0])
				if err != nil {
					return die(err)
				}
				defer file.Close()
				reader = file
			}
			vars, err := parseDotenv(reader)
			if err != nil {
				return err
			}
			if len(vars) == 0 {
				return usagef("no KEY=VALUE lines found")
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			target, err := resolveVarsTarget(ctx, c.API, nil, serviceFlag, envFlag, shared)
			if err != nil {
				return err
			}
			if err := writeVariables(ctx, c.API, target, vars, build.value()); err != nil {
				return err
			}
			if isJSON() {
				return p.JSON(map[string]any{"target": target.label, "set": keysOfMap(vars)})
			}
			p.Success("Set %d variable(s) on %s", len(vars), target.label)
			if note := buildNote(build.value()); note != "" {
				p.Line("  %s", note)
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&serviceFlag, "service", "", "Service (default: the linked one)")
	fs.StringVar(&envFlag, "environment", "", "Deploy target (default: the linked one)")
	fs.StringVar(&envFlag, "deploy-target", "", "Alias of --environment")
	fs.BoolVar(&shared, "shared", false, "Write the Deploy target's shared variables")
	build = addBuildFlags(cmd)
	return cmd
}

// parseDotenv reads KEY=VALUE lines, tolerating comments, blanks, "export " and
// surrounding quotes.
func parseDotenv(reader io.Reader) (map[string]any, error) {
	vars := map[string]any{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, usagef("cannot read %q as KEY=VALUE", line)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if err := validateVariableValue(value); err != nil {
			return nil, usagef("%s: %v", key, err)
		}
		vars[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, die(err)
	}
	return vars, nil
}

func variablesExportCmd() *cobra.Command {
	var serviceFlag, envFlag string
	var shared, shell bool
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Print the target's variables as .env lines",
		Long: `Print KEY="VALUE" lines, or ` + "`export KEY=\"VALUE\"`" + ` with --shell so the output
can be sourced. Values are printed to stdout only; nothing is written to disk.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			ctx := cmd.Context()
			target, err := resolveVarsTarget(ctx, c.API, nil, serviceFlag, envFlag, shared)
			if err != nil {
				return err
			}
			rows, err := readVariables(ctx, c.API, target)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if shell {
					p.Line("export %s=%q", row.Name, row.Shown)
				} else {
					p.Line("%s=%q", row.Name, row.Shown)
				}
			}
			return nil
		},
	}
	fs := cmd.Flags()
	fs.StringVar(&serviceFlag, "service", "", "Service (default: the linked one)")
	fs.StringVar(&envFlag, "environment", "", "Deploy target (default: the linked one)")
	fs.StringVar(&envFlag, "deploy-target", "", "Alias of --environment")
	fs.BoolVar(&shared, "shared", false, "The Deploy target's shared variables")
	fs.BoolVar(&shell, "shell", false, "Prefix each line with export")
	return cmd
}

func keysOfMap(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
