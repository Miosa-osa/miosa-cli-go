package commands_test

import (
	"strings"
	"testing"
)

const stopWarning = "Stop saves this computer and frees its resources. Its disk and files are always kept. Starting it again usually resumes exactly where it left off; if the saved state can't be restored, it starts fresh from its disk and open apps are closed. Computers have no separate pause: stop is how you pause one."

func TestComputerStopWarnsOffATerminalAndStillStops(t *testing.T) {
	api := newFakeAPI(t, func(_ call) (int, string) { return 200, `{"id":"c1","status":"stopping"}` })
	out, err := run(t, "computer", "stop", "c1")
	if err != nil {
		t.Fatalf("a script must not be blocked: %v\n%s", err, out)
	}
	if len(api.find("POST", "/computers/c1/stop")) != 1 {
		t.Fatalf("stop was not sent: %v", api.calls)
	}
	for _, want := range []string{stopWarning} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestComputerStopYesSkipsThePrompt(t *testing.T) {
	api := newFakeAPI(t, func(_ call) (int, string) { return 200, `{"id":"c1"}` })
	if _, err := run(t, "computer", "stop", "c1", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(api.find("POST", "/computers/c1/stop")) != 1 {
		t.Fatalf("stop was not sent: %v", api.calls)
	}
}

func TestComputerStopHelpSaysItIsNotPause(t *testing.T) {
	out, _ := run(t, "computer", "stop", "--help")
	for _, want := range []string{stopWarning} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

func TestSandboxStopAndPauseHelpStateTheDifference(t *testing.T) {
	for verb, want := range map[string]string{
		"stop":  "Stop keeps the disk; unlike 'miosa pause', it does not promise to keep memory or running processes.",
		"pause": "Pause keeps the disk and memory; processes continue where they were on resume.",
	} {
		out, _ := run(t, verb, "--help")
		if !strings.Contains(out, want) {
			t.Errorf("%s help lacks %q:\n%s", verb, want, out)
		}
	}
}
