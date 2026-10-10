package commands

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/envspec"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
	miosa "github.com/Miosa-osa/miosa-go"
)

// machineEnvFlags are the environment and setup flags shared by every command
// that creates a machine.
type machineEnvFlags struct {
	env       string
	noEnv     bool
	setupFile string
}

func (f *machineEnvFlags) bind(cmd *cobra.Command, noun string) {
	cmd.Flags().StringVar(&f.env, "env", "", "Environment the "+noun+" starts from (default: your default environment)")
	cmd.Flags().BoolVar(&f.noEnv, "no-env", false, "Pass nothing of yours to the "+noun+" (safe to hand to third parties)")
	cmd.Flags().StringVar(&f.setupFile, "setup-file", "", "Shell script (UTF-8, max 64KB) run in the background once the "+noun+" is ready")
	cmd.MarkFlagsMutuallyExclusive("env", "no-env")
}

// resolve validates the flags and reads the setup file. It touches no network.
func (f *machineEnvFlags) resolve() (env string, noEnv bool, setup string, err error) {
	if f.env != "" && f.noEnv {
		return "", false, "", errors.New("--env and --no-env cannot be combined")
	}
	if f.setupFile != "" {
		setup, err = envspec.ReadSetupFile(f.setupFile)
		if err != nil {
			return "", false, "", err
		}
	}
	return f.env, f.noEnv, setup, nil
}

// printMachineEnv prints the environment and setup block of a machine.
func printMachineEnv(p *output.Printer, m miosa.MachineEnvironment) {
	switch {
	case m.Environment == "" && m.EnvironmentID == "":
		p.Line("  Env:      (none - machine predates environments)")
	default:
		p.Line("  Env:      %s v%s", m.Environment, versionLabel(m))
		if m.EnvironmentProtected {
			p.Line("            protected: nothing of yours is passed")
		}
		if m.EnvironmentUpgradeAvailable {
			p.Line("            upgrade available: v%d -> v%d (miosa env upgrade <id>)", m.EnvironmentVersion, m.EnvironmentLatestVersion)
		} else {
			p.Line("            up to date")
		}
		if m.EnvironmentStatus != "" {
			p.Line("            status: %s", m.EnvironmentStatus)
		}
		if m.EnvironmentError != "" {
			p.Line("            error: %s", m.EnvironmentError)
		}
	}
	if m.SetupStatus != "" {
		p.Line("  Setup:    %s", m.SetupStatus)
		if m.SetupError != "" {
			p.Line("            error: %s", m.SetupError)
		}
	}
}

func versionLabel(m miosa.MachineEnvironment) string {
	if m.EnvironmentVersion == 0 {
		return "?"
	}
	return fmt.Sprintf("%d", m.EnvironmentVersion)
}

// ─── info ─────────────────────────────────────────────────────────────────────

func newInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "info [name|id]",
		Aliases: []string{"status"},
		Short:   "Show a machine's state, environment and setup status",
		Long: `Show a sandbox (or computer): state, size, the environment and version it
is pinned to, whether an upgrade is available, and the status and error of its
setup script.

Example:
  miosa info my-box
  miosa info            # uses current sandbox`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			nameOrID := ""
			if len(args) > 0 {
				nameOrID = args[0]
			}
			nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			id := lookupComputerID(nameOrID)
			sb, err := c.SDK.Sandboxes.Get(cmd.Context(), id)
			if err != nil {
				var nf *miosa.NotFoundError
				if !errors.As(err, &nf) {
					return die(err)
				}
				comp, cerr := c.SDK.Computers.Get(cmd.Context(), id)
				if cerr != nil {
					return die(err)
				}
				if isJSON() {
					return p.JSON(comp.ComputerData)
				}
				p.Line("%s (%s)", comp.Name, comp.ID)
				p.Line("  Kind:     computer")
				p.Line("  Status:   %s", comp.Status)
				p.Line("  Size:     %s", comp.Size)
				printMachineEnv(p, comp.MachineEnvironment)
				return nil
			}
			if isJSON() {
				return p.JSON(sb.SandboxData)
			}
			p.Line("%s (%s)", sb.Name, sb.ID)
			p.Line("  Kind:     sandbox")
			p.Line("  Status:   %s", sb.State)
			p.Line("  Size:     %s", sb.Size)
			p.Line("  Template: %s", sb.TemplateID)
			printMachineEnv(p, sb.MachineEnvironment)
			return nil
		},
	}
}
