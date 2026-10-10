package commands

import (
	"fmt"
	"strconv"
	"strings"
	"time"
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
