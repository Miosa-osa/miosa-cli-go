package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "files",
		Short: "Manage files in a sandbox",
		Long:  `Copy, list, read, delete, and create files inside a sandbox.`,
	}

	cmd.AddCommand(
		newFilesCpCmd(),
		newFilesLsCmd(),
		newFilesCatCmd(),
		newFilesRmCmd(),
		newFilesMkdirCmd(),
	)

	return cmd
}

// parseRemotePath splits "name:path" into (name, path).
// Returns ("", path) if no colon prefix is found (local path).
func parseRemotePath(s string) (sandbox, path string, isRemote bool) {
	// Avoid mistaking Windows drive letters (C:\) as sandbox names.
	if idx := strings.Index(s, ":"); idx > 0 && idx < len(s)-1 {
		candidate := s[:idx]
		// A sandbox name won't contain slashes.
		if !strings.Contains(candidate, "/") && !strings.Contains(candidate, "\\") {
			return candidate, s[idx+1:], true
		}
	}
	return "", s, false
}

func newFilesCpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cp <src> <dst>",
		Short: "Copy files to or from a sandbox",
		Long: `Copy files between local and remote (sandbox) paths.

  Local → Remote:  miosa files cp ./local.txt my-box:/remote/path.txt
  Remote → Local:  miosa files cp my-box:/remote/path.txt ./local.txt
  Remote → Remote: miosa files cp box1:/src.txt box2:/dst.txt`,
		Args: cobra.ExactArgs(2),
		RunE: runFilesCp,
	}
}

func runFilesCp(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	src, dst := args[0], args[1]
	srcSandbox, srcPath, srcRemote := parseRemotePath(src)
	dstSandbox, dstPath, dstRemote := parseRemotePath(dst)

	switch {
	case !srcRemote && dstRemote:
		// Local → Remote upload.
		nameOrID, err := requireSandbox(dstSandbox, cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		sandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(nameOrID))
		if err != nil {
			return die(err)
		}
		if err := sandbox.Files.Upload(cmd.Context(), srcPath, dstPath); err != nil {
			return die(err)
		}
		if isJSON() {
			return p.JSON(map[string]string{"status": "uploaded", "remote": dstPath})
		}
		p.Success("Uploaded %s → %s:%s", srcPath, nameOrID, dstPath)

	case srcRemote && !dstRemote:
		// Remote → Local download.
		nameOrID, err := requireSandbox(srcSandbox, cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		sandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(nameOrID))
		if err != nil {
			return die(err)
		}
		var w io.Writer
		if dstPath == "-" {
			w = os.Stdout
		} else {
			f, err := os.Create(dstPath)
			if err != nil {
				return die(fmt.Errorf("creating local file %q: %w", dstPath, err))
			}
			defer f.Close()
			w = f
		}
		if err := sandbox.Files.DownloadTo(cmd.Context(), srcPath, w); err != nil {
			return die(err)
		}
		if dstPath != "-" {
			if isJSON() {
				return p.JSON(map[string]string{"status": "downloaded", "local": dstPath})
			}
			p.Success("Downloaded %s:%s → %s", nameOrID, srcPath, dstPath)
		}

	case srcRemote && dstRemote:
		// Remote → Remote: download then upload.
		srcName, err := requireSandbox(srcSandbox, cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		dstName, err := requireSandbox(dstSandbox, cfg.CurrentSandbox)
		if err != nil {
			return die(err)
		}
		srcSandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(srcName))
		if err != nil {
			return die(err)
		}
		dstSandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(dstName))
		if err != nil {
			return die(err)
		}
		data, err := srcSandbox.Files.Download(cmd.Context(), srcPath)
		if err != nil {
			return die(err)
		}
		r := strings.NewReader(string(data))
		filename := srcPath[strings.LastIndex(srcPath, "/")+1:]
		if err := dstSandbox.Files.UploadReader(cmd.Context(), r, filename, dstPath); err != nil {
			return die(err)
		}
		if isJSON() {
			return p.JSON(map[string]string{"status": "copied", "src": src, "dst": dst})
		}
		p.Success("Copied %s → %s", src, dst)

	default:
		return die(fmt.Errorf("at least one of src or dst must be a remote path (sandbox:path)"))
	}
	return nil
}

func newFilesLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls <name|id>:<path>",
		Short: "List files in a sandbox directory",
		Args:  cobra.ExactArgs(1),
		RunE:  runFilesLs,
	}
}

func runFilesLs(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID, remotePath, isRemote := parseRemotePath(args[0])
	if !isRemote {
		nameOrID = cfg.CurrentSandbox
		remotePath = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	sandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(nameOrID))
	if err != nil {
		return die(err)
	}

	result, err := sandbox.Files.List(cmd.Context(), remotePath)
	if err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(result)
	}

	if len(result.Entries) == 0 {
		p.Line("(empty)")
		return nil
	}

	headers := []string{"NAME", "SIZE", "TYPE", "MODIFIED"}
	rows := make([][]string, 0, len(result.Entries))
	for _, e := range result.Entries {
		typ := "file"
		if e.IsDir {
			typ = "dir"
		}
		rows = append(rows, []string{
			e.Name,
			formatBytes(e.Size),
			typ,
			e.ModifiedAt,
		})
	}
	p.Table(headers, rows)
	return nil
}

func newFilesCatCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cat <name|id>:<path>",
		Short: "Print a file from a sandbox to stdout",
		Args:  cobra.ExactArgs(1),
		RunE:  runFilesCat,
	}
}

func runFilesCat(cmd *cobra.Command, args []string) error {
	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID, remotePath, isRemote := parseRemotePath(args[0])
	if !isRemote {
		nameOrID = cfg.CurrentSandbox
		remotePath = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	sandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(nameOrID))
	if err != nil {
		return die(err)
	}

	return sandbox.Files.DownloadTo(cmd.Context(), remotePath, os.Stdout)
}

func newFilesRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name|id>:<path>",
		Short: "Remove a file or directory from a sandbox",
		Args:  cobra.ExactArgs(1),
		RunE:  runFilesRm,
	}
}

func runFilesRm(cmd *cobra.Command, args []string) error {
	p := printerFor(cmd)

	c, cfg, err := buildClient()
	if err != nil {
		return die(err)
	}

	nameOrID, remotePath, isRemote := parseRemotePath(args[0])
	if !isRemote {
		nameOrID = cfg.CurrentSandbox
		remotePath = args[0]
	}
	nameOrID, err = requireSandbox(nameOrID, cfg.CurrentSandbox)
	if err != nil {
		return die(err)
	}

	sandbox, err := c.SDK.Sandboxes.Get(cmd.Context(), lookupComputerID(nameOrID))
	if err != nil {
		return die(err)
	}

	if err := sandbox.Files.Delete(cmd.Context(), remotePath); err != nil {
		return die(err)
	}

	if isJSON() {
		return p.JSON(map[string]string{"status": "deleted", "path": remotePath})
	}
	p.Success("Removed %s:%s", nameOrID, remotePath)
	return nil
}

func newFilesMkdirCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mkdir <name|id>:<path>",
		Short: "Create a directory in a sandbox (Phase 7)",
		Long: `Create a directory at the given path in a sandbox.

Note: Requires Phase 7 (mkdir endpoint). Until then the command returns
a clear error explaining what is needed.`,
		Args: cobra.ExactArgs(1),
		RunE: runFilesMkdir,
	}
}

func runFilesMkdir(_ *cobra.Command, _ []string) error {
	return die(fmt.Errorf("files mkdir requires control-plane server v0.7.0 — upgrade with: miosa upgrade"))
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
