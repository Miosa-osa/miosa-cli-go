package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeStaticSite puts a tiny static site in dir for the --dir upload paths.
func writeStaticSite(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sandboxDeployBody is the sandbox publish reply for deployment d1.
const sandboxDeployBody = `{"deployment_id":"d1","url":"https://d1.miosa.app","state":"running",
"data":{"deployment":{"id":"d1","name":"my-app","slug":"my-app","state":"running","public_url":"https://d1.miosa.app"},"url":"https://d1.miosa.app"}}`

func deploySandboxAPI(t *testing.T) *fakeAPI {
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/sandboxes/by-name/box-1"):
			return 200, `{"id":"sbx-1"}`
		case c.is("POST", "/sandboxes/sbx-1/deploy"):
			return 201, sandboxDeployBody
		case c.is("POST", "/deployments/d1/env"):
			return 200, `{"data":[],"env_vars":[]}`
		case c.is("GET", "/deployments/d1"):
			return 200, `{"data":{"id":"d1","name":"my-app","slug":"my-app","state":"running","public_url":"https://d1.miosa.app"}}`
		case c.is("POST", "/deployments/d1/domains"):
			return 201, `{"data":{"id":"dom-1","hostname":"app.example.com","status":"pending"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestDeployCreateNeedsASource(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "create")
	if err == nil {
		t.Fatal("expected an error when no source is given")
	}
	if !strings.Contains(err.Error(), "--from-sandbox") {
		t.Fatalf("error should name the sources, got: %v", err)
	}
}

func TestDeployCreateRejectsTwoSources(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "create", "--git", "https://github.com/me/app", "--from-sandbox", "box-1")
	if err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Fatalf("expected an exactly-one-source error, got: %v", err)
	}
}

func TestDeployCreateRejectsImage(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "create", "--image", "nginx:latest")
	if err == nil || !strings.Contains(err.Error(), "--image is not available") {
		t.Fatalf("expected --image to be rejected, got: %v", err)
	}
}

func TestDeployCreatePortOnlyForSandbox(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "create", "--git", "https://github.com/me/app", "--port", "3000")
	if err == nil || !strings.Contains(err.Error(), "--port only applies") {
		t.Fatalf("expected a --port error, got: %v", err)
	}
}

func TestDeployCreateBuildCommandNeedsAServerForSandbox(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "create", "--from-sandbox", "box-1", "--build-command", "npm run build")
	if err == nil || !strings.Contains(err.Error(), "needs --run-command or --type dynamic") {
		t.Fatalf("expected a --build-command error for a static publish, got: %v", err)
	}
}

func TestDeployCreateRejectsBadEnv(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	_, err := run(t, "deploy", "create", "--from-sandbox", "box-1", "--env", "NOVALUE")
	if err == nil || !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Fatalf("expected a KEY=VALUE error, got: %v", err)
	}
}

func TestDeployCreateFromSandbox(t *testing.T) {
	f := deploySandboxAPI(t)
	out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1",
		"--dir", "/srv/app", "--port", "3000", "--type", "dynamic", "--run-command", "npm start",
		"--env", "NODE_ENV=production", "--domain", "app.example.com")
	if err != nil {
		t.Fatalf("deploy create: %v (out=%q)", err, out)
	}
	deploy := f.find("POST", "/sandboxes/sbx-1/deploy")
	if len(deploy) != 1 {
		t.Fatalf("expected one sandbox deploy call, got %d", len(deploy))
	}
	b := deploy[0].Body
	if b["name"] != "my-app" || b["output_path"] != "/srv/app" || b["run_command"] != "npm start" {
		t.Fatalf("unexpected deploy body: %+v", b)
	}
	if n, _ := b["port"].(float64); n != 3000 {
		t.Fatalf("port not sent, got %v", b["port"])
	}
	if b["deployment_type"] != "dynamic" || b["domain"] != "app.example.com" {
		t.Fatalf("type/domain not sent: %+v", b)
	}
	if _, ok := b["build_command"]; ok {
		t.Fatalf("build_command is only sent when given: %+v", b)
	}
	// env is set after create for sandbox publishes.
	envCall := f.find("POST", "/deployments/d1/env")
	if len(envCall) != 1 {
		t.Fatalf("expected env to be set after create, got %d calls", len(envCall))
	}
	if env, _ := envCall[0].Body["env"].(map[string]interface{}); env["NODE_ENV"] != "production" {
		t.Fatalf("env not sent: %+v", envCall[0].Body)
	}
	if !strings.Contains(out, "https://d1.miosa.app") {
		t.Fatalf("URL not printed: %q", out)
	}
}

func TestDeployCreateFromSandboxWait(t *testing.T) {
	deploySandboxAPI(t)
	out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1", "--wait")
	if err != nil {
		t.Fatalf("deploy create --wait: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "https://d1.miosa.app") {
		t.Fatalf("--wait should print the live URL: %q", out)
	}
}

const gitCreateBody = `{"data":{"id":"d2","name":"app","slug":"app","state":"pending","public_url":"https://d2.miosa.app"},
"webhook_secret":"whsec_abc"}`

func gitDeployAPI(t *testing.T) *fakeAPI {
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments"):
			return 201, gitCreateBody
		case c.is("POST", "/deployments/d2/redeploy"):
			return 202, `{"data":{"id":"b1","state":"queued"}}`
		case c.is("GET", "/deployments/d2"):
			return 200, `{"data":{"id":"d2","name":"app","state":"running","public_url":"https://d2.miosa.app"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
}

func TestDeployCreateFromGit(t *testing.T) {
	f := gitDeployAPI(t)
	out, err := run(t, "deploy", "create", "--git", "https://github.com/me/app", "--branch", "dev",
		"--env", "A=1")
	if err != nil {
		t.Fatalf("deploy create --git: %v (out=%q)", err, out)
	}
	create := f.find("POST", "/deployments")
	if len(create) != 1 {
		t.Fatalf("expected one create call, got %d", len(create))
	}
	b := create[0].Body
	if b["repo_url"] != "https://github.com/me/app" || b["branch"] != "dev" {
		t.Fatalf("unexpected create body: %+v", b)
	}
	if b["name"] != "app" {
		t.Fatalf("name should default to the repo name, got %v", b["name"])
	}
	if env, _ := b["env"].(map[string]interface{}); env["A"] != "1" {
		t.Fatalf("env should be inline for git creates: %+v", b["env"])
	}
	if len(f.find("POST", "/deployments/d2/redeploy")) != 1 {
		t.Fatal("expected the first build to be queued with a redeploy")
	}
	if !strings.Contains(out, "whsec_abc") {
		t.Fatalf("the one-time webhook secret should be printed: %q", out)
	}
}

// The server builds on connect and answers "building"; a redeploy on top of
// that built the same head a second time (QA a0042ac1, 2026-10-17).
func TestDeployCreateFromGitDoesNotRebuildWhatTheServerBuilds(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/deployments") {
			return 201, `{"data":{"id":"d3","name":"app","state":"building"},"webhook_secret":"whsec_abc"}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	if out, err := run(t, "deploy", "create", "--git", "https://github.com/me/app"); err != nil {
		t.Fatalf("deploy create --git: %v (out=%q)", err, out)
	}
	if n := len(f.find("POST", "/deployments/d3/redeploy")); n != 0 {
		t.Fatalf("the server already queued the first build; got %d redeploy calls", n)
	}
}

// A create response with no state is treated like "pending": the CLI queues
// the first build, or the deployment would never build.
func TestDeployCreateFromGitQueuesTheFirstBuildWhenStateIsMissing(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments"):
			return 201, `{"data":{"id":"d3","name":"app"}}`
		case c.is("POST", "/deployments/d3/redeploy"):
			return 202, `{"data":{"id":"b1","state":"queued"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	if out, err := run(t, "deploy", "create", "--git", "https://github.com/me/app"); err != nil {
		t.Fatalf("deploy create --git: %v (out=%q)", err, out)
	}
	if n := len(f.find("POST", "/deployments/d3/redeploy")); n != 1 {
		t.Fatalf("a create with no state needs the first build queued; got %d redeploy calls", n)
	}
}

func TestDeployCreateFromGitWait(t *testing.T) {
	gitDeployAPI(t)
	out, err := run(t, "deploy", "create", "--git", "https://github.com/me/app", "--wait")
	if err != nil {
		t.Fatalf("deploy create --git --wait: %v (out=%q)", err, out)
	}
	if !strings.Contains(out, "https://d2.miosa.app") {
		t.Fatalf("--wait should print the live URL: %q", out)
	}
}

func TestDeployCreateFromDir(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d3","source_type":"upload","chunk_count":0}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{"session_id":"s1","chunk_index":0,"bytes_received":10}}`
		case c.is("POST", "/deployments/d3/drop/sessions/s1/complete"):
			return 200, `{"data":{"version_id":"v1","deployment_id":"d3","public_url":"https://d3.miosa.app","promoted":true}}`
		case c.is("POST", "/deployments/d3/domains"):
			return 201, `{"data":{"id":"dom-2","hostname":"site.example.com"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	dir := t.TempDir()
	writeStaticSite(t, dir)
	out, err := run(t, "deploy", "create", "--dir", dir, "--name", "site", "--domain", "site.example.com")
	if err != nil {
		t.Fatalf("deploy create --dir: %v (out=%q)", err, out)
	}
	if len(f.find("POST", "/deployments/drop/sessions")) != 1 {
		t.Fatal("expected a drop session to be opened")
	}
	chunks := f.find("POST", "/deployments/d3/drop/sessions/s1/chunks")
	if len(chunks) != 1 {
		t.Fatalf("expected one chunk upload, got %d", len(chunks))
	}
	if ct := chunks[0].Headers.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("chunk content type = %q, want application/octet-stream", ct)
	}
	complete := f.find("POST", "/deployments/d3/drop/sessions/s1/complete")
	if len(complete) != 1 {
		t.Fatal("expected the session to be completed")
	}
	if complete[0].Body["promote"] != true {
		t.Fatalf("complete should promote, got %+v", complete[0].Body)
	}
	if len(f.find("POST", "/deployments/d3/domains")) != 1 {
		t.Fatal("expected the domain to be attached after create")
	}
	if !strings.Contains(out, "https://d3.miosa.app") {
		t.Fatalf("public_url not printed: %q", out)
	}
}

func TestDeployCreateJSON(t *testing.T) {
	deploySandboxAPI(t)
	out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1", "--json")
	if err != nil {
		t.Fatalf("deploy create --json: %v (out=%q)", err, out)
	}
	for _, want := range []string{`"id": "d1"`, `"source": "sandbox"`, `"url": "https://d1.miosa.app"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON output missing %s: %q", want, out)
		}
	}
}

func TestDeployBareNeedsConfirmationOffTerminal(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	// Off a terminal the bare shortcut refuses without --yes, whatever the source.
	_, err := run(t, "deploy")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected a confirmation error, got: %v", err)
	}
}

func TestDeployCreatePublishAlias(t *testing.T) {
	deploySandboxAPI(t)
	if _, err := run(t, "deploy", "publish", "my-app", "--from-sandbox", "box-1"); err != nil {
		t.Fatalf("the publish alias should work: %v", err)
	}
}

func TestDeployCreateFromSandboxSendsBuildCommandForAServer(t *testing.T) {
	// Customer write-up #4 (2026-10-16): --build-command was refused with --from-sandbox.
	f := deploySandboxAPI(t)
	if out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1", "--dir", "/home/user/app",
		"--port", "3000", "--run-command", "npx next start -p 3000", "--build-command", "npm ci && npm run build"); err != nil {
		t.Fatalf("deploy create: %v (out=%q)", err, out)
	}
	b := f.find("POST", "/sandboxes/sbx-1/deploy")[0].Body
	if b["build_command"] != "npm ci && npm run build" || b["run_command"] != "npx next start -p 3000" {
		t.Fatalf("build_command not sent: %+v", b)
	}
}
