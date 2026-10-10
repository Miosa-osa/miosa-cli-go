package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{Subprotocols: []string{Subprotocol}, CheckOrigin: func(*http.Request) bool { return true }}

type fakeTerm struct {
	srv    *httptest.Server
	mu     sync.Mutex
	stdin  bytes.Buffer
	texts  []string
	auth   string
	proto  string
	closed chan struct{}
}

// newFakeTerm serves a terminal that echoes stdin back upper-cased, then runs
// script (if any) against the connection.
func newFakeTerm(t *testing.T, script func(c *websocket.Conn)) *fakeTerm {
	t.Helper()
	f := &fakeTerm{closed: make(chan struct{})}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.auth = r.Header.Get("Authorization")
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer close(f.closed)
		f.proto = c.Subprotocol()
		if script != nil {
			script(c)
			return
		}
		for {
			typ, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			f.mu.Lock()
			if typ == websocket.TextMessage {
				f.texts = append(f.texts, string(data))
			} else {
				f.stdin.Write(data)
				c.WriteMessage(websocket.BinaryMessage, []byte(strings.ToUpper(string(data))))
			}
			f.mu.Unlock()
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTerm) session() Session {
	return Session{SessionID: "s1", WSURL: "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/terminal/stream"}
}

func TestDialSendsKeyAndSubprotocol(t *testing.T) {
	f := newFakeTerm(t, func(c *websocket.Conn) { c.Close() })
	conn, err := Dial(context.Background(), f.session(), "msk_u_secret")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	<-f.closed
	if f.auth != "Bearer msk_u_secret" || f.proto != Subprotocol {
		t.Fatalf("auth=%q proto=%q", f.auth, f.proto)
	}
}

func TestDialErrors(t *testing.T) {
	if _, err := Dial(context.Background(), Session{}, "k"); err == nil {
		t.Fatal("empty URL must fail")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusForbidden) }))
	defer srv.Close()
	_, err := Dial(context.Background(), Session{WSURL: "ws" + strings.TrimPrefix(srv.URL, "http")}, "k")
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestBridgeCopiesStdinAndOutputAndReturnsExitCode(t *testing.T) {
	f := newFakeTerm(t, func(c *websocket.Conn) {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		c.WriteMessage(websocket.BinaryMessage, append([]byte("got:"), data...))
		c.WriteMessage(websocket.TextMessage, []byte(`{"type":"exit","exit_code":7}`))
		c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	})
	conn, err := Dial(context.Background(), f.session(), "k")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	res, err := Bridge(context.Background(), conn, strings.NewReader("ls\n"), &out, make(chan Size))
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "got:ls\n" || res.ExitCode != 7 {
		t.Fatalf("out=%q exit=%d", out.String(), res.ExitCode)
	}
}

func TestBridgeForwardsResizeFrames(t *testing.T) {
	f := newFakeTerm(t, nil)
	conn, err := Dial(context.Background(), f.session(), "k")
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe()
	resizes := make(chan Size, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var out bytes.Buffer
	var outMu sync.Mutex
	go func() {
		Bridge(ctx, conn, pr, &lockedWriter{&out, &outMu}, resizes)
		close(done)
	}()
	resizes <- Size{Cols: 120, Rows: 40}
	pw.Write([]byte("abc"))
	deadline := time.After(3 * time.Second)
	for {
		f.mu.Lock()
		n, in := len(f.texts), f.stdin.String()
		f.mu.Unlock()
		if n == 1 && in == "abc" {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("texts=%v stdin=%q", f.texts, in)
		case <-time.After(10 * time.Millisecond):
		}
	}
	var frame map[string]any
	json.Unmarshal([]byte(f.texts[0]), &frame)
	if frame["type"] != "resize" || frame["cols"] != float64(120) || frame["rows"] != float64(40) {
		t.Fatalf("frame = %v", frame)
	}
	cancel()
	<-done
	pw.Close()
	outMu.Lock()
	defer outMu.Unlock()
	if out.String() != "ABC" {
		t.Fatalf("echo = %q", out.String())
	}
}

type lockedWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func TestBridgeMapsServerCloseCodes(t *testing.T) {
	cases := []struct {
		code int
		want string
	}{
		{4009, "not running"},
		{4013, "too many open terminals"},
		{4001, "authentication failed"},
		{1011, "failed upstream"},
		{4999, "terminal closed (4999)"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			f := newFakeTerm(t, func(c *websocket.Conn) {
				c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(tc.code, "bye"))
			})
			conn, err := Dial(context.Background(), f.session(), "k")
			if err != nil {
				t.Fatal(err)
			}
			_, err = Bridge(context.Background(), conn, strings.NewReader(""), io.Discard, nil)
			var ce *CloseError
			if !errors.As(err, &ce) || ce.Code != tc.code || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestBridgeNormalCloseIsNotAnError(t *testing.T) {
	f := newFakeTerm(t, func(c *websocket.Conn) {
		c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	})
	conn, _ := Dial(context.Background(), f.session(), "k")
	res, err := Bridge(context.Background(), conn, strings.NewReader(""), io.Discard, nil)
	if err != nil || !res.Closed || res.ExitCode != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
