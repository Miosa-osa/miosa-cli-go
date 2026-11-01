package commands

import (
	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// ownerPrivilegesFlag is --owner-privileges on 'db create' and 'db update'. The
// API's owner_privileges is a boolean: elevated is true, standard false. It is
// sent only when given.
var ownerPrivilegesFlag = Flag{
	Name:  "owner-privileges",
	Usage: "Owner role privileges: standard, or elevated for CREATEROLE, CREATEDB and BYPASSRLS (PostgreSQL 16+, paid plans)",
	Enum:  []string{"standard", "elevated"},
}

// ownerPrivilegesHelp is the paragraph 'db create' and 'db update' show for the flag.
const ownerPrivilegesHelp = `--owner-privileges elevated (PostgreSQL 16+, paid plans) gives the owner role CREATEROLE,
CREATEDB and BYPASSRLS, and a NOLOGIN role named postgres it is a member of. It is not a
superuser. Use it for Supabase or Railway migrations that create roles, grant to postgres or
rely on row-level security being bypassed. Anyone with the owner's connection string can
then create roles that can sign in and read past row-level security in this database;
standard takes the attributes away again but does not drop the roles or databases created
meanwhile.`

// ownerPrivilegesBody turns the flag's word into the boolean the API takes.
func ownerPrivilegesBody(body map[string]any) map[string]any {
	if v, ok := body["owner_privileges"].(string); ok {
		body["owner_privileges"] = v == "elevated"
	}
	return body
}

// ownerPrivilegesCell shows a PostgreSQL database's owner_privileges as the
// flag's word; other engines have none to show.
func ownerPrivilegesCell(row map[string]any) string {
	if engine, _ := row["engine"].(string); engine != "postgresql" && engine != "postgres" {
		return ""
	}
	if elevated, _ := row["owner_privileges"].(bool); elevated {
		return "elevated"
	}
	return "standard"
}

// dbChangeDetail is what 'db create' and 'db update' show for the database they answer with.
var dbChangeDetail = []Col{
	{Head: "Name", Path: "name"}, {Head: "Id", Path: "id"}, {Head: "Engine", Path: "engine"}, {Head: "Version", Path: "engine_version"},
	{Head: "State", Path: "state"}, {Head: "Memory (MB)", Path: "memory_mb"}, {Head: "Owner privileges", Compute: ownerPrivilegesCell},
	{Head: "Workspace", Path: "workspace_id"},
}

// dbUpdateOp is 'db update': PATCH /databases/:id with the owner privileges.
func dbUpdateOp(d Arg) Op {
	flag := ownerPrivilegesFlag
	flag.Required = true
	return Op{
		Use: "update <database>", Short: "Change a database's owner privileges",
		Long: `Change a PostgreSQL database's owner privileges.

  miosa db update app-db --owner-privileges elevated
  miosa db update app-db --owner-privileges standard

On a running database the host applies the change before the command returns; on a stopped
one it is saved and applied when the database next starts.

` + ownerPrivilegesHelp,
		Method: "PATCH", Path: "/databases/{0}", Args: []Arg{d},
		Flags:   []Flag{flag},
		Body:    func(_ []string, body map[string]any) (map[string]any, error) { return ownerPrivilegesBody(body), nil },
		Human:   renderDBUpdate,
		OnError: explainOwnerPrivilegesRefusal,
	}
}

// renderDBUpdate shows the database the update answered with and, when it is
// not running, says the change waits for its next start.
func renderDBUpdate(cmd *cobra.Command, args []string, resp any) error {
	if err := (Op{Detail: dbChangeDetail}).render(cmd, args, resp); err != nil {
		return err
	}
	if state, _ := lookupPath(unwrapObject(resp), "state").(string); state != "" && state != "running" {
		printerFor(cmd).Line("%s is %s: the change is saved and applies when it next starts.", args[0], state)
	}
	return nil
}

// explainOwnerPrivilegesRefusal names the next step for a refusal of
// --owner-privileges on 'db create' and 'db update'.
func explainOwnerPrivilegesRefusal(cmd *cobra.Command, _ *api.Client, _ []string, err error) error {
	elevated := false
	if v, _ := cmd.Flags().GetString(ownerPrivilegesFlag.Name); v == "elevated" {
		elevated = true
	}
	switch {
	case api.IsCode(err, "OWNER_PRIVILEGES_UNSUPPORTED"):
		return withHints(err, "the elevated owner needs PostgreSQL 16 or newer: create the database with --engine postgres --version 16, or leave --owner-privileges out")
	case api.IsCode(err, "PLAN_LIMIT_EXCEEDED") && elevated:
		return withHints(err, "--owner-privileges elevated needs a paid plan: 'miosa billing plan' shows yours, 'miosa onboard --plan developer' opens the checkout")
	case api.IsCode(err, "OWNER_PRIVILEGES_NOT_APPLIED"):
		return withHints(err, "nothing changed: the database's host could not apply it; run the same command again in a few minutes")
	}
	return err
}
