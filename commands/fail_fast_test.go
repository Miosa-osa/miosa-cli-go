package commands_test

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

const failFastHeader = "X-Miosa-Fail-Fast"

func TestFailFastRidesOnlyOnTheRequestThatStartsTheMachine(t *testing.T) {
	f := boxRoutes(t, map[string]any{
		"POST /sandboxes":                resp{201, string(fakeSandbox(boxID, "my-box"))},
		"POST " + boxPath + "/fork":      `{"id":"fork-id","state":"provisioning"}`,
		"POST " + boxPath + "/resume":    `{"id":"` + boxID + `","state":"running"}`,
		"POST " + boxPath + "/pause":     `{"id":"` + boxID + `","state":"paused"}`,
		"GET /sandboxes/by-name/fork-id": `{"id":"fork-id"}`,
	})
	for _, args := range [][]string{
		{"create", "my-box", "--fail-fast"},
		{"fork", "my-box", "--fail-fast"},
		{"resume", "my-box", "--fail-fast"},
		{"pause", "my-box"},
		{"create", "other"},
	} {
		if out, err := run(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	want := map[string][]string{
		"POST /sandboxes":             {"true", ""},
		"POST " + boxPath + "/fork":   {"true"},
		"POST " + boxPath + "/resume": {"true"},
		"POST " + boxPath + "/pause":  {""},
	}
	for route, values := range want {
		method, path, _ := strings.Cut(route, " ")
		calls := f.find(method, path)
		if len(calls) != len(values) {
			t.Fatalf("%s: %d calls, want %d", route, len(calls), len(values))
		}
		for i, c := range calls {
			if got := c.Headers.Get(failFastHeader); got != values[i] {
				t.Fatalf("%s call %d: %s=%q, want %q", route, i, failFastHeader, got, values[i])
			}
		}
	}
	for _, c := range f.find("GET", "/sandboxes/by-name/my-box") {
		if c.Headers.Get(failFastHeader) != "" {
			t.Fatal("a read must never carry fail-fast")
		}
	}
}

func TestFailFastNoReadyMachineIsReported(t *testing.T) {
	fakeRoutes(t, map[string]any{
		"POST /sandboxes": resp{503, `{"ok":false,"error":{"code":"NO_READY_MACHINE","message":"No warm machine is ready, and fail_fast was requested. Nothing was started or billed."}}`},
	})
	out, err := run(t, "create", "my-box", "--fail-fast")
	if err == nil || !strings.Contains(err.Error()+out, "Nothing was started or billed") {
		t.Fatalf("got %v\n%s", err, out)
	}
}

func TestFailFastRefusesCount(t *testing.T) {
	f := fakeRoutes(t, map[string]any{})
	if _, err := run(t, "create", "ci", "--count", "3", "--fail-fast"); commands.ExitCode(err) != commands.ExitUsage {
		t.Fatalf("--count with --fail-fast: %v", err)
	}
	if f.count() != 0 {
		t.Fatal("nothing may be sent")
	}
}
