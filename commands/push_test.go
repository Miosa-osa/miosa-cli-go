package commands_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPushLeavesOutWhatGitignoreNames(t *testing.T) {
	fs := newFSAPI(t)
	dir := filepath.Join(t.TempDir(), "app")
	tree := map[string]string{}
	tree[".gitignore"] = "node_modules/\n*.log\n/dist\n!keep.log\n**/cache/\n"
	tree["src/index.js"] = "console.log(1)"
	tree["node_modules/x/index.js"] = "x"
	tree["debug.log"] = "noise"
	tree["keep.log"] = "kept"
	tree["dist/out.js"] = "built"
	tree["src/dist/ok.js"] = "nested dist is not anchored"
	tree["src/cache/c.bin"] = "cache"
	tree[".git/HEAD"] = "ref: refs/heads/main"
	tree[".env"] = "LOCAL=1"
	writeTree(t, dir, tree)

	out, err := run(t, "push", "my-box", dir)
	if err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
	for _, want := range []string{"/workspace/app/src/index.js", "/workspace/app/keep.log", "/workspace/app/src/dist/ok.js", "/workspace/app/.gitignore", "/workspace/app/.env"} {
		if _, ok := fs.files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for _, gone := range []string{"/workspace/app/node_modules/x/index.js", "/workspace/app/debug.log", "/workspace/app/dist/out.js", "/workspace/app/src/cache/c.bin", "/workspace/app/.git/HEAD"} {
		if _, ok := fs.files[gone]; ok {
			t.Errorf("pushed %s", gone)
		}
	}
	if !strings.Contains(out, "Pushed 5 files") || !strings.Contains(out, "Left out 5 paths") {
		t.Fatalf("out = %s", out)
	}
}

func TestPushTwiceLandsInTheSamePlace(t *testing.T) {
	fs := newFSAPI(t)
	dir := filepath.Join(t.TempDir(), "app")
	tree := map[string]string{}
	tree["a.txt"] = "one"
	writeTree(t, dir, tree)
	for i := 0; i < 2; i++ {
		if _, err := run(t, "push", "my-box", dir, "--to", "/workspace/site"); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := fs.files["/workspace/site/a.txt"]; !ok {
		t.Fatalf("files = %v", fs.files)
	}
	if _, ok := fs.files["/workspace/site/app/a.txt"]; ok {
		t.Fatal("the second push nested the directory")
	}
}

func TestPushNoIgnoreCopiesEverything(t *testing.T) {
	fs := newFSAPI(t)
	dir := filepath.Join(t.TempDir(), "app")
	tree := map[string]string{}
	tree[".gitignore"] = "*.log\n"
	tree["debug.log"] = "noise"
	writeTree(t, dir, tree)
	if _, err := run(t, "push", "my-box", dir, "--no-ignore"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.files["/workspace/app/debug.log"]; !ok {
		t.Fatal("--no-ignore left out debug.log")
	}
}

func TestPushRefusesAFile(t *testing.T) {
	newFSAPI(t)
	f := filepath.Join(t.TempDir(), "one.txt")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "push", "my-box", f); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestPushInAGitRepoSendsWhatGitWould(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	fs := newFSAPI(t)
	dir := filepath.Join(t.TempDir(), "repo")
	tree := map[string]string{}
	tree[".gitignore"] = "*.log\n"
	tree["src/.gitignore"] = "generated/\n"
	tree["src/main.go"] = "package main"
	tree["src/generated/big.go"] = "nested ignore"
	tree["untracked.txt"] = "not ignored, so pushed"
	tree["app.log"] = "ignored"
	writeTree(t, dir, tree)
	for _, args := range [][]string{{"init", "-q"}, {"add", ".gitignore", "src/.gitignore", "src/main.go"}} {
		c := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if _, err := run(t, "push", "my-box", dir); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/workspace/repo/src/main.go", "/workspace/repo/untracked.txt", "/workspace/repo/src/.gitignore"} {
		if _, ok := fs.files[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for _, gone := range []string{"/workspace/repo/src/generated/big.go", "/workspace/repo/app.log"} {
		if _, ok := fs.files[gone]; ok {
			t.Errorf("pushed %s (a nested .gitignore names it)", gone)
		}
	}
	for f := range fs.files {
		if strings.Contains(f, "/.git/") {
			t.Fatalf("pushed git metadata %s", f)
		}
	}
}
