package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

func init() {
	rootCmd.AddCommand(exportCmd())
}

// exportCmd takes an organization out of MIOSA: one NDJSON file per collection
// (agents and versions, triggers, schedules, deployments and the names of their
// variables, buckets and object keys, volumes, connections, secret names, the
// audit log) plus a manifest carrying every file's SHA-256.
//
// Secret values, token bytes, environment values and object bodies are not in
// an export; the manifest says so, and the checksums let anyone verify what is.
func exportCmd() *cobra.Command {
	var (
		to        string
		workspace string
		prefix    string
	)
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export everything an organization (or one workspace) owns",
		Long: `Writes an organization's records into a bucket of the same organization and
prints the manifest, which lists every file with its SHA-256 and byte size.

  miosa export --to bucket:exports
  miosa export --workspace prod --to bucket:backups --prefix exports/2026-10-19/

Buckets can be named by id or by name. What is never exported: secret values,
token bytes, deploy variable values and object bodies (the bucket objects are
listed by key and size, and stay where they are).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := printerFor(cmd)
			bucketRef, err := bucketTarget(to)
			if err != nil {
				return err
			}
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			bucketID, err := resolveBucketID(cmd.Context(), c, bucketRef)
			if err != nil {
				return die(err)
			}
			workspaceID := ""
			if strings.TrimSpace(workspace) != "" {
				workspaceID, err = resolveWorkspaceID(cmd.Context(), c, strings.TrimSpace(workspace))
				if err != nil {
					return die(err)
				}
			}
			body := map[string]any{"bucket_id": bucketID}
			if workspaceID != "" {
				body["workspace_id"] = workspaceID
			}
			if strings.TrimSpace(prefix) != "" {
				body["prefix"] = prefix
			}
			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/exports", Body: body})
			if err != nil {
				return die(err)
			}
			// unwrapObject already steps into {"data": {...}}.
			data, _ := unwrapObject(resp).(map[string]any)
			// A slice the key could not read is not in the export; say so on stderr in every
			// output mode, so a script that parses stdout still sees it.
			omitted, _ := data["omitted"].([]any)
			for _, o := range omitted {
				entry, _ := o.(map[string]any)
				output.Warn("%v was left out of the export (%v)", orEmpty(entry["slice"], ""), orEmpty(entry["omitted"], "not allowed"))
			}
			if isJSON() {
				return p.JSON(resp)
			}
			if data == nil {
				return p.JSON(resp)
			}
			p.Success("Exported to %s", fmt.Sprint(orEmpty(data["manifest_key"], "")))
			files, _ := data["files"].([]any)
			p.Line("%v files, %v rows, manifest sha256 %v", len(files), orEmpty(data["rows"], 0), orEmpty(data["manifest_sha256"], ""))
			for _, f := range files {
				entry, _ := f.(map[string]any)
				p.Line("  %-24s %8v bytes  %v", orEmpty(entry["path"], ""), orEmpty(entry["bytes"], 0), orEmpty(entry["sha256"], ""))
			}
			p.Line("Secret values, tokens, environment values and object bodies are not in the export; only their names and keys.")
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Bucket to write into: bucket:<id-or-name>")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Narrow the export to one workspace (id or slug)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "Key prefix inside the bucket (default exports/<timestamp>/)")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

// bucketTarget accepts only the bucket: form, so a typo cannot silently mean
// "export nowhere".
func bucketTarget(to string) (string, error) {
	kind, ref, ok := strings.Cut(strings.TrimSpace(to), ":")
	if !ok || strings.ToLower(kind) != "bucket" || strings.TrimSpace(ref) == "" {
		return "", usagef("--to expects bucket:<id-or-name> (got %q)", to)
	}
	return strings.TrimSpace(ref), nil
}

func resolveBucketID(ctx context.Context, c *client.Client, ref string) (string, error) {
	if api.IsUUID(ref) {
		return ref, nil
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(ctx, "/storage/buckets", nil, &list); err != nil {
		return "", err
	}
	for _, b := range list.Data {
		if strings.EqualFold(fmt.Sprint(orEmpty(b["name"], "")), ref) {
			return fmt.Sprint(orEmpty(b["id"], "")), nil
		}
	}
	return "", fmt.Errorf("no bucket named %q in this organization; see 'miosa storage list'", ref)
}
