package commands

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func tarNames(t *testing.T, raw []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	sort.Strings(names)
	return names
}

// S1 (2026-10-16, owner's CLI tester): 2.3.9 published id_rsa, .npmrc and
// .htpasswd from `deploy create --dir`. Secrets stay out by default, each one
// is named, and .miosaignore adds patterns or puts a default back with !.
func TestUploadLeavesSecretsOutAndNamesThem(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"index.html": "<h1>ok</h1>", "id_rsa": "k", "id_rsa.pub": "k", "id_ed25519": "k",
		".npmrc": "//registry.npmjs.org/:_authToken=x", ".pypirc": "x", ".netrc": "x", ".htpasswd": "x",
		"server.key": "k", "cert.pem": "k", ".env": "A=1", ".env.local": "A=1",
		".aws/credentials": "x", ".ssh/id_ed25519": "k", ".miosa/state.json": "{}",
		"keep.key": "public test vector", "drafts/a.md": "x", "notes.bak": "x", "assets/app.js": "x",
		".miosaignore": "# extra\ndrafts/\n*.bak\n!keep.key\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	raw, skipped, err := tarGzDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(tarNames(t, raw), " ")
	if got != "assets/ assets/app.js index.html keep.key" {
		t.Fatalf("uploaded %q", got)
	}

	var warn bytes.Buffer
	warnSkipped(&warn, skipped)
	for _, want := range []string{"id_rsa (private key)", ".npmrc (package registry token)", ".htpasswd (password file)",
		".aws/ (cloud credentials)", ".ssh/ (SSH keys)", "cert.pem", ".env.local (environment secrets)", "drafts/ (.miosaignore)", "notes.bak"} {
		if !strings.Contains(warn.String(), want) {
			t.Errorf("warning missing %q:\n%s", want, warn.String())
		}
	}
	if strings.Contains(warn.String(), ".miosa/") || strings.Contains(warn.String(), "keep.key") {
		t.Errorf("CLI state and re-included files are not warnings:\n%s", warn.String())
	}
}
