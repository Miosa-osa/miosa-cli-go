package commands_test

import (
	"strings"
	"testing"
)

// A ClinicIQ user could not upload an app that needs a server (2026-10-16).
// --dir with --run-command sends the start command and port on complete, and
// reports the deployment as building (the runtime starts it).
func TestDeployCreateFromDirWithRunCommandPublishesAServer(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d3","source_type":"upload","chunk_count":0}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{"session_id":"s1","chunk_index":0,"bytes_received":10}}`
		case c.is("POST", "/deployments/d3/drop/sessions/s1/complete"):
			return 202, `{"data":{"version_id":"v1","deployment_id":"d3","public_url":"https://d3.miosa.app","kind":"dynamic","state":"building","skipped_paths":[".npmrc"]}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	dir := t.TempDir()
	writeStaticSite(t, dir)
	out, err := run(t, "deploy", "create", "--dir", dir, "--name", "api", "--run-command", "node server.js", "--port", "3000")
	if err != nil {
		t.Fatalf("deploy create --dir --run-command: %v (out=%q)", err, out)
	}
	complete := f.find("POST", "/deployments/d3/drop/sessions/s1/complete")
	if len(complete) != 1 {
		t.Fatal("expected the session to be completed")
	}
	if complete[0].Body["start_command"] != "node server.js" {
		t.Fatalf("start_command not sent: %+v", complete[0].Body)
	}
	if complete[0].Body["port"] != float64(3000) {
		t.Fatalf("port not sent: %+v", complete[0].Body)
	}
	if !strings.Contains(out, "building") {
		t.Fatalf("a server publish should report building: %q", out)
	}
	if !strings.Contains(out, ".npmrc") {
		t.Fatalf("skipped paths should be printed: %q", out)
	}
}

func TestDeployCreateFromDirWithoutRunCommandStaysStatic(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d3","source_type":"upload","chunk_count":0}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{"session_id":"s1","chunk_index":0,"bytes_received":10}}`
		case c.is("POST", "/deployments/d3/drop/sessions/s1/complete"):
			return 200, `{"data":{"version_id":"v1","deployment_id":"d3","public_url":"https://d3.miosa.app","promoted":true}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	dir := t.TempDir()
	writeStaticSite(t, dir)
	if _, err := run(t, "deploy", "create", "--dir", dir, "--name", "site"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.find("POST", "/deployments/d3/drop/sessions/s1/complete")[0].Body["start_command"]; ok {
		t.Fatal("a static upload must not send a start command")
	}
}

// --dir --type dynamic without a start command is refused locally with what
// it needs (customer write-up #4 saw PUBLISH_VALIDATION_FAILED from the server).
func TestDeployCreateFromDirTypeDynamicNeedsRunCommand(t *testing.T) {
	fakeRoutes(t, map[string]any{})
	dir := t.TempDir()
	if _, err := run(t, "deploy", "create", "--dir", dir, "--type", "dynamic"); err == nil ||
		!strings.Contains(err.Error(), "needs --run-command") {
		t.Fatalf("expected a --run-command usage error, got: %v", err)
	}
	if _, err := run(t, "deploy", "create", "--dir", dir, "--type", "docker_deploy"); err == nil ||
		!strings.Contains(err.Error(), "--from-sandbox") {
		t.Fatalf("expected a --from-sandbox usage error, got: %v", err)
	}
}
