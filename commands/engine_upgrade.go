package commands

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

// A managed Engine upgrade is one synchronous API call. The control plane
// validates the target manifest and the backup, stops the guest, boots the
// target release, verifies the guest's own GET /api/version and rolls back on
// failure before it answers. These commands drive that route and add no state
// of their own; a rollback asks the control plane for the version the last
// upgrade recorded.

// engineUpgradeResult is what the upgrade and rollback routes answer.
type engineUpgradeResult struct {
	Action     string `json:"action"`
	From       string `json:"from"`
	To         string `json:"to"`
	State      string `json:"state"`
	BackupID   string `json:"backup_id"`
	RolledBack bool   `json:"rolled_back"`
}

// engineUpgradePlan is the dry run: what the upgrade would do, without doing it.
type engineUpgradePlan struct {
	From              string `json:"from"`
	To                string `json:"to"`
	GitSHA            string `json:"git_sha"`
	SHA256            string `json:"sha256"`
	ExpectedMigration int    `json:"expected_migration"`
	ReleaseChannel    string `json:"release_channel"`
	Safe              bool   `json:"safe"`
	CurrentState      string `json:"current_state"`
	HostImage         string `json:"host_image"`
	EstimatedDowntime int    `json:"estimated_downtime_seconds"`
	Backup            struct {
		ID string `json:"id"`
	} `json:"backup"`
}

func newEngineUpgradeCmd() *cobra.Command {
	var (
		to      string
		dryRun  bool
		force   bool
		timeout int
	)

	cmd := &cobra.Command{
		Use:   "upgrade <engine> --to <version>",
		Short: "Upgrade a managed Optimal Engine to a staged release",
		Long: `Swaps a managed Engine to a staged release. The control plane checks the
target manifest against the artifact on disk, requires a completed backup,
requires the release's rootfs image on the host, stops the Engine, boots the
new release and verifies the guest's own /api/version. If the guest does not
report the target version, the control plane rolls back and reports what it
restored. Data on /data is not touched.

  miosa engine upgrade qa-brain --to 0.3.2 --dry-run
  miosa engine upgrade qa-brain --to 0.3.2

--force overrides the refusal to upgrade from a version that has no staged
release to roll back to. Use it only when that version is known to be
recoverable another way.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return usagef("--to is required: the staged version to upgrade to")
			}
			return runEngineUpgrade(cmd, args[0], to, dryRun, force, time.Duration(timeout)*time.Second)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&to, "to", "", "Target Engine version (required)")
	flags.BoolVar(&dryRun, "dry-run", false, "Show what the upgrade would do, without doing it")
	flags.BoolVar(&force, "force", false, "Upgrade even when the current version has no staged release to roll back to")
	flags.IntVar(&timeout, "timeout", 600, "Seconds to wait for the upgrade call to answer")
	return cmd
}

func newEngineRollbackCmd() *cobra.Command {
	var timeout int

	cmd := &cobra.Command{
		Use:   "rollback <engine>",
		Short: "Return a managed Optimal Engine to the version before its last upgrade",
		Long: `Returns the Engine to the version recorded by its last upgrade. The previous
version is never guessed: it is the one the upgrade recorded, and it must still
be a staged release. Data on /data is not touched; a backup restore is a
separate action.

  miosa engine rollback qa-brain
  miosa engine rollback qa-brain --json | jq '{from, to, state}'`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngineRollback(cmd, args[0], time.Duration(timeout)*time.Second)
		},
	}

	cmd.Flags().IntVar(&timeout, "timeout", 600, "Seconds to wait for the rollback call to answer")
	return cmd
}

func runEngineUpgrade(cmd *cobra.Command, ref, version string, dryRun, force bool, timeout time.Duration) error {
	c, _, err := buildClientTimeout(timeout)
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()
	p := printerFor(cmd)

	engine, err := resolveManagedEngine(ctx, c.API.Get, ref)
	if err != nil {
		return die(err)
	}

	body := map[string]any{"version": version}
	if dryRun {
		body["dry_run"] = true
	}
	if force {
		body["force"] = true
	}

	if dryRun {
		var resp struct {
			Data engineUpgradePlan `json:"data"`
		}
		if err := c.API.Post(ctx, "/databases/"+url.PathEscape(engine.ID)+"/upgrade", body, &resp); err != nil {
			return die(err)
		}
		if isJSON() {
			return p.JSON(resp.Data)
		}
		plan := resp.Data
		p.Line("%s: %s -> %s", engine.Name, plan.From, plan.To)
		p.Line("release %s on channel %s, migration %s", shortSHA(plan.GitSHA), plan.ReleaseChannel, migrationLabel(plan.ExpectedMigration))
		if plan.Backup.ID != "" {
			p.Line("rollback point: backup %s", shortRef(plan.Backup.ID))
		}
		if plan.EstimatedDowntime > 0 {
			p.Line("estimated downtime: %ds", plan.EstimatedDowntime)
		}
		if plan.Safe {
			p.Success("safe to upgrade")
		} else {
			p.Line("not safe: the control plane would refuse this upgrade")
		}
		return nil
	}

	var resp struct {
		Data engineUpgradeResult `json:"data"`
	}
	if err := c.API.Post(ctx, "/databases/"+url.PathEscape(engine.ID)+"/upgrade", body, &resp); err != nil {
		return die(err)
	}
	return reportEngineSwap(p, engine.Name, "upgraded", resp.Data)
}

func runEngineRollback(cmd *cobra.Command, ref string, timeout time.Duration) error {
	c, _, err := buildClientTimeout(timeout)
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
		Data engineUpgradeResult `json:"data"`
	}
	if err := c.API.Post(ctx, "/databases/"+url.PathEscape(engine.ID)+"/rollback", nil, &resp); err != nil {
		return die(err)
	}
	return reportEngineSwap(p, engine.Name, "rolled back", resp.Data)
}

func reportEngineSwap(p *output.Printer, name, verb string, result engineUpgradeResult) error {
	if isJSON() {
		return p.JSON(result)
	}
	line := fmt.Sprintf("%s %s %s -> %s (state %s", name, verb, result.From, result.To, result.State)
	if result.BackupID != "" {
		line += ", backup " + shortRef(result.BackupID)
	}
	line += ")"
	p.Success("%s", line)
	return nil
}

// resolveManagedEngine accepts an id or a name. A name is looked up among
// managed Engines only, so a Postgres database with the same name is never
// upgraded by mistake.
func resolveManagedEngine(ctx context.Context, get apiGetter, ref string) (*engineRef, error) {
	if uuidRE.MatchString(ref) {
		var engine engineRef
		if err := get(ctx, "/databases/"+url.PathEscape(ref), nil, &engine); err != nil {
			return nil, err
		}
		if engine.Engine != "optimal" {
			return nil, fmt.Errorf("%s is a %s database, not a managed Optimal Engine", ref, engine.Engine)
		}
		return &engine, nil
	}

	engine, err := findEngineByName(ctx, get, ref)
	if err != nil {
		return nil, err
	}
	if engine == nil {
		return nil, fmt.Errorf("no managed Optimal Engine named %q; 'miosa engine list' shows them", ref)
	}
	return engine, nil
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func migrationLabel(migration int) string {
	if migration == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", migration)
}
