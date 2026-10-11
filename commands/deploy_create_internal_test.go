package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// unwrapTarGz returns the file names (and contents) in a gzip-compressed tar.
func unwrapTarGz(t *testing.T, data []byte) (names []string, files map[string]string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("not a gzip stream: %v", err)
	}
	tr := tar.NewReader(gz)
	files = map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading tar: %v", err)
		}
		names = append(names, hdr.Name)
		if hdr.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			files[hdr.Name] = string(b)
		}
	}
	return names, files
}

func TestTarGzDirSkipsGitAndKeepsTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	raw, err := tarGzDir(dir)
	if err != nil {
		t.Fatalf("tarGzDir: %v", err)
	}
	if len(raw) < 2 || raw[0] != 0x1f || raw[1] != 0x8b {
		t.Fatalf("output is not gzip (magic missing): % x", raw)
	}
	names, files := unwrapTarGz(t, raw)
	if _, ok := files["index.html"]; !ok {
		t.Fatalf("index.html missing from archive: %v", names)
	}
	if files["assets/app.js"] != "x" {
		t.Fatalf("nested file missing or wrong: %v", files)
	}
	for _, n := range names {
		if len(n) >= 4 && n[:4] == ".git" {
			t.Fatalf(".git should be skipped, found %q", n)
		}
	}
	if files["index.html"] != "<h1/>" {
		t.Fatalf("index.html content changed: %q", files["index.html"])
	}
}

func TestRepoNameFromURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/me/app":        "app",
		"https://github.com/me/app.git":    "app",
		"git@github.com:me/app.git":        "app",
		"https://github.com/me/app/":       "app",
		"https://gitlab.com/group/sub/app": "app",
	}
	for in, want := range cases {
		got, err := repoNameFromURL(in)
		if err != nil {
			t.Fatalf("repoNameFromURL(%q): %v", in, err)
		}
		if got != want {
			t.Fatalf("repoNameFromURL(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := repoNameFromURL("   "); err == nil {
		t.Fatal("expected an error for an empty URL")
	}
}

func TestNormalizeGitURL(t *testing.T) {
	if got := normalizeGitURL("git@github.com:me/app.git"); got != "https://github.com/me/app" {
		t.Fatalf("ssh remote not normalised: %q", got)
	}
	if got := normalizeGitURL("https://github.com/me/app.git"); got != "https://github.com/me/app" {
		t.Fatalf("https remote not normalised: %q", got)
	}
	if got := normalizeGitURL(""); got != "" {
		t.Fatalf("empty URL should stay empty, got %q", got)
	}
}

func TestDeployEnvMap(t *testing.T) {
	if m, err := deployEnvMap(nil); err != nil || m != nil {
		t.Fatalf("no pairs should give a nil map, got %v, %v", m, err)
	}
	m, err := deployEnvMap([]string{"A=1", "B=", "C=a=b"})
	if err != nil {
		t.Fatalf("deployEnvMap: %v", err)
	}
	want := map[string]any{"A": "1", "B": "", "C": "a=b"}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("deployEnvMap = %v, want %v", m, want)
	}
	if _, err := deployEnvMap([]string{"NOEQUALS"}); err == nil {
		t.Fatal("expected an error for a pair without '='")
	}
}
