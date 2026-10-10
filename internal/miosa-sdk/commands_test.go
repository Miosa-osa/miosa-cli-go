package miosa

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fastBackoff(int) time.Duration { return time.Millisecond }

func TestShellCommandComposition(t *testing.T) {
	tests := []struct {
		name string
		in   CommandInput
		want string
	}{
		{"plain", CommandInput{Command: "ls"}, "ls"},
		{"relative cwd", CommandInput{Command: "ls", Cwd: "my repo"}, `cd "$HOME"/'my repo' && ls`},
		{"absolute cwd", CommandInput{Command: "ls", Cwd: "/srv/app"}, `cd '/srv/app' && ls`},
		{"cwd quote", CommandInput{Command: "ls", Cwd: "it's"}, `cd "$HOME"/'it'\''s' && ls`},
		{"stdin", CommandInput{Command: "bash -s", Stdin: []byte("echo hi\n")},
			"printf %s '" + base64.StdEncoding.EncodeToString([]byte("echo hi\n")) + "' | base64 -d | { bash -s\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.ShellCommand(); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestCommandInputValidate(t *testing.T) {
	bad := []CommandInput{
		{Command: ""},
		{Command: "  "},
		{Command: "x", TimeoutSeconds: -1},
		{Command: "x", TimeoutSeconds: 301},
		{Command: "x", Stdin: make([]byte, MaxStdinBytes+1)},
	}
	for i, in := range bad {
		if in.Validate() == nil {
			t.Errorf("case %d should be invalid", i)
		}
	}
	good := []CommandInput{{Command: "x"}, {Command: "x", TimeoutSeconds: 1}, {Command: "x", TimeoutSeconds: 300}, {Command: "x", Stdin: make([]byte, MaxStdinBytes)}}
	for i, in := range good {
		if err := in.Validate(); err != nil {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestRunSendsCommandAndTimeout(t *testing.T) {
	var got map[string]interface{}
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"data":{"stdout":"ok","stderr":"","exit_code":3}}`))
	}))
	defer srv.Close()
	c := NewClient("k", WithBaseURL(srv.URL))
	res, err := c.Commands.Run(context.Background(), "sb1", CommandInput{Command: "make", Cwd: "app", TimeoutSeconds: 120})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/sandboxes/sb1/exec" || got["command"] != `cd "$HOME"/'app' && make` || got["timeout"] != float64(120) {
		t.Fatalf("path=%s body=%v", path, got)
	}
	if res.Stdout != "ok" || res.ExitCode != 3 {
		t.Fatalf("res=%+v", res)
	}
}

func TestRunRetriesWhileStarting(t *testing.T) {
	cases := []struct {
		name  string
		first func(w http.ResponseWriter)
	}{
		{"explicit starting code", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"SANDBOX_STARTING","message":"sandbox is starting"}}`))
		}},
		{"not running while state is starting", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"SANDBOX_NOT_RUNNING","message":"sandbox must be in running state to exec"}}`))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var execCalls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet:
					_, _ = w.Write([]byte(`{"id":"sb1","state":"starting"}`))
				case atomic.AddInt32(&execCalls, 1) < 3:
					tc.first(w)
				default:
					_, _ = w.Write([]byte(`{"data":{"stdout":"up","exit_code":0}}`))
				}
			}))
			defer srv.Close()
			c := NewClient("k", WithBaseURL(srv.URL), WithMaxRetries(0))
			c.Commands.Backoff = fastBackoff
			res, err := c.Commands.Run(context.Background(), "sb1", CommandInput{Command: "true"})
			if err != nil {
				t.Fatal(err)
			}
			if res.Stdout != "up" || atomic.LoadInt32(&execCalls) != 3 {
				t.Fatalf("res=%+v calls=%d", res, execCalls)
			}
		})
	}
}

func TestRunDoesNotRetryWhenStopped(t *testing.T) {
	var execCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"id":"sb1","state":"stopped"}`))
			return
		}
		atomic.AddInt32(&execCalls, 1)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"SANDBOX_NOT_RUNNING","message":"sandbox must be in running state to exec"}}`))
	}))
	defer srv.Close()
	c := NewClient("k", WithBaseURL(srv.URL), WithMaxRetries(0))
	c.Commands.Backoff = fastBackoff
	_, err := c.Commands.Run(context.Background(), "sb1", CommandInput{Command: "true"})
	if err == nil || atomic.LoadInt32(&execCalls) != 1 {
		t.Fatalf("a stopped sandbox must fail at once: err=%v calls=%d", err, execCalls)
	}
}

func TestRunGivesUpAfterMaxWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"BOAT_STARTING","message":"still starting"}}`))
	}))
	defer srv.Close()
	c := NewClient("k", WithBaseURL(srv.URL), WithMaxRetries(0))
	c.Commands.Backoff = func(int) time.Duration { return 20 * time.Millisecond }
	c.Commands.MaxStartingWait = 100 * time.Millisecond
	start := time.Now()
	_, err := c.Commands.Run(context.Background(), "sb1", CommandInput{Command: "true"})
	if err == nil || !strings.Contains(err.Error(), "still starting") {
		t.Fatalf("want give-up error, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("did not stop retrying")
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"SANDBOX_STARTING","message":"starting"}}`))
	}))
	defer srv.Close()
	c := NewClient("k", WithBaseURL(srv.URL), WithMaxRetries(0))
	c.Commands.Backoff = func(int) time.Duration { return time.Hour }
	c.Commands.MaxStartingWait = 10 * time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Commands.Run(ctx, "sb1", CommandInput{Command: "true"}); err == nil {
		t.Fatal("want context error")
	}
}

func TestDefaultStartingBackoff(t *testing.T) {
	want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}
	for i, w := range want {
		if got := DefaultStartingBackoff(i); got != w {
			t.Errorf("attempt %d: got %v want %v", i, got, w)
		}
	}
}

func TestIsStarting(t *testing.T) {
	mk := func(status int, code, msg string) error {
		return &MiosaError{StatusCode: status, Code: code, Message: msg}
	}
	if !IsStarting(mk(409, "SANDBOX_STARTING", "x")) || !IsStarting(mk(503, "", "machine is starting")) || !IsStarting(&ServerError{MiosaError{StatusCode: 503, Code: "boat_starting"}}) {
		t.Error("starting refusals must match")
	}
	if IsStarting(mk(409, "SANDBOX_NOT_RUNNING", "must be running")) || IsStarting(mk(404, "", "starting")) || IsStarting(nil) {
		t.Error("non-starting errors must not match")
	}
}

func TestNestedErrorMessage(t *testing.T) {
	if got := extractMessage([]byte(`{"error":{"code":"X","message":"nested text"}}`), 422); got != "nested text" {
		t.Fatalf("got %q", got)
	}
	if got := extractMessage([]byte(`{"error":"flat text"}`), 400); got != "flat text" {
		t.Fatalf("got %q", got)
	}
}
