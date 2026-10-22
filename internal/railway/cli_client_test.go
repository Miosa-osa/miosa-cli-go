package railway

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRailway writes a stand-in for the Railway CLI that prints body and exits
// with code. It also records the arguments it was given.
func fakeRailway(t *testing.T, body string, code int) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "railway")
	argsFile := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat <<'EOF'\n" + body + "\nEOF\nexit " + itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return "1"
}

func TestCLIClientReadsThroughTheRailwaySession(t *testing.T) {
	bin, argsFile := fakeRailway(t, `{"data":{"me":{"id":"u1"}}}`, 0)
	var out struct {
		Me struct {
			ID string `json:"id"`
		} `json:"me"`
	}
	if err := (&CLIClient{Binary: bin}).Query(context.Background(), "query Me { me { id } }", map[string]any{"a": "b"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Me.ID != "u1" {
		t.Fatalf("out = %+v", out)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(args)
	for _, want := range []string{"api", "--compact", "--variables", `{"a":"b"}`, "query Me"} {
		if !strings.Contains(got, want) {
			t.Fatalf("railway api was called without %q:\n%s", want, got)
		}
	}
	// The token never appears: the CLI carries its own session.
	if strings.Contains(strings.ToLower(got), "token") {
		t.Fatalf("the token must not be passed on the command line:\n%s", got)
	}
}

func TestCLIClientReportsGraphQLErrors(t *testing.T) {
	bin, _ := fakeRailway(t, `{"errors":[{"message":"Unauthorized"}]}`, 0)
	err := (&CLIClient{Binary: bin}).Query(context.Background(), "query Me { me { id } }", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Unauthorized") {
		t.Fatalf("want the API's message, got %v", err)
	}
}

func TestCLIClientNamesASignedOutSession(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "railway")
	script := "#!/bin/sh\necho 'not logged in, run railway login' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	err := (&CLIClient{Binary: bin}).Query(context.Background(), "query Me { me { id } }", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "railway login") {
		t.Fatalf("want a sign-in hint, got %v", err)
	}
}
