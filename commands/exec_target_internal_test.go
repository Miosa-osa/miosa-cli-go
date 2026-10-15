package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

const execTestComputer = "78aa09c5-d7a4-450f-b765-7b4b4ce96c8a"

func execTargetServer(t *testing.T) *client.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sandboxes/by-name/my-box":
			fmt.Fprint(w, `{"data":{"id":"s1","name":"my-box"}}`)
		case r.URL.Path == "/computers":
			fmt.Fprint(w, `{"total":1,"computers":[{"id":"`+execTestComputer+`","name":"my-desktop"}]}`)
		case r.URL.Path == "/computers/"+execTestComputer:
			fmt.Fprint(w, `{"id":"`+execTestComputer+`","name":"my-desktop"}`)
		default:
			// Anything else 404s, like the real API for a name it does not have.
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":"NOT_FOUND"}}`)
		}
	}))
	t.Cleanup(srv.Close)
	return &client.Client{API: api.New(srv.URL, "k"), BaseURL: srv.URL}
}

// 'miosa exec' runs on a computer as well as a sandbox. A sandbox name keeps
// the sandbox route, a computer name, short id and full id pick the computer
// route, and a reference neither knows is left to the sandbox route as before.
func TestExecTargetPicksTheRightMachine(t *testing.T) {
	c := execTargetServer(t)
	// A command built by hand has no context; cobra sets one during Execute.
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())

	cases := []struct {
		ref        string
		wantID     string
		wantIsComp bool
	}{
		{"my-box", "s1", false},
		{"my-desktop", execTestComputer, true},
		{"78aa09c5", execTestComputer, true},
		{execTestComputer, execTestComputer, true},
		{"ffffffff-ffff-4fff-8fff-ffffffffffff", "ffffffff-ffff-4fff-8fff-ffffffffffff", false},
		{"nothing-here", "nothing-here", false},
	}
	for _, tc := range cases {
		id, isComputer := resolveExecTarget(cmd, c, tc.ref)
		if id != tc.wantID || isComputer != tc.wantIsComp {
			t.Errorf("resolveExecTarget(%q) = %q, computer=%v; want %q, computer=%v",
				tc.ref, id, isComputer, tc.wantID, tc.wantIsComp)
		}
	}
}
