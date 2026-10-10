package commands

import "time"

// SetSnapshotPollInterval lets the external tests poll without waiting.
func SetSnapshotPollInterval(d time.Duration) func() {
	old := snapPollInterval
	snapPollInterval = d
	return func() { snapPollInterval = old }
}
