package miosa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// ExecService runs commands on a computer, supporting both one-shot execution
// and interactive PTY sessions via WebSocket.
// Accessed via Computer.Exec.
type ExecService struct {
	client     *Client
	computerID string
}

func (s *ExecService) base() string {
	return fmt.Sprintf("/computers/%s/exec", s.computerID)
}

// Bash runs a shell command synchronously and returns the combined output.
func (s *ExecService) Bash(ctx context.Context, command string, timeoutSecs ...int) (*ExecResult, error) {
	const op = "ExecService.Bash"
	input := ExecInput{Command: command}
	if len(timeoutSecs) > 0 {
		input.Timeout = timeoutSecs[0]
	}
	var out ExecResult
	if err := s.client.postJSON(ctx, s.base(), input, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}

// Python runs Python code synchronously and returns the combined output.
func (s *ExecService) Python(ctx context.Context, code string, timeoutSecs ...int) (*ExecResult, error) {
	const op = "ExecService.Python"
	input := ExecPythonInput{Code: code}
	if len(timeoutSecs) > 0 {
		input.Timeout = timeoutSecs[0]
	}
	var out ExecResult
	if err := s.client.postJSON(ctx, s.base()+"/python", input, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out, nil
}

// SpawnOptions configures an interactive PTY session.
type SpawnOptions struct {
	// Rows is the initial terminal height in lines (default 24).
	Rows uint16
	// Cols is the initial terminal width in columns (default 80).
	Cols uint16
	// Env holds additional environment variables for the spawned process.
	Env map[string]string
}

// Spawn opens an interactive PTY session for command over WebSocket.
// Returns a *Cmd that mirrors the os/exec.Cmd shape: read Stdout/Stderr,
// write to Stdin, resize the terminal, and wait for exit.
//
//	cmd, err := computer.Exec.Spawn(ctx, "bash", SpawnOptions{Rows: 40, Cols: 120})
//	if err != nil { ... }
//	defer cmd.Kill()
//	io.WriteString(cmd.Stdin, "ls -la\n")
//	exit, _ := cmd.Wait()
func (s *ExecService) Spawn(ctx context.Context, command string, opts SpawnOptions) (*Cmd, error) {
	const op = "ExecService.Spawn"

	wsURL := buildExecWSURL(s.client.baseURL, s.computerID, command, opts)
	header := http.Header{
		"Authorization": []string{"Bearer " + s.client.apiKey},
		"User-Agent":    []string{"miosa-go/" + sdkVersion},
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()

	cmd := &Cmd{
		Stdin:  stdinW,
		Stdout: stdoutR,
		Stderr: stderrR,
		conn:   conn,
		done:   make(chan int, 1),
	}

	// stdin pump: read from stdinR and send as binary frames.
	go func() {
		defer stdinR.Close()
		buf := make([]byte, 4096)
		for {
			n, err := stdinR.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// read pump: demux stdout / stderr / control frames.
	go func() {
		defer stdoutW.Close()
		defer stderrW.Close()
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				cmd.done <- -1
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				// Convention: first byte is stream selector (1=stdout, 2=stderr).
				if len(msg) == 0 {
					continue
				}
				switch msg[0] {
				case 2:
					stderrW.Write(msg[1:])
				default:
					stdoutW.Write(msg[1:])
				}
			case websocket.TextMessage:
				// Text frames carry JSON control messages (exit code, port notifications).
				var ctrl struct {
					Type    string          `json:"type"`
					Exit    *int            `json:"exit"`
					Payload json.RawMessage `json:"payload"`
				}
				if err := json.Unmarshal(msg, &ctrl); err != nil {
					continue
				}
				switch ctrl.Type {
				case "exit":
					code := 0
					if ctrl.Exit != nil {
						code = *ctrl.Exit
					}
					cmd.done <- code
					return
				case "port":
					if cmd.TextMessageHandler != nil {
						cmd.TextMessageHandler(msg)
					}
				}
			}
		}
	}()

	return cmd, nil
}

// buildExecWSURL constructs the WebSocket URL for an interactive session.
func buildExecWSURL(baseURL, computerID, command string, opts SpawnOptions) string {
	wsBase := strings.Replace(baseURL, "https://", "wss://", 1)
	wsBase = strings.Replace(wsBase, "http://", "ws://", 1)

	rows := opts.Rows
	if rows == 0 {
		rows = 24
	}
	cols := opts.Cols
	if cols == 0 {
		cols = 80
	}

	return fmt.Sprintf("%s/computers/%s/exec/spawn?command=%s&rows=%d&cols=%d",
		wsBase, computerID,
		encodeQueryParam(command), rows, cols)
}

func encodeQueryParam(s string) string {
	return strings.NewReplacer(
		" ", "%20", "/", "%2F", "?", "%3F", "&", "%26", "=", "%3D",
	).Replace(s)
}

// ─── Cmd ─────────────────────────────────────────────────────────────────────

// Cmd represents a running interactive PTY session.
// It mirrors the shape of os/exec.Cmd with WebSocket-backed I/O.
type Cmd struct {
	// Stdin writes data to the PTY's stdin.
	Stdin io.Writer
	// Stdout reads data from the PTY's stdout.
	Stdout io.Reader
	// Stderr reads data from the PTY's stderr.
	Stderr io.Reader
	// TextMessageHandler, if set, is called for each text-frame control message
	// (e.g. port-ready notifications).
	TextMessageHandler func([]byte)

	conn *websocket.Conn
	done chan int
}

// Wait blocks until the remote process exits and returns its exit code.
// Returns -1 if the connection was lost before an exit code was received.
func (c *Cmd) Wait() (int, error) {
	code := <-c.done
	return code, nil
}

// Resize sends a terminal resize event to the PTY.
func (c *Cmd) Resize(rows, cols uint16) error {
	msg, err := json.Marshal(map[string]interface{}{
		"type": "resize",
		"rows": rows,
		"cols": cols,
	})
	if err != nil {
		return fmt.Errorf("Cmd.Resize: %w", err)
	}
	return c.conn.WriteMessage(websocket.TextMessage, msg)
}

// Kill closes the WebSocket connection, terminating the remote process.
func (c *Cmd) Kill() error {
	return c.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "killed"))
}
