package commands_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

func TestRestartPostsAndWaitsWhenAsked(t *testing.T) {
	defer commands.SetPollIntervalForTest(time.Millisecond)()
	polls := 0
	sandboxByNameAPI(t, func(c call) (int, string, bool) {
		switch {
		case c.is("POST", boxPath+"/restart"):
			return 202, `{"id":"` + boxID + `","state":"resuming","lifecycle":{"status":"in_progress","operation":"restart"}}`, true
		case c.is("GET", boxPath):
			polls++
			if polls < 2 {
				return 200, `{"state":"resuming"}`, true
			}
			return 200, `{"state":"running","ready":true}`, true
		}
		return 0, "", false
	})
	if out, err := run(t, "restart", "my-box", "--wait"); err != nil || polls < 2 || !strings.Contains(out, "running") {
		t.Fatalf("%v polls=%d %s", err, polls, out)
	}
}

func TestRestartRefusalsSayWhatToDoNext(t *testing.T) {
	cases := []struct {
		status int
		code   string
		hint   string
	}{
		{409, "RESTART_NOT_POWER_CYCLED", "miosa stop my-box"},
		{502, "RESTART_BOOT_FAILED", "miosa resume my-box --wait"},
		{503, "RESTART_STOP_IN_PROGRESS", "miosa resume my-box --wait"},
		{409, "RESTART_UNAVAILABLE", "miosa stop my-box"},
		{409, "SANDBOX_NOT_RUNNING", "miosa resume my-box"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			boxRoutes(t, map[string]any{"POST " + boxPath + "/restart": resp{tc.status, `{"error":{"code":"` + tc.code + `","message":"refused"}}`}})
			out, err := run(t, "restart", "my-box")
			if err == nil {
				t.Fatalf("expected an error\n%s", out)
			}
			if !strings.Contains(commands.ErrorText(err, false), tc.hint) {
				t.Fatalf("missing hint %q:\n%s\n%v", tc.hint, out, err)
			}
		})
	}
}
