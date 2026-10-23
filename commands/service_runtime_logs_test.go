package commands_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const svcLogsPath = "/projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/logs"

// serviceLogsAPI serves the project and service lookups, and the runtime log
// from logs (called with how many times it was read).
func serviceLogsAPI(t *testing.T, logs func(n int32) (int, string)) *fakeAPI {
	t.Helper()
	var reads atomic.Int32
	return newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/projects"):
			return http.StatusOK, serviceProjectJSON
		case c.is("GET", "/projects/11111111-1111-1111-1111-111111111111/services"):
			return http.StatusOK, webServiceJSON
		case c.is("GET", svcLogsPath):
			return logs(reads.Add(1))
		}
		return http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`
	})
}

func TestServiceLogsShowsTheRuntimeLogByDefault(t *testing.T) {
	f := serviceLogsAPI(t, func(int32) (int, string) {
		return 200, `{"data":{"source":"runtime","replicas":[{"replica_id":"r1","slot":0,"state":"running","lines":["listening on 8080","GET / 200"],"error":null}]}}`
	})
	out, err := run(t, "service", "logs", "cliniciq", "API", "--tail", "50")
	if err != nil {
		t.Fatal(err)
	}
	if out != "listening on 8080\nGET / 200\n" {
		t.Fatalf("out = %q", out)
	}
	if q := f.find("GET", svcLogsPath)[0].Query; q != "tail=50" {
		t.Fatalf("query = %q", q)
	}
}

func TestServiceLogsNamesTheReplicaWhenThereAreSeveral(t *testing.T) {
	serviceLogsAPI(t, func(int32) (int, string) {
		return 200, `{"data":{"replicas":[{"replica_id":"r1","slot":0,"state":"running","lines":["a"]},{"replica_id":"r2","slot":1,"state":"failed","lines":[],"error":"host unreachable"}]}}`
	})
	stdout, stderr, err := runSplit(t, "service", "logs", "cliniciq", "API")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "[replica 0] a\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "replica 1 (failed): host unreachable") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestServiceLogsFollowPrintsOnlyNewLines(t *testing.T) {
	defer commands.SetServiceLogPollEveryForTest(5 * time.Millisecond)()
	pages := []string{
		`["one","two"]`,
		`["one","two","three"]`,
		`["two","three","four","five"]`,
	}
	serviceLogsAPI(t, func(n int32) (int, string) {
		if int(n) > len(pages) {
			return 503, `{"error":{"code":"DONE_IN_TEST","message":"end of test"}}`
		}
		return 200, `{"data":{"replicas":[{"replica_id":"r1","slot":0,"state":"running","lines":` + pages[n-1] + `}]}}`
	})
	out, err := run(t, "service", "logs", "cliniciq", "API", "-f", "--retries", "0")
	if err == nil || !strings.Contains(err.Error(), "end of test") {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(out, "one\ntwo\nthree\nfour\nfive\n") {
		t.Fatalf("out = %q", out)
	}
}

func TestServiceLogsFollowJSONIsOneLinePerObject(t *testing.T) {
	defer commands.SetServiceLogPollEveryForTest(5 * time.Millisecond)()
	serviceLogsAPI(t, func(n int32) (int, string) {
		if n > 1 {
			return 503, `{"error":{"code":"DONE_IN_TEST","message":"end of test"}}`
		}
		return 200, `{"data":{"replicas":[{"replica_id":"r1","slot":0,"state":"running","lines":["hi"]}]}}`
	})
	stdout, _, _ := runSplit(t, "service", "logs", "cliniciq", "API", "-f", "--json", "--retries", "0")
	if stdout != "{\"line\":\"hi\",\"replica_id\":\"r1\",\"slot\":0}\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

// `miosa logs` in a linked directory is the service's runtime log, in the
// shape the API serves it (per replica).
func TestLogsInALinkedDirectoryReadTheRuntimeLog(t *testing.T) {
	t.Chdir(t.TempDir())
	routes := linkedRoutes()
	routes["GET /projects/"+linkProjectID+"/services/"+linkServiceID+"/logs"] = `{"data":{"source":"runtime","replicas":[{"replica_id":"r1","slot":0,"state":"running","lines":["server up"],"error":null}]}}`
	f := fakeRoutes(t, routes)
	setupEnv(t, f.srv)
	if _, err := run(t, "link", "cliniciq", "--service", "API"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "logs", "-n", "20")
	if err != nil {
		t.Fatalf("logs: %v\n%s", err, out)
	}
	if out != "server up\n" {
		t.Fatalf("out = %q", out)
	}
	if q := f.find("GET", "/projects/"+linkProjectID+"/services/"+linkServiceID+"/logs")[0].Query; q != "tail=20" {
		t.Fatalf("query = %q", q)
	}
}

// The release events route answers {from, to, reason: {code, detail}}.
func TestServiceLogsEventsReadTheAPIsEventShape(t *testing.T) {
	routes := map[string]any{}
	routes["GET /projects"] = serviceProjectJSON
	routes["GET /projects/11111111-1111-1111-1111-111111111111/services"] = webServiceJSON
	routes["GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/deployments"] = releasesJSON
	routes["GET /projects/11111111-1111-1111-1111-111111111111/services/33333333-3333-3333-3333-333333333333/deployments/44444444-4444-4444-4444-444444444444/events"] = `{"data":[{"seq":1,"from":null,"to":"queued","reason":null,"actor":"user","at":"2026-10-23T12:59:40Z"},{"seq":2,"from":"queued","to":"failed","reason":{"code":"build_failed","detail":"exit 1"},"actor":"system","at":"2026-10-23T13:00:00Z"}]}`
	fakeRoutes(t, routes)
	out, err := run(t, "service", "logs", "cliniciq", "API", "--events")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "- -> queued") || !strings.Contains(out, "queued -> failed  build_failed: exit 1") {
		t.Fatalf("out = %s", out)
	}
}
