package commands

import (
	"context"
	"io"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/internal/terminal"
)

// Hooks for the external test package. They exist only in test builds.

// SetVersionForTest changes the reported CLI version and returns a restore func.
func SetVersionForTest(v string) func() {
	old := cliVersion
	cliVersion = v
	return func() { cliVersion = old }
}

// SetSleepForTest replaces the device-login poll sleep.
func SetSleepForTest(f func(ctx context.Context, d time.Duration) error) func() {
	old := sleepFn
	sleepFn = f
	return func() { sleepFn = old }
}

// SetOpenBrowserForTest replaces the browser opener.
func SetOpenBrowserForTest(f func(url string) error) func() {
	old := openBrowser
	openBrowser = f
	return func() { openBrowser = old }
}

// SetSelfPathForTest replaces the running-binary path used by update.
func SetSelfPathForTest(path string) func() {
	old := selfPath
	selfPath = func() (string, error) { return path, nil }
	return func() { selfPath = old }
}

// ErrorText renders err as the CLI would print it.
func ErrorText(err error, asJSON bool) string {
	var b stringsBuilder
	writeError(&b, err, asJSON)
	return b.String()
}

// MaybeNotifyUpdateForTest runs the passive update notice against w.
func MaybeNotifyUpdateForTest(w io.Writer) { maybeNotifyUpdate(w) }

type stringsBuilder struct{ buf []byte }

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	return len(p), nil
}
func (s *stringsBuilder) String() string { return string(s.buf) }

// MaskKeyForTest exposes maskKey.
func MaskKeyForTest(k string) string { return maskKey(k) }

// CompareVersionsForTest exposes compareVersions.
func CompareVersionsForTest(a, b string) int { return compareVersions(a, b) }

// ConsoleURLForTest exposes consoleURL.
func ConsoleURLForTest(u string) string { return consoleURL(u) }

// InstallScriptURL exposes the install script URL for a guard test.
const InstallScriptURL = installScriptURL

// SetPollIntervalForTest shortens the wait poll interval.
func SetPollIntervalForTest(d time.Duration) func() {
	old := pollEvery
	pollEvery = d
	return func() { pollEvery = old }
}

// TerminalRequestForTest exposes the terminal request body builder.
func TerminalRequestForTest(cols, rows int, shell, cwd string, command []string) map[string]any {
	return terminalRequest(terminal.Size{Cols: cols, Rows: rows}, shell, cwd, command)
}

// SetRunPollForTest shortens the run follow interval.
func SetRunPollForTest(d time.Duration) func() {
	old := pollRunEvery
	pollRunEvery = d
	return func() { pollRunEvery = old }
}

// SetSigninPollForTest shortens the sign-in poll interval.
func SetSigninPollForTest(d time.Duration) func() {
	old := signinPollEvery
	signinPollEvery = d
	return func() { signinPollEvery = old }
}

// SummarizeRunErrorForTest exposes summarizeRunError.
func SummarizeRunErrorForTest(msg string) string { return summarizeRunError(msg, "failed") }

// NewUUIDForTest exposes newUUID.
func NewUUIDForTest() string { return newUUID() }

func init() {
	// Tests never reach for the machine's real ssh; ones that want it install fakes.
	lookSSH = func() (string, string, bool) { return "", "", false }
}

// SetSSHToolsForTest replaces the ssh and ssh-keygen lookup.
func SetSSHToolsForTest(ssh, keygen string) func() {
	old := lookSSH
	lookSSH = func() (string, string, bool) { return ssh, keygen, true }
	return func() { lookSSH = old }
}
