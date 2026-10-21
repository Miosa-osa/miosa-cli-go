package commands

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// newDBRestoreCmd restores a backup, either over the database it came from or
// into a NEW database (--to-new), which never touches the source.
func newDBRestoreCmd() *cobra.Command {
	var backup, toNew string
	var yes bool

	cmd := &cobra.Command{
		Use:   "recover <database>",
		Short: "Restore a backup into the database, or into a new one",
		Long: `Restore a backup (also available as "miosa db restore").

  miosa db recover app-db --to-new app-db-restored     # safe: a new database, the source is untouched
  miosa db recover app-db --backup 778439c0 --to-new app-db-restored
  miosa db recover app-db --backup 778439c0 --yes      # over app-db itself

--backup is a backup id (see 'miosa db backups'); without it the newest completed
backup is used. --to-new creates a database of the same engine, version and size
in the same workspace, waits for it to run, then restores into it. Restoring over
the source replaces its data, so it asks first (or pass --yes).

A Redis restore is a merge: keys in the backup replace the same-named keys and
other keys stay. A backup only restores into a database of the same engine.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDBRestore(cmd, args[0], backup, toNew, yes)
		},
	}
	cmd.Flags().StringVar(&backup, "backup", "", "Backup id (default: the newest completed backup)")
	cmd.Flags().StringVar(&toNew, "to-new", "", "Create a new database with this name and restore into it")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask before restoring over the source")
	return cmd
}

func runDBRestore(cmd *cobra.Command, ref, backup, toNew string, yes bool) error {
	c, _, err := buildClientTimeout(0)
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	path, err := c.API.RewritePath(ctx, "/databases/"+url.PathEscape(ref))
	if err != nil {
		return die(err)
	}
	var src dbRef
	if err := c.API.Get(ctx, path, nil, &src); err != nil {
		return die(err)
	}
	source := map[string]any{
		"engine": src.Engine, "engine_version": src.EngineVersion, "cpu_count": src.CPUCount,
		"memory_mb": src.MemoryMB, "storage_mb": src.StorageMB, "workspace_id": src.WorkspaceID,
	}
	sourceID := src.ID

	if backup == "" {
		backup, err = newestCompletedBackup(ctx, c.API.Get, sourceID)
		if err != nil {
			return die(err)
		}
	} else if !api.IsUUID(backup) {
		// 'miosa db backups' prints short ids; the restore route takes the
		// full id, so resolve the prefix against this database's backups.
		backup, err = resolveBackupRef(ctx, c.API.Get, sourceID, backup)
		if err != nil {
			return die(err)
		}
	}

	targetID := sourceID
	body := map[string]any{"backup_id": backup}
	if toNew != "" {
		body["allow_overwrite"] = true
		targetID, err = createRestoreTarget(ctx, cmd, c.API, source, toNew)
		if err != nil {
			return die(err)
		}
	} else {
		if !yes {
			if err := confirmDestructive(cmd, fmt.Sprintf("Restore backup %s over database %s? Its current data is replaced", shortRef(backup), ref)); err != nil {
				return err
			}
		}
		body["allow_in_place"] = true
		body["allow_overwrite"] = true
	}

	var started struct {
		ID string `json:"id"`
	}
	if err := c.API.Post(ctx, "/databases/"+url.PathEscape(targetID)+"/restore", body, &started); err != nil {
		return die(err)
	}
	if !isJSON() {
		p.Line("Restoring backup %s into %s ...", shortRef(backup), orDefault(toNew, ref))
	}
	result, err := waitForRestore(ctx, c.API.Get, targetID, started.ID)
	if err != nil {
		return die(err)
	}
	if isJSON() {
		return p.JSON(map[string]any{"restore": result, "database_id": targetID, "backup_id": backup})
	}
	p.Success("Restored backup %s into %s (%s)", shortRef(backup), orDefault(toNew, ref), targetID)
	return nil
}

// dbRef is the flat shape of a database response (the API answers it unwrapped).
type dbRef struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Engine        string `json:"engine"`
	EngineVersion string `json:"engine_version"`
	MemoryMB      int    `json:"memory_mb"`
	CPUCount      int    `json:"cpu_count"`
	StorageMB     int    `json:"storage_mb"`
	WorkspaceID   string `json:"workspace_id"`
	State         string `json:"state"`
	LastError     any    `json:"last_error"`
}

type apiGetter func(ctx context.Context, path string, q url.Values, out any) error

// resolveBackupRef turns a short (or partial) backup id into the full id the
// restore route requires, using the source database's own backup list.
func resolveBackupRef(ctx context.Context, get apiGetter, databaseID, ref string) (string, error) {
	prefix := strings.ToLower(strings.TrimSpace(ref))
	if prefix == "" {
		return "", usagef("--backup is empty")
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := get(ctx, "/databases/"+url.PathEscape(databaseID)+"/backups", nil, &list); err != nil {
		return "", err
	}
	var matches []string
	for _, b := range list.Data {
		if strings.HasPrefix(strings.ToLower(b.ID), prefix) {
			matches = append(matches, b.ID)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", usagef("no backup of this database matches %q (see 'miosa db backups')", ref)
	default:
		return "", usagef("%q matches %d backups; use more of the id (see 'miosa db backups')", ref, len(matches))
	}
}

func newestCompletedBackup(ctx context.Context, get apiGetter, databaseID string) (string, error) {
	var list struct {
		Data []struct {
			ID        string `json:"id"`
			State     string `json:"state"`
			CreatedAt string `json:"created_at"`
		} `json:"data"`
	}
	if err := get(ctx, "/databases/"+url.PathEscape(databaseID)+"/backups", nil, &list); err != nil {
		return "", err
	}
	// The API lists newest first.
	for _, b := range list.Data {
		if b.State == "completed" {
			return b.ID, nil
		}
	}
	return "", usagef("this database has no completed backup: take one with 'miosa db backup'")
}

type apiPoster interface {
	Post(ctx context.Context, path string, body, out any) error
	Get(ctx context.Context, path string, q url.Values, out any) error
}

// createRestoreTarget creates the new database a --to-new restore fills and
// waits until it is running.
func createRestoreTarget(ctx context.Context, cmd *cobra.Command, api apiPoster, source map[string]any, name string) (string, error) {
	body := map[string]any{"name": name}
	for _, k := range []string{"engine", "engine_version", "cpu_count", "memory_mb", "storage_mb", "workspace_id"} {
		if v, ok := source[k]; ok && v != nil && v != "" && v != 0 {
			body[k] = v
		}
	}
	var created dbRef
	if err := api.Post(ctx, "/databases", body, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("the API did not return the new database's id")
	}
	if !isJSON() {
		printerFor(cmd).Line("Created %s (%s), waiting for it to run ...", name, shortRef(created.ID))
	}
	deadline := time.Now().Add(10 * time.Minute)
	for {
		var db dbRef
		if err := api.Get(ctx, "/databases/"+url.PathEscape(created.ID), nil, &db); err != nil {
			return "", err
		}
		switch db.State {
		case "running":
			return created.ID, nil
		case "error", "destroyed", "destroying":
			return "", fmt.Errorf("the new database ended in state %s (id %s): see 'miosa db get %s'", db.State, created.ID, created.ID)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("the new database %s is still %s after 10 minutes: restore into it with 'miosa db restore ... ' once it runs", created.ID, db.State)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// pollInterval is how often a restore or a new database is checked.
var pollInterval = 3 * time.Second

func waitForRestore(ctx context.Context, get apiGetter, targetID, restoreID string) (map[string]any, error) {
	deadline := time.Now().Add(30 * time.Minute)
	for {
		var list struct {
			Data []map[string]any `json:"data"`
		}
		if err := get(ctx, "/databases/"+url.PathEscape(targetID)+"/restores", nil, &list); err != nil {
			return nil, err
		}
		for _, r := range list.Data {
			if r["id"] != restoreID {
				continue
			}
			switch r["state"] {
			case "completed":
				return r, nil
			case "failed":
				code := "RESTORE_FAILED"
				if f, ok := r["failure"].(map[string]any); ok {
					if c, ok := f["code"].(string); ok {
						code = c
					}
				}
				return nil, fmt.Errorf("the restore failed (%s)", code)
			}
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("the restore is still running after 30 minutes: check 'miosa api GET /databases/%s/restores'", targetID)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// newDBRestoreAliasCmd is `miosa db restore`: the same command under the name
// the work orders and the API use, hidden so help shows one verb.
func newDBRestoreAliasCmd() *cobra.Command {
	alias := newDBRestoreCmd()
	alias.Use = "restore <database>"
	alias.Hidden = true
	return alias
}
