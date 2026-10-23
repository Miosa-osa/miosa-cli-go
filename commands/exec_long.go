package commands

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// A command longer than the synchronous route allows (300 s) streams instead:
// POST /sandboxes/:id/exec/stream answers server-sent events (stdout and stderr
// lines, then one exit event) for up to a day. A command that should outlive
// the CLI runs as a background process (--detach) whose output is kept; --attach
// prints it as it grows and exits with the process's code.

// execStreamMaxSeconds is the longest command the API runs (exec timeout 1..86400).
const execStreamMaxSeconds = 86400

// syncExecMaxSeconds is the longest command the synchronous route takes.
const syncExecMaxSeconds = 300

// execAttachPollEvery is how often --attach reads a process's log; tests
// shorten it.
var execAttachPollEvery = time.Second

// execFlags are exec's background and streaming options.
type execFlags struct {
	stream bool
	detach bool
	attach string
	status string
	kill   string
}

// wantsStream reports whether a command runs on the streaming route: asked
// for, unlimited, or longer than the synchronous route allows.
func (f execFlags) wantsStream(timeout int, explicit bool) bool {
	return f.stream || (explicit && (timeout == 0 || timeout > syncExecMaxSeconds))
}

// streamExec runs command on the streaming route, printing output as it comes,
// and returns the exit as an *ExitError (124 when the command ran out of time).
func streamExec(cmd *cobra.Command, c *api.Client, sandboxID, command, cwd string, timeout int) error {
	if timeout <= 0 || timeout > execStreamMaxSeconds {
		timeout = execStreamMaxSeconds
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	body := map[string]any{"command": command, "timeout": timeout}
	if cwd != "" {
		body["cwd"] = cwd
	}
	resp, err := c.Stream(ctx, api.Request{
		Method: http.MethodPost, Path: "/sandboxes/" + url.PathEscape(sandboxID) + "/exec/stream",
		Body: body, Headers: map[string]string{"Accept": "text/event-stream"}, NoRetry: true,
	})
	if err != nil {
		return die(err)
	}
	defer resp.Body.Close()
	p := printerFor(cmd)
	exit, sawExit := 0, false
	err = api.ReadSSE(ctx, resp.Body, func(ev api.Event) error {
		var v struct {
			Line     string `json:"line"`
			ExitCode *int   `json:"exit_code"`
			TimedOut bool   `json:"timed_out"`
		}
		_ = jsonUnmarshal(ev.Data, &v)
		switch ev.Name {
		case "stdout", "stderr":
			if isJSON() {
				return p.JSONLine(map[string]any{"stream": ev.Name, "line": v.Line})
			}
			w := cmd.OutOrStdout()
			if ev.Name == "stderr" {
				w = cmd.ErrOrStderr()
			}
			// The server sends chunks as the command writes them, newlines included.
			fmt.Fprint(w, v.Line)
		case "exit":
			sawExit = true
			switch {
			case v.TimedOut:
				exit = execTimeoutCode
			case v.ExitCode != nil:
				exit = *v.ExitCode
			}
			if isJSON() {
				return p.JSONLine(map[string]any{"event": "exit", "exit_code": exit, "timed_out": v.TimedOut})
			}
			if v.TimedOut {
				fmt.Fprintf(cmd.ErrOrStderr(), "miosa: the command did not finish within %ds\n", timeout)
			}
		}
		return nil
	})
	switch {
	case ctx.Err() != nil:
		fmt.Fprintln(cmd.ErrOrStderr(), "miosa: stopped following; the command may still be running in the sandbox")
		return &ExitError{Code: 130}
	case err != nil:
		return die(err)
	case !sawExit:
		return die(fmt.Errorf("the stream ended without the command's exit status"))
	case exit != 0:
		return &ExitError{Code: exit}
	}
	return nil
}

// sandboxProcess is a background process as the API reports it.
type sandboxProcess struct {
	ID       string `json:"id"`
	PID      any    `json:"pid"`
	Status   string `json:"status"`
	ExitCode *int   `json:"exit_code"`
	Command  string `json:"command"`
}

func processPath(sandboxID, id string) string {
	return "/sandboxes/" + url.PathEscape(sandboxID) + "/processes/" + url.PathEscape(id)
}

func getProcess(ctx context.Context, c *api.Client, sandboxID, id string) (sandboxProcess, error) {
	var out struct {
		Data sandboxProcess `json:"data"`
	}
	err := c.Get(ctx, processPath(sandboxID, id), nil, &out)
	return out.Data, err
}

// processRunning reports whether a process status means it has not ended.
func processRunning(status string) bool {
	switch status {
	case "running", "starting", "pending", "queued":
		return true
	}
	return false
}

// detachExec starts command as a background process and prints its id.
func detachExec(cmd *cobra.Command, c *api.Client, sandbox, sandboxID, command, cwd string) error {
	body := map[string]any{"command": command}
	if cwd != "" {
		body["cwd"] = cwd
	}
	var out struct {
		Data sandboxProcess `json:"data"`
	}
	if err := c.Post(cmd.Context(), "/sandboxes/"+url.PathEscape(sandboxID)+"/processes", body, &out); err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(out.Data)
	}
	fmt.Fprintln(cmd.OutOrStdout(), out.Data.ID)
	fmt.Fprintf(cmd.ErrOrStderr(), "Running in the background. Follow: miosa exec %s --attach %s   Stop: --kill %s\n", sandbox, out.Data.ID, out.Data.ID)
	return nil
}

// attachExec prints a background process's output as it grows and, once the
// process ends, exits with its code. Ctrl-C stops following, not the process.
func attachExec(cmd *cobra.Command, c *api.Client, sandboxID, id string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p := printerFor(cmd)
	var seen []string
	for {
		proc, err := getProcess(ctx, c, sandboxID, id)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return die(err)
		}
		var logs struct {
			Data struct {
				Lines []string `json:"lines"`
			} `json:"data"`
		}
		q := url.Values{"tail": {"1000"}}
		if err := c.Get(ctx, processPath(sandboxID, id)+"/logs", q, &logs); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return die(err)
		}
		for _, line := range newLogLines(seen, logs.Data.Lines) {
			if isJSON() {
				if err := p.JSONLine(map[string]any{"line": line}); err != nil {
					return err
				}
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
		}
		seen = logs.Data.Lines
		if !processRunning(proc.Status) {
			code := 0
			if proc.ExitCode != nil {
				code = *proc.ExitCode
			}
			if isJSON() {
				_ = p.JSONLine(map[string]any{"event": "exit", "status": proc.Status, "exit_code": code})
			}
			if code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			fmt.Fprintln(cmd.ErrOrStderr(), "miosa: stopped following; the process keeps running")
			return nil
		case <-time.After(execAttachPollEvery):
		}
	}
}

// statusExec prints a background process's state; it exits 0 whatever the
// process did, so a script reads the code from the output.
func statusExec(cmd *cobra.Command, c *api.Client, sandboxID, id string) error {
	proc, err := getProcess(cmd.Context(), c, sandboxID, id)
	if err != nil {
		return die(err)
	}
	p := printerFor(cmd)
	if isJSON() {
		return p.JSON(proc)
	}
	code := "-"
	if proc.ExitCode != nil {
		code = strconv.Itoa(*proc.ExitCode)
	}
	p.Fields([][2]string{{"Id", proc.ID}, {"Status", proc.Status}, {"Exit code", code}, {"Command", proc.Command}})
	return nil
}

func killExec(cmd *cobra.Command, c *api.Client, sandboxID, id string) error {
	if _, err := c.JSONAny(cmd.Context(), api.Request{Method: http.MethodDelete, Path: processPath(sandboxID, id)}); err != nil {
		return die(err)
	}
	if isJSON() {
		return printerFor(cmd).JSON(map[string]any{"killed": id})
	}
	printerFor(cmd).Success("Stopped %s", id)
	return nil
}
