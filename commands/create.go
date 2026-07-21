package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	miosa "github.com/Miosa-osa/miosa-go"
)

func newCreateCmd() *cobra.Command {
	var (
		size      string
		template  string
		workspace string
	)

	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new sandbox",
		Long: `Provision a new MIOSA sandbox.

If no name is provided, one is generated automatically.

Examples:
  miosa create my-box
  miosa create my-box --size medium --template miosa-sandbox
  miosa create --size large`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCreate(cmd, args, size, template, workspace)
		},
	}

	cmd.Flags().StringVar(&size, "size", "small", "Sandbox size: xs, small, medium, large, or xl")
	cmd.Flags().StringVar(&template, "template", "miosa-sandbox", "Sandbox template, for example miosa-sandbox, nextjs, fastapi, or hono")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace slug to assign the sandbox to")

	return cmd
}

func runCreate(cmd *cobra.Command, args []string, size, template, workspace string) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	_ = cfg

	var name string
	if len(args) > 0 {
		name = args[0]
	}

	var sz miosa.ComputerSize
	switch size {
	case "xs":
		sz = miosa.SizeXS
	case "small":
		sz = miosa.SizeSmall
	case "medium":
		sz = miosa.SizeMedium
	case "large":
		sz = miosa.SizeLarge
	case "xl", "xlarge":
		sz = miosa.SizeXL
	default:
		return die(fmt.Errorf("invalid size %q: must be xs, small, medium, large, or xl", size))
	}

	input := miosa.CreateSandboxInput{
		Name:       name,
		TemplateID: template,
		Size:       sz,
	}
	if workspace != "" {
		input.Metadata = map[string]string{"workspace": workspace}
	}

	if !isJSON() {
		p.Line("Creating sandbox…")
	}

	sandbox, err := c.SDK.Sandboxes.Create(cmd.Context(), input)
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(sandbox.SandboxData)
	}

	p.Success("Created sandbox %q (%s)", sandbox.Name, sandbox.ID)
	p.Line("  Status:   %s", sandbox.State)
	p.Line("  Size:     %s", sandbox.Size)
	p.Line("  Template: %s", sandbox.TemplateID)
	p.Line("")
	p.Line("Run 'miosa use %s' to set it as your default.", sandbox.Name)
	return nil
}
