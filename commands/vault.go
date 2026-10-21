package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

func init() {
	rootCmd.AddCommand(secretCmd())
}

// The vault holds organization and workspace secrets. Names, scopes, shares and
// versions are metadata and always readable; a value leaves only through
// `secret reveal`, which the server audits and only allows for an owner or admin
// acting as themselves.
func secretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secret",
		Aliases: []string{"secrets", "vault"},
		Short:   "Organization and workspace secrets",
		Long: `Secrets belong to the organization or to one workspace and are shared to
workspaces explicitly. Lists show names, never values.

  miosa secret ls
  miosa secret set GHL_TOKEN --value-from-file ./token
  miosa secret share GHL_TOKEN --workspace staging
  miosa secret reveal GHL_TOKEN`,
	}

	cmd.AddCommand(
		secretListCmd(),
		secretSetCmd(),
		secretGetCmd(),
		secretVersionsCmd(),
		secretRevealCmd(),
		secretShareCmd(),
		secretUnshareCmd(),
		secretImportCmd(),
		secretRemoveCmd(),
	)

	return cmd
}

func secretListCmd() *cobra.Command {
	var scope, workspaceID string

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List secrets (names and metadata, never values)",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			path := "/secrets"
			if scope != "" || workspaceID != "" {
				parts := []string{}
				if scope != "" {
					parts = append(parts, "scope="+scope)
				}
				if workspaceID != "" {
					parts = append(parts, "workspace_id="+workspaceID)
				}
				path += "?" + strings.Join(parts, "&")
			}

			var out struct {
				Data []map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), path, nil, &out); err != nil {
				return die(err)
			}

			if p.JSONMode() {
				return p.JSON(map[string]any{"data": out.Data})
			}

			rows := make([][]string, 0, len(out.Data))
			for _, s := range out.Data {
				rows = append(rows, []string{
					str(s["name"]),
					str(s["scope"]),
					str(s["current_version"]),
					joinList(s["shared_with"]),
					str(s["description"]),
				})
			}
			p.Table([]string{"NAME", "SCOPE", "VERSION", "SHARED WITH", "DESCRIPTION"}, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&scope, "scope", "", "Only this scope: org or workspace")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Only this workspace")
	return cmd
}

func secretSetCmd() *cobra.Command {
	var value, valueFromFile, description, scope, workspaceID string

	cmd := &cobra.Command{
		Use:   "set <name>",
		Short: "Create a secret, or rotate an existing one",
		Long: `Create or rotate. A rotation adds a version and keeps the name, the scope,
the shares and (unless --description is passed) the description.

The value can come from --value, --value-from-file, or standard input when it is
piped: printf %s "$SECRET" | miosa secret set NAME`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			body := map[string]any{"name": args[0]}
			if scope != "" {
				body["scope"] = scope
			}
			if workspaceID != "" {
				body["workspace_id"] = workspaceID
			}
			if cmd.Flags().Changed("description") {
				body["description"] = description
			}

			switch {
			case valueFromFile != "":
				data, readErr := os.ReadFile(valueFromFile)
				if readErr != nil {
					return die(readErr)
				}
				body["value"] = strings.TrimRight(string(data), "\n")
			case value != "":
				body["value"] = value
			default:
				piped, readErr := io.ReadAll(cmd.InOrStdin())
				if readErr != nil {
					return die(readErr)
				}
				trimmed := strings.TrimRight(string(piped), "\n")
				if trimmed == "" {
					return die(errors.New("no value: pass --value, --value-from-file, or pipe it on stdin"))
				}
				body["value"] = trimmed
			}

			var out struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), "/secrets", body, &out); err != nil {
				return die(err)
			}
			if p.JSONMode() {
				return p.JSON(map[string]any{"data": out.Data})
			}
			p.Success("%s is at version %s", str(out.Data["name"]), str(out.Data["current_version"]))
			return nil
		},
	}
	cmd.Flags().StringVar(&value, "value", "", "The value (prefer --value-from-file or stdin)")
	cmd.Flags().StringVar(&valueFromFile, "value-from-file", "", "Read the value from a file")
	cmd.Flags().StringVar(&description, "description", "", "What it is for")
	cmd.Flags().StringVar(&scope, "scope", "org", "Org or workspace")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Required with --scope workspace")
	return cmd
}

func secretGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <id|name>",
		Short: "Show a secret's metadata and versions (never its value)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			id, err := vaultResolveID(cmd.Context(), c, args[0])
			if err != nil {
				return die(err)
			}

			var out struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/secrets/"+id, nil, &out); err != nil {
				return die(err)
			}
			if p.JSONMode() {
				return p.JSON(map[string]any{"data": out.Data})
			}

			p.Fields([][2]string{
				{"Name", str(out.Data["name"])},
				{"Scope", str(out.Data["scope"])},
				{"Workspace", str(out.Data["workspace_id"])},
				{"Description", str(out.Data["description"])},
				{"Version", str(out.Data["current_version"])},
				{"Shared with", joinList(out.Data["shared_with"])},
				{"Updated", str(out.Data["updated_at"])},
			})
			return nil
		},
	}
}

func secretVersionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "versions <id|name>",
		Short: "List a secret's versions (numbers and dates, never values)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			id, err := vaultResolveID(cmd.Context(), c, args[0])
			if err != nil {
				return die(err)
			}

			var out struct {
				Data []map[string]any `json:"data"`
			}
			if err := c.API.Get(cmd.Context(), "/secrets/"+id+"/versions", nil, &out); err != nil {
				return die(err)
			}
			if p.JSONMode() {
				return p.JSON(map[string]any{"data": out.Data})
			}

			rows := make([][]string, 0, len(out.Data))
			for _, v := range out.Data {
				rows = append(rows, []string{str(v["version"]), str(v["created_at"])})
			}
			p.Table([]string{"VERSION", "CREATED"}, rows)
			return nil
		},
	}
}

func secretRevealCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reveal <id|name>",
		Short: "Print a secret's value (owner or admin only; audited)",
		Long: `Print the current value. The server allows this only for an owner or admin
acting as themselves, never for a machine, CI, sandbox or organization key, and
writes an audit row every time.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			id, err := vaultResolveID(cmd.Context(), c, args[0])
			if err != nil {
				return die(err)
			}

			var out struct {
				Data map[string]any `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), "/secrets/"+id+"/reveal", map[string]any{}, &out); err != nil {
				return die(err)
			}
			if p.JSONMode() {
				return p.JSON(map[string]any{"data": out.Data})
			}
			p.Line("%s", str(out.Data["value"]))
			return nil
		},
	}
}

func secretShareCmd() *cobra.Command {
	var workspaces []string

	cmd := &cobra.Command{
		Use:   "share <id|name>",
		Short: "Share a secret with workspaces",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			id, err := vaultResolveID(cmd.Context(), c, args[0])
			if err != nil {
				return die(err)
			}

			// The server takes workspace ids, but a slug is what people type,
			// so a slug or name is resolved the same way --workspace-id is
			// elsewhere. A value that matches nothing says so; it used to
			// reach the API and come back as a bare BAD_REQUEST.
			ids := make([]string, 0, len(workspaces))
			for _, ref := range workspaces {
				workspaceID, err := c.API.ResolveWorkspace(cmd.Context(), ref)
				if err != nil {
					return die(err)
				}
				ids = append(ids, workspaceID)
			}

			var out struct {
				Data map[string]any `json:"data"`
			}
			body := map[string]any{"workspace_ids": ids}
			if err := c.API.Post(cmd.Context(), "/secrets/"+id+"/shares", body, &out); err != nil {
				return die(err)
			}
			if p.JSONMode() {
				return p.JSON(map[string]any{"data": out.Data})
			}
			p.Success("%s is shared with %s", str(out.Data["name"]), joinList(out.Data["shared_with"]))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&workspaces, "workspace", nil, "Workspace slug or id to share with (repeatable)")
	_ = cmd.MarkFlagRequired("workspace")
	return cmd
}

func secretUnshareCmd() *cobra.Command {
	var workspace string

	cmd := &cobra.Command{
		Use:   "unshare <id|name>",
		Short: "Stop sharing a secret with a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			id, err := vaultResolveID(cmd.Context(), c, args[0])
			if err != nil {
				return die(err)
			}

			workspaceID, err := c.API.ResolveWorkspace(cmd.Context(), workspace)
			if err != nil {
				return die(err)
			}

			var out struct {
				Data map[string]any `json:"data"`
			}
			path := "/secrets/" + id + "/shares/" + workspaceID
			if err := c.API.Delete(cmd.Context(), path, nil, &out); err != nil {
				return die(err)
			}
			if p.JSONMode() {
				return p.JSON(map[string]any{"data": out.Data})
			}
			p.Success("%s is shared with %s", str(out.Data["name"]), joinList(out.Data["shared_with"]))
			return nil
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace slug or id to stop sharing with")
	_ = cmd.MarkFlagRequired("workspace")
	return cmd
}

func secretImportCmd() *cobra.Command {
	var file, scope, workspaceID string
	var strict bool

	cmd := &cobra.Command{
		Use:   "import --file <dotenv>",
		Short: "Load many secrets from a dotenv file",
		Long: `Load many names at once. Values are read from the file and never echoed; the
command prints the names it set.

A line the vault cannot take is reported with its line number and the reason
instead of being skipped silently. When every line is unusable the command
fails, and --strict fails on the first unusable line before anything is set.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			data, readErr := os.ReadFile(file)
			if readErr != nil {
				return die(readErr)
			}

			scan := scanDotenv(string(data))
			for _, warning := range scan.warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s:%d: %s\n", file, warning.line, warning.reason)
			}
			if strict && scan.skipped > 0 {
				return die(fmt.Errorf("%s: %d line(s) cannot be imported; --strict refuses a partial import", file, scan.skipped))
			}
			if scan.valid == 0 && scan.skipped > 0 {
				return die(fmt.Errorf("%s: no lines could be imported; all %d line(s) were skipped", file, scan.skipped))
			}

			body := map[string]any{"content": string(data), "scope": scope}
			if workspaceID != "" {
				body["workspace_id"] = workspaceID
			}

			var out struct {
				Data struct {
					Names []string `json:"names"`
				} `json:"data"`
			}
			if err := c.API.Post(cmd.Context(), "/secrets/import", body, &out); err != nil {
				return die(err)
			}
			if p.JSONMode() {
				return p.JSON(map[string]any{"data": map[string]any{"names": out.Data.Names}})
			}
			p.Success("Set %d secrets: %s", len(out.Data.Names), strings.Join(out.Data.Names, ", "))
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "Dotenv file to read")
	cmd.Flags().StringVar(&scope, "scope", "org", "Org or workspace")
	cmd.Flags().StringVar(&workspaceID, "workspace-id", "", "Required with --scope workspace")
	cmd.Flags().BoolVar(&strict, "strict", false, "Fail on the first line that cannot be imported")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

// dotenvWarning is one line the vault cannot take, with the line number the
// user sees in their editor (1-based) and the reason. It never carries a value.
type dotenvWarning struct {
	line   int
	reason string
}

type dotenvScan struct {
	valid    int
	skipped  int
	warnings []dotenvWarning
}

// scanDotenv applies the vault's dotenv rules locally so the command can say
// what it is dropping. The server keeps the same rules: a line is NAME=value
// with a name of letters, digits and underscores that starts with a letter or
// underscore; blank lines and comments are ignored; the name is upcased, so
// two spellings of one name are a duplicate.
func scanDotenv(content string) dotenvScan {
	var scan dotenvScan
	seen := map[string]int{}

	for i, raw := range strings.Split(content, "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, ok := strings.Cut(line, "=")
		if !ok {
			scan.skipped++
			scan.warnings = append(scan.warnings, dotenvWarning{lineNo, "not a NAME=value line"})
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			scan.skipped++
			scan.warnings = append(scan.warnings, dotenvWarning{lineNo, "empty name"})
			continue
		}
		if !validSecretName(name) {
			scan.skipped++
			scan.warnings = append(scan.warnings, dotenvWarning{lineNo,
				fmt.Sprintf("invalid name %q: use letters, digits and underscores, starting with a letter or underscore", name)})
			continue
		}
		upper := strings.ToUpper(name)
		if first, dup := seen[upper]; dup {
			scan.warnings = append(scan.warnings, dotenvWarning{lineNo,
				fmt.Sprintf("duplicate name %s (first set on line %d); the later value wins", upper, first)})
			continue
		}
		seen[upper] = lineNo
		scan.valid++
	}
	return scan
}

// validSecretName matches the vault's rule: [A-Za-z_][A-Za-z0-9_]*.
func validSecretName(name string) bool {
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return name != ""
}

func secretRemoveCmd() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "rm <id|name>",
		Aliases: []string{"delete"},
		Short:   "Delete a secret and all of its versions",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			p := printerFor(cmd)

			id, err := vaultResolveID(cmd.Context(), c, args[0])
			if err != nil {
				return die(err)
			}
			if !yes {
				return die(errors.New("refusing to delete without --yes"))
			}

			if err := c.API.Delete(cmd.Context(), "/secrets/"+id, nil, nil); err != nil {
				return die(err)
			}
			p.Success("Deleted %s", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Do not ask for confirmation")
	return cmd
}

// vaultResolveID accepts an id or a name. A name is looked up in the caller's
// visible secrets, so `secret reveal GHL_TOKEN` works without copying an id.
func vaultResolveID(ctx context.Context, c *client.Client, arg string) (string, error) {
	if looksLikeUUID(arg) {
		return arg, nil
	}

	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(ctx, "/secrets", nil, &out); err != nil {
		return "", err
	}

	var matches []string
	for _, s := range out.Data {
		if str(s["name"]) == arg {
			matches = append(matches, str(s["id"]))
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("no secret named %s in this organization", arg)
	default:
		return "", fmt.Errorf("%s matches %d secrets; pass the id from 'miosa secret ls'", arg, len(matches))
	}
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
				return false
			}
		}
	}
	return true
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func joinList(v any) string {
	items, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			return strings.Join(ss, ", ")
		}
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, str(item))
	}
	return strings.Join(parts, ", ")
}
