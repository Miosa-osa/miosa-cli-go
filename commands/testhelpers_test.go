package commands_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
)

// resp is a canned reply with an explicit status.
type resp struct {
	Status int
	Body   string
}

// routeReply serves a route table for newFakeAPI. Keys are "METHOD /path".
// A string value is a 200 JSON body; a resp sets the status. Anything not in
// the table is a 404.
func routeReply(routes map[string]any) func(c call) (int, string) {
	return func(c call) (int, string) {
		v, ok := routes[c.Method+" "+c.Path]
		if !ok {
			return http.StatusNotFound, `{"error":{"code":"NOT_FOUND","message":"no route in test"}}`
		}
		switch t := v.(type) {
		case string:
			return http.StatusOK, t
		case resp:
			return t.Status, t.Body
		}
		return http.StatusInternalServerError, `{"error":{"code":"TEST_BUG"}}`
	}
}

// fakeRoutes starts a fake API for a route table and points the CLI at it.
func fakeRoutes(t *testing.T, routes map[string]any) *fakeAPI {
	t.Helper()
	return newFakeAPI(t, routeReply(routes))
}

// writeConfigFile writes raw TOML as the config file under the test HOME.
func writeConfigFile(t *testing.T, toml string) string {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".miosa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// plainServer is a throwaway httptest server for tests that need headers.
func plainServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

// runSplit is run with stdout and stderr captured separately.
func runSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	commands.ResetForTest()
	root := commands.Root()
	var o, e bytes.Buffer
	root.SetOut(&o)
	root.SetErr(&e)
	root.SetArgs(args)
	err = root.Execute()
	return o.String(), e.String(), err
}

// runSplitIn is runSplit with a stdin.
func runSplitIn(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	commands.ResetForTest()
	root := commands.Root()
	var o, e bytes.Buffer
	root.SetOut(&o)
	root.SetErr(&e)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	t.Cleanup(func() { root.SetIn(nil) })
	err = root.Execute()
	return o.String(), e.String(), err
}
