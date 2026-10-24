package commands

import (
	"io"
	"net/http"
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

// A member or billing change refused for a missing second factor must say where
// to turn it on: the CLI cannot enroll, and "MFA_REQUIRED" alone is not an instruction.
func TestMfaRequiredNamesTheEnrollPage(t *testing.T) {
	t.Setenv("MIOSA_CONSOLE_URL", "")
	resp := &http.Response{
		StatusCode: 403,
		Header:     http.Header{"X-Request-Id": []string{"rid-1"}},
		Body: io.NopCloser(strings.NewReader(`{"error":"MFA_REQUIRED","mfa_required":true,` +
			`"enroll_path":"/account?section=security",` +
			`"message":"Turn on two-factor authentication to continue."}`)),
	}

	got := friendlyMessage(api.ParseError(resp))

	for _, want := range []string{
		"Turn on two-factor authentication to continue.",
		"MFA_REQUIRED",
		"https://miosa.ai/account?section=security",
		"request_id=rid-1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "lack the required scope") {
		t.Fatalf("a two-factor refusal is not a scope problem: %q", got)
	}
}

func TestMfaEnrollURLFollowsTheConsole(t *testing.T) {
	t.Setenv("MIOSA_CONSOLE_URL", "https://console.example.com/")
	got := mfaEnrollURL(&api.Error{Code: "MFA_REQUIRED", EnrollPath: "//evil.example"})
	if got != "https://console.example.com/account?section=security" {
		t.Fatalf("got %q", got)
	}
}

// A flat error whose "error" is a sentence keeps it as the message.
func TestFlatSentenceErrorIsNotTreatedAsACode(t *testing.T) {
	resp := &http.Response{
		StatusCode: 403,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":"not a member of this tenant","message":"ignored"}`)),
	}
	e := api.ParseError(resp)
	if e.Message != "not a member of this tenant" || e.Code != "" {
		t.Fatalf("got code=%q message=%q", e.Code, e.Message)
	}
}
