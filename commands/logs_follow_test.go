package commands_test

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const followDeployID = "5d0c3f0e-1b2a-4c3d-9e8f-0a1b2c3d4e5f"

func TestDeployLogsFollowStreamsTheRuntimeLog(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/deployments/my-app/logs"):
			return 200, `{"data":{"deployment_id":"` + followDeployID + `","state":"running","lines":["built","started"]}}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"tkt-9"}`
		case c.is("GET", "/deployments/"+followDeployID+"/runtime-logs/stream"):
			return 200, "event: log_line\ndata: {\"timestamp\":\"2026-10-23T12:00:00Z\",\"message\":\"GET / 200\",\"unit\":\"app\",\"priority\":6}\n\n"
		}
		return 404, `{"error":{"code":"NOT_FOUND"}}`
	})
	out, err := run(t, "deploy", "logs", "my-app", "--follow")
	if err != nil {
		t.Fatal(err)
	}
	if out != "built\nstarted\nGET / 200\n" {
		t.Fatalf("out = %q", out)
	}
	if q := f.find("GET", "/deployments/"+followDeployID+"/runtime-logs/stream")[0].Query; q != "ticket=tkt-9" {
		t.Fatalf("stream query = %q", q)
	}
	// --follow is the CLI's: it is never sent as a query parameter.
	if q := f.find("GET", "/deployments/my-app/logs")[0].Query; strings.Contains(q, "follow") {
		t.Fatalf("snapshot query = %q", q)
	}
}

func TestDeployLogsFollowJSONIsOneObjectPerLine(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/deployments/my-app/logs"):
			return 200, `{"data":{"deployment_id":"` + followDeployID + `","lines":["built"]}}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"t"}`
		case c.is("GET", "/deployments/"+followDeployID+"/runtime-logs/stream"):
			return 200, "event: log_line\ndata: {\"message\":\"hi\"}\n\n"
		}
		return 404, `{}`
	})
	out, err := run(t, "deploy", "logs", "my-app", "-f", "--json")
	if err != nil {
		t.Fatal(err)
	}
	want := "{\"line\":\"built\"}\n{\"data\":{\"message\":\"hi\"},\"event\":\"log_line\"}\n"
	if out != want {
		t.Fatalf("out = %q, want %q", out, want)
	}
}

func TestDeployLogsFollowSaysWhenNothingRuns(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/deployments/site/logs"):
			return 200, `{"data":{"deployment_id":"` + followDeployID + `","lines":[]}}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"t"}`
		case c.is("GET", "/deployments/"+followDeployID+"/runtime-logs/stream"):
			return 409, `{"error":{"code":"DEPLOYMENT_NOT_RUNNING"}}`
		}
		return 404, `{}`
	})
	_, err := run(t, "deploy", "logs", "site", "-f")
	if err == nil || !strings.Contains(err.Error(), "no live log to follow") {
		t.Fatalf("err = %v", err)
	}
}

func TestDBLogsFollowStreamsTheDatabaseLog(t *testing.T) {
	const dbID = "0f1e2d3c-4b5a-4968-8776-655443322110"
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/databases/my-db/logs"):
			return 200, `{"logs":[{"t":null,"stream":"stdout","line":"ready to accept connections"}],"count":1,"database_id":"` + dbID + `"}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"t"}`
		case c.is("GET", "/databases/"+dbID+"/logs/stream"):
			return 200, "event: log_line\ndata: {\"message\":\"checkpoint complete\"}\n\n"
		}
		return 404, `{}`
	})
	out, err := run(t, "db", "logs", "my-db", "-f")
	if err != nil {
		t.Fatal(err)
	}
	if out != "ready to accept connections\ncheckpoint complete\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestSandboxLogsFollowPrintsTheCollectorMessage(t *testing.T) {
	newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/sandboxes/by-name/my-box"):
			return 200, `{"id":"` + boxID + `"}`
		case c.is("GET", boxPath+"/logs"):
			return 200, `{"logs":[]}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"t"}`
		case c.is("GET", boxPath+"/logs/stream"):
			return 200, "event: log_line\ndata: {\"timestamp\":\"2026-10-23T12:00:00Z\",\"message\":\"started\",\"unit\":\"miosa\",\"priority\":6}\n\n"
		}
		return 404, `{}`
	})
	out, err := run(t, "logs", "my-box", "-f")
	if err != nil {
		t.Fatal(err)
	}
	if out != "started\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestDeployLogsFollowABuildUntilItFinishes(t *testing.T) {
	f := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/deployments/my-app/logs"):
			return 200, `{"data":{"deployment_id":"` + followDeployID + `","state":"building","lines":["step 1/3"]}}`
		case c.is("POST", "/sse/ticket"):
			return 200, `{"ticket":"t"}`
		case c.is("GET", "/deployments/"+followDeployID+"/logs"):
			return 200, "data: step 2/3\n\ndata: step 3/3\n\nevent: done\ndata: {\"status\":\"success\"}\n\n"
		case c.is("GET", "/deployments/"+followDeployID):
			return 200, `{"data":{"state":"running"}}`
		case c.is("GET", "/deployments/"+followDeployID+"/runtime-logs/stream"):
			return 200, "event: log_line\ndata: {\"message\":\"listening on 3000\"}\n\n"
		}
		return 404, `{}`
	})
	out, err := run(t, "deploy", "logs", "my-app", "-f")
	if err != nil {
		t.Fatal(err)
	}
	if out != "step 1/3\nstep 2/3\nstep 3/3\nbuild finished: success\nthe app is running; following its output\nlistening on 3000\n" {
		t.Fatalf("out = %q", out)
	}
	if q := f.find("GET", "/deployments/"+followDeployID+"/logs")[0].Query; !strings.Contains(q, "follow=1") {
		t.Fatalf("build stream query = %q", q)
	}
}

// A build that finished before its stream opened never sends "done"; the
// follow notices the deployment left the building state and moves on.
func TestDeployLogsFollowMovesOnWhenTheBuildEndsSilently(t *testing.T) {
	defer commands.SetDeployWatchEveryForTest(20 * time.Millisecond)()
	var polls atomic.Int32
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/deployments/my-app/logs":
			_, _ = w.Write([]byte(`{"data":{"deployment_id":"` + followDeployID + `","state":"building","lines":[]}}`))
		case r.URL.Path == "/sse/ticket":
			_, _ = w.Write([]byte(`{"ticket":"t"}`))
		case r.URL.Path == "/deployments/"+followDeployID+"/logs":
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done() // the stream stays open and silent
		case r.URL.Path == "/deployments/"+followDeployID:
			state := "building"
			if polls.Add(1) > 2 {
				state = "failed"
			}
			_, _ = w.Write([]byte(`{"data":{"state":"` + state + `"}}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{}`))
		}
	})
	setupEnv(t, srv)
	done := make(chan error, 1)
	go func() {
		_, err := run(t, "deploy", "logs", "my-app", "-f")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "the deployment is failed") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("follow did not move on when the build ended")
	}
}
