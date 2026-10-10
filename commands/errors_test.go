package commands_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Miosa-osa/miosa-cli-go/commands"
	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

func TestExitCodesFollowTheAPIStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		code   int
		want   string
	}{
		{401, `{"error":"Token is invalid or malformed","code":"INVALID_TOKEN"}`, commands.ExitAuth, "INVALID_TOKEN"},
		{403, `{"error":{"code":"FORBIDDEN","message":"insufficient scope - requires agents:read"}}`, commands.ExitAuth, "agents:read"},
		{404, `{"error":{"code":"NOT_FOUND"}}`, commands.ExitNotFound, "NOT_FOUND"},
		{409, `{"error":{"code":"CONFLICT","message":"name taken"}}`, commands.ExitConflict, "name taken"},
		{422, `{"error":{"code":"VALIDATION_FAILED","message":"bad size"}}`, commands.ExitConflict, "VALIDATION_FAILED"},
		{429, `{"error":{"code":"RATE_LIMITED","message":"slow down"}}`, commands.ExitRetry, "RATE_LIMITED"},
		{500, `{"error":{"code":"INTERNAL","message":"boom"}}`, commands.ExitServer, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.body, func(t *testing.T) {
			fakeRoutes(t, map[string]any{"GET /sandboxes": resp{tc.status, tc.body}})
			_, err := run(t, "list")
			if err == nil {
				t.Fatal("want an error")
			}
			if got := commands.ExitCode(err); got != tc.code {
				t.Errorf("exit = %d, want %d (%v)", got, tc.code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("message %q lacks %q", err.Error(), tc.want)
			}
			if strings.Contains(err.Error(), "command failed") {
				t.Errorf("message still has the old filler: %q", err.Error())
			}
		})
	}
}

func TestErrorKeepsCodeAndRequestID(t *testing.T) {
	err := &api.Error{Status: 400, Code: "INVALID_PATH", Message: "invalid path", RequestID: "GNzv123"}
	text := commands.ErrorText(err, false)
	if text != "miosa: invalid path [INVALID_PATH] (request_id=GNzv123)\n" {
		t.Fatalf("text = %q", text)
	}
	var j struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Status    int    `json:"status"`
			RequestID string `json:"request_id"`
			ExitCode  int    `json:"exit_code"`
		} `json:"error"`
	}
	if e := json.Unmarshal([]byte(commands.ErrorText(err, true)), &j); e != nil {
		t.Fatal(e)
	}
	if j.Error.Code != "INVALID_PATH" || j.Error.Status != 400 || j.Error.RequestID != "GNzv123" || j.Error.ExitCode != commands.ExitConflict {
		t.Fatalf("json = %+v", j)
	}
}

func TestErrorHints(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&api.Error{Status: 401, Message: "Token is invalid"}, "miosa login"},
		{&api.Error{Status: 402, Message: "no credits"}, "billing"},
		{&api.Error{Status: 403, Message: "nope"}, "scope or role"},
		{&api.Error{Status: 429, Message: "slow", RetryAfter: 3e9}, "retry in 3s"},
		{&api.Error{Status: 400, Code: "INVALID_ID"}, "no sandbox has that name"},
	}
	for _, tc := range cases {
		if got := commands.ErrorText(tc.err, false); !strings.Contains(got, tc.want) {
			t.Errorf("%v: %q lacks %q", tc.err, got, tc.want)
		}
	}
	// A server-supplied scope message needs no extra hint.
	if got := commands.ErrorText(&api.Error{Status: 403, Message: "requires the 'agents:read' scope"}, false); strings.Contains(got, "may lack") {
		t.Errorf("redundant hint: %q", got)
	}
}

func TestTransportErrorExitsServer(t *testing.T) {
	err := &api.TransportError{Err: errors.New("connection refused")}
	if commands.ExitCode(err) != commands.ExitServer || !strings.Contains(commands.ErrorText(err, false), "cannot reach the MIOSA API") {
		t.Fatalf("exit=%d text=%q", commands.ExitCode(err), commands.ErrorText(err, false))
	}
}

func TestUsageErrorsExitTwo(t *testing.T) {
	fakeRoutes(t, nil)
	for _, args := range [][]string{{"bogus-command"}, {"list", "--bogus"}, {"exec"}, {"profile", "use"}} {
		_, err := run(t, args...)
		if err == nil {
			t.Errorf("%v: want error", args)
			continue
		}
		// exec with no args reaches RunE and is a real failure; the rest are cobra usage errors.
		if args[0] == "exec" {
			continue
		}
		if commands.ExitCode(err) != commands.ExitUsage {
			t.Errorf("%v: exit = %d (%v)", args, commands.ExitCode(err), err)
		}
	}
}

func TestRemoteExitCodePassesThrough(t *testing.T) {
	if got := commands.ExitCode(&commands.ExitError{Code: 7}); got != 7 {
		t.Fatalf("got %d", got)
	}
	if commands.ErrorText(&commands.ExitError{Code: 7}, false) != "" {
		t.Fatal("a remote exit prints nothing of its own")
	}
	if commands.ExitCode(nil) != 0 {
		t.Fatal("nil is success")
	}
}

func TestSignedOutCommandsFailBeforeAnyRequest(t *testing.T) {
	f := fakeRoutes(t, nil)
	t.Setenv("MIOSA_API_KEY", "")
	_, err := run(t, "list")
	if err == nil || commands.ExitCode(err) != commands.ExitAuth {
		t.Fatalf("err = %v", err)
	}
	if f.count() != 0 {
		t.Fatalf("a signed-out command must not call the API: %+v", f.calls)
	}
}

func TestRetriesOnTransientFailure(t *testing.T) {
	n := 0
	f := newFakeAPI(t, func(c call) (int, string) {
		n++
		if n == 1 {
			return 503, `{"error":{"code":"UNAVAILABLE","message":"try again"}}`
		}
		return 200, `{"data":[]}`
	})
	t.Setenv("MIOSA_RETRIES", "2")
	out, err := run(t, "list", "--retries", "2")
	if err != nil {
		t.Fatalf("list should succeed after a retry: %v\n%s", err, out)
	}
	if f.count() != 2 {
		t.Fatalf("calls = %d, want 2", f.count())
	}
}

func TestJSONFlagIsOutputJSON(t *testing.T) {
	fakeRoutes(t, map[string]any{"GET /sandboxes": `{"data":[{"id":"8ba07e95-5085-4600-b758-60b6951c5786","name":"a","state":"running","template_id":"t","size":"small","created_at":"2026-10-09T00:00:00Z"}],"meta":{"total":1,"page":1}}`})
	for _, args := range [][]string{{"list", "--json"}, {"list", "-o", "json"}} {
		out, err := run(t, args...)
		if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "{") || !strings.Contains(out, `"data"`) {
			t.Errorf("%v: %v %s", args, err, out)
		}
	}
	t.Setenv("MIOSA_OUTPUT", "json")
	out, err := run(t, "list")
	if err != nil || !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("MIOSA_OUTPUT=json ignored: %v %s", err, out)
	}
	out, err = run(t, "list", "-o", "text")
	if err != nil || strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("explicit -o text must win over MIOSA_OUTPUT: %v %s", err, out)
	}
}
