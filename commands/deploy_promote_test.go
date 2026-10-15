package commands_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A version pushed with --no-promote is uploaded but left off the air: the
// site keeps serving the previous version, and the command prints the id to
// promote later.
func TestDeployPushNoPromoteLeavesTheVersionOffTheAir(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("POST", "/deployments/my-site/drop/sessions"):
			return 201, `{"data":{"session_id":"s1","deployment_id":"d3"}}`
		case c.Method == "POST" && strings.Contains(c.Path, "/drop/sessions/s1/chunks"):
			return 200, `{"data":{"session_id":"s1","chunk_index":0,"bytes_received":10}}`
		case strings.HasSuffix(c.Path, "/drop/sessions/s1/complete"):
			return 200, `{"data":{"version_id":"v2","deployment_id":"d3","public_url":"https://d3.miosa.app","promoted":false}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})

	dir := t.TempDir()
	writeStaticSite(t, dir)

	out, err := run(t, "deploy", "push", "my-site", "--dir", dir, "--no-promote")
	if err != nil {
		t.Fatalf("deploy push --no-promote: %v (out=%q)", err, out)
	}

	complete := f.find("POST", "/deployments/my-site/drop/sessions/s1/complete")
	if len(complete) != 1 {
		t.Fatal("expected the session to be completed")
	}
	if complete[0].Body["promote"] != false {
		t.Fatalf("complete should not promote, got %+v", complete[0].Body)
	}
	if !strings.Contains(out, "not live") {
		t.Fatalf("the output should say the version is not live: %q", out)
	}
	if !strings.Contains(out, "miosa deploy promote v2") {
		t.Fatalf("the output should name the promote command: %q", out)
	}
	if !strings.Contains(out, "https://d3.miosa.app") {
		t.Fatalf("the output should say what is still serving: %q", out)
	}
}

// 'miosa deploy promote <version>' takes the deployment from the folder link
// when --deployment is not given.
func TestDeployPromoteVersionUsesTheFolderLink(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/deployments/d3/versions/v2/promote") {
			return 200, `{"data":{"id":"d3","name":"my-site","state":"running","public_url":"https://d3.miosa.app"}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})

	dir := t.TempDir()
	link, err := json.Marshal(map[string]string{"deployment_id": "d3", "name": "my-site"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".miosa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".miosa", "deployment.json"), append(link, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, "deploy", "promote", "v2", "--dir", dir)
	if err != nil {
		t.Fatalf("deploy promote: %v (out=%q)", err, out)
	}
	if len(f.find("POST", "/deployments/d3/versions/v2/promote")) != 1 {
		t.Fatal("the version promote was not called")
	}
	if !strings.Contains(out, "https://d3.miosa.app") {
		t.Fatalf("the live URL should be printed: %q", out)
	}
}

// Two arguments keep the release-level promote that shipped before versions
// could be promoted.
func TestDeployPromoteReleaseStays(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		if c.is("POST", "/deployments/my-site/releases/rel-1/promote") {
			return 200, `{}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})

	out, err := run(t, "deploy", "promote", "my-site", "rel-1")
	if err != nil {
		t.Fatalf("deploy promote <deployment> <release>: %v (out=%q)", err, out)
	}
	if len(f.find("POST", "/deployments/my-site/releases/rel-1/promote")) != 1 {
		t.Fatal("the release promote was not called")
	}
	if !strings.Contains(out, "Promoted release rel-1") {
		t.Fatalf("release promote output = %q", out)
	}
}
