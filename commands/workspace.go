package commands

import (
	"fmt"
	"net/url"
	"sort"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/spf13/cobra"
)

func newWorkspaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage workspaces",
		Long:  `Workspaces group machines, deployments and data inside your organization. <workspace> is a slug, name or id.`,
	}

	cmd.AddCommand(
		newWorkspaceCreateCmd(),
		newWorkspaceListCmd(),
		newWorkspaceDeleteCmd(),
	)
	cmd.AddCommand(workspaceOps()...)
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
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <workspace>",
		Short: "Delete a workspace (asks you to type its name)",
		Long: `Deletes the workspace. On a terminal it shows the workspace and asks you to
type its name to confirm. Off a terminal (scripts, CI) it refuses unless --yes
is given. <workspace> is its name, slug or id.`,
		Example: "  miosa workspace delete acme\n  miosa workspace delete acme --yes",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkspaceDelete(cmd, args, yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Delete without the typed confirmation")
	return cmd
}

func runWorkspaceDelete(cmd *cobra.Command, args []string, yes bool) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}
	var ws struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Get(cmd.Context(), "/workspaces/"+url.PathEscape(args[0]), nil, &ws); err != nil {
		return die(fmt.Errorf("workspace %q: %w", args[0], err))
	}
	id, _ := ws.Data["id"].(string)
	name, _ := ws.Data["name"].(string)
	slug, _ := ws.Data["slug"].(string)
	label := firstNonEmpty(name, slug, args[0])
	if !yes {
		if err := confirmByTypedName(cmd, fmt.Sprintf("Delete workspace %q (%s)?", label, shortRef(id)), label, slug); err != nil {
			return err
		}
	}
	target := firstNonEmpty(id, args[0])
	resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "DELETE", Path: "/workspaces/" + url.PathEscape(target)})
	if err != nil {
		return die(err)
	}
	// The server lists the leftover rows it let go of (deleted volumes, old cron jobs and so on).
	released, _ := lookupPath(resp, "released").(map[string]any)

	if isJSON() {
		return p.JSON(map[string]any{"status": "deleted", "id": target, "released": released})
	}
	p.Success("Deleted workspace %q", label)
	keys := make([]string, 0, len(released))
	for k := range released {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(p.Writer(), "  released %v %s\n", released[k], k)
	}
	return nil
}
