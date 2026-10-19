package commands

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// 'miosa wait --timeout 180' used to fail with "missing unit in duration"
// because the local flag was a Go duration while the global --timeout is whole
// seconds. The flag takes both.
func TestTimeoutFlagTakesSecondsAndDurations(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"180": 180 * time.Second, "3m": 3 * time.Minute, "90s": 90 * time.Second, "2h": 2 * time.Hour,
	} {
		var d time.Duration
		cmd := &cobra.Command{Use: "x"}
		timeoutVar(cmd.Flags(), &d, time.Minute, "Give up")
		if d != time.Minute {
			t.Fatalf("default = %s", d)
		}
		if err := cmd.Flags().Set("timeout", in); err != nil {
			t.Fatalf("--timeout %s: %v", in, err)
		}
		if d != want {
			t.Fatalf("--timeout %s = %s, want %s", in, d, want)
		}
	}
	var d time.Duration
	cmd := &cobra.Command{Use: "x"}
	timeoutVar(cmd.Flags(), &d, time.Minute, "Give up")
	for _, bad := range []string{"soon", "", "-5", "0"} {
		if err := cmd.Flags().Set("timeout", bad); err == nil {
			t.Errorf("--timeout %q must be refused", bad)
		}
	}
}
