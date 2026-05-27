package commands

import (
	"github.com/spf13/cobra"
)

func newCheckpointCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Manage sandbox checkpoints",
		Long:  `Create, list, inspect, and delete checkpoints for a sandbox.`,
	}

	cmd.AddCommand(
		newCheckpointCreateCmd(),
		newCheckpointListCmd(),
		newCheckpointInfoCmd(),
		newCheckpointDeleteCmd(),
	)
	return cmd
}

func newCheckpointCreateCmd() *cobra.Command {
	var comment string
	cmd := &cobra.Command{
		Use:   "create [name|id]",
		Short: "Create a checkpoint",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runCheckpointCreate(c, args, comment)
		},
	}
	cmd.Flags().StringVar(&comment, "comment", "", "Optional description for the checkpoint")
	return cmd
}

func runCheckpointCreate(cmd *cobra.Command, args []string, comment string) error {
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

	cp, err := c.Checkpoints.Create(cmd.Context(), lookupComputerID(nameOrID), comment)
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(cp)
	}
	p.Success("Checkpoint created: %s", cp.ID)
	return nil
}

func newCheckpointListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list [name|id]",
		Aliases: []string{"ls"},
		Short:   "List checkpoints for a sandbox",
		Args:    cobra.MaximumNArgs(1),
		RunE:    runCheckpointList,
	}
}

func runCheckpointList(cmd *cobra.Command, args []string) error {
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

	cps, err := c.Checkpoints.List(cmd.Context(), lookupComputerID(nameOrID))
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(cps)
	}

	headers := []string{"ID", "COMMENT", "CREATED"}
	rows := make([][]string, 0, len(cps))
	for _, cp := range cps {
		rows = append(rows, []string{cp.ID, cp.Comment, cp.CreatedAt.String()})
	}
	p.Table(headers, rows)
	return nil
}

func newCheckpointInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <checkpoint-id>",
		Short: "Show details for a checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE:  runCheckpointInfo,
	}
}

func runCheckpointInfo(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	cp, err := c.Checkpoints.Get(cmd.Context(), args[0])
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(cp)
	}
	p.Line("ID:         %s", cp.ID)
	p.Line("Sandbox:    %s", cp.ComputerID)
	p.Line("Comment:    %s", cp.Comment)
	p.Line("Created:    %s", cp.CreatedAt)
	return nil
}

func newCheckpointDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <checkpoint-id>",
		Short: "Delete a checkpoint",
		Args:  cobra.ExactArgs(1),
		RunE:  runCheckpointDelete,
	}
}

func runCheckpointDelete(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)
	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	if err := c.Checkpoints.Delete(cmd.Context(), args[0]); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(map[string]string{"status": "deleted", "id": args[0]})
	}
	p.Success("Deleted checkpoint %s", args[0])
	return nil
}
