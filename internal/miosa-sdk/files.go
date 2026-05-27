package miosa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// FilesService manages files on a specific compute resource.
// When accessed via Client.Files it is unscoped; when accessed via
// Computer.Files it is scoped to that computer's ID.
type FilesService struct {
	client       *Client
	computerID   string // empty when accessed from client root
	resourceBase string // "computers" or "sandboxes"
}

// For returns a FilesService scoped to the given computer ID.
// Use this when you hold a *Client but want to operate on a specific computer.
func (s *FilesService) For(computerID string) *FilesService {
	return &FilesService{client: s.client, computerID: computerID, resourceBase: s.baseResource()}
}

// ForSandbox returns a FilesService scoped to a sandbox ID.
func (s *FilesService) ForSandbox(sandboxID string) *FilesService {
	return &FilesService{client: s.client, computerID: sandboxID, resourceBase: "sandboxes"}
}

func (s *FilesService) baseResource() string {
	if s.resourceBase != "" {
		return s.resourceBase
	}
	return "computers"
}

func (s *FilesService) base() string {
	return fmt.Sprintf("/%s/%s/files", s.baseResource(), s.computerID)
}

// Upload copies a local file to the computer at remotePath.
// localPath must refer to a readable file on disk.
func (s *FilesService) Upload(ctx context.Context, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("miosa: cannot open %q: %w", localPath, err)
	}
	defer f.Close()

	return s.UploadReader(ctx, f, filepath.Base(localPath), remotePath)
}

// UploadReader sends the content from r to remotePath on the computer.
// filename is used as the multipart field filename.
func (s *FilesService) UploadReader(ctx context.Context, r io.Reader, filename, remotePath string) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	// Remote path field.
	if err := mw.WriteField("path", remotePath); err != nil {
		return fmt.Errorf("miosa: failed to write multipart field: %w", err)
	}

	// File field.
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("miosa: failed to create form file: %w", err)
	}
	if _, err := io.Copy(fw, r); err != nil {
		return fmt.Errorf("miosa: failed to buffer upload: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("miosa: failed to close multipart writer: %w", err)
	}

	return s.client.postMultipart(ctx, s.base()+"/upload", bytes.NewReader(buf.Bytes()), mw.FormDataContentType(), nil)
}

// Download retrieves a file from remotePath on the computer and returns its
// raw bytes. For large files consider using DownloadTo.
func (s *FilesService) Download(ctx context.Context, remotePath string) ([]byte, error) {
	q := buildQuery(map[string]string{"path": remotePath})
	data, _, err := s.client.getRaw(ctx, s.base()+"/download"+q)
	return data, err
}

// DownloadTo streams a remote file directly to w without buffering the entire
// body in memory.
func (s *FilesService) DownloadTo(ctx context.Context, remotePath string, w io.Writer) error {
	q := buildQuery(map[string]string{"path": remotePath})
	resp, err := s.client.do(ctx, "GET", s.base()+"/download"+q, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

// List returns the directory listing at path on the computer.
func (s *FilesService) List(ctx context.Context, path string) (*FileListResult, error) {
	q := buildQuery(map[string]string{"path": path})
	var out FileListResult
	if err := s.client.getJSON(ctx, s.base()+q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Export generates a temporary download URL for a file on the computer.
func (s *FilesService) Export(ctx context.Context, remotePath string) (*FileExportResult, error) {
	body := map[string]string{"path": remotePath}
	var out FileExportResult
	if err := s.client.postJSON(ctx, s.base()+"/export", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Delete removes a file or directory at remotePath on the computer.
func (s *FilesService) Delete(ctx context.Context, remotePath string) error {
	body, _ := json.Marshal(map[string]string{"path": remotePath})
	resp, err := s.client.do(ctx, "DELETE", s.base(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ─── stdlib-parity extensions ─────────────────────────────────────────────────

// Stat returns metadata for remotePath. Does not follow symlinks (lstat semantics).
func (s *FilesService) Stat(ctx context.Context, remotePath string) (*FileStat, error) {
	const op = "FilesService.Stat"
	var out struct {
		Data FileStat `json:"data"`
	}
	if err := s.client.postJSON(ctx, s.base()+"/stat", map[string]string{"path": remotePath}, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Mkdir creates a directory at remotePath.
// By default it creates parent directories recursively (mode 0755).
func (s *FilesService) Mkdir(ctx context.Context, remotePath string, opts ...MkdirOptions) error {
	const op = "FilesService.Mkdir"
	recursive := true
	mode := "0755"
	if len(opts) > 0 {
		o := opts[0]
		if !o.Recursive {
			recursive = false
		}
		if o.Mode != "" {
			mode = o.Mode
		}
	}
	body := map[string]interface{}{
		"path":      remotePath,
		"recursive": recursive,
		"mode":      mode,
	}
	if err := s.client.postJSON(ctx, s.base()+"/mkdir", body, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// Rename moves from to to inside the computer. Creates destination parent
// directories if they do not exist.
func (s *FilesService) Rename(ctx context.Context, from, to string) error {
	const op = "FilesService.Rename"
	if err := s.client.postJSON(ctx, s.base()+"/rename", map[string]string{"from": from, "to": to}, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// Copy copies from to to inside the computer.
// Set opts.Recursive = true when copying a directory tree.
func (s *FilesService) Copy(ctx context.Context, from, to string, opts ...CopyOptions) error {
	const op = "FilesService.Copy"
	recursive := false
	if len(opts) > 0 {
		recursive = opts[0].Recursive
	}
	body := map[string]interface{}{"from": from, "to": to, "recursive": recursive}
	if err := s.client.postJSON(ctx, s.base()+"/copy", body, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// Chmod changes permissions on remotePath.
// mode may be an octal integer (0o755) or an octal string ("0755").
func (s *FilesService) Chmod(ctx context.Context, remotePath string, mode interface{}) error {
	const op = "FilesService.Chmod"
	var modeStr string
	switch v := mode.(type) {
	case int:
		modeStr = fmt.Sprintf("%04o", v)
	case string:
		modeStr = v
	default:
		return fmt.Errorf("%s: mode must be int or string", op)
	}
	body := map[string]string{"path": remotePath, "mode": modeStr}
	if err := s.client.postJSON(ctx, s.base()+"/chmod", body, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// Readdir returns a rich directory listing for remotePath.
// Each entry includes name, size, is_dir, is_symlink, and modified_at.
func (s *FilesService) Readdir(ctx context.Context, remotePath string) ([]DirEntry, error) {
	const op = "FilesService.Readdir"
	q := buildQuery(map[string]string{"path": remotePath})
	var out struct {
		Data struct {
			Entries []DirEntry `json:"entries"`
		} `json:"data"`
	}
	if err := s.client.getJSON(ctx, s.base()+"/readdir"+q, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out.Data.Entries, nil
}

// FS returns a ScopedFS that prepends workingDir to all relative path arguments.
//
//	fs := computer.Files.FS("/workspace")
//	_ = fs.WriteFile(ctx, "main.py", []byte("print(1)"))
//	data, _ := fs.ReadFile(ctx, "main.py")
func (s *FilesService) FS(workingDir string) *ScopedFS {
	return &ScopedFS{files: s, workingDir: cleanDir(workingDir)}
}

// cleanDir normalises a working directory: removes trailing slash unless root.
func cleanDir(dir string) string {
	if dir == "/" {
		return dir
	}
	return strings.TrimRight(dir, "/")
}

// ─── ScopedFS ─────────────────────────────────────────────────────────────────

// ScopedFS is a thin wrapper over FilesService that prepends a fixed working
// directory to all path arguments. Obtain via FilesService.FS("/workspace").
type ScopedFS struct {
	files      *FilesService
	workingDir string
}

func (fs *ScopedFS) resolve(p string) string {
	if path.IsAbs(p) {
		return p
	}
	joined := fs.workingDir + "/" + p
	return path.Clean(joined)
}

// WriteFile writes content to path (relative or absolute).
func (fs *ScopedFS) WriteFile(ctx context.Context, p string, content []byte) error {
	return fs.files.UploadReader(ctx, bytes.NewReader(content), filepath.Base(p), fs.resolve(p))
}

// ReadFile reads the file at path and returns its bytes.
func (fs *ScopedFS) ReadFile(ctx context.Context, p string) ([]byte, error) {
	return fs.files.Download(ctx, fs.resolve(p))
}

// Readdir lists a directory (relative or absolute path).
func (fs *ScopedFS) Readdir(ctx context.Context, p string) ([]DirEntry, error) {
	return fs.files.Readdir(ctx, fs.resolve(p))
}

// Stat stats a path (relative or absolute).
func (fs *ScopedFS) Stat(ctx context.Context, p string) (*FileStat, error) {
	return fs.files.Stat(ctx, fs.resolve(p))
}

// Mkdir creates a directory (relative or absolute).
func (fs *ScopedFS) Mkdir(ctx context.Context, p string, opts ...MkdirOptions) error {
	return fs.files.Mkdir(ctx, fs.resolve(p), opts...)
}
