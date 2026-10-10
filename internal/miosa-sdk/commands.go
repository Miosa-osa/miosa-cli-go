package miosa

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Commands run inside a sandbox through POST /sandboxes/:id/exec
// (miosa-compute docs/api/openapi.yaml, operation execInSandbox), which takes
// {command, timeout} and answers {data: {stdout, stderr, exit_code}}.
//
// The exec endpoint has no working-directory or stdin parameter, so both are
// composed into the shell command here. If the API later grows first-class
// fields (a dedicated /commands endpoint), this file is the only place to
// change.

// Limits of the exec endpoint.
const (
	// MinCommandTimeoutSeconds and MaxCommandTimeoutSeconds bound the
	// synchronous timeout the API accepts.
	MinCommandTimeoutSeconds = 1
	MaxCommandTimeoutSeconds = 300
	// DefaultCommandTimeoutSeconds is what the API applies when none is sent.
	DefaultCommandTimeoutSeconds = 30
	// MaxStdinBytes caps stdin that is carried inside the command line. A
	// single Linux argument is limited to 128 KiB; base64 inflates by 4/3.
	MaxStdinBytes = 80 * 1024
)

// CommandInput describes one command.
type CommandInput struct {
	// Command is run by a shell.
	Command string
	// Cwd is the working directory: relative to the home directory, or absolute.
	Cwd string
	// TimeoutSeconds is the synchronous timeout; 0 uses the API default.
	TimeoutSeconds int
	// Stdin, when non-nil, is piped to the command's standard input.
	Stdin []byte
}

// CommandResult is the outcome of a synchronous command.
type CommandResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Output   string `json:"output,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// Validate checks the input without sending anything.
func (in CommandInput) Validate() error {
	if strings.TrimSpace(in.Command) == "" {
		return errors.New("command is required")
	}
	if in.TimeoutSeconds != 0 && (in.TimeoutSeconds < MinCommandTimeoutSeconds || in.TimeoutSeconds > MaxCommandTimeoutSeconds) {
		return fmt.Errorf("timeout must be between %d and %d seconds", MinCommandTimeoutSeconds, MaxCommandTimeoutSeconds)
	}
	if len(in.Stdin) > MaxStdinBytes {
		return fmt.Errorf("stdin is %d bytes; the limit is %d. Copy the file with 'miosa files cp' and run it instead", len(in.Stdin), MaxStdinBytes)
	}
	if strings.ContainsRune(in.Cwd, 0) {
		return errors.New("cwd contains a NUL byte")
	}
	return nil
}

// ShellCommand composes the exact shell line sent to the API: the working
// directory change and the stdin pipe wrapped around Command.
func (in CommandInput) ShellCommand() string {
	cmd := in.Command
	if in.Stdin != nil {
		enc := base64.StdEncoding.EncodeToString(in.Stdin)
		// printf of base64 is shell-safe; the decoded bytes become the command's stdin.
		cmd = "printf %s '" + enc + "' | base64 -d | { " + cmd + "\n}"
	}
	if in.Cwd != "" {
		cmd = "cd " + cwdArg(in.Cwd) + " && " + cmd
	}
	return cmd
}

// cwdArg quotes a working directory. Relative paths hang off $HOME.
func cwdArg(cwd string) string {
	if strings.HasPrefix(cwd, "/") {
		return shellQuote(cwd)
	}
	if cwd == "~" {
		return `"$HOME"`
	}
	cwd = strings.TrimPrefix(cwd, "~/")
	return `"$HOME"/` + shellQuote(cwd)
}

// ShellQuote single-quotes s for a POSIX shell.
func ShellQuote(s string) string { return shellQuote(s) }

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CommandsService runs commands inside a sandbox. Accessed via Client.Commands.
type CommandsService struct {
	client *Client

	// Backoff overrides the wait before retry n (0-based) while the sandbox is
	// starting. Nil uses DefaultStartingBackoff. Tests set it to avoid sleeping.
	Backoff func(attempt int) time.Duration
	// MaxStartingWait bounds the total time spent retrying a "starting"
	// refusal. Zero means DefaultMaxStartingWait.
	MaxStartingWait time.Duration
}

// DefaultMaxStartingWait is how long Run keeps retrying a "starting" refusal.
const DefaultMaxStartingWait = 90 * time.Second

// DefaultStartingBackoff is 500ms doubling up to 8s.
func DefaultStartingBackoff(attempt int) time.Duration {
	if attempt > 4 {
		return 8 * time.Second
	}
	return 500 * time.Millisecond << uint(attempt)
}

// Run executes a command and waits for it. While the sandbox is still
// starting, the API refuses; Run backs off and retries until MaxStartingWait
// elapses or ctx is cancelled. Any other error is returned at once.
func (s *CommandsService) Run(ctx context.Context, sandboxID string, in CommandInput) (*CommandResult, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	body := map[string]interface{}{"command": in.ShellCommand()}
	if in.TimeoutSeconds > 0 {
		body["timeout"] = in.TimeoutSeconds
	}
	maxWait := s.MaxStartingWait
	if maxWait <= 0 {
		maxWait = DefaultMaxStartingWait
	}
	backoff := s.Backoff
	if backoff == nil {
		backoff = DefaultStartingBackoff
	}
	deadline := time.Now().Add(maxWait)
	path := "/sandboxes/" + url.PathEscape(sandboxID) + "/exec"

	for attempt := 0; ; attempt++ {
		var out struct {
			Data *CommandResult `json:"data"`
			CommandResult
		}
		err := s.client.postJSON(ctx, path, body, &out)
		if err == nil {
			if out.Data != nil {
				return out.Data, nil
			}
			res := out.CommandResult
			return &res, nil
		}
		if !s.stillStarting(ctx, sandboxID, err) {
			return nil, err
		}
		wait := backoff(attempt)
		if time.Now().Add(wait).After(deadline) {
			return nil, fmt.Errorf("sandbox %s is still starting after %s: %w", sandboxID, maxWait, err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
}

// stillStarting reports whether err means "retry shortly": an explicit
// starting refusal, or the 409 "not running" refusal while the sandbox state
// is still creating/starting.
func (s *CommandsService) stillStarting(ctx context.Context, sandboxID string, err error) bool {
	if IsStarting(err) {
		return true
	}
	var base *MiosaError
	if !errors.As(err, &base) || base.StatusCode != http.StatusConflict {
		return false
	}
	sb, gerr := s.client.Sandboxes.Get(ctx, sandboxID)
	if gerr != nil {
		return false
	}
	switch sb.State {
	case StatusCreating, StatusStarting, StatusProvisioning:
		return true
	}
	return false
}
