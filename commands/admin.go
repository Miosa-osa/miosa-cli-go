package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newAdminCmd groups the platform-administration commands. These call the
// admin API surface and require an admin credential; a normal account key gets
// a clear 403 rather than a confusing failure from a customer endpoint.
func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Platform administration (platform operators only)",
		Long: `Platform administration.

These commands operate on platform state, not on your own sandboxes, and they
require a platform operator API key.`,
	}
	cmd.AddCommand(newAdminSlotRevocationsCmd())
	return cmd
}

// newAdminSlotRevocationsCmd is the parent for the revoked producer bake
// revision commands.
func newAdminSlotRevocationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "slot-revocations",
		Aliases: []string{"slot-revocation"},
		Short:   "Revoke a producer bake revision (bad-bake protection)",
		Long: `Manage the revoked producer bake revisions.

A revocation is the supported replacement for a manual sweep after a bad bake.
Every compute host refuses to acquire or warm-claim a slot whose manifest
names a revoked revision, and the reaper retires the idle slots cut from it.
A revocation reaches running hosts within about a minute, without a restart.`,
	}
	cmd.AddCommand(newAdminSlotRevocationsListCmd())
	cmd.AddCommand(newAdminSlotRevocationsRevokeCmd())
	return cmd
}

func newAdminSlotRevocationsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List every revoked producer bake revision",
		Long: `List every revoked producer bake revision, newest first.

  miosa admin slot-revocations list
  miosa admin slot-revocations list --json | jq -r '.data[].revision'`,
		Annotations: map[string]string{routeAnnotation: "GET /admin/slot-producer-revocations"},
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAdminSlotRevocationsList(cmd)
		},
	}
}

func newAdminSlotRevocationsRevokeCmd() *cobra.Command {
	var reason string

	cmd := &cobra.Command{
		Use:     "revoke <revision>",
		Aliases: []string{"add"},
		Short:   "Revoke a producer bake revision",
		Long: `Revoke a producer bake revision.

Take the revision from the manifest of the bad bake (producer.vcs_revision), or
from the blocklist of the bake that produced it. Revoking is immediate and
idempotent: revoking an already-revoked revision reports already_revoked rather
than failing.

  miosa admin slot-revocations revoke 1a2b3c4d --reason "mmds token absent"`,
		Annotations: map[string]string{routeAnnotation: "POST /admin/slot-producer-revocations"},
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdminSlotRevocationsRevoke(cmd, args[0], reason)
		},
	}

	cmd.Flags().StringVar(&reason, "reason", "", "Why the revision is revoked (recorded in the audit trail)")

	return cmd
}

func runAdminSlotRevocationsList(cmd *cobra.Command) error {
	p := printerFor(cmd)

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	revocations, err := c.ListSlotRevocations(cmd.Context())
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(map[string]interface{}{"data": revocations, "total": len(revocations)})
	}

	if len(revocations) == 0 {
		p.Line("No producer revisions are revoked.")
		return nil
	}

	rows := make([][]string, 0, len(revocations))
	for _, revocation := range revocations {
		rows = append(rows, []string{
			revocation.Revision,
			orDash(revocation.Reason),
			formatShortTime(revocation.InsertedAt),
		})
	}
	p.Table([]string{"REVISION", "REASON", "REVOKED AT"}, rows)
	return nil
}

func runAdminSlotRevocationsRevoke(cmd *cobra.Command, revision, reason string) error {
	p := printerFor(cmd)

	if strings.TrimSpace(revision) == "" {
		return die(fmt.Errorf("a revision is required, for example: miosa admin slot-revocations revoke 1a2b3c4d"))
	}

	c, _, err := buildClient()
	if err != nil {
		return die(err)
	}

	revocation, outcome, err := c.RevokeSlotRevision(cmd.Context(), revision, reason)
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(map[string]interface{}{"data": revocation, "outcome": outcome})
	}

	if outcome == "already_revoked" {
		p.Line(fmt.Sprintf("%s was already revoked.", revocation.Revision))
		return nil
	}

	p.Line(fmt.Sprintf("Revoked %s. Hosts stop using it within about a minute.", revocation.Revision))

	return nil
}

// formatShortTime trims a timestamp to its date for a compact table.
func formatShortTime(value string) string {
	if value == "" {
		return "-"
	}
	if idx := strings.IndexByte(value, 'T'); idx > 0 {
		return value[:idx]
	}
	return value
}
