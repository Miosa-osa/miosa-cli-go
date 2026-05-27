package commands

import (
	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspaces",
		Long:  `Create, list, and delete workspaces.`,
	}

	cmd.AddCommand(
		newWorkspaceCreateCmd(),
		newWorkspaceListCmd(),
		newWorkspaceDeleteCmd(),
	)
	return cmd
}

func newWorkspaceCreateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new workspace",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkspaceCreate,
	}
}

func runWorkspaceCreate(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	ws, err := c.Workspaces.Create(cmd.Context(), args[0])
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(ws)
	}
	p.Success("Created workspace %q (%s)", ws.Name, ws.Slug)
	return nil
}

func newWorkspaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List workspaces",
		RunE:    runWorkspaceList,
	}
}

func runWorkspaceList(cmd *cobra.Command, _ []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	workspaces, err := c.Workspaces.List(cmd.Context())
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(workspaces)
	}

	headers := []string{"SLUG", "NAME", "ID"}
	rows := make([][]string, 0, len(workspaces))
	for _, ws := range workspaces {
		rows = append(rows, []string{ws.Slug, ws.Name, ws.ID})
	}
	p.Table(headers, rows)
	return nil
}

func newWorkspaceDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a workspace",
		Args:  cobra.ExactArgs(1),
		RunE:  runWorkspaceDelete,
	}
}

func runWorkspaceDelete(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	if err := c.Workspaces.Delete(cmd.Context(), args[0]); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(map[string]string{"status": "deleted", "id": args[0]})
	}
	p.Success("Deleted workspace %q", args[0])
	return nil
}
