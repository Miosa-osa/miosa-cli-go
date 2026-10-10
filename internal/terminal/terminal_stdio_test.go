package terminal

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.miosa.ai/api/v1":   "wss://api.miosa.ai/api/v1/sandboxes/x/ssh-tunnel",
		"http://127.0.0.1:4000/api/v1/": "ws://127.0.0.1:4000/api/v1/sandboxes/x/ssh-tunnel",
	} {
		got, err := WebSocketURL(in, "/sandboxes/x/ssh-tunnel")
		if err != nil || got != want {
			t.Errorf("WebSocketURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func tunnelServer(t *testing.T, handle func(*websocket.Conn)) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		handle(c)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestBridgeStdioCopiesBothWaysAndWaitsForTheServerAfterStdinEnds(t *testing.T) {
	url := tunnelServer(t, func(c *websocket.Conn) {
		for {
			mt, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			_ = c.WriteMessage(mt, bytes.ToUpper(msg))
		}
	})
	conn, err := DialStream(context.Background(), url, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var out bytes.Buffer
	if err := BridgeStdio(context.Background(), conn, strings.NewReader("ssh banner"), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "SSH BANNER" {
		t.Fatalf("out = %q", out.String())
	}
}

func TestBridgeStdioReportsAServerCloseCode(t *testing.T) {
	url := tunnelServer(t, func(c *websocket.Conn) {
		_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(4009, "sandbox not running"), time.Now().Add(time.Second))
	})
	conn, err := DialStream(context.Background(), url, "k")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = BridgeStdio(context.Background(), conn, blockingReader{}, &bytes.Buffer{})
	var ce *CloseError
	if !errors.As(err, &ce) || ce.Code != 4009 || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("err = %v", err)
	}
}

// blockingReader never returns, like a terminal nobody types into.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }
