package commands

import (
	"bytes"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newPushCmd()) }

// newPushCmd is `miosa push`: copy a project directory into a sandbox the way
// git would see it, without node_modules, build output or the .git directory.
func newPushCmd() *cobra.Command {
	var to string
	var noIgnore bool
	cmd := &cobra.Command{
		Use:   "push [sandbox] <dir>",
		Short: "Copy a directory into a sandbox, leaving out what .gitignore names",
		Long: `Copy a local directory into a sandbox, by default to /workspace/<dir name>.
In a git repository only the files git tracks or does not ignore are copied
(git ls-files: every .gitignore, .git/info/exclude and the global excludes apply).
Outside one, the directory's .gitignore is read. Either way .git and what
.miosaignore names are left out; --no-ignore copies everything. Patterns follow
.gitignore: '#' comments, '!' to put a path back, a trailing '/' for directories,
a leading '/' to anchor at the root, '**/' for any depth.

Without a sandbox the current one is used ('miosa use').

  miosa push ./app
  miosa push my-box ./app --to /workspace/app
  miosa push my-box . --no-ignore`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sandbox, dir := "", args[0]
			if len(args) == 2 {
				sandbox, dir = args[0], args[1]
			}
			fi, err := os.Stat(dir)
			if err != nil {
				return usagef("%v", err)
			}
			if !fi.IsDir() {
				return usagef("%s is not a directory; 'miosa files cp' copies a file", dir)
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return die(err)
			}
			dest := to
			if dest == "" {
				dest = path.Join("/workspace", filepath.Base(abs))
			}
			c, cfg, err := buildClient()
			if err != nil {
				return die(err)
			}
			ref, err := requireSandbox(sandbox, cfg.CurrentSandbox)
			if err != nil {
				return usagef("%v", err)
			}
			var skip func(string, bool) bool
			skipped := 0
			if !noIgnore {
				s, err := pushSkipper(abs)
				if err != nil {
					return die(err)
				}
				skip = func(rel string, isDir bool) bool {
					out := s(rel, isDir)
					if out {
						skipped++
					}
					return out
				}
			}
			res, err := uploadDirTo(cmd.Context(), c.API, abs, target{ref: ref, path: remotePath(dest)}, skip)
			if err != nil {
				return die(err)
			}
			var total int64
			for _, r := range res {
				total += r.Bytes
			}
			p := printerFor(cmd)
			if isJSON() {
				return p.JSON(map[string]any{"sandbox": ref, "to": remotePath(dest), "files": len(res), "bytes": total, "skipped": skipped})
			}
			p.Success("Pushed %d files (%s) to %s:%s", len(res), formatBytes(total), ref, remotePath(dest))
			if skipped > 0 {
				p.Line("Left out %d paths named by .gitignore, .miosaignore or .git (--no-ignore copies everything).", skipped)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Where in the sandbox (default: /workspace/<dir name>)")
	cmd.Flags().BoolVar(&noIgnore, "no-ignore", false, "Copy everything, ignoring .gitignore, .miosaignore and .git")
	return cmd
}

// pushSkipper decides what `miosa push` leaves out of dir. In a git work tree
// git itself says what belongs (tracked, plus untracked files no ignore rule
// names); .miosaignore still applies on top. Elsewhere the root's .gitignore
// and .miosaignore are read.
func pushSkipper(dir string) (func(rel string, isDir bool) bool, error) {
	filter, err := newPushFilter(dir)
	if err != nil {
		return nil, err
	}
	files, ok := gitPushFiles(dir)
	if !ok {
		return func(rel string, isDir bool) bool { out, _ := filter.skip(rel, isDir); return out }, nil
	}
	keepDirs := map[string]bool{}
	for f := range files {
		for d := path.Dir(f); d != "." && d != "/" && !keepDirs[d]; d = path.Dir(d) {
			keepDirs[d] = true
		}
	}
	return func(rel string, isDir bool) bool {
		if out, _ := filter.skip(rel, isDir); out {
			return true
		}
		if isDir {
			return !keepDirs[rel]
		}
		return !files[rel]
	}, nil
}

// gitPushFiles lists the files git would hand over from dir (relative to it,
// slash-separated), or reports that dir is not in a git work tree.
func gitPushFiles(dir string) (map[string]bool, bool) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, false
	}
	inside, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	if err != nil || strings.TrimSpace(string(inside)) != "true" {
		return nil, false
	}
	out, err := exec.Command("git", "-C", dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, false
	}
	files := map[string]bool{}
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files[filepath.ToSlash(string(f))] = true
		}
	}
	return files, true
}
