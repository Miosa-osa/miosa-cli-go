package commands

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// homeDir is where relative remote paths start, and the only directory shared
// between a sandbox's VM and its containers.
const homeDir = "/home/user"

func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Copy, list, read, move and delete files in a sandbox",
		Long: `Work with files inside a sandbox. A remote path is written <sandbox>:<path>;
the sandbox may be a name or id, and can be omitted for the current sandbox.
Relative remote paths start in ` + homeDir + `.

  miosa files ls my-box:/workspace
  miosa files cp ./app.py my-box:/workspace/app.py
  miosa files cp -r ./src my-box:/workspace/
  miosa files cp my-box:/workspace/out.json ./out.json
  miosa files cat my-box:/workspace/out.json
  echo hi | miosa files write my-box:/tmp/hi.txt`,
	}
	cmd.AddCommand(
		newFilesLsCmd(),
		newFilesCatCmd(),
		newFilesCpCmd(),
		newFilesWriteCmd(),
		newFilesMkdirCmd(),
		newFilesRmCmd(),
		newFilesMvCmd(),
		newFilesStatCmd(),
		newFilesChmodCmd(),
	)
	return cmd
}

// parseRemotePath splits "name:path" into (name, path).
// Returns ("", path, false) if no colon prefix is found (local path).
func parseRemotePath(s string) (sandbox, path string, isRemote bool) {
	// Avoid mistaking Windows drive letters (C:\) as sandbox names.
	if idx := strings.Index(s, ":"); idx > 0 {
		candidate := s[:idx]
		// A sandbox name won't contain slashes or dots-and-slashes.
		if !strings.ContainsAny(candidate, "/\\") && !(len(candidate) == 1 && strings.ContainsAny(s[idx+1:], "\\")) {
			return candidate, s[idx+1:], true
		}
	}
	return "", s, false
}

// remotePath makes p absolute: relative paths start in the home directory.
func remotePath(p string) string {
	if p == "" {
		return homeDir
	}
	if !strings.HasPrefix(p, "/") {
		return homeDir + "/" + p
	}
	return path.Clean(p)
}

// target is a resolved remote location.
type target struct {
	ref  string // name, id or "current"
	path string
}

func (t target) fs(op string) string {
	return "/sandboxes/" + url.PathEscape(t.ref) + "/fs" + op
}

// remoteTarget parses a remote argument. A bare path (no "sandbox:") refers to
// the current sandbox.
func remoteTarget(arg, current string) (target, error) {
	name, p, remote := parseRemotePath(arg)
	if !remote {
		name, p = "", arg
	}
	ref, err := requireSandbox(name, current)
	if err != nil {
		return target{}, usageErr{err}
	}
	return target{ref: ref, path: remotePath(p)}, nil
}

// fsEntry is one directory entry from the file API.
type fsEntry struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	SizeBytes  int64  `json:"size_bytes"`
	ModifiedAt string `json:"modified_at"`
}

func (e fsEntry) isDir() bool { return e.Type == "directory" || e.Type == "dir" }

type fsList struct {
	Path  string    `json:"path"`
	Files []fsEntry `json:"files"`
}

func fsListDir(ctx context.Context, c *api.Client, t target) (fsList, error) {
	var out fsList
	q := url.Values{}
	q.Set("path", t.path)
	err := c.Get(ctx, t.fs(""), q, &out)
	return out, err
}

type fsStat struct {
	IsDir bool   `json:"is_dir"`
	Mode  string `json:"mode"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Mtime string `json:"modified_at"`
}

func fsStatPath(ctx context.Context, c *api.Client, t target) (fsStat, error) {
	var out struct {
		Data fsStat `json:"data"`
		fsStat
	}
	if err := c.Post(ctx, t.fs("/stat"), map[string]string{"path": t.path}, &out); err != nil {
		return fsStat{}, err
	}
	if out.Data.Path != "" || out.Data.Name != "" {
		return out.Data, nil
	}
	return out.fsStat, nil
}

// fsDownload streams a remote file into w.
func fsDownload(ctx context.Context, c *api.Client, t target, w io.Writer) (int64, error) {
	q := url.Values{}
	q.Set("path", t.path)
	resp, err := c.Do(ctx, api.Request{Method: http.MethodGet, Path: t.fs("/download"), Query: q})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return io.Copy(w, resp.Body)
}

// fsWrite uploads data to a remote file as base64, which carries any bytes.
func fsWrite(ctx context.Context, c *api.Client, t target, data []byte) error {
	body := map[string]string{"path": t.path, "content_base64": base64.StdEncoding.EncodeToString(data)}
	return c.Post(ctx, "/sandboxes/"+url.PathEscape(t.ref)+"/files/write", body, nil)
}

func fsMkdir(ctx context.Context, c *api.Client, t target) error {
	return c.Post(ctx, t.fs("/mkdir"), map[string]any{"path": t.path, "recursive": true, "mode": "0755"}, nil)
}

// ─── ls ───────────────────────────────────────────────────────────────────────

func newFilesLsCmd() *cobra.Command {
	var long bool
	cmd := &cobra.Command{
		Use:   "ls [<sandbox>:]<path>",
		Short: "List a sandbox directory",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			arg := ""
			if len(args) > 0 {
				arg = args[0]
			}
			t, err := remoteTarget(arg, cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			out, err := fsListDir(cmd.Context(), c.API, t)
			if err != nil {
				return die(err)
			}
			sort.Slice(out.Files, func(i, j int) bool {
				if out.Files[i].isDir() != out.Files[j].isDir() {
					return out.Files[i].isDir()
				}
				return out.Files[i].Name < out.Files[j].Name
			})
			if isJSON() {
				return p.JSON(out)
			}
			if len(out.Files) == 0 {
				p.Line("(empty)")
				return nil
			}
			rows := make([][]string, 0, len(out.Files))
			for _, e := range out.Files {
				name, typ := e.Name, "file"
				if e.isDir() {
					name, typ = name+"/", "dir"
				}
				if long {
					rows = append(rows, []string{name, formatBytes(e.SizeBytes), typ, e.ModifiedAt})
				} else {
					rows = append(rows, []string{name, formatBytes(e.SizeBytes), typ})
				}
			}
			if long {
				p.Table([]string{"NAME", "SIZE", "TYPE", "MODIFIED"}, rows)
			} else {
				p.Table([]string{"NAME", "SIZE", "TYPE"}, rows)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&long, "long", "l", false, "Show modification times")
	return cmd
}

// ─── cat ──────────────────────────────────────────────────────────────────────

func newFilesCatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cat [<sandbox>:]<path>",
		Short: "Print a file from a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			t, err := remoteTarget(args[0], cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			if _, err := fsDownload(cmd.Context(), c.API, t, cmd.OutOrStdout()); err != nil {
				return die(err)
			}
			return nil
		},
	}
}

// ─── write ────────────────────────────────────────────────────────────────────

func newFilesWriteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "write [<sandbox>:]<path>",
		Short: "Write standard input to a file in a sandbox",
		Long: `Read standard input and store it as a file in the sandbox, replacing any
existing file. Parent directories are created.

  cat config.yaml | miosa files write my-box:/workspace/config.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			t, err := remoteTarget(args[0], cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			data, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return die(fmt.Errorf("reading standard input: %w", err))
			}
			if err := ensureParent(cmd.Context(), c.API, t); err != nil {
				return die(err)
			}
			if err := fsWrite(cmd.Context(), c.API, t, data); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]any{"path": t.path, "bytes": len(data)})
			}
			p.Success("Wrote %s (%s)", t.path, formatBytes(int64(len(data))))
			return nil
		},
	}
}

// ensureParent creates the directory that will hold t.
func ensureParent(ctx context.Context, c *api.Client, t target) error {
	dir := path.Dir(t.path)
	if dir == "/" || dir == "." {
		return nil
	}
	return fsMkdir(ctx, c, target{ref: t.ref, path: dir})
}

// ─── cp ───────────────────────────────────────────────────────────────────────

type copied struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Bytes int64  `json:"bytes"`
}

func newFilesCpCmd() *cobra.Command {
	var recursive bool
	cmd := &cobra.Command{
		Use:   "cp <src> <dst>",
		Short: "Copy files to, from or between sandboxes",
		Long: `Copy files between your machine and a sandbox, or between sandboxes.
Remote paths are <sandbox>:<path>. A directory needs -r.

  Local to remote:   miosa files cp ./local.txt my-box:/workspace/local.txt
  Remote to local:   miosa files cp my-box:/workspace/out.txt ./out.txt
  Remote to remote:  miosa files cp a:/src.txt b:/dst.txt
  A directory:       miosa files cp -r ./site my-box:/workspace/

A destination ending in / (or an existing directory) receives the source's name.
File contents go through the JSON API as base64; the server's upload limit applies.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFilesCp(cmd, args, recursive)
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Copy directories")
	return cmd
}

func runFilesCp(cmd *cobra.Command, args []string, recursive bool) error {
	p := printerFor(cmd)
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}
	ctx := cmd.Context()

	srcName, srcPath, srcRemote := parseRemotePath(args[0])
	dstName, dstPath, dstRemote := parseRemotePath(args[1])
	if srcRemote && !(dstRemote) {
		// remote -> local
		src, err := remoteTarget(args[0], cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		_ = srcName
		_ = srcPath
		res, err := downloadTree(ctx, c.API, src, args[1], recursive)
		return finishCopy(p, res, err)
	}
	if !srcRemote && dstRemote {
		// local -> remote
		dst, err := remoteTarget(args[1], cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		_ = dstName
		_ = dstPath
		res, err := uploadTree(ctx, c.API, args[0], dst, strings.HasSuffix(args[1], "/"), recursive)
		return finishCopy(p, res, err)
	}
	if srcRemote && dstRemote {
		src, err := remoteTarget(args[0], cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		dst, err := remoteTarget(args[1], cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		st, err := fsStatPath(ctx, c.API, src)
		if err != nil {
			return die(err)
		}
		if st.IsDir {
			return usagef("copying a directory between sandboxes is not supported; copy it to your machine first")
		}
		var buf bytes.Buffer
		if _, err := fsDownload(ctx, c.API, src, &buf); err != nil {
			return die(err)
		}
		if strings.HasSuffix(args[1], "/") {
			dst.path = path.Join(dst.path, path.Base(src.path))
		}
		if err := ensureParent(ctx, c.API, dst); err != nil {
			return die(err)
		}
		if err := fsWrite(ctx, c.API, dst, buf.Bytes()); err != nil {
			return die(err)
		}
		return finishCopy(p, []copied{{From: args[0], To: args[1], Bytes: int64(buf.Len())}}, nil)
	}
	return usagef("one side must be a sandbox path (<sandbox>:<path>)")
}

func finishCopy(p interface {
	JSON(v interface{}) error
	Success(format string, args ...interface{})
}, res []copied, err error) error {
	if err != nil {
		return die(err)
	}
	var total int64
	for _, r := range res {
		total += r.Bytes
	}
	if isJSON() {
		return p.JSON(map[string]any{"files": res, "bytes": total})
	}
	if len(res) == 1 {
		p.Success("Copied %s -> %s (%s)", res[0].From, res[0].To, formatBytes(total))
	} else {
		p.Success("Copied %d files (%s)", len(res), formatBytes(total))
	}
	return nil
}

func uploadTree(ctx context.Context, c *api.Client, local string, dst target, dstIsDir, recursive bool) ([]copied, error) {
	fi, err := os.Stat(local)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		to := dst
		if dstIsDir {
			to.path = path.Join(dst.path, filepath.Base(local))
		} else if st, err := fsStatPath(ctx, c, dst); err == nil && st.IsDir {
			to.path = path.Join(dst.path, filepath.Base(local))
		}
		data, err := os.ReadFile(local)
		if err != nil {
			return nil, err
		}
		if err := ensureParent(ctx, c, to); err != nil {
			return nil, err
		}
		if err := fsWrite(ctx, c, to, data); err != nil {
			return nil, err
		}
		return []copied{{From: local, To: to.path, Bytes: int64(len(data))}}, nil
	}
	if !recursive {
		return nil, usageErr{fmt.Errorf("%s is a directory; use -r to copy it", local)}
	}
	// The directory lands inside dst when dst ends in / or exists, else becomes dst.
	root := dst.path
	if dstIsDir {
		root = path.Join(dst.path, filepath.Base(filepath.Clean(local)))
	} else if st, err := fsStatPath(ctx, c, dst); err == nil && st.IsDir {
		root = path.Join(dst.path, filepath.Base(filepath.Clean(local)))
	}
	var res []copied
	err = filepath.WalkDir(local, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, _ := filepath.Rel(local, p)
		remote := target{ref: dst.ref, path: path.Join(root, filepath.ToSlash(rel))}
		if d.IsDir() {
			return fsMkdir(ctx, c, remote)
		}
		if !d.Type().IsRegular() {
			return nil // skip sockets, devices; symlinks are not followed
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := fsWrite(ctx, c, remote, data); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		res = append(res, copied{From: p, To: remote.path, Bytes: int64(len(data))})
		return nil
	})
	return res, err
}

func downloadTree(ctx context.Context, c *api.Client, src target, local string, recursive bool) ([]copied, error) {
	st, err := fsStatPath(ctx, c, src)
	if err != nil {
		return nil, err
	}
	localIsDir := strings.HasSuffix(local, "/") || isLocalDir(local)
	if !st.IsDir {
		to := local
		if localIsDir {
			to = filepath.Join(local, path.Base(src.path))
		}
		n, err := downloadFile(ctx, c, src, to)
		if err != nil {
			return nil, err
		}
		return []copied{{From: src.path, To: to, Bytes: n}}, nil
	}
	if !recursive {
		return nil, usageErr{fmt.Errorf("%s is a directory; use -r to copy it", src.path)}
	}
	root := local
	if localIsDir {
		root = filepath.Join(local, path.Base(src.path))
	}
	var res []copied
	var walk func(rt target, dir string) error
	walk = func(rt target, dir string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		list, err := fsListDir(ctx, c, rt)
		if err != nil {
			return err
		}
		for _, e := range list.Files {
			child := target{ref: rt.ref, path: path.Join(rt.path, e.Name)}
			if e.isDir() {
				if err := walk(child, filepath.Join(dir, e.Name)); err != nil {
					return err
				}
				continue
			}
			to := filepath.Join(dir, e.Name)
			n, err := downloadFile(ctx, c, child, to)
			if err != nil {
				return fmt.Errorf("%s: %w", child.path, err)
			}
			res = append(res, copied{From: child.path, To: to, Bytes: n})
		}
		return nil
	}
	return res, walk(src, root)
}

func isLocalDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// downloadFile saves a remote file; a failed transfer leaves no partial file.
func downloadFile(ctx context.Context, c *api.Client, src target, to string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".miosa-dl-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())
	n, err := fsDownload(ctx, c, src, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return 0, err
	}
	return n, os.Rename(tmp.Name(), to)
}

// ─── mkdir, rm, mv, stat, chmod ───────────────────────────────────────────────

func newFilesMkdirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mkdir [<sandbox>:]<path>",
		Short: "Create a directory (and its parents) in a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			t, err := remoteTarget(args[0], cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			if err := fsMkdir(cmd.Context(), c.API, t); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"created": t.path})
			}
			p.Success("Created directory %s", t.path)
			return nil
		},
	}
}

func newFilesRmCmd() *cobra.Command {
	var recursive, force bool
	cmd := &cobra.Command{
		Use:   "rm [<sandbox>:]<path>...",
		Short: "Delete files or directories in a sandbox",
		Long: `Delete one or more paths. A directory needs -r. A path that does not exist is an
error unless -f is given.

  miosa files rm my-box:/tmp/old.log
  miosa files rm -r my-box:/workspace/build`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			var removed []string
			for _, a := range args {
				t, err := remoteTarget(a, cfg.CurrentSandbox)
				if err != nil {
					return die(err)
				}
				st, err := fsStatPath(cmd.Context(), c.API, t)
				if err != nil {
					if force && api.IsStatus(err, http.StatusNotFound) {
						continue
					}
					return die(fmt.Errorf("%s: %w", t.path, err))
				}
				if st.IsDir && !recursive {
					return usagef("%s is a directory; use -r to delete it", t.path)
				}
				body := map[string]any{"path": t.path, "recursive": recursive}
				if err := c.API.JSON(cmd.Context(), api.Request{Method: http.MethodDelete, Path: t.fs(""), Body: body}, nil); err != nil {
					return die(fmt.Errorf("%s: %w", t.path, err))
				}
				removed = append(removed, t.path)
			}
			if isJSON() {
				return p.JSON(map[string]any{"removed": removed})
			}
			for _, r := range removed {
				p.Success("Removed %s", r)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Delete directories and their contents")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Ignore paths that do not exist")
	return cmd
}

func newFilesMvCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "mv [<sandbox>:]<from> <to>",
		Aliases: []string{"rename"},
		Short:   "Move or rename a file inside a sandbox",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			from, err := remoteTarget(args[0], cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			to := args[1]
			if n, tp, ok := parseRemotePath(to); ok {
				if n != "" && n != from.ref {
					return usagef("mv works inside one sandbox; use 'files cp' between sandboxes")
				}
				to = tp
			}
			dest := remotePath(to)
			if err := c.API.Post(cmd.Context(), from.fs("/rename"), map[string]string{"from": from.path, "to": dest}, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"from": from.path, "to": dest})
			}
			p.Success("Moved %s -> %s", from.path, dest)
			return nil
		},
	}
}

func newFilesStatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stat [<sandbox>:]<path>",
		Short: "Show a file's type, size, mode and modification time",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			t, err := remoteTarget(args[0], cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			st, err := fsStatPath(cmd.Context(), c.API, t)
			if err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(st)
			}
			kind := "file"
			if st.IsDir {
				kind = "directory"
			}
			p.Fields([][2]string{{"Path", orDefault(st.Path, t.path)}, {"Type", kind}, {"Size", formatBytes(st.Size)}, {"Mode", st.Mode}, {"Modified", st.Mtime}})
			return nil
		},
	}
}

func newFilesChmodCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "chmod <mode> [<sandbox>:]<path>",
		Short: "Change a file's permissions (octal mode, for example 755)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := printerFor(cmd)
			mode := strings.TrimPrefix(args[0], "0o")
			for _, ch := range mode {
				if ch < '0' || ch > '7' {
					return usagef("mode must be octal, for example 755 (got %q)", args[0])
				}
			}
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			t, err := remoteTarget(args[1], cfg.CurrentSandbox)
			if err != nil {
				return die(err)
			}
			if err := c.API.Post(cmd.Context(), t.fs("/chmod"), map[string]string{"path": t.path, "mode": mode}, nil); err != nil {
				return die(err)
			}
			if isJSON() {
				return p.JSON(map[string]string{"path": t.path, "mode": mode})
			}
			p.Success("chmod %s %s", mode, t.path)
			return nil
		},
	}
}
