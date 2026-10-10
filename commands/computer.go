package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"
)

func newComputerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "computer",
		Short: "Manage computers",
	}
	cmd.AddCommand(newComputerCreateCmd())
	cmd.AddCommand(computerOps()...)
	return cmd
}

func newComputerCreateCmd() *cobra.Command {
	var (
		size     string
		template string
		envFlags machineEnvFlags
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a computer",
		Long: `Provision a MIOSA computer (a persistent desktop machine).

--env picks the environment (repositories, secrets, connections) the computer
starts from; without it your default environment is used. --no-env passes
nothing of yours. --setup-file runs a script (UTF-8, up to 64KB) in the
background once the computer is ready.

Examples:
  miosa computer create my-desktop
  miosa computer create my-desktop --env staging --setup-file ./setup.sh`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			envName, noEnv, setup, err := envFlags.resolve()
			if err != nil {
				return die(err)
			}
			sz, err := parseComputerSize(size)
			if err != nil {
				return die(err)
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if !isJSON() {
				p.Line("Creating computer…")
			}
			comp, err := c.SDK.Computers.Create(cmd.Context(), miosa.CreateComputerInput{
				Name:         args[0],
				TemplateType: template,
				Size:         sz,
				Environment:  envName,
				NoEnv:        noEnv,
				SetupFile:    setup,
			})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(comp.ComputerData)
			}
			p.Success("Created computer %q (%s)", comp.Name, comp.ID)
			p.Line("  Status:   %s", comp.Status)
			p.Line("  Size:     %s", comp.Size)
			if envName != "" || noEnv || comp.Environment != "" || setup != "" {
				printMachineEnv(p, comp.MachineEnvironment)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&size, "size", "small", "Computer size: xs, small, medium, large, or xl")
	cmd.Flags().StringVar(&template, "template", "", "Computer template type (default: server default)")
	envFlags.bind(cmd, "computer")
	return cmd
}

func parseComputerSize(size string) (miosa.ComputerSize, error) {
	switch size {
	case "xs":
		return miosa.SizeXS, nil
	case "small":
		return miosa.SizeSmall, nil
	case "medium":
		return miosa.SizeMedium, nil
	case "large":
		return miosa.SizeLarge, nil
	case "xl", "xlarge":
		return miosa.SizeXL, nil
	}
	return "", fmt.Errorf("invalid size %q: must be xs, small, medium, large, or xl", size)
}
