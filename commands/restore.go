package commands

import (
	"github.com/spf13/cobra"
)

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore [name|id] <checkpoint-id>",
		Short: "Restore a sandbox from a checkpoint",
		Long: `Create a new sandbox from a checkpoint snapshot.

The original sandbox is not modified — a new computer is provisioned
branched from the checkpoint state.

Example:
  miosa restore my-box cp_abc123
  miosa restore cp_abc123        # uses current sandbox`,
		Args: cobra.RangeArgs(1, 2),
		RunE: runRestore,
	}
}

func runRestore(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	var nameOrID, checkpointID string
	if len(args) == 2 {
		nameOrID = args[0]
		checkpointID = args[1]
	} else {
		checkpointID = args[0]
	}

	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	computer, err := c.Checkpoints.Restore(cmd.Context(), lookupComputerID(nameOrID), checkpointID)
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(computer)
	}
	p.Success("Restored checkpoint %s → new sandbox %q (%s)", checkpointID, computer.Name, computer.ID)
	return nil
}
