package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/config"
	"github.com/Miosa-osa/miosa-cli-go/internal/envspec"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	miosa "github.com/Miosa-osa/miosa-go"
)

// envTarget is the --env flag shared by commands that edit one environment.
// An empty value means the account's default environment.
type envTarget struct{ name string }

func (t *envTarget) bind(cmd *cobra.Command) {
	cmd.Flags().StringVar(&t.name, "env", "", "Environment name (default: the default environment)")
}

// resolve returns the environment name to edit. When --env is omitted it asks
// the API which environment is the default.
func (t *envTarget) resolve(cmd *cobra.Command, envs *miosa.EnvironmentsService) (string, error) {
	if t.name != "" {
		return t.name, nil
	}
	def, err := envs.Default(cmd.Context())
	if err != nil {
		return "", err
	}
	return def.Name, nil
}

// envWorkspace is the persistent --workspace flag of the env command group.
var envWorkspace string

// envClient builds the API client and the environments service scoped to the
// --workspace flag. Without --workspace the API resolves the scope: the
// workspace of a workspace-bound key, else the organization level.
func envClient(cmd *cobra.Command) (*client.Client, *miosa.EnvironmentsService, error) {
	c, _, err := buildClient()
	if err != nil {
		return nil, nil, err
	}
	if envWorkspace == "" {
		return c, c.SDK.Environments, nil
	}
	id, err := resolveWorkspaceID(cmd.Context(), c, envWorkspace)
	if err != nil {
		return nil, nil, err
	}
	return c, c.SDK.Environments.ForWorkspace(id), nil
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// resolveWorkspaceID turns a workspace slug (or id) into an id.
func resolveWorkspaceID(ctx context.Context, c *client.Client, slugOrID string) (string, error) {
	if uuidRE.MatchString(slugOrID) {
		return slugOrID, nil
	}
	list, err := c.Workspaces.List(ctx)
	if err != nil {
		return "", fmt.Errorf("resolving workspace %q: %w", slugOrID, err)
	}
	for _, w := range list {
		if w.Slug == slugOrID || w.ID == slugOrID {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("workspace %q not found (see 'miosa workspace list')", slugOrID)
}

func newEnvCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "env",
		Aliases: []string{"environment", "environments"},
		Short:   "Manage environments (what new machines start with)",
		Long: `Manage environments: the template a new sandbox or computer inherits when it
starts. An environment decides which repositories are cloned, which variables
and secret files are injected, and which of your connections the machine may use.

Every save mints a new immutable version. A machine keeps the version it
started with until you run 'miosa env upgrade'.

Environments belong to a workspace, or to the organization when no workspace
applies. Pass --workspace <slug> to work in one workspace; without it the API
uses the workspace of a workspace-bound key, else the organization level.

Commands that edit one environment take --env <name>. Without it they edit
the default environment of that scope.

  miosa env list
  miosa env new staging
  miosa env set --env staging STRIPE_KEY=sk_live_123
  miosa env set --env staging --from-file .env
  miosa env file put --env staging backend/.env ./backend.env
  miosa env repo add --env staging octocat/hello-world --branch develop
  miosa env toggle --env staging github off
  miosa create my-box --env staging`,
	}
	cmd.PersistentFlags().StringVar(&envWorkspace, "workspace", "", "Workspace slug the environment belongs to (default: the API resolves it; organization level for an org key)")
	cmd.AddCommand(
		newEnvListCmd(),
		newEnvGetCmd(),
		newEnvCreateCmd(),
		newEnvRenameCmd(),
		newEnvDeleteCmd(),
		newEnvDefaultCmd(),
		newEnvSetVarCmd(),
		newEnvUnsetVarCmd(),
		newEnvAddFileCmd(),
		newEnvRmFileCmd(),
		newEnvRepoCmd(),
		newEnvToggleCmd(),
		newEnvSafeCmd(),
		newEnvVersionsCmd(),
		newEnvUpgradeCmd(),
	)
	alignEnvNames(cmd)
	return cmd
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// ─── list / get ───────────────────────────────────────────────────────────────

func newEnvListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List environments",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			list, err := envs.List(cmd.Context())
			if err != nil {
				return die(err)
			}
			if isJSON() {
				out := make([]envSummary, 0, len(list.Environments))
				for _, e := range list.Environments {
					out = append(out, summarizeEnv(e))
				}
				return p.JSON(map[string]interface{}{"environments": out, "default_environment_id": list.DefaultEnvironmentID})
			}
			if len(list.Environments) == 0 {
				p.Line("No environments found.")
				return nil
			}
			rows := make([][]string, 0, len(list.Environments))
			for _, e := range list.Environments {
				name := e.Name
				if e.IsDefault || e.ID == list.DefaultEnvironmentID {
					name += " *"
				}
				rows = append(rows, []string{
					name,
					fmt.Sprintf("v%d", e.LatestVersion),
					fmt.Sprintf("%d", e.PinnedMachineCount),
					fmt.Sprintf("%d", e.OutdatedMachineCount),
					onOff(e.SafeForThirdParties),
					fmt.Sprintf("%d", len(e.Variables)),
					fmt.Sprintf("%d", len(e.Repositories)),
				})
			}
			p.Table([]string{"NAME", "LATEST", "MACHINES", "OUTDATED", "SAFE-3P", "VARS", "REPOS"}, rows)
			p.Line("")
			p.Line("* = default environment")
			return nil
		},
	}
}

// envSummary is the list view: counts only, never values.
type envSummary struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	IsDefault           bool   `json:"is_default"`
	LatestVersion       int    `json:"latest_version"`
	PinnedMachineCount  int    `json:"pinned_machine_count"`
	OutdatedMachines    int    `json:"outdated_machine_count"`
	SafeForThirdParties bool   `json:"safe_for_third_parties"`
	Variables           int    `json:"variables"`
	SecretFiles         int    `json:"secret_files"`
	Repositories        int    `json:"repositories"`
}

func summarizeEnv(e miosa.Environment) envSummary {
	return envSummary{
		ID: e.ID, Name: e.Name, IsDefault: e.IsDefault, LatestVersion: e.LatestVersion,
		PinnedMachineCount: e.PinnedMachineCount, OutdatedMachines: e.OutdatedMachineCount,
		SafeForThirdParties: e.SafeForThirdParties, Variables: len(e.Variables),
		SecretFiles: len(e.SecretFiles), Repositories: len(e.Repositories),
	}
}

// envView is the get view. Variable values are masked unless revealed.
type envView struct {
	ID                     string                        `json:"id"`
	Name                   string                        `json:"name"`
	IsDefault              bool                          `json:"is_default"`
	LatestVersion          int                           `json:"latest_version"`
	SafeForThirdParties    bool                          `json:"safe_for_third_parties"`
	PassGithub             bool                          `json:"pass_github"`
	PassSecrets            bool                          `json:"pass_secrets"`
	PassSandboxCredentials bool                          `json:"pass_sandbox_credentials"`
	PassAgentsCredentials  bool                          `json:"pass_agents_credentials"`
	Effective              miosa.EnvironmentEffective    `json:"effective"`
	PinnedMachineCount     int                           `json:"pinned_machine_count"`
	OutdatedMachineCount   int                           `json:"outdated_machine_count"`
	Variables              []envVarView                  `json:"variables"`
	SecretFiles            []miosa.EnvironmentSecretFile `json:"secret_files"`
	Repositories           []miosa.EnvironmentRepository `json:"repositories"`
}

type envVarView struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func newEnvGetCmd() *cobra.Command {
	var reveal bool
	cmd := &cobra.Command{
		Use:     "info <name>",
		Aliases: []string{"get"},
		Short:   "Show one environment in full",
		Long: `Show an environment: toggles, variables, secret files, repositories.

Variable values are masked. Pass --reveal to fetch and print them; each reveal
is recorded in the audit log. Secret file contents are never printed, only
their paths and sizes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			e, err := envs.Get(cmd.Context(), args[0])
			if err != nil {
				return die(err)
			}
			view := envView{
				ID: e.ID, Name: e.Name, IsDefault: e.IsDefault, LatestVersion: e.LatestVersion,
				SafeForThirdParties: e.SafeForThirdParties, PassGithub: e.PassGithub,
				PassSecrets: e.PassSecrets, PassSandboxCredentials: e.PassSandboxCredentials,
				PassAgentsCredentials: e.PassAgentsCredentials, Effective: e.Effective,
				PinnedMachineCount: e.PinnedMachineCount, OutdatedMachineCount: e.OutdatedMachineCount,
				SecretFiles: e.SecretFiles, Repositories: e.Repositories,
				Variables: make([]envVarView, 0, len(e.Variables)),
			}
			for _, v := range e.Variables {
				val := envspec.Mask("x")
				if reveal {
					val, err = envs.RevealVariable(cmd.Context(), e.ID, v.Name)
					if err != nil {
						return die(err)
					}
				}
				view.Variables = append(view.Variables, envVarView{Name: v.Name, Value: val})
			}
			if isJSON() {
				return p.JSON(view)
			}
			printEnvView(p, view, reveal)
			return nil
		},
	}
	cmd.Flags().BoolVar(&reveal, "reveal", false, "Print variable values instead of masking them")
	return cmd
}

func printEnvView(p *output.Printer, v envView, reveal bool) {
	def := ""
	if v.IsDefault {
		def = " (default)"
	}
	p.Line("%s%s  latest v%d", v.Name, def, v.LatestVersion)
	p.Line("  Protected: %s", onOff(v.SafeForThirdParties))
	if v.SafeForThirdParties {
		p.Line("    nothing of yours is passed, whatever the toggles below say")
	}
	p.Line("  GitHub:              %s", onOff(v.PassGithub))
	p.Line("  Secrets:             %s", onOff(v.PassSecrets))
	p.Line("  Machine key:         %s", onOff(v.PassSandboxCredentials))
	p.Line("  Agent connections:   %s", onOff(v.PassAgentsCredentials))
	p.Line("  Machines: %d pinned, %d behind the latest version", v.PinnedMachineCount, v.OutdatedMachineCount)
	p.Line("")
	p.Line("Variables (%d)", len(v.Variables))
	for _, x := range v.Variables {
		p.Line("  %s=%s", x.Name, x.Value)
	}
	if !reveal && len(v.Variables) > 0 {
		p.Line("  (values masked; use --reveal to show them)")
	}
	p.Line("")
	p.Line("Secret files (%d)", len(v.SecretFiles))
	for _, f := range v.SecretFiles {
		p.Line("  %s  (%d bytes)", f.Path, f.SizeBytes)
	}
	p.Line("")
	p.Line("Repositories (%d)", len(v.Repositories))
	for _, r := range v.Repositories {
		mode := "non-blocking"
		if r.SetupBlocking {
			mode = "blocking"
		}
		setup := ""
		if r.SetupScript != "" {
			setup = fmt.Sprintf("  setup: %d bytes, %s", len(r.SetupScript), mode)
		}
		p.Line("  %s  branch %s  -> ~/%s%s", r.Repo, orDefault(r.BaseBranch, "(default)"), r.Path, setup)
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// ─── create / rename / delete / default ───────────────────────────────────────

func newEnvCreateCmd() *cobra.Command {
	var safe bool
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create an environment",
		Long: `Create an environment at version 1. It starts with everything injected
(GitHub, secrets, agent connections) unless it is protected.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			in := miosa.CreateEnvironmentInput{Name: args[0]}
			if cmd.Flags().Changed("safe-for-third-parties") {
				in.SafeForThirdParties = &safe
			}
			e, err := envs.Create(cmd.Context(), in)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(summarizeEnv(*e))
			}
			p.Success("Created environment %q (v%d)", e.Name, e.LatestVersion)
			return nil
		},
	}
	cmd.Flags().BoolVar(&safe, "safe-for-third-parties", false, "Pass nothing of yours to machines using this environment")
	return cmd
}

func newEnvRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <name> <new-name>",
		Short: "Rename an environment",
		Long:  `Rename an environment. Machines stay pinned to their versions.`,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			e, err := envs.Rename(cmd.Context(), args[0], args[1])
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(summarizeEnv(*e))
			}
			p.Success("Renamed environment %q to %q", args[0], e.Name)
			return nil
		},
	}
}

func newEnvDeleteCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete an environment",
		Long: `Soft-delete an environment. Machines pinned to its versions keep running;
new machines can no longer use it. The default environment cannot be deleted:
make another one the default first. You are asked to confirm unless --force
is given.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			if !confirm(cmd, force, fmt.Sprintf("Environment %q will no longer be usable by new machines. Machines already pinned to it keep running.", args[0])) {
				return die(errors.New("aborted"))
			}
			if err := envs.Delete(cmd.Context(), args[0]); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"status": "deleted", "environment": args[0]})
			}
			p.Success("Deleted environment %q", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip the confirmation prompt")
	return cmd
}

func newEnvDefaultCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "default <name>",
		Short: "Make an environment the default",
		Long:  `Make an environment the one new machines use when none is named.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			e, err := envs.SetDefault(cmd.Context(), args[0])
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(summarizeEnv(*e))
			}
			p.Success("%q is now the default environment", e.Name)
			return nil
		},
	}
}

// ─── variables ────────────────────────────────────────────────────────────────

// collectVars merges --from-file content with the named variables. A value is
// never taken from an argument: a bare NAME reads its value through secret
// (hidden prompt, or standard input), and NAME=VALUE is refused. Names win over
// the file. Keys keep first-seen order.
func collectVars(args []string, fromFile string, stdin io.Reader, secret func(name string) (string, error)) ([]envspec.Pair, error) {
	var pairs []envspec.Pair
	idx := map[string]int{}
	add := func(k, v string) {
		if i, ok := idx[k]; ok {
			pairs[i].Value = v
			return
		}
		idx[k] = len(pairs)
		pairs = append(pairs, envspec.Pair{Key: k, Value: v})
	}
	if fromFile != "" {
		var r io.Reader = stdin
		if fromFile != "-" {
			b, err := envspec.ReadLimited(fromFile, envspec.MaxSecretFileBytes)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", fromFile, err)
			}
			if err := envspec.ValidateSecretContents(b); err != nil {
				return nil, fmt.Errorf("%s: %w", fromFile, err)
			}
			r = strings.NewReader(string(b))
		}
		fp, err := envspec.ParseDotenv(r)
		if err != nil {
			return nil, fmt.Errorf("parsing env file: %w", err)
		}
		for _, p := range fp {
			add(p.Key, p.Value)
		}
	}
	if len(args) > 1 {
		return nil, errors.New("give one NAME, or load several with --from-file")
	}
	for _, a := range args {
		if strings.Contains(a, "=") {
			return nil, errors.New("a value is not accepted as an argument (it would land in shell history); pass the NAME and enter the value when asked, pipe it on standard input, or use --from-file")
		}
		if err := envspec.ValidateVarName(a); err != nil {
			return nil, err
		}
		v, err := secret(a)
		if err != nil {
			return nil, err
		}
		if v == "" {
			return nil, fmt.Errorf("no value for %s (enter it when asked or pipe it on standard input)", a)
		}
		add(a, v)
	}
	if len(pairs) == 0 {
		return nil, errors.New("nothing to set: pass a NAME or --from-file <path>")
	}
	if len(pairs) > 100 {
		return nil, fmt.Errorf("%d variables; an environment holds at most 100", len(pairs))
	}
	return pairs, nil
}

func newEnvSetVarCmd() *cobra.Command {
	var target envTarget
	var fromFile string
	cmd := &cobra.Command{
		Use:   "set-var [NAME]",
		Short: "Set environment variables",
		Long: `Set one or more variables on an environment. Each call changes only the
variables you name; the rest are untouched. The whole call mints one version
per variable.

A value is never an argument, because arguments land in shell history and
process listings. Give the NAME and enter the value when asked (input hidden),
pipe the value on standard input, or load KEY=VALUE lines with --from-file.

Examples:
  miosa env set-var STRIPE_KEY
  echo "$VALUE" | miosa env set-var --env staging STRIPE_KEY
  miosa env set-var --env staging --from-file .env
  cat .env | miosa env set-var --env staging --from-file -`,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			pairs, err := collectVars(args, fromFile, cmd.InOrStdin(), func(name string) (string, error) {
				return readSecret(cmd, "Value for "+name+" (input hidden): ", false)
			})
			if err != nil {
				return die(err)
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			for i, kv := range pairs {
				if err := envs.SetVariable(cmd.Context(), name, kv.Key, kv.Value); err != nil {
					return die(fmt.Errorf("setting %s (%d of %d applied): %w", kv.Key, i, len(pairs), err))
				}
			}
			keys := make([]string, len(pairs))
			for i, kv := range pairs {
				keys[i] = kv.Key
			}
			if isJSON() {
				return p.JSON(map[string]interface{}{"environment": name, "set": keys})
			}
			p.Success("Set %d variable(s) on %q: %s", len(keys), name, strings.Join(keys, ", "))
			p.Line("Running machines keep their version; run 'miosa env upgrade <machine-id>' to apply.")
			return nil
		},
	}
	target.bind(cmd)
	cmd.Flags().StringVar(&fromFile, "from-file", "", "Read KEY=VALUE lines from a .env file (- for stdin)")
	return cmd
}

func newEnvUnsetVarCmd() *cobra.Command {
	var target envTarget
	cmd := &cobra.Command{
		Use:   "unset-var <KEY>...",
		Short: "Remove environment variables",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			for _, k := range args {
				if err := envspec.ValidateVarName(k); err != nil {
					return die(err)
				}
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			for i, k := range args {
				if err := envs.DeleteVariable(cmd.Context(), name, k); err != nil {
					return die(fmt.Errorf("removing %s (%d of %d applied): %w", k, i, len(args), err))
				}
			}
			if isJSON() {
				return p.JSON(map[string]interface{}{"environment": name, "removed": args})
			}
			p.Success("Removed %d variable(s) from %q: %s", len(args), name, strings.Join(args, ", "))
			return nil
		},
	}
	target.bind(cmd)
	return cmd
}

// ─── secret files ─────────────────────────────────────────────────────────────

func newEnvAddFileCmd() *cobra.Command {
	var target envTarget
	cmd := &cobra.Command{
		Use:   "add-file <relpath> <local-file>",
		Short: "Add or replace a secret file",
		Long: `Write a secret file into an environment. <relpath> is relative to the
machine's home directory; include the repository folder to land a file inside
a clone. <local-file> is read from disk, or from stdin when it is -.

Files are written mode 0600. At most 256KB each.

Example:
  miosa env add-file --env staging hello-world/backend/.env ./backend.env`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			rel, local := args[0], args[1]
			if err := envspec.ValidateSecretPath(rel); err != nil {
				return die(err)
			}
			var b []byte
			var err error
			if local == "-" {
				b, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), envspec.MaxSecretFileBytes+1))
			} else {
				b, err = envspec.ReadLimited(local, envspec.MaxSecretFileBytes)
			}
			if err != nil {
				return die(fmt.Errorf("reading %s: %w", local, err))
			}
			if err := envspec.ValidateSecretContents(b); err != nil {
				return die(err)
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			if err := envs.SetSecretFile(cmd.Context(), name, rel, string(b)); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]interface{}{"environment": name, "path": rel, "size_bytes": len(b)})
			}
			p.Success("Wrote secret file %s (%d bytes) to %q", rel, len(b), name)
			return nil
		},
	}
	target.bind(cmd)
	return cmd
}

func newEnvRmFileCmd() *cobra.Command {
	var target envTarget
	cmd := &cobra.Command{
		Use:   "rm-file <relpath>",
		Short: "Remove a secret file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if err := envspec.ValidateSecretPath(args[0]); err != nil {
				return die(err)
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			if err := envs.DeleteSecretFile(cmd.Context(), name, args[0]); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]interface{}{"environment": name, "removed": args[0]})
			}
			p.Success("Removed secret file %s from %q", args[0], name)
			return nil
		},
	}
	target.bind(cmd)
	return cmd
}

// ─── repositories ─────────────────────────────────────────────────────────────

func newEnvRepoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "Manage repositories cloned into new machines",
	}
	cmd.AddCommand(newEnvRepoAddCmd(), newEnvRepoRmCmd())
	return cmd
}

func newEnvRepoAddCmd() *cobra.Command {
	var (
		target                    envTarget
		branch, setupFile, source string
		blocking, nonBlocking     bool
	)
	cmd := &cobra.Command{
		Use:   "add <owner/repo>",
		Short: "Clone a repository into new machines",
		Long: `Attach a repository to an environment. It is cloned at its base branch into
the machine's home directory, in a folder named after the repository. MIOSA
never creates a branch.

--setup-file attaches a script (UTF-8, up to 64KB) that runs once in the
repository folder when the machine starts. With --blocking the machine is not
ready until it finishes; --non-blocking (the default) runs it alongside
everything else.

Adding a repository that is already attached replaces its settings.

Example:
  miosa env repo add --env staging octocat/hello-world --branch develop \
    --setup-file ./setup.sh --blocking`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if err := envspec.ValidateRepo(args[0]); err != nil {
				return die(err)
			}
			if source != "github" && source != "forge" {
				return die(fmt.Errorf("invalid --source %q: must be github or forge", source))
			}
			in := miosa.AddRepositoryInput{Repo: args[0], Source: source, BaseBranch: branch}
			if setupFile != "" {
				script, err := envspec.ReadSetupFile(setupFile)
				if err != nil {
					return die(err)
				}
				in.SetupScript = script
			}
			if (blocking || nonBlocking) && setupFile == "" {
				return die(errors.New("--blocking and --non-blocking apply to a setup script: pass --setup-file"))
			}
			if blocking || nonBlocking {
				b := blocking
				in.SetupBlocking = &b
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			if err := envs.AddRepository(cmd.Context(), name, in); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]interface{}{"environment": name, "repository": in.Repo, "base_branch": in.BaseBranch})
			}
			p.Success("Added %s to %q", in.Repo, name)
			return nil
		},
	}
	target.bind(cmd)
	cmd.Flags().StringVar(&branch, "branch", "", "Base branch to clone (default: the repository's default branch)")
	cmd.Flags().StringVar(&setupFile, "setup-file", "", "Script to run in the repository folder at machine start (max 64KB)")
	cmd.Flags().StringVar(&source, "source", "github", "Repository source: github or forge")
	cmd.Flags().BoolVar(&blocking, "blocking", false, "Machine is not ready until the setup script finishes")
	cmd.Flags().BoolVar(&nonBlocking, "non-blocking", false, "Setup script runs alongside everything else (default)")
	cmd.MarkFlagsMutuallyExclusive("blocking", "non-blocking")
	return cmd
}

func newEnvRepoRmCmd() *cobra.Command {
	var target envTarget
	cmd := &cobra.Command{
		Use:     "rm <owner/repo>",
		Aliases: []string{"remove"},
		Short:   "Stop cloning a repository into new machines",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if err := envspec.ValidateRepo(args[0]); err != nil {
				return die(err)
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			if err := envs.RemoveRepository(cmd.Context(), name, args[0]); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]interface{}{"environment": name, "removed": args[0]})
			}
			p.Success("Removed %s from %q", args[0], name)
			return nil
		},
	}
	target.bind(cmd)
	return cmd
}

// ─── toggles ──────────────────────────────────────────────────────────────────

// toggleSections maps the CLI section name to its setter on the update input.
var toggleSections = map[string]func(*miosa.UpdateEnvironmentInput, *bool){
	"github":             func(in *miosa.UpdateEnvironmentInput, v *bool) { in.PassGithub = v },
	"secrets":            func(in *miosa.UpdateEnvironmentInput, v *bool) { in.PassSecrets = v },
	"miosa-credentials":  func(in *miosa.UpdateEnvironmentInput, v *bool) { in.PassSandboxCredentials = v },
	"agents-credentials": func(in *miosa.UpdateEnvironmentInput, v *bool) { in.PassAgentsCredentials = v },
	// The names the product uses; the two above remain accepted.
	"machine-key":       func(in *miosa.UpdateEnvironmentInput, v *bool) { in.PassSandboxCredentials = v },
	"agent-connections": func(in *miosa.UpdateEnvironmentInput, v *bool) { in.PassAgentsCredentials = v },
}

func toggleSectionNames() string {
	names := make([]string, 0, len(toggleSections))
	for k := range toggleSections {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}

func newEnvToggleCmd() *cobra.Command {
	var target envTarget
	cmd := &cobra.Command{
		Use:   "toggle <github|secrets|machine-key|agent-connections> <on|off>",
		Short: "Choose what a machine receives from your account",
		Long: `Switch one of the four sections an environment can pass to a machine:

  github              repositories cloned in, plus the GitHub token
  secrets             variables and secret files
  machine-key         a key scoped to the machine's own lifecycle
  agent-connections   your Claude, Codex and OSA connections

Turning a section off keeps what you typed; it applies again when switched on.
Toggles are ignored while the environment is protected.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			set, ok := toggleSections[args[0]]
			if !ok {
				return die(fmt.Errorf("unknown section %q: must be one of %s", args[0], toggleSectionNames()))
			}
			val, err := envspec.ParseOnOff(args[1])
			if err != nil {
				return die(err)
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			var in miosa.UpdateEnvironmentInput
			set(&in, &val)
			e, err := envs.Update(cmd.Context(), name, in)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(summarizeEnv(*e))
			}
			p.Success("%s is now %s on %q (v%d)", args[0], onOff(val), e.Name, e.LatestVersion)
			return nil
		},
	}
	target.bind(cmd)
	return cmd
}

func newEnvSafeCmd() *cobra.Command {
	var target envTarget
	cmd := &cobra.Command{
		Use:   "safe-third-parties <on|off>",
		Short: "Pass nothing of yours to machines using this environment",
		Long: `The master switch. On: machines other people drive receive no GitHub access,
no secrets, no machine key and no agent connections, whatever the section toggles say.
Off: the four toggles apply.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			val, err := envspec.ParseOnOff(args[0])
			if err != nil {
				return die(err)
			}
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			name, err := target.resolve(cmd, envs)
			if err != nil {
				return die(err)
			}
			e, err := envs.Update(cmd.Context(), name, miosa.UpdateEnvironmentInput{SafeForThirdParties: &val})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(summarizeEnv(*e))
			}
			p.Success("Protected is now %s on %q (v%d)", onOff(val), e.Name, e.LatestVersion)
			return nil
		},
	}
	target.bind(cmd)
	return cmd
}

// ─── versions / upgrade ───────────────────────────────────────────────────────

func newEnvVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <name>",
		Short: "List an environment's versions and how many machines sit on each",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			_, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			vs, err := envs.Versions(cmd.Context(), args[0])
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]interface{}{"versions": vs})
			}
			if len(vs) == 0 {
				p.Line("No versions.")
				return nil
			}
			rows := make([][]string, 0, len(vs))
			for _, v := range vs {
				rows = append(rows, []string{
					fmt.Sprintf("v%d", v.Version),
					fmt.Sprintf("%d", v.PinnedMachineCount),
					fmt.Sprintf("%d", len(v.VariableNames)),
					fmt.Sprintf("%d", len(v.SecretFilePaths)),
					fmt.Sprintf("%d", len(v.Repositories)),
					onOff(v.SafeForThirdParties),
					v.CreatedAt,
				})
			}
			p.Table([]string{"VERSION", "MACHINES", "VARS", "FILES", "REPOS", "SAFE-3P", "CREATED"}, rows)
			return nil
		},
	}
}

func newEnvUpgradeCmd() *cobra.Command {
	var all string
	var force bool
	cmd := &cobra.Command{
		Use:   "upgrade [name|id]...",
		Short: "Move machines onto their environment's latest version",
		Long: `Upgrade machines to the latest version of the environment they are pinned to.
Running machines get the new configuration now; stopped ones pick it up when
they resume. Nothing upgrades on its own.

This is not reversible on the machine's disk: a secret the new version
withholds is deleted from the machine, not hidden. You are asked to confirm
unless --force is given.

With no machine it upgrades the current sandbox (see 'miosa use').

  miosa env upgrade my-box            one machine
  miosa env upgrade                   the current sandbox
  miosa env upgrade --all staging     every machine behind on "staging"`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			if all != "" && len(args) > 0 {
				return die(errors.New("pass machines or --all <environment>, not both"))
			}
			c, envs, err := envClient(cmd)
			if err != nil {
				return die(err)
			}
			if all != "" {
				if !confirmUpgrade(cmd, force, fmt.Sprintf("every machine behind on environment %q", all)) {
					return die(errors.New("aborted"))
				}
				res, err := envs.Upgrade(cmd.Context(), all, nil)
				if err != nil {
					return die(err)
				}
				return printUpgrade(p, []*miosa.EnvironmentUpgradeResult{res})
			}
			if len(args) == 0 {
				cfg, lerr := config.Load()
				if lerr != nil {
					return die(lerr)
				}
				current, rerr := requireSandbox("", cfg.CurrentSandbox)
				if rerr != nil {
					return die(rerr)
				}
				args = []string{current}
			}
			// Group machines by environment; one upgrade call per environment.
			var order []string
			groups := map[string][]string{}
			for _, id := range args {
				m, err := lookupMachineEnv(cmd, c.SDK, id)
				if err != nil {
					return die(err)
				}
				if m.EnvironmentID == "" {
					return die(fmt.Errorf("machine %s is not pinned to an environment", id))
				}
				if !m.EnvironmentUpgradeAvailable {
					if !isJSON() {
						p.Line("%s is already on %s v%d", id, m.Environment, m.EnvironmentVersion)
					}
					continue
				}
				if _, seen := groups[m.EnvironmentID]; !seen {
					order = append(order, m.EnvironmentID)
				}
				groups[m.EnvironmentID] = append(groups[m.EnvironmentID], lookupComputerID(id))
			}
			var results []*miosa.EnvironmentUpgradeResult
			for _, envID := range order {
				if !confirmUpgrade(cmd, force, strings.Join(groups[envID], ", ")) {
					return die(errors.New("aborted"))
				}
				res, err := envs.Upgrade(cmd.Context(), envID, groups[envID])
				if err != nil {
					return die(err)
				}
				results = append(results, res)
			}
			return printUpgrade(p, results)
		},
	}
	cmd.Flags().StringVar(&all, "all", "", "Upgrade every machine that is behind on this environment")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Skip the confirmation prompt")
	return cmd
}

// confirmUpgrade asks before an irreversible upgrade. JSON mode and --force
// skip the prompt, like 'miosa destroy'.
func confirmUpgrade(cmd *cobra.Command, force bool, what string) bool {
	return confirm(cmd, force, fmt.Sprintf("Upgrading %s deletes any secret the new version withholds from the machine. This cannot be undone.", what))
}

// confirm prints msg and requires an explicit yes. EOF or anything else aborts.
func confirm(cmd *cobra.Command, force bool, msg string) bool {
	if force || isJSON() {
		return true
	}
	fmt.Fprintln(cmd.OutOrStdout(), msg)
	fmt.Fprint(cmd.OutOrStdout(), "Continue? [y/N] ")
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func printUpgrade(p *output.Printer, results []*miosa.EnvironmentUpgradeResult) error {
	if isJSON() {
		if results == nil {
			results = []*miosa.EnvironmentUpgradeResult{}
		}
		return p.JSON(map[string]interface{}{"upgrades": results})
	}
	n := 0
	for _, r := range results {
		for _, m := range r.Machines {
			n++
			p.Line("  %s %s  v%d -> v%d  %s", m.Kind, m.ID, m.FromVersion, m.ToVersion, m.Status)
		}
	}
	if n == 0 {
		p.Line("Nothing to upgrade.")
		return nil
	}
	p.Success("Upgraded %d machine(s)", n)
	p.Line("Secrets the new version withholds were deleted from the machines.")
	return nil
}

// lookupMachineEnv reads a machine's environment block, trying sandboxes and
// then computers.
func lookupMachineEnv(cmd *cobra.Command, c *miosa.Client, id string) (miosa.MachineEnvironment, error) {
	sb, err := c.Sandboxes.Get(cmd.Context(), lookupComputerID(id))
	if err == nil {
		return sb.MachineEnvironment, nil
	}
	var nf *miosa.NotFoundError
	if !errors.As(err, &nf) {
		return miosa.MachineEnvironment{}, err
	}
	comp, cerr := c.Computers.Get(cmd.Context(), id)
	if cerr != nil {
		return miosa.MachineEnvironment{}, fmt.Errorf("machine %q not found as a sandbox or computer: %w", id, cerr)
	}
	return comp.MachineEnvironment, nil
}
