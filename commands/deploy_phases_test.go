package commands_test

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// publishJSON is the `publish` object GET /deployments/:id returns, with phases up to `through` done.
func publishJSON(status, waitReason string, done []string, failedPhase, failedReason string) string {
	names := []string{"build", "runtime", "route", "certificate", "reachable", "live"}
	isDone := map[string]bool{}
	for _, n := range done {
		isDone[n] = true
	}
	var phases []string
	for _, n := range names {
		state := "pending"
		started, finished := "null", "null"
		if isDone[n] {
			state, started, finished = "done", `"2026-10-19T00:00:00Z"`, `"2026-10-19T00:00:12Z"`
		}
		if n == failedPhase {
			state, started, finished = "failed", `"2026-10-19T00:00:00Z"`, `"2026-10-19T00:00:03Z"`
		}
		phases = append(phases, fmt.Sprintf(`{"name":%q,"state":%q,"started_at":%s,"finished_at":%s}`, n, state, started, finished))
	}
	failed := "null"
	if failedPhase != "" {
		failed = fmt.Sprintf(`{"phase":%q,"reason":%q}`, failedPhase, failedReason)
	}
	wait := "null"
	if waitReason != "" {
		wait = fmt.Sprintf("%q", waitReason)
	}
	return fmt.Sprintf(`"publish":{"attempt_id":"a1","status":%q,"phase":"certificate","phases":[%s],"wait_reason":%s,"failed":%s}`, status, strings.Join(phases, ","), wait, failed)
}

func phaseAPI(t *testing.T, polls []string) (*fakeAPI, *int32) {
	var n int32
	api := newFakeAPI(t, func(c call) (int, string) {
		switch {
		case c.is("GET", "/sandboxes/by-name/box-1"):
			return 200, `{"id":"sbx-1"}`
		case c.is("POST", "/sandboxes/sbx-1/deploy"):
			return 201, sandboxDeployBody
		case c.is("GET", "/deployments/d1"):
			i := int(atomic.AddInt32(&n, 1)) - 1
			if i >= len(polls) {
				i = len(polls) - 1
			}
			return 200, `{"data":{"id":"d1","name":"my-app","slug":"my-app","state":"running","public_url":"https://d1.miosa.app",` + polls[i] + `}}`
		}
		return 404, `{"error":{"code":"NOT_FOUND","message":"no route"}}`
	})
	return api, &n
}

func TestDeployWaitsForLiveByDefaultAndPrintsEachPhase(t *testing.T) {
	defer commands.SetPollIntervalForTest(5 * time.Millisecond)()
	phaseAPI(t, []string{
		publishJSON("starting", "", []string{"build"}, "", ""),
		publishJSON("waiting", "waiting for HTTPS certificate", []string{"build", "runtime", "route"}, "", ""),
		publishJSON("waiting", "waiting for HTTPS certificate", []string{"build", "runtime", "route"}, "", ""),
		publishJSON("live", "", []string{"build", "runtime", "route", "certificate", "reachable", "live"}, "", ""),
	})

	out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1")
	if err != nil {
		t.Fatalf("deploy create: %v\n%s", err, out)
	}
	for _, want := range []string{"Waiting for my-app to go live", "build", "runtime started", "route published", "waiting for HTTPS certificate", "HTTPS certificate", "reachable", "live", "https://d1.miosa.app"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "waiting for HTTPS certificate") != 1 {
		t.Fatalf("a wait reason is printed when it changes, not on every poll:\n%s", out)
	}
	if !strings.Contains(out, "12s") {
		t.Fatalf("each finished phase shows how long it took:\n%s", out)
	}
	// Order: the build line comes before the route, which comes before the certificate.
	if !(strings.Index(out, "build ") < strings.Index(out, "route published") && strings.Index(out, "route published") < strings.Index(out, "HTTPS certificate  ")) {
		t.Fatalf("phases are printed in order:\n%s", out)
	}
}

func TestDeployExitsNonZeroWhenPublishingFails(t *testing.T) {
	defer commands.SetPollIntervalForTest(5 * time.Millisecond)()
	phaseAPI(t, []string{
		publishJSON("waiting", "waiting for HTTPS certificate", []string{"build", "runtime", "route"}, "", ""),
		publishJSON("failed", "", []string{"build", "runtime", "route"}, "certificate", "waiting for HTTPS certificate"),
	})

	out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1")
	if err == nil {
		t.Fatalf("a failed publish must exit non-zero:\n%s", out)
	}
	if !strings.Contains(err.Error(), "publishing failed at HTTPS certificate") || !strings.Contains(err.Error(), "waiting for HTTPS certificate") {
		t.Fatalf("the error names the phase and the reason: %v", err)
	}
	if !strings.Contains(out, "FAILED") {
		t.Fatalf("the failed phase is printed:\n%s", out)
	}
}

func TestDeployNoWaitReturnsAtOnce(t *testing.T) {
	_, polls := phaseAPI(t, []string{publishJSON("starting", "", []string{"build"}, "", "")})

	out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1", "--no-wait")
	if err != nil {
		t.Fatalf("deploy create --no-wait: %v\n%s", err, out)
	}
	if atomic.LoadInt32(polls) != 0 {
		t.Fatalf("--no-wait must not poll the deployment, polled %d times", atomic.LoadInt32(polls))
	}
	if strings.Contains(out, "Waiting for") {
		t.Fatalf("--no-wait must not wait:\n%s", out)
	}
}

func TestDeployStillWaitsOnAServerWithoutPhases(t *testing.T) {
	defer commands.SetPollIntervalForTest(5 * time.Millisecond)()
	phaseAPI(t, []string{`"publish":null`})

	out, err := run(t, "deploy", "create", "my-app", "--from-sandbox", "box-1")
	if err != nil || !strings.Contains(out, "https://d1.miosa.app") {
		t.Fatalf("an older server is judged by its state: err=%v\n%s", err, out)
	}
}
