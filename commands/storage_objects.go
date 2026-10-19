package commands

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// Objects larger than this go up in parts (the server's single-PUT limit is
// 256 MiB; GET /storage/limits has the live number).
const (
	storageSingleMax = 256 << 20
	storagePartSize  = 64 << 20
)

func bucketObjectPath(bucket, key string) string {
	segs := strings.Split(strings.TrimLeft(key, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return "/storage/buckets/" + url.PathEscape(bucket) + "/objects/" + strings.Join(segs, "/")
}

// objectRows turns the objects listing into table rows. Newer servers send
// data.objects with size and time; older ones send data.keys (plain names).
func objectRows(resp any) {
	m, ok := resp.(map[string]any)
	if !ok {
		return
	}
	data, ok := m["data"].(map[string]any)
	if !ok {
		return
	}
	if objs, ok := data["objects"].([]any); ok {
		m["data"] = objs
		return
	}
	if keys, ok := data["keys"].([]any); ok {
		rows := make([]any, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, map[string]any{"key": k})
		}
		m["data"] = rows
	}
}

func storageObjectCmds() []*cobra.Command {
	return []*cobra.Command{storagePutCmd(), storagePresignCmd(), storageRmObjectCmd()}
}

// extendStorageGet turns 'storage get <bucket>' (show a bucket) into
// 'storage get <bucket> [key] [file]': with a key it downloads the object.
func extendStorageGet(group *cobra.Command) {
	for _, sub := range group.Commands() {
		if sub.Name() != "get" {
			continue
		}
		show := sub.RunE
		download := storageGetCmd()
		sub.Use = "get <bucket> [key] [file]"
		sub.Short = "Show a bucket, or download one of its objects"
		sub.Long = download.Long + "\n\nWith only a bucket, show the bucket."
		sub.Args = cobra.RangeArgs(1, 3)
		sub.RunE = func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				return show(cmd, args)
			}
			return download.RunE(cmd, args)
		}
		return
	}
}

// ─── put ──────────────────────────────────────────────────────────────────────

func storagePutCmd() *cobra.Command {
	var contentType string
	cmd := &cobra.Command{
		Use:   "put <bucket> <file|-> [key]",
		Short: "Upload a file, or standard input, to a bucket",
		Long: `Upload a local file, or standard input with -, as an object. The key defaults to
the file name; with - it is required. Files over 256 MiB go up in 64 MiB parts
automatically (the object appears only when every part is in).

  miosa storage put assets ./logo.png brand/logo.png
  miosa storage put builds ./App.dmg
  cat backup.sql | miosa storage put backups - db/backup.sql`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			bucket, file := args[0], args[1]
			key := filepath.Base(file)
			if len(args) == 3 {
				key = args[2]
			}
			if file == "-" {
				if len(args) < 3 {
					return usagef("reading from standard input needs a key: miosa storage put %s - <key>", bucket)
				}
				ctype := contentType
				if ctype == "" {
					ctype = mime.TypeByExtension(filepath.Ext(key))
				}
				if ctype == "" {
					ctype = "application/octet-stream"
				}
				if _, err := c.API.JSONAny(cmd.Context(), api.Request{Method: http.MethodPut, Path: bucketObjectPath(bucket, key), Body: cmd.InOrStdin(), ContentType: ctype, NoRetry: true}); err != nil {
					return die(err)
				}
				if isJSON() {
					return p.JSON(map[string]any{"bucket": bucket, "key": key})
				}
				p.Success("Uploaded %s to %s", key, bucket)
				return nil
			}
			st, err := os.Stat(file)
			if err != nil {
				return die(fmt.Errorf("reading %s: %w", file, err))
			}
			if st.IsDir() {
				return die(fmt.Errorf("%s is a directory", file))
			}
			ctype := contentType
			if ctype == "" {
				ctype = mime.TypeByExtension(filepath.Ext(file))
			}
			if ctype == "" {
				ctype = "application/octet-stream"
			}
			if st.Size() > storageSingleMax {
				err = storageMultipartPut(cmd.Context(), c.API, bucket, key, file, st.Size(), ctype)
			} else {
				err = storageSinglePut(cmd.Context(), c.API, bucket, key, file, st.Size(), ctype)
			}
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"bucket": bucket, "key": key, "size": st.Size()})
			}
			p.Success("Uploaded %s to %s (%s)", key, bucket, formatBytes(st.Size()))
			return nil
		},
	}
	cmd.Flags().StringVar(&contentType, "content-type", "", "Content type (default: from the file extension)")
	return cmd
}

func storageSinglePut(ctx context.Context, c *api.Client, bucket, key, file string, size int64, ctype string) error {
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	resp, err := c.Do(ctx, api.Request{
		Method: http.MethodPut, Path: bucketObjectPath(bucket, key), Body: f, ContentType: ctype, NoRetry: true,
		Headers: map[string]string{"Content-Length": strconv.FormatInt(size, 10)},
	})
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func storageMultipartPut(ctx context.Context, c *api.Client, bucket, key, file string, size int64, ctype string) error {
	base := "/storage/buckets/" + url.PathEscape(bucket) + "/multipart"
	var created struct {
		Data struct {
			UploadID string `json:"upload_id"`
		} `json:"data"`
	}
	if err := c.Post(ctx, base, map[string]any{"key": key, "content_type": ctype}, &created); err != nil {
		return err
	}
	id := created.Data.UploadID
	abort := func() { _ = c.Delete(context.Background(), base+"/"+id, nil, nil) }

	f, err := os.Open(file)
	if err != nil {
		abort()
		return err
	}
	defer f.Close()

	type part struct {
		N    int    `json:"part_number"`
		Etag string `json:"etag"`
	}
	var parts []part
	for n, off := 1, int64(0); off < size; n, off = n+1, off+storagePartSize {
		length := int64(storagePartSize)
		if off+length > size {
			length = size - off
		}
		var res struct {
			Data struct {
				Etag string `json:"etag"`
			} `json:"data"`
		}
		var lastErr error
		for attempt := 0; attempt < 3; attempt++ {
			lastErr = c.JSON(ctx, api.Request{
				Method: http.MethodPut, Path: fmt.Sprintf("%s/%s/parts/%d", base, id, n),
				Body: io.NewSectionReader(f, off, length), ContentType: "application/octet-stream", NoRetry: true,
				Headers: map[string]string{"Content-Length": strconv.FormatInt(length, 10)},
			}, &res)
			if lastErr == nil {
				break
			}
		}
		if lastErr != nil {
			abort()
			return fmt.Errorf("part %d: %w", n, lastErr)
		}
		parts = append(parts, part{N: n, Etag: res.Data.Etag})
	}
	if err := c.Post(ctx, base+"/"+id+"/complete", map[string]any{"parts": parts}, nil); err != nil {
		abort()
		return err
	}
	return nil
}

// ─── get ──────────────────────────────────────────────────────────────────────

func storageGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <bucket> <key> [file]",
		Short: "Download an object",
		Long: `Download an object to a file (default: the key's base name). Use - to write to
standard output.

  miosa storage get builds App.dmg
  miosa storage get assets brand/logo.png -`,
		Args: cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			bucket, key := args[0], args[1]
			dest := filepath.Base(key)
			if len(args) == 3 {
				dest = args[2]
			}
			resp, err := c.API.Stream(cmd.Context(), api.Request{Method: http.MethodGet, Path: bucketObjectPath(bucket, key)})
			if err != nil {
				return die(err)
			}
			defer resp.Body.Close()
			if dest == "-" {
				if _, err := io.Copy(cmd.OutOrStdout(), resp.Body); err != nil {
					return die(err)
				}
				return nil
			}
			tmp := dest + ".part"
			out, err := os.Create(tmp)
			if err != nil {
				return die(err)
			}
			n, copyErr := io.Copy(out, resp.Body)
			closeErr := out.Close()
			if copyErr == nil && resp.ContentLength >= 0 && n != resp.ContentLength {
				copyErr = fmt.Errorf("download ended after %d of %d bytes", n, resp.ContentLength)
			}
			if copyErr != nil || closeErr != nil {
				_ = os.Remove(tmp)
				if copyErr == nil {
					copyErr = closeErr
				}
				return die(copyErr)
			}
			if err := os.Rename(tmp, dest); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"bucket": bucket, "key": key, "file": dest, "size": n})
			}
			p.Success("Saved %s (%s)", dest, formatBytes(n))
			return nil
		},
	}
}

// ─── presign ──────────────────────────────────────────────────────────────────

func parseTTL(s string) (int, error) {
	if s == "" {
		return 3600, nil
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Second {
		return 0, usageErr{fmt.Errorf("--ttl %q: use seconds or a duration like 10m", s)}
	}
	return int(d.Seconds()), nil
}

func storagePresignCmd() *cobra.Command {
	var method, ttl string
	cmd := &cobra.Command{
		Use:   "presign <bucket> <key>",
		Short: "Make a temporary URL for one object",
		Long: `Print a signed URL that reads (GET) or writes (PUT) one object without an API
key, for up to 7 days. A private bucket refuses presigned URLs.

  miosa storage presign builds App.dmg --ttl 10m
  miosa storage presign builds incoming.bin --method PUT`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			secs, err := parseTTL(ttl)
			if err != nil {
				return die(err)
			}
			m := strings.ToUpper(method)
			if m != "GET" && m != "PUT" {
				return die(usageErr{fmt.Errorf("--method must be GET or PUT")})
			}
			var out struct {
				URL  string `json:"url"`
				Data struct {
					URL string `json:"url"`
				} `json:"data"`
			}
			err = c.API.Post(cmd.Context(), "/storage/buckets/"+url.PathEscape(args[0])+"/presign",
				map[string]any{"key": args[1], "method": m, "expires_in": secs}, &out)
			if err != nil {
				return die(err)
			}
			u := out.URL
			if u == "" {
				u = out.Data.URL
			}
			if isJSON() {
				return p.JSON(map[string]any{"url": u, "method": m, "expires_in": secs})
			}
			p.Line("%s", u)
			return nil
		},
	}
	cmd.Flags().StringVar(&method, "method", "GET", "GET to read, PUT to upload")
	cmd.Flags().StringVar(&ttl, "ttl", "1h", "How long the URL works: seconds or a duration (10m, 2h)")
	return cmd
}

// ─── rm-object ────────────────────────────────────────────────────────────────

func storageRmObjectCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "rm-object <bucket> <key>",
		Short: "Delete one object",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, _, err := buildClient()
			if err != nil {
				return die(err)
			}
			if err := c.API.Delete(cmd.Context(), bucketObjectPath(args[0], args[1]), nil, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"bucket": args[0], "key": args[1], "deleted": true})
			}
			p.Success("Deleted %s from %s", args[1], args[0])
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Accepted for scripts; deleting one object does not ask")
	return cmd
}

// ─── keys ─────────────────────────────────────────────────────────────────────

func storageKeysCmd() *cobra.Command {
	b := idArg("bucket")
	keyCols := []Col{{Head: "KEY ID", Path: "access_key_id"}, {Head: "NAME", Path: "name"}, {Head: "PERMISSION", Path: "permission"}, {Head: "PREFIX", Path: "key_prefix"}, {Head: "STATUS", Path: "status"}, {Head: "ID", Path: "id"}, {Head: "CREATED", Path: "created_at", Fmt: "age"}}
	return group("keys", "S3 keys for a bucket",
		"Per-bucket keys for S3 clients (aws s3, SDKs). The secret is shown once, at creation.", []string{"key"},
		Op{Use: "list <bucket>", Aliases: []string{"ls"}, Short: "List a bucket's keys (no secrets)", Method: "GET", Path: "/storage/buckets/{0}/credentials", Args: []Arg{b}, Cols: keyCols},
		Op{
			Use: "create <bucket>", Short: "Create an S3 key (the secret is shown once)", Method: "POST", Path: "/storage/buckets/{0}/credentials", Args: []Arg{b},
			Flags: []Flag{
				{Name: "name", Usage: "Label for the key", Default: "cli"},
				{Name: "read-write", Type: "bool", Usage: "Allow writes and deletes (default: read only)", In: "body", Field: "read_write"},
				{Name: "prefix", Usage: "Only keys under this prefix", Field: "key_prefix"},
				{Name: "expires-at", Usage: "Expiry as an RFC 3339 time", Field: "expires_at"},
			},
			Body: func(a []string, body map[string]any) (map[string]any, error) {
				body["permission"] = "read"
				if rw, _ := body["read_write"].(bool); rw {
					body["permission"] = "read_write"
				}
				delete(body, "read_write")
				return body, nil
			},
		},
		Op{Use: "revoke <bucket> <id>", Aliases: []string{"rm"}, Short: "Revoke a key", Method: "DELETE", Path: "/storage/buckets/{0}/credentials/{1}", Args: []Arg{b, idArg("id")}, Confirm: "Revoke key {1}?", Done: "Revoked {1}"},
	)
}
