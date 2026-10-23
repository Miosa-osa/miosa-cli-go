package commands_test

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const historyRow = `{"id":"c1","command":"make test","mode":"exec","status":"failed","exit_code":2,"started_at":"2026-10-23T10:00:00Z","duration_ms":1500,"output_cut":false}`

func TestHistoryListsCommandsWithFilters(t *testing.T) {
	f := sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if c.is("GET", boxPath+"/history") {
			return 200, `{"data":[` + historyRow + `]}`, true
		}
		return 0, "", false
	})
	out, err := run(t, "history", "my-box", "--mode", "exec", "-n", "5")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"c1", "exec", "failed", "2", "1.5s", "make test"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	q := f.find("GET", boxPath+"/history")[0].Query
	if !strings.Contains(q, "mode=exec") || !strings.Contains(q, "limit=5") {
		t.Fatalf("query = %q", q)
	}
}

func TestHistoryEmptySaysSo(t *testing.T) {
	sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if c.is("GET", boxPath+"/history") {
			return 200, `{"data":[]}`, true
		}
		return 0, "", false
	})
	out, err := run(t, "history", "my-box")
	if err != nil || !strings.Contains(out, "No commands recorded yet.") {
		t.Fatalf("%v %s", err, out)
	}
}

func TestHistoryPrintsOneCommandsOutput(t *testing.T) {
	sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if c.is("GET", boxPath+"/history/c1") {
			return 200, `{"data":{"id":"c1","command":"make test","stdout":"partial\n","stderr":"boom\n","output_cut":true}}`, true
		}
		return 0, "", false
	})
	stdout, stderr, err := runSplit(t, "history", "my-box", "c1")
	if err != nil || stdout != "partial\n" || !strings.Contains(stderr, "last 64 KiB") {
		t.Fatalf("%v %q %q", err, stdout, stderr)
	}
	stdout, _, err = runSplit(t, "history", "my-box", "c1", "--stderr")
	if err != nil || stdout != "boom\n" {
		t.Fatalf("%v %q", err, stdout)
	}
	stdout, _, err = runSplit(t, "history", "my-box", "c1", "--json")
	if err != nil || !strings.Contains(stdout, `"stdout": "partial\n"`) && !strings.Contains(stdout, `"stdout":"partial\n"`) {
		t.Fatalf("%v %q", err, stdout)
	}
}

func TestHistoryUnknownCommandIsNotFound(t *testing.T) {
	sandboxByNameAPI(t, func(c call) (int, string, bool) { return 0, "", false })
	if _, err := run(t, "history", "my-box", "nope"); commands.ExitCode(err) != commands.ExitNotFound {
		t.Fatalf("err = %v code %d", err, commands.ExitCode(err))
	}
}

func TestHistoryFollowPrintsEachCommandOnce(t *testing.T) {
	defer commands.SetHistoryPollEveryForTest(5 * time.Millisecond)()
	row := func(id, cmd string) string {
		return `{"id":"` + id + `","command":"` + cmd + `","mode":"exec","status":"succeeded","exit_code":0,"started_at":"2026-10-23T10:00:00Z"}`
	}
	pages := []string{
		`[` + row("c2", "two") + `,` + row("c1", "one") + `]`,
		`[` + row("c3", "three") + `,` + row("c2", "two") + `,` + row("c1", "one") + `]`,
	}
	var n int32
	sandboxByNameAPI(t, func(c call) (int, string, bool) {
		if !c.is("GET", boxPath+"/history") {
			return 0, "", false
		}
		i := atomic.AddInt32(&n, 1)
		if int(i) > len(pages) {
			return 503, `{"error":{"code":"DONE_IN_TEST","message":"end of test"}}`, true
		}
		return 200, `{"data":` + pages[i-1] + `}`, true
	})
	out, err := run(t, "history", "my-box", "-f", "--retries", "0")
	if err == nil || !strings.Contains(err.Error(), "end of test") {
		t.Fatalf("err = %v", err)
	}
	one, two, three := strings.Index(out, "one"), strings.Index(out, "two"), strings.Index(out, "three")
	if one < 0 || !(one < two && two < three) || strings.Count(out, "three") != 1 || strings.Count(out, "one") != 1 {
		t.Fatalf("out = %q", out)
	}
}
