package commands

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// usageWindowQuery turns --since/--until (a time or a duration back from now)
// into the RFC 3339 window GET /sandboxes/:id/usage takes. --until alone is
// refused: the server reports a window only from a start.
func usageWindowQuery(_ []string, q url.Values) error {
	now := time.Now().UTC()
	for _, key := range []string{"since", "until"} {
		v := strings.TrimSpace(q.Get(key))
		if v == "" {
			continue
		}
		at, err := windowTime(v, now)
		if err != nil {
			return usagef("--%s: %v", key, err)
		}
		q.Set(key, at.Format(time.RFC3339))
	}
	if q.Get("until") != "" && q.Get("since") == "" {
		return usagef("--until needs --since")
	}
	return nil
}

func windowTime(v string, now time.Time) (time.Time, error) {
	if at, err := time.Parse(time.RFC3339, v); err == nil {
		return at, nil
	}
	n, err := parseSeconds(v)
	if err != nil {
		return time.Time{}, fmt.Errorf("use a time (2026-10-20T10:00:00Z) or a duration back from now (24h, 7d)")
	}
	return now.Add(-time.Duration(n) * time.Second), nil
}

// usageWindowAfter adds a readable label and the run time (the runtime meter
// in seconds) to the window the server returned.
func usageWindowAfter(_ *cobra.Command, resp any) error {
	obj, _ := unwrapObject(resp).(map[string]any)
	window, _ := obj["window"].(map[string]any)
	if window == nil {
		return nil
	}
	window["label"] = fmt.Sprintf("%v to %v", window["since"], window["until"])
	runtime := 0.0
	meters, _ := window["meters"].([]any)
	for _, m := range meters {
		row, _ := m.(map[string]any)
		if row["meter"] == "runtime" && row["unit"] == "second" {
			runtime += num(row["quantity"])
		}
	}
	window["runtime_sec"] = int64(runtime)
	return nil
}
