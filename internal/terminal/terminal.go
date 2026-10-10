// Package terminal speaks the miosa-terminal-v1 WebSocket protocol:
// binary frames carry stdin and PTY output, a text frame {"type":"resize"}
// resizes the PTY, and the server ends the session with {"type":"exit"}.
package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Subprotocol is the WebSocket subprotocol the server requires.
const Subprotocol = "miosa-terminal-v1"

// Session is what POST /sandboxes/:id/terminal returns.
type Session struct {
	SessionID  string `json:"session_id"`
	WSURL      string `json:"ws_url"`
	StreamAuth string `json:"stream_auth"`
	ExpiresAt  int64  `json:"expires_at"`
}

// Size is a terminal size in cells.
type Size struct{ Cols, Rows int }

// Dial opens the terminal stream. It authenticates with the API key in the
// Authorization header, as the protocol specifies for non-browser clients.
func Dial(ctx context.Context, s Session, apiKey string) (*websocket.Conn, error) {
	if s.WSURL == "" {
		return nil, errors.New("the server returned no terminal URL")
	}
	d := websocket.Dialer{
		Subprotocols:     []string{Subprotocol},
		HandshakeTimeout: 20 * time.Second,
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+apiKey)
	conn, resp, err := d.DialContext(ctx, s.WSURL, hdr)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("terminal handshake failed: %s", resp.Status)
		}
		return nil, fmt.Errorf("terminal connection failed: %w", err)
	}
	return conn, nil
}

// Result is how a session ended.
type Result struct {
	ExitCode int
	// Closed is true when the server closed the stream without an exit frame.
	Closed bool
}

// CloseError carries a server close code the user can act on.
type CloseError struct {
	Code int
	Text string
}

func (e *CloseError) Error() string {
	switch e.Code {
	case 4001:
		return "terminal authentication failed"
	case 4003:
		return "terminal access is forbidden for this key"
	case 4004:
		return "sandbox not found"
	case 4000:
		return "that is not a valid port"
	case 4005:
		return "connection refused: nothing is listening on that port in the sandbox"
	case 4006:
		return "connecting to that port timed out"
	case 4008:
		return "the session reached its time limit"
	case 4009:
		return "sandbox is not running (try 'miosa resume')"
	case 4010:
		return "tunnels work for sandboxes, not computers"
	case 4011:
		return "that port is not allowed (the sandbox agent's own port cannot be forwarded)"
	case 4013:
		return "too many open terminals for this organization"
	case 1011:
		return "the terminal failed upstream; try again"
	}
	if e.Text != "" {
		return fmt.Sprintf("terminal closed (%d): %s", e.Code, e.Text)
	}
	return fmt.Sprintf("terminal closed (%d)", e.Code)
}

// Bridge copies stdin to the terminal and terminal output to out until the
// session ends, ctx is cancelled, or in reaches EOF. Resize events from
// resizes are forwarded as resize frames. On a clean end it sends a normal
// close so the server removes the PTY.
func Bridge(ctx context.Context, conn *websocket.Conn, in io.Reader, out io.Writer, resizes <-chan Size) (Result, error) {
	var (
		wmu  sync.Mutex
		res  Result
		done = make(chan error, 3)
	)
	write := func(typ int, data []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		return conn.WriteMessage(typ, data)
	}

	// stdin -> terminal
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				if werr := write(websocket.BinaryMessage, append([]byte(nil), buf[:n]...)); werr != nil {
					done <- nil
					return
				}
			}
			if err != nil {
				// Input ended (a pipe or Ctrl-D with no PTY echo): keep reading
				// output until the shell exits on its own.
				return
			}
		}
	}()

	// resizes -> terminal
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case sz, ok := <-resizes:
				if !ok {
					return
				}
				b, _ := json.Marshal(map[string]any{"type": "resize", "cols": sz.Cols, "rows": sz.Rows})
				if write(websocket.TextMessage, b) != nil {
					return
				}
			}
		}
	}()

	// terminal -> stdout
	go func() {
		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					if ce.Code == websocket.CloseNormalClosure || ce.Code == websocket.CloseNoStatusReceived {
						res.Closed = true
						done <- nil
						return
					}
					done <- &CloseError{Code: ce.Code, Text: ce.Text}
					return
				}
				done <- err
				return
			}
			switch typ {
			case websocket.BinaryMessage:
				if _, werr := out.Write(data); werr != nil {
					done <- werr
					return
				}
			case websocket.TextMessage:
				var ev struct {
					Type     string `json:"type"`
					ExitCode int    `json:"exit_code"`
				}
				if json.Unmarshal(data, &ev) == nil && ev.Type == "exit" {
					res.ExitCode = ev.ExitCode
					done <- nil
					return
				}
			}
		}
	}()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	// Normal close: the server deletes the PTY and its process.
	_ = write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = conn.Close()
	return res, err
}
