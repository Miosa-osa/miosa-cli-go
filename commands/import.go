package commands

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	"github.com/Miosa-osa/miosa-cli-go/internal/output"
)

func init() {
	rootCmd.AddCommand(importCmd())
}

// importCmd loads rows into a managed database, or a Notion export into a
// workspace's Optimal Engine, from a local file or an object already in storage.
//
// A local file is staged into the organization's hidden `imports` bucket first
// and then imported from there: the import job records a bucket object as its
// source, and a control plane should not hold a 40,000-row CSV in a request
// body. The job row is the receipt - rows read, rows loaded, rows refused with
// their line numbers. An Engine import works the same way: the platform mints a
// presigned URL for the object and hands it to the Engine, so the export never
// passes through the CLI.
func importCmd() *cobra.Command {
	var (
		from           string
		to             string
		overrides      []string
		job            string
		watch          bool
		idempotencyKey string
	)
	cmd := &cobra.Command{
		Use:   "import <file> | --from bucket:<bucket>/<key> --to <target>",
		Short: "Load a CSV into a managed database, or a Notion export into an Engine",
		Long: `Load a CSV into a table of a managed Postgres database, or a Notion export zip
into a workspace's Optimal Engine.

  miosa import ./people.csv --to db:app-db/people
  miosa import ./notion.zip --to engine:research --watch

A local file is uploaded to the organization's hidden 'imports' bucket under a
content-addressed key and imported from there, so the same file lands on the
same object twice. A file already in storage can be named instead:

  miosa import --from bucket:exports/people.csv --to db:app-db/people

Types are inferred from the values (bigint, numeric, boolean, timestamptz,
text). A column that needs another type can be pinned:

  miosa import ./people.csv --to db:app-db/people --override zip=text

An Engine import makes every page one context at optimal://<workspace>/notion/<page>,
with parent and link edges, and is searchable when the job reports done. It is a
durable job: '--watch' follows it, and 'miosa import --job <id> --to engine:<workspace>'
reprints it later.

Every import prints its job id and its counts; refused rows come with their line
numbers.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)

			// Usage is checked before anything needs a login: a mistyped target
			// is a usage error, not an authentication one.
			target, targetRef, err := importTarget(to)
			if err != nil {
				return err
			}

			local := ""
			if len(args) == 1 {
				local = args[0]
			}
			if local != "" && from != "" {
				return usagef("name the source once: a local file, or --from bucket:<bucket>/<key>")
			}
			if local == "" && from == "" && job == "" {
				return usagef("name a file to import, or --from bucket:<bucket>/<key>")
			}
			if job != "" && target != "engine" {
				return usagef("--job reprints an Engine import; pass --to engine:<workspace>")
			}
			if local == "" && job == "" {
				// The bucket reference is a usage check too: a mistyped --from
				// must not need a login to be reported.
				if _, _, err := fromTarget(from); err != nil {
					return err
				}
			}

			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}

			if job != "" {
				return printEngineImport(p, cmd, c, targetRef, job, watch)
			}

			var bucketID, key string

			if local != "" {
				bucketID, key, err = stageForImport(cmd, c, p, local)
				if err != nil {
					return die(err)
				}
			} else {
				bucketRef, objectKey, err := fromTarget(from)
				if err != nil {
					return err
				}

				bucketID, err = resolveBucketID(cmd.Context(), c, bucketRef)
				if err != nil {
					return die(err)
				}
				key = objectKey
			}

			if target == "engine" {
				return startEngineImport(p, cmd, c, targetRef, bucketID, key, watch, idempotencyKey)
			}

			databaseRef, table, err := databaseTarget(to)
			if err != nil {
				return err
			}

			databaseID, err := resolveDatabaseID(cmd.Context(), c, databaseRef)
			if err != nil {
				return die(err)
			}

			body := map[string]any{
				"database_id": databaseID,
				"table":       table,
				"bucket_id":   bucketID,
				"key":         key,
			}
			if types := overrideMap(overrides); len(types) > 0 {
				body["overrides"] = types
			}

			resp, err := c.API.JSONAny(cmd.Context(), api.Request{Method: "POST", Path: "/data-imports", Body: body})
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(resp)
			}

			data, _ := unwrapObject(resp).(map[string]any)
			if data == nil {
				return p.JSON(resp)
			}

			p.Success("Imported into %s", table)
			p.Line("job %v: %v rows read, %v loaded, %v refused",
				orEmpty(data["id"], ""), orEmpty(data["rows_total"], 0),
				orEmpty(data["rows_loaded"], 0), orEmpty(data["rejected_total"], 0))

			if errors, ok := data["errors"].(map[string]any); ok {
				if rows, ok := errors["rows"].([]any); ok {
					for _, row := range rows {
						entry, _ := row.(map[string]any)
						p.Line("  line %v: %v", orEmpty(entry["line"], "?"), orEmpty(entry["reason"], ""))
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "Source: bucket:<bucket>/<key>")
	cmd.Flags().StringVar(&to, "to", "", "Destination: db:<database>/<table> or engine:<workspace>")
	cmd.Flags().StringArrayVar(&overrides, "override", nil, "Pin a column's type: column=text (repeatable)")
	cmd.Flags().StringVar(&job, "job", "", "Reprint one Engine import job by id")
	cmd.Flags().BoolVar(&watch, "watch", false, "Follow an Engine import until it finishes")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "",
		"Engine import key; a random one is used when empty, and repeating it returns the same job")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

// fromTarget splits bucket:<bucket>/<key>. The key may contain slashes; only
// the first one after the bucket name separates the two.
func fromTarget(value string) (bucket, key string, err error) {
	kind, rest, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok || strings.ToLower(kind) != "bucket" {
		return "", "", usagef("--from expects bucket:<bucket>/<key> (got %q)", value)
	}
	bucket, key, ok = strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	if !ok || strings.TrimSpace(bucket) == "" || strings.TrimSpace(key) == "" {
		return "", "", usagef("--from expects bucket:<bucket>/<key> (got %q)", value)
	}
	return bucket, key, nil
}

// importTarget splits the --to destination into the kind of target and its
// reference, so the two import shapes stay one command:
//
//	db:<database>/<table>   rows into a managed database
//	engine:<workspace>      a Notion export into that workspace's Engine
func importTarget(value string) (kind, ref string, err error) {
	kind, rest, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok || strings.TrimSpace(rest) == "" {
		return "", "", usagef("--to expects db:<database>/<table> or engine:<workspace> (got %q)", value)
	}
	switch strings.ToLower(kind) {
	case "db":
		database, _, ok := strings.Cut(strings.TrimPrefix(rest, "/"), "/")
		if !ok || strings.TrimSpace(database) == "" {
			return "", "", usagef("--to expects db:<database>/<table> (got %q)", value)
		}
		return "db", value, nil
	case "engine":
		return "engine", strings.TrimSpace(rest), nil
	default:
		return "", "", usagef("--to expects db:<database>/<table> or engine:<workspace> (got %q)", value)
	}
}

func databaseTarget(value string) (database, table string, err error) {
	kind, rest, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok || strings.ToLower(kind) != "db" {
		return "", "", usagef("--to expects db:<database>/<table> (got %q)", value)
	}
	database, table, ok = strings.Cut(strings.TrimPrefix(rest, "/"), "/")
	if !ok || strings.TrimSpace(database) == "" || strings.TrimSpace(table) == "" {
		return "", "", usagef("--to expects db:<database>/<table> (got %q)", value)
	}
	return database, table, nil
}

func overrideMap(values []string) map[string]string {
	types := map[string]string{}
	for _, value := range values {
		column, kind, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(column) == "" || strings.TrimSpace(kind) == "" {
			continue
		}
		types[strings.TrimSpace(column)] = strings.TrimSpace(kind)
	}
	return types
}

func resolveDatabaseID(ctx context.Context, c *client.Client, ref string) (string, error) {
	if api.IsUUID(ref) {
		return ref, nil
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(ctx, "/databases", nil, &list); err != nil {
		return "", err
	}
	for _, d := range list.Data {
		if strings.EqualFold(fmt.Sprint(orEmpty(d["name"], "")), ref) {
			return fmt.Sprint(orEmpty(d["id"], "")), nil
		}
	}
	return "", fmt.Errorf("no database named %q in this organization; see 'miosa db list'", ref)
}

// ---------------------------------------------------------------------------
// Notion export into a workspace's Engine (WO-29 requirement 1/3)
// ---------------------------------------------------------------------------

// startEngineImport hands the workspace's Engine a presigned URL for the object.
// The platform mints the URL and calls the Engine, so neither the CLI nor the
// caller ever holds an Engine key or the zip itself.
func startEngineImport(p *output.Printer, cmd *cobra.Command, c *client.Client, workspaceRef, bucketID, objectKey string, watch bool, idempotencyKey string) error {
	workspaceID, err := c.API.ResolveWorkspace(cmd.Context(), workspaceRef)
	if err != nil {
		return die(err)
	}

	key, err := idempotencyHeader(idempotencyKey)
	if err != nil {
		return die(err)
	}

	resp, err := c.API.JSONAny(cmd.Context(), api.Request{
		Method:  "POST",
		Path:    "/workspaces/" + workspaceID + "/engine/imports/notion",
		Body:    map[string]any{"bucket_id": bucketID, "key": objectKey},
		Headers: map[string]string{"Idempotency-Key": key},
	})
	if err != nil {
		return die(err)
	}
	if isJSON() {
		return p.JSON(resp)
	}

	data, _ := unwrapObject(resp).(map[string]any)
	if data == nil {
		return p.JSON(resp)
	}

	jobID := fmt.Sprint(orEmpty(data["job_id"], ""))
	p.Success("Importing into the Engine for %s", workspaceRef)
	p.Line("job %s: %v", jobID, orEmpty(data["status"], "queued"))
	p.Line("idempotency key %s (repeat it with --idempotency-key to get this job back)", key)

	if watch {
		return followEngineImport(p, cmd, c, workspaceID, workspaceRef, jobID)
	}
	p.Line("Follow it with: miosa import --job %s --to engine:%s --watch", jobID, workspaceRef)
	return nil
}

// idempotencyHeader is the key sent as Idempotency-Key: the caller's, or a
// fresh v4 UUID so an unretried invocation still cannot start one import twice.
func idempotencyHeader(key string) (string, error) {
	if strings.TrimSpace(key) != "" {
		return strings.TrimSpace(key), nil
	}

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// printEngineImport reprints one job, and follows it with --watch.
func printEngineImport(p *output.Printer, cmd *cobra.Command, c *client.Client, workspaceRef, jobID string, watch bool) error {
	workspaceID, err := c.API.ResolveWorkspace(cmd.Context(), workspaceRef)
	if err != nil {
		return die(err)
	}
	if watch {
		return followEngineImport(p, cmd, c, workspaceID, workspaceRef, jobID)
	}
	job, err := engineImportJob(cmd.Context(), c, workspaceID, jobID)
	if err != nil {
		return die(err)
	}
	if isJSON() {
		return p.JSON(job)
	}
	printEngineJob(p, workspaceRef, job)
	return nil
}

// followEngineImport polls the job until it finishes or ten minutes pass. The
// Engine owns the work; this only reports it.
func followEngineImport(p *output.Printer, cmd *cobra.Command, c *client.Client, workspaceID, workspaceRef, jobID string) error {
	deadline := time.Now().Add(10 * time.Minute)

	for {
		job, err := engineImportJob(cmd.Context(), c, workspaceID, jobID)
		if err != nil {
			return die(err)
		}
		if isJSON() {
			return p.JSON(job)
		}

		status := fmt.Sprint(orEmpty(job["status"], ""))
		switch status {
		case "done", "succeeded", "failed", "error", "cancelled":
			printEngineJob(p, workspaceRef, job)
			if status == "failed" || status == "error" {
				return fmt.Errorf("Engine import %s %s", jobID, status)
			}
			return nil
		}

		if time.Now().After(deadline) {
			p.Line("job %s is still %v after 10 minutes; follow it later with --job %s", jobID, status, jobID)
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

func engineImportJob(ctx context.Context, c *client.Client, workspaceID, jobID string) (map[string]any, error) {
	var resp map[string]any
	path := "/workspaces/" + workspaceID + "/engine/imports/notion/" + jobID
	if err := c.API.Get(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	job, _ := unwrapObject(resp).(map[string]any)
	if job == nil {
		return resp, nil
	}
	return job, nil
}

func printEngineJob(p *output.Printer, workspaceRef string, job map[string]any) {
	status := fmt.Sprint(orEmpty(job["status"], "unknown"))
	p.Success("Engine import into %s: %s", workspaceRef, status)

	if counts, ok := job["counts"].(map[string]any); ok {
		p.Line("pages %v, contexts %v, links %v",
			orEmpty(counts["pages"], 0), orEmpty(counts["contexts"], 0), orEmpty(counts["links"], 0))
	}
	if roots, ok := job["root_uris"].([]any); ok && len(roots) > 0 {
		p.Line("roots: %v", roots[0])
	}
	if errors, ok := job["errors"].([]any); ok {
		for _, entry := range errors {
			p.Line("  error: %v", entry)
		}
	}
}

// ---------------------------------------------------------------------------
// Staging a local file
// ---------------------------------------------------------------------------

// importsBucketName is the organization bucket local files are staged in. It is
// created on first use, marked in its metadata so the console can leave it out
// of the Files view, and reused after that.
const importsBucketName = "imports"

// stageForImport uploads a local file to the staging bucket under a key derived
// from its content, and answers the bucket id and key the import reads. The same
// file staged twice lands on the same object.
func stageForImport(cmd *cobra.Command, c *client.Client, p *output.Printer, file string) (string, string, error) {
	st, err := os.Stat(file)
	if err != nil {
		return "", "", fmt.Errorf("reading %s: %w", file, err)
	}
	if st.IsDir() {
		return "", "", fmt.Errorf("%s is a directory; import takes one file", file)
	}

	key, err := stagedKey(file)
	if err != nil {
		return "", "", err
	}

	bucket, err := stagingBucket(cmd.Context(), c)
	if err != nil {
		return "", "", err
	}

	ctype := mime.TypeByExtension(filepath.Ext(file))
	if ctype == "" {
		ctype = "application/octet-stream"
	}

	// Uploads stream with no wall clock; --timeout is the idle limit (see storage put).
	idle := time.Duration(globalFlags.Timeout) * time.Second
	if st.Size() > storageSingleMax {
		err = storageMultipartPut(cmd.Context(), c.API, bucket, key, file, st.Size(), ctype, idle)
	} else {
		err = storageSinglePut(cmd.Context(), c.API, bucket, key, file, st.Size(), ctype, idle)
	}
	if err != nil {
		return "", "", err
	}

	p.Line("Staged %s as %s in %s (%s)", filepath.Base(file), key, importsBucketName, formatBytes(st.Size()))
	return bucket, key, nil
}

// stagedKey is content-addressed: the first 16 hex characters of the file's
// SHA-256, then its name, so two files that share a name never overwrite each
// other and the same file staged twice reuses one object.
func stagedKey(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return "staged/" + hex.EncodeToString(h.Sum(nil))[:16] + "/" + filepath.Base(file), nil
}

// stagingBucket finds the staging bucket by name, or creates it.
func stagingBucket(ctx context.Context, c *client.Client) (string, error) {
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.API.Get(ctx, "/storage/buckets", nil, &list); err != nil {
		return "", err
	}
	for _, bucket := range list.Data {
		if strings.EqualFold(fmt.Sprint(orEmpty(bucket["name"], "")), importsBucketName) {
			if id := fmt.Sprint(orEmpty(bucket["id"], "")); id != "" {
				return id, nil
			}
		}
	}

	var created struct {
		Data map[string]any `json:"data"`
	}
	body := map[string]any{
		"name":     importsBucketName,
		"metadata": map[string]any{"import_staging": true},
	}
	if err := c.API.Post(ctx, "/storage/buckets", body, &created); err != nil {
		return "", err
	}
	if id := fmt.Sprint(orEmpty(created.Data["id"], "")); id != "" {
		return id, nil
	}
	return importsBucketName, nil
}
