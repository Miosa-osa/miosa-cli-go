// Package testenv keeps a package's tests away from the developer's own machine.
//
// The CLI reads ~/.miosa and MIOSA_* from the environment (an active org, a key, a
// base URL). A test that forgets to set its own would then call the live API with the
// developer's profile, which may belong to a real customer. Every test package calls
// Run from TestMain so the whole binary starts with a temporary HOME and no MIOSA_*,
// and individual tests only set what they need.
package testenv

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Run executes the package's tests under a temporary HOME with every MIOSA_* variable
// removed, then restores nothing: the process ends right after.
func Run(m *testing.M) int {
	home, err := os.MkdirTemp("", "miosa-cli-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		return 1
	}
	defer os.RemoveAll(home)

	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "MIOSA_") {
			os.Unsetenv(name)
		}
	}
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	os.Setenv("XDG_CONFIG_HOME", home+"/.config")
	return m.Run()
}
