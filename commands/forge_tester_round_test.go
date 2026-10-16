package commands_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
	"github.com/spf13/cobra"
)

func gitGlobal(t *testing.T, key string) string {
	t.Helper()
	out, err := exec.Command("git", "config", "--global", "--get-all", key).CombinedOutput()
	if err != nil {
		t.Fatalf("git config --get-all %s: %v %s", key, err, out)
	}
	return string(out)
}

// A helper written as an absolute path breaks when the binary moves (a new Node
// version under nvm); setup-git names the CLI by PATH, the way gh does.
func TestForgeSetupGitNamesTheCLIByPath(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	defer setupEnv(t, server)()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	defer commands.SetLookPathForTest(func(string) (string, error) { return "/usr/local/bin/miosa", nil })()

	out, err := run(t, "forge", "auth", "setup-git")
	if err != nil {
		t.Fatalf("setup-git: %v %s", err, out)
	}
	got := gitGlobal(t, "credential.https://forge.miosa.ai.helper")
	if !strings.Contains(got, "\n!miosa forge auth git-credential\n") || strings.Contains(got, "/") {
		t.Fatalf("helper should be PATH-resolved, got %q", got)
	}
}

func TestForgeSetupGitFallsBackToTheFullPathOffPath(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	defer setupEnv(t, server)()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	defer commands.SetLookPathForTest(func(string) (string, error) { return "", errors.New("not found") })()

	out, err := run(t, "forge", "auth", "setup-git")
	if err != nil {
		t.Fatalf("setup-git: %v %s", err, out)
	}
	if !strings.Contains(out, "not on your PATH") {
		t.Fatalf("expected the off-PATH warning, got %s", out)
	}
	if got := gitGlobal(t, "credential.https://forge.miosa.ai.helper"); !strings.Contains(got, "/") {
		t.Fatalf("off PATH the helper names this binary, got %q", got)
	}
}

func TestRootHelpDocsLinkIsLive(t *testing.T) {
	// Read the text instead of running --help: executing the root makes cobra
	// add its own --version flag, which the product-language guard then reads.
	out := commands.Root().Long
	if !strings.Contains(out, "Docs: https://miosa.ai/docs/cli") || strings.Contains(out, "docs.miosa.ai") {
		t.Fatalf("help must point at https://miosa.ai/docs/cli: %s", out)
	}
}

const avatar = "https://lh3.googleusercontent.com/a/very-long-avatar"

func forgeRoundServer(t *testing.T, seen map[string]any) *httptest.Server {
	t.Helper()
	author := map[string]any{"id": "u1", "name": "Ada", "avatar_url": avatar}
	policy := map[string]any{"branch": "main", "version": 2, "required_approvals": 1, "require_independent_approval": true,
		"required_checks": []any{}, "self_merge_allowed": false, "default_managed": false, "dismiss_stale_reviews": true}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(status int, v any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/forge/repositories/platform/pull-requests":
			write(200, map[string]any{"data": []any{
				map[string]any{"number": 2, "state": "open", "title": "Open one", "head_branch": "b", "base_branch": "main", "author": author, "base_sha": "aaa", "head_sha": "bbb", "repository_id": "r1", "head_repository_id": "r1"},
				map[string]any{"number": 1, "state": "merged", "title": "Merged one", "head_branch": "a", "base_branch": "release", "author": author},
			}})
		case r.Method == http.MethodGet && r.URL.Path == "/forge/repositories/platform/pull-requests/2/collaboration":
			write(200, map[string]any{"data": map[string]any{"state": "open", "head_sha": "bbbbbbbbbbbbbbbb", "policy": policy,
				"reviews":    []any{map[string]any{"decision": "commented", "reviewer": author, "head_sha": "bbbbbbbbbbbbbbbb", "body": "looks fine"}},
				"check_runs": []any{}, "commit_checks": []any{}, "merge_receipt": nil}})
		case r.Method == http.MethodPost && r.URL.Path == "/forge/repositories/platform/pull-requests/2/merge":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			seen["merge"] = body
			if body["merge_method"] == "squash" {
				write(201, map[string]any{"data": map[string]any{"merge_sha": "cccccccccccccccc", "ref": "refs/heads/main", "method": "squash", "policy_version": 2}})
				return
			}
			write(422, map[string]any{"error": map[string]any{"code": "REQUIRED_APPROVALS_MISSING", "retryable": false}})
		case r.Method == http.MethodGet && r.URL.Path == "/forge/capabilities":
			if seen["old_server"] == true {
				write(200, map[string]any{"data": map[string]any{"features": map[string]any{}}})
				return
			}
			write(200, map[string]any{"data": map[string]any{"merge_methods": []any{"merge", "squash", "rebase"}, "features": map[string]any{"auto_merge": true}}})
		case r.Method == http.MethodPost && r.URL.Path == "/forge/repositories/platform/releases/v1/assets":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			seen["asset"] = body
			write(201, map[string]any{"data": map[string]any{"name": body["name"], "byte_size": 5, "sha256": "dddddddddddddddd"}})
		case r.Method == http.MethodGet && r.URL.Path == "/deployments/app/builds":
			write(200, map[string]any{"data": []any{map[string]any{"id": "b1234567890", "state": "failed", "commit_sha": "e1e1e1e1e1", "triggered_by": "push",
				"created_at": "2026-10-16T00:00:00Z", "error_message": "BUILDER_UNREACHABLE: the builder connection closed"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/deployments/app/logs":
			write(200, map[string]any{"data": map[string]any{"lines": []any{}}})
		case r.Method == http.MethodGet && r.URL.Path == "/deployments/app":
			write(200, map[string]any{"data": map[string]any{"id": "app", "state": "failed"}})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestForgePRShowIsTextWithoutAvatars(t *testing.T) {
	server := forgeRoundServer(t, map[string]any{})
	defer server.Close()
	defer setupEnv(t, server)()

	out, err := run(t, "forge", "pr", "show", "platform", "2")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Merging needs:", "1 approval from someone other than the author", "Approvals:", "0 of 1", "REVIEWER", "Ada"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{") || strings.Contains(out, "googleusercontent") {
		t.Fatalf("text output must not be JSON or carry avatars:\n%s", out)
	}
	js, err := run(t, "forge", "pr", "show", "platform", "2", "--json")
	if err != nil || !strings.Contains(js, `"policy"`) || strings.Contains(js, "avatar_url") {
		t.Fatalf("--json must print the response without avatars: err=%v\n%s", err, js)
	}
}

func TestForgePRListFiltersDefaultToOpen(t *testing.T) {
	server := forgeRoundServer(t, map[string]any{})
	defer server.Close()
	defer setupEnv(t, server)()

	out, _ := run(t, "forge", "pr", "list", "platform")
	if !strings.Contains(out, "Open one") || strings.Contains(out, "Merged one") {
		t.Fatalf("default list must show open only:\n%s", out)
	}
	out, _ = run(t, "forge", "pr", "list", "platform", "--state", "all", "--base", "release", "--author", "ada")
	if strings.Contains(out, "Open one") || !strings.Contains(out, "Merged one") {
		t.Fatalf("--state all --base release --author ada:\n%s", out)
	}
}

func TestForgeMergeRefusalNamesThePolicyAndTheFix(t *testing.T) {
	server := forgeRoundServer(t, map[string]any{})
	defer server.Close()
	defer setupEnv(t, server)()

	_, err := run(t, "forge", "pr", "merge", "platform", "2")
	if err == nil {
		t.Fatal("merge must fail")
	}
	var buf bytes.Buffer
	commands.WriteErrorForTest(&buf, err, false)
	out := buf.String()
	for _, want := range []string{"policy main v2 needs 1 approval from someone other than the author", "[REQUIRED_APPROVALS_MISSING]",
		"miosa forge policy set platform --branch main --approvals 0 --independent=false", "miosa forge pr review platform 2 --decision approved"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if commands.ExitCode(err) != commands.ExitConflict {
		t.Fatalf("exit code %d", commands.ExitCode(err))
	}
}

func TestForgeMergeSquashSendsTheMethod(t *testing.T) {
	seen := map[string]any{}
	server := forgeRoundServer(t, seen)
	defer server.Close()
	defer setupEnv(t, server)()

	out, err := run(t, "forge", "pr", "merge", "platform", "2", "--squash")
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if body, _ := seen["merge"].(map[string]any); body["merge_method"] != "squash" {
		t.Fatalf("merge body %v", seen["merge"])
	}
	if !strings.Contains(out, "Merged pull request #2 into main as cccccccccccc") || !strings.Contains(out, "squash") {
		t.Fatalf("receipt text:\n%s", out)
	}
	if _, err := run(t, "forge", "pr", "merge", "platform", "2", "--squash", "--rebase"); err == nil {
		t.Fatal("--squash with --rebase must be refused")
	}
}

// An older server ignores merge_method and would merge with a merge commit.
func TestForgeMergeSquashIsRefusedByAnOldServer(t *testing.T) {
	seen := map[string]any{"old_server": true}
	server := forgeRoundServer(t, seen)
	defer server.Close()
	defer setupEnv(t, server)()

	for _, flag := range []string{"--squash", "--auto"} {
		_, err := run(t, "forge", "pr", "merge", "platform", "2", flag)
		if err == nil || !strings.Contains(err.Error(), "does not support "+flag) {
			t.Fatalf("%s: want a refusal, got %v", flag, err)
		}
	}
	if _, merged := seen["merge"]; merged {
		t.Fatal("nothing may be sent to the merge route")
	}
}

func TestForgeReleaseUploadSendsTheFile(t *testing.T) {
	seen := map[string]any{}
	server := forgeRoundServer(t, seen)
	defer server.Close()
	defer setupEnv(t, server)()
	file := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "forge", "release", "upload", "platform", "v1", file)
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	body, _ := seen["asset"].(map[string]any)
	if body["kind"] != "upload" || body["name"] != "notes.txt" || body["content_base64"] != "aGVsbG8=" || body["content_type"] != "text/plain" {
		t.Fatalf("asset body %v", body)
	}
	if !strings.Contains(out, "notes.txt") || !strings.Contains(out, "5 B") {
		t.Fatalf("upload table:\n%s", out)
	}
}

func TestDeployBuildsAndLogsShowTheBuildFailure(t *testing.T) {
	server := forgeRoundServer(t, map[string]any{})
	defer server.Close()
	defer setupEnv(t, server)()

	out, err := run(t, "deploy", "builds", "app")
	if err != nil || !strings.Contains(out, "ERROR") || !strings.Contains(out, "BUILDER_UNREACHABLE") {
		t.Fatalf("builds table: err=%v\n%s", err, out)
	}
	out, err = run(t, "deploy", "logs", "app")
	if err != nil || !strings.Contains(out, "Build b1234567 failed") || !strings.Contains(out, "BUILDER_UNREACHABLE: the builder connection closed") {
		t.Fatalf("logs must name the failed build: err=%v\n%s", err, out)
	}
}

// Tester item 15 (2026-10-16): every Forge command shows examples.
func TestEveryForgeCommandHasExamples(t *testing.T) {
	var walk func(c *cobra.Command)
	missing := []string{}
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			if len(sub.Commands()) == 0 && sub.Example == "" {
				missing = append(missing, sub.CommandPath())
			}
			// An example may only use flags the command has.
			for _, field := range strings.Fields(sub.Example) {
				if !strings.HasPrefix(field, "--") {
					continue
				}
				name := strings.SplitN(strings.TrimPrefix(field, "--"), "=", 2)[0]
				if sub.Flags().Lookup(name) == nil && sub.InheritedFlags().Lookup(name) == nil {
					missing = append(missing, sub.CommandPath()+" uses unknown --"+name)
				}
			}
			walk(sub)
		}
	}
	forge, _, err := commands.Root().Find([]string{"forge"})
	if err != nil {
		t.Fatal(err)
	}
	walk(forge)
	if len(missing) > 0 {
		t.Fatalf("Forge commands without examples: %v", missing)
	}
}
