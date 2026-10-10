package commands_test

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// fsAPI is an in-memory sandbox filesystem behind the real /fs routes.
type fsAPI struct {
	*fakeAPI
	files map[string]string // path -> content
	dirs  map[string]bool
}

func newFSAPI(t *testing.T) *fsAPI {
	t.Helper()
	fs := &fsAPI{files: map[string]string{}, dirs: map[string]bool{"/home/user": true, "/workspace": true}}
	base := "/sandboxes/" + boxID
	fs.fakeAPI = newFakeAPI(t, func(c call) (int, string) {
		q, _ := url.ParseQuery(c.Query)
		path, _ := c.Body["path"].(string)
		switch {
		case c.is("GET", "/sandboxes/by-name/my-box"):
			return 200, `{"id":"` + boxID + `"}`
		case c.is("GET", base+"/fs"):
			p := q.Get("path")
			if !fs.dirs[p] {
				return 502, `{"error":"list_failed","code":"AGENT_UNAVAILABLE"}`
			}
			var entries []string
			for d := range fs.dirs {
				if filepathDir(d) == p && d != p {
					entries = append(entries, `{"name":"`+filepath.Base(d)+`","type":"directory","size_bytes":4096,"modified_at":"2026-01-01T00:00:00Z"}`)
				}
			}
			for f, content := range fs.files {
				if filepathDir(f) == p {
					entries = append(entries, `{"name":"`+filepath.Base(f)+`","type":"file","size_bytes":`+itoa(len(content))+`,"modified_at":"2026-01-02T00:00:00Z"}`)
				}
			}
			return 200, `{"path":"` + p + `","files":[` + strings.Join(entries, ",") + `]}`
		case c.is("GET", base+"/fs/download"):
			content, ok := fs.files[q.Get("path")]
			if !ok {
				return 404, `{"error":{"code":"NOT_FOUND","message":"file not found"}}`
			}
			return 200, content
		case c.is("POST", base+"/fs/stat"):
			if fs.dirs[path] {
				return 200, `{"data":{"is_dir":true,"is_file":false,"mode":"0o755","name":"` + filepath.Base(path) + `","path":"` + path + `","size":4096}}`
			}
			if content, ok := fs.files[path]; ok {
				return 200, `{"data":{"is_dir":false,"is_file":true,"mode":"0o644","name":"` + filepath.Base(path) + `","path":"` + path + `","size":` + itoa(len(content)) + `,"modified_at":"2026-01-02T00:00:00Z"}}`
			}
			return 404, `{"error":{"code":"NOT_FOUND","message":"path not found"}}`
		case c.is("POST", base+"/files/write"):
			b, _ := base64.StdEncoding.DecodeString(c.Body["content_base64"].(string))
			fs.files[path] = string(b)
			return 201, `{"success":true}`
		case c.is("POST", base+"/fs/mkdir"):
			for d := path; d != "/" && d != "."; d = filepathDir(d) {
				fs.dirs[d] = true
			}
			return 201, `{"data":{"status":"ok"}}`
		case c.is("DELETE", base+"/fs"):
			delete(fs.files, path)
			delete(fs.dirs, path)
			return 200, `{"success":true}`
		case c.is("POST", base+"/fs/rename"):
			from, to := c.Body["from"].(string), c.Body["to"].(string)
			fs.files[to] = fs.files[from]
			delete(fs.files, from)
			return 200, `{}`
		case c.is("POST", base+"/fs/chmod"):
			return 200, `{}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route in test"}}`
	})
	return fs
}

func filepathDir(p string) string { return filepath.ToSlash(filepath.Dir(p)) }
func itoa(n int) string           { return strconv.Itoa(n) }

func (fs *fsAPI) writes() map[string]string {
	out := map[string]string{}
	for _, c := range fs.find("POST", "/sandboxes/"+boxID+"/files/write") {
		b, _ := base64.StdEncoding.DecodeString(c.Body["content_base64"].(string))
		out[c.Body["path"].(string)] = string(b)
	}
	return out
}

func TestFilesLsUsesTheFilesField(t *testing.T) {
	fs := newFSAPI(t)
	fs.dirs["/workspace/src"] = true
	fs.files["/workspace/main.py"] = "print()"
	out, err := run(t, "files", "ls", "my-box:/workspace")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.py") || !strings.Contains(out, "src/") {
		t.Fatalf("ls output:\n%s", out)
	}
	if strings.Index(out, "src/") > strings.Index(out, "main.py") {
		t.Fatalf("directories sort first:\n%s", out)
	}
	jout, _ := run(t, "files", "ls", "my-box:/workspace", "--json")
	if !strings.Contains(jout, `"files"`) {
		t.Fatalf("json = %s", jout)
	}
}

func TestFilesLsRelativeAndBarePathsUseHomeAndCurrentSandbox(t *testing.T) {
	fs := newFSAPI(t)
	fs.files["/home/user/a.txt"] = "x"
	writeConfigFile(t, "api_key = \"msk_u_test\"\ncurrent_sandbox = \""+boxID+"\"\n")
	if out, err := run(t, "files", "ls"); err != nil || !strings.Contains(out, "a.txt") {
		t.Fatalf("%v\n%s", err, out)
	}
	if out, err := run(t, "files", "ls", "my-box:"); err != nil || !strings.Contains(out, "a.txt") {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := fs.find("GET", "/sandboxes/"+boxID+"/fs"); len(got) != 2 || got[0].Query != "path=%2Fhome%2Fuser" {
		t.Fatalf("calls = %+v", got)
	}
}

func TestFilesLsMissingDirectoryIsAnError(t *testing.T) {
	newFSAPI(t)
	_, err := run(t, "files", "ls", "my-box:/nope")
	if err == nil || commands.ExitCode(err) != commands.ExitServer || !strings.Contains(err.Error(), "AGENT_UNAVAILABLE") {
		t.Fatalf("err = %v", err)
	}
}

func TestFilesCat(t *testing.T) {
	fs := newFSAPI(t)
	fs.files["/workspace/out.json"] = `{"ok":true}`
	out, err := run(t, "files", "cat", "my-box:/workspace/out.json")
	if err != nil || out != `{"ok":true}` {
		t.Fatalf("%v %q", err, out)
	}
	if _, err := run(t, "files", "cat", "my-box:/workspace/missing"); err == nil || commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("missing: %v", err)
	}
}

func TestFilesCpUploadsAFileAsBase64(t *testing.T) {
	fs := newFSAPI(t)
	local := filepath.Join(t.TempDir(), "app.py")
	os.WriteFile(local, []byte("print('hi')\x00\xff"), 0o644)
	out, err := run(t, "files", "cp", local, "my-box:/workspace/app.py")
	if err != nil || !strings.Contains(out, "Copied") {
		t.Fatalf("%v %s", err, out)
	}
	if got := fs.writes()["/workspace/app.py"]; got != "print('hi')\x00\xff" {
		t.Fatalf("remote content = %q (binary must round-trip)", got)
	}
}

func TestFilesCpTrailingSlashAndExistingDirReceiveTheName(t *testing.T) {
	fs := newFSAPI(t)
	local := filepath.Join(t.TempDir(), "app.py")
	os.WriteFile(local, []byte("a"), 0o644)
	if _, err := run(t, "files", "cp", local, "my-box:/workspace/"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "files", "cp", local, "my-box:/home/user"); err != nil { // /home/user exists as a directory
		t.Fatal(err)
	}
	w := fs.writes()
	if w["/workspace/app.py"] != "a" || w["/home/user/app.py"] != "a" {
		t.Fatalf("writes = %v", w)
	}
}

func TestFilesCpDirectoryNeedsRecursiveFlag(t *testing.T) {
	newFSAPI(t)
	dir := t.TempDir()
	_, err := run(t, "files", "cp", dir, "my-box:/workspace/")
	if err == nil || !strings.Contains(err.Error(), "-r") || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("err = %v", err)
	}
}

func TestFilesCpRecursiveUpload(t *testing.T) {
	fs := newFSAPI(t)
	dir := filepath.Join(t.TempDir(), "site")
	os.MkdirAll(filepath.Join(dir, "css"), 0o755)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>"), 0o644)
	os.WriteFile(filepath.Join(dir, "css", "a.css"), []byte("body{}"), 0o644)
	if _, err := run(t, "files", "cp", "-r", dir, "my-box:/workspace/"); err != nil {
		t.Fatal(err)
	}
	w := fs.writes()
	if w["/workspace/site/index.html"] != "<h1>" || w["/workspace/site/css/a.css"] != "body{}" {
		t.Fatalf("writes = %v", w)
	}
}

func TestFilesCpDownloadFileAndTree(t *testing.T) {
	fs := newFSAPI(t)
	fs.dirs["/workspace/out"] = true
	fs.dirs["/workspace/out/deep"] = true
	fs.files["/workspace/out/a.txt"] = "AAA"
	fs.files["/workspace/out/deep/b.txt"] = "BBB"
	tmp := t.TempDir()

	if _, err := run(t, "files", "cp", "my-box:/workspace/out/a.txt", filepath.Join(tmp, "got.txt")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(tmp, "got.txt")); string(b) != "AAA" {
		t.Fatalf("file = %q", b)
	}
	if _, err := run(t, "files", "cp", "my-box:/workspace/out/a.txt", tmp+"/"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(tmp, "a.txt")); string(b) != "AAA" {
		t.Fatal("trailing slash should receive the file name")
	}
	if _, err := run(t, "files", "cp", "my-box:/workspace/out", filepath.Join(tmp, "tree")); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("directory without -r: %v", err)
	}
	if _, err := run(t, "files", "cp", "-r", "my-box:/workspace/out", filepath.Join(tmp, "tree")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(tmp, "tree", "deep", "b.txt")); string(b) != "BBB" {
		t.Fatalf("tree = %q", b)
	}
}

func TestFilesCpFailedDownloadLeavesNoPartialFile(t *testing.T) {
	newFSAPI(t)
	tmp := t.TempDir()
	if _, err := run(t, "files", "cp", "my-box:/workspace/missing", filepath.Join(tmp, "x")); err == nil {
		t.Fatal("want error")
	}
	entries, _ := os.ReadDir(tmp)
	if len(entries) != 0 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestFilesCpBetweenSandboxes(t *testing.T) {
	fs := newFSAPI(t)
	fs.files["/workspace/a.txt"] = "AAA"
	if _, err := run(t, "files", "cp", "my-box:/workspace/a.txt", "my-box:/home/user/copy/a2.txt"); err != nil {
		t.Fatal(err)
	}
	if fs.writes()["/home/user/copy/a2.txt"] != "AAA" {
		t.Fatalf("writes = %v", fs.writes())
	}
}

func TestFilesCpNeedsOneRemoteSide(t *testing.T) {
	newFSAPI(t)
	if _, err := run(t, "files", "cp", "a.txt", "b.txt"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("err = %v", err)
	}
}

func TestFilesWriteReadsStdin(t *testing.T) {
	fs := newFSAPI(t)
	if _, err := runIn(t, "from stdin", "files", "write", "my-box:/workspace/cfg/app.yaml"); err != nil {
		t.Fatal(err)
	}
	if fs.writes()["/workspace/cfg/app.yaml"] != "from stdin" || !fs.dirs["/workspace/cfg"] {
		t.Fatalf("writes=%v dirs=%v", fs.writes(), fs.dirs)
	}
}

func TestFilesMkdirSendsRecursiveAndMode(t *testing.T) {
	fs := newFSAPI(t)
	out, err := run(t, "files", "mkdir", "my-box:/workspace/output/deep")
	if err != nil || !strings.Contains(out, "Created") {
		t.Fatalf("%v %s", err, out)
	}
	c := fs.find("POST", "/sandboxes/"+boxID+"/fs/mkdir")
	if len(c) != 1 || c[0].Body["path"] != "/workspace/output/deep" || c[0].Body["recursive"] != true || c[0].Body["mode"] != "0755" {
		t.Fatalf("calls = %+v", c)
	}
}

func TestFilesRmChecksFirst(t *testing.T) {
	fs := newFSAPI(t)
	fs.files["/workspace/old.log"] = "x"
	fs.dirs["/workspace/build"] = true

	if _, err := run(t, "files", "rm", "my-box:/workspace/nope"); err == nil || commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("missing path: %v", err)
	}
	if len(fs.find("DELETE", "/sandboxes/"+boxID+"/fs")) != 0 {
		t.Fatal("must not delete what does not exist")
	}
	if _, err := run(t, "files", "rm", "-f", "my-box:/workspace/nope"); err != nil {
		t.Fatalf("-f: %v", err)
	}
	if _, err := run(t, "files", "rm", "my-box:/workspace/build"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("dir without -r: %v", err)
	}
	if _, err := run(t, "files", "rm", "my-box:/workspace/old.log"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "files", "rm", "-r", "my-box:/workspace/build"); err != nil {
		t.Fatal(err)
	}
	d := fs.find("DELETE", "/sandboxes/"+boxID+"/fs")
	if len(d) != 2 || d[1].Body["recursive"] != true || fs.dirs["/workspace/build"] {
		t.Fatalf("deletes = %+v", d)
	}
}

func TestFilesMvStatChmod(t *testing.T) {
	fs := newFSAPI(t)
	fs.files["/workspace/a.txt"] = "AAA"

	if out, err := run(t, "files", "stat", "my-box:/workspace/a.txt"); err != nil || !strings.Contains(out, "file") || !strings.Contains(out, "3 B") {
		t.Fatalf("stat: %v\n%s", err, out)
	}
	if _, err := run(t, "files", "mv", "my-box:/workspace/a.txt", "my-box:/workspace/b.txt"); err != nil {
		t.Fatal(err)
	}
	if fs.files["/workspace/b.txt"] != "AAA" {
		t.Fatalf("files = %v", fs.files)
	}
	if _, err := run(t, "files", "mv", "my-box:/workspace/b.txt", "other-box:/x"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("cross-sandbox mv: %v", err)
	}
	if _, err := run(t, "files", "chmod", "755", "my-box:/workspace/b.txt"); err != nil {
		t.Fatal(err)
	}
	if c := fs.find("POST", "/sandboxes/"+boxID+"/fs/chmod"); len(c) != 1 || c[0].Body["mode"] != "755" {
		t.Fatalf("chmod = %+v", c)
	}
	if _, err := run(t, "files", "chmod", "rwx", "my-box:/workspace/b.txt"); err == nil || commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("bad mode: %v", err)
	}
}
