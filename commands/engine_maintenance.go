package commands

import (
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

// Engine backups, restore and delete. These are the managed database routes
// under the Engine's own names, so an operator never has to remember that an
// Engine is a database row: every command resolves a name among managed
// Engines only.

type engineBackupRef struct {
	ID           string `json:"id"`
	State        string `json:"state"`
	BackupType   string `json:"backup_type"`
	SizeBytes    int64  `json:"size_bytes"`
	CreatedAt    string `json:"created_at"`
	ErrorMessage string `json:"error_message"`
}

func newEngineBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup <engine>",
		Short: "Take a backup of a managed Optimal Engine now",
		Long: `Starts a backup of the Engine's data volume and prints its id and state.

  miosa engine backup qa-brain
  miosa engine backups qa-brain          # what exists already`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineBackup(cmd, args[0])
		},
	}
	return cmd
}

func newEngineBackupsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "backups <engine>",
		Aliases: []string{"backup-list"},
		Short:   "List a managed Optimal Engine's backups",
		Long: `Lists the Engine's backups, newest first, with the state each one reached.

  miosa engine backups qa-brain
  miosa engine backups qa-brain --json | jq '.data[] | {id, state, created_at}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineBackups(cmd, args[0])
		},
	}
	return cmd
}

func newEngineRestoreCmd() *cobra.Command {
	var backup, toNew string
	var yes bool

	cmd := &cobra.Command{
		Use:   "recover <engine>",
		Short: "Restore a backup into the Engine, or into a new one",
		Long: `Restores a backup, either over the Engine it came from or into a NEW Engine
(--to-new), which never touches the source.

  miosa engine recover qa-brain --to-new qa-brain-restored
  miosa engine recover qa-brain --backup 778439c0 --yes

--backup is a backup id (see 'miosa engine backups'); without it the newest
completed backup is used. Restoring over the Engine replaces its data, so it
asks first (or pass --yes).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDBRestore(cmd, args[0], backup, toNew, yes)
		},
	}
	cmd.Flags().StringVar(&backup, "backup", "", "Backup id (default: the newest completed backup)")
	cmd.Flags().StringVar(&toNew, "to-new", "", "Create a new Engine with this name and restore into it")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask before restoring over the source")
	return cmd
}

// The retired spelling stays as a hidden deprecated command: the guard skips
// hidden commands, and an existing script keeps working with a warning.
func newEngineRestoreDeprecatedCmd() *cobra.Command {
	var backup, toNew string
	var yes bool

	cmd := &cobra.Command{
		Use:        "restore <engine>",
		Hidden:     true,
		Deprecated: "use \"miosa engine recover\"",
		Args:       cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDBRestore(cmd, args[0], backup, toNew, yes)
		},
	}
	cmd.Flags().StringVar(&backup, "backup", "", "Backup id (default: the newest completed backup)")
	cmd.Flags().StringVar(&toNew, "to-new", "", "Create a new Engine with this name and restore into it")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask before restoring over the source")
	return cmd
}

func newEngineRmCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "rm <engine>",
		Aliases: []string{"delete", "destroy"},
		Short:   "Delete a managed Optimal Engine",
		Long: `Deletes the Engine and its data volume. This cannot be undone; take a backup
first if the data matters ('miosa engine backup'). It asks unless --yes is
given, and a non-interactive run without --yes refuses instead of deleting.

  miosa engine rm qa-brain
  miosa engine rm qa-brain --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineRm(cmd, args[0], yes)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Delete without asking")
	return cmd
}

func runEngineBackup(cmd *cobra.Command, ref string) error {
	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	engine, err := resolveManagedEngine(ctx, c.API.Get, ref)
	if err != nil {
		return die(err)
	}

	var resp struct {
		Data engineBackupRef `json:"data"`
	}
	if err := c.API.Post(ctx, "/databases/"+url.PathEscape(engine.ID)+"/backups", map[string]any{}, &resp); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(resp.Data)
	}
	p.Success("%s backup %s is %s", engine.Name, shortRef(resp.Data.ID), resp.Data.State)
	return nil
}

func runEngineBackups(cmd *cobra.Command, ref string) error {
	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	engine, err := resolveManagedEngine(ctx, c.API.Get, ref)
	if err != nil {
		return die(err)
	}

	var resp struct {
		Data []engineBackupRef `json:"data"`
	}
	if err := c.API.Get(ctx, "/databases/"+url.PathEscape(engine.ID)+"/backups", nil, &resp); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(resp.Data)
	}
	if len(resp.Data) == 0 {
		p.Line("%s has no backups yet; 'miosa engine backup %s' takes one", engine.Name, engine.Name)
		return nil
	}
	p.Line("BACKUP\tSTATE\tTYPE\tBYTES\tCREATED")
	for _, backup := range resp.Data {
		p.Line("%s\t%s\t%s\t%d\t%s", shortRef(backup.ID), backup.State, backup.BackupType, backup.SizeBytes, backup.CreatedAt)
	}
	return nil
}

func runEngineRm(cmd *cobra.Command, ref string, yes bool) error {
	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	engine, err := resolveManagedEngine(ctx, c.API.Get, ref)
	if err != nil {
		return die(err)
	}

	if !yes {
		if err := confirmDestructive(cmd, fmt.Sprintf("Delete %s (%s) and its data volume?", engine.Name, shortRef(engine.ID))); err != nil {
			return die(err)
		}
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := c.API.Delete(ctx, "/databases/"+url.PathEscape(engine.ID), nil, &resp); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(resp.Data)
	}
	p.Success("%s deleted", engine.Name)
	return nil
}
