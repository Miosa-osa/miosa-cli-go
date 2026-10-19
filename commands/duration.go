package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"
)

// parseSeconds reads a duration as whole seconds. It accepts a plain number of
// seconds ("7200"), Go durations ("90m", "2h30m") and days ("2d").
func parseSeconds(s string) (int, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("duration must be positive (got %q)", s)
		}
		return n, nil
	}
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64); err == nil && n > 0 {
			return int(n * 86400), nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid duration %q (use seconds, 90m, 2h or 2d)", s)
	}
	return int(d.Seconds()), nil
}

// secondsOrDuration is a --timeout flag value that takes plain seconds ("180")
// as well as Go durations ("3m"). The global --timeout is an integer number of
// seconds, so a local duration flag of the same name used to reject "180".
type secondsOrDuration struct{ d *time.Duration }

func (v secondsOrDuration) String() string {
	if v.d == nil {
		return ""
	}
	return v.d.String()
}

func (v secondsOrDuration) Set(s string) error {
	n, err := parseSeconds(s)
	if err != nil {
		return err
	}
	*v.d = time.Duration(n) * time.Second
	return nil
}

func (secondsOrDuration) Type() string { return "duration" }

// timeoutVar registers a --timeout flag that accepts seconds or a duration.
func timeoutVar(f *pflag.FlagSet, p *time.Duration, def time.Duration, usage string) {
	*p = def
	f.Var(secondsOrDuration{d: p}, "timeout", usage+" (seconds, or a duration like 3m)")
}
