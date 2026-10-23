package commands_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const qaProjectID = "55555555-5555-4555-8555-555555555555"

func TestProjectDeleteAcceptedWhileServicesStopSaysSo(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"GET /projects":                   `{"data":[{"id":"` + qaProjectID + `","name":"shop","slug":"shop"}]}`,
		"DELETE /projects/" + qaProjectID: resp{202, `{"data":{"id":"` + qaProjectID + `","status":"deleting","waiting_on":["s1"]}}`},
	})
	out, err := run(t, "project", "rm", "shop", "--yes")
	if err != nil || !strings.Contains(out, "finishes as soon as its services have stopped") {
		t.Fatalf("%v %s", err, out)
	}
}

// An older server refuses the delete while a service is still stopping (QA 10-23,
// right after 'service delete'): the CLI waits and tries again.
func TestProjectDeleteRetriesWhileAServiceIsStopping(t *testing.T) {
	defer commands.SetProjectDeleteRetryForTest(time.Millisecond, time.Second)()
	var deletes int32
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/projects"):
			return 200, `{"data":[{"id":"` + qaProjectID + `","name":"shop","slug":"shop"}]}`
		case c.is("DELETE", "/projects/"+qaProjectID):
			if atomic.AddInt32(&deletes, 1) < 3 {
				return 409, `{"error":{"code":"SERVICES_NOT_STOPPED","message":"A running service could not be stopped. Try again in a moment."}}`
			}
			return 204, ``
		}
		return 404, `{}`
	})
	out, err := run(t, "project", "rm", "shop", "--yes")
	if err != nil || !strings.Contains(out, "Deleted shop") {
		t.Fatalf("%v %s", err, out)
	}
	if n := len(f.find("DELETE", "/projects/"+qaProjectID)); n != 3 {
		t.Fatalf("deletes = %d", n)
	}
}

func TestProjectDeleteGivesUpAfterTheWait(t *testing.T) {
	defer commands.SetProjectDeleteRetryForTest(time.Millisecond, 20*time.Millisecond)()
	newFakeAPI(t, func(c call) (int, string) {
		if c.is("GET", "/projects") {
			return 200, `{"data":[{"id":"` + qaProjectID + `","name":"shop","slug":"shop"}]}`
		}
		return 409, `{"error":{"code":"SERVICES_NOT_STOPPED","message":"A running service could not be stopped. Try again in a moment."}}`
	})
	if _, err := run(t, "project", "rm", "shop", "--yes"); err == nil || !strings.Contains(err.Error(), "could not be stopped") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceLogsTakesNForTail(t *testing.T) {
	cmd, _, err := commands.Root().Find([]string{"service", "logs"})
	if err != nil || cmd.Flags().ShorthandLookup("n") == nil || cmd.Flags().ShorthandLookup("n").Name != "tail" {
		t.Fatalf("service logs -n: %v", err)
	}
}

// QA 10-23: a Node app folder published as the default static site failed on
// the server as a bare PUBLISH_VALIDATION_FAILED.
func TestDeployDirOfAServerAppSaysToAddARunCommand(t *testing.T) {
	f := fakeRoutes(t, map[string]any{})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "deploy", "create", "--dir", dir, "--no-wait")
	if commands.ExitCode(err) != commands.ExitUsage || !strings.Contains(err.Error(), "--run-command") || !strings.Contains(err.Error(), "package.json") {
		t.Fatalf("err = %v", err)
	}
	if len(f.find("POST", "/deployments/drop/sessions")) != 0 {
		t.Fatal("nothing may be uploaded")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "deploy", "create", "--dir", dir, "--no-wait"); err != nil && strings.Contains(err.Error(), "--run-command") {
		t.Fatalf("a folder with index.html is a site: %v", err)
	}
}
