package commands

import (
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A failure wrapped by die() used to be printed with its sentence twice: the
// context prefix was taken from the *Failure's Error(), which is itself the
// friendly rendering of the error inside it, so trimming left that text behind
// and friendlyMessage appended the sentence again.
func TestAWrappedAPIErrorIsNotPrintedTwice(t *testing.T) {
	inner := &api.Error{
		Status:    401,
		Code:      "UNAUTHORIZED",
		Message:   "Invalid or expired token",
		RequestID: "abc123",
	}

	got := friendlyMessage(die(inner))

	if n := strings.Count(got, "Invalid or expired token"); n != 1 {
		t.Fatalf("the sentence appears %d times: %q", n, got)
	}
	if !strings.Contains(got, "miosa login") {
		t.Fatalf("the sign-in hint should survive: %q", got)
	}
	if !strings.Contains(got, "request_id=abc123") {
		t.Fatalf("the request id should survive: %q", got)
	}
}

// The API requires template_type on the computer route (400 without it), so the
// default has to be a real template rather than the server's absent default.
func TestComputerCreateSendsTheDesktopTemplateByDefault(t *testing.T) {
	cmd := newComputerCreateCmd()

	f := cmd.Flags().Lookup("template")
	if f == nil {
		t.Fatal("--template is not bound")
	}
	if f.DefValue != "miosa-desktop" {
		t.Fatalf("--template default = %q, want miosa-desktop", f.DefValue)
	}
	if !strings.Contains(cmd.Long, "miosa-desktop") {
		t.Fatalf("the help should name the default template:\n%s", cmd.Long)
	}
}
