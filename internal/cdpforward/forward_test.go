package cdpforward

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeCDPHost stands in for the cdp host: it answers CDP discovery with
// webSocketDebuggerUrl pointing at its own /devtools/* sockets and echoes any
// message sent on them.
func fakeCDPHost(t *testing.T) (*httptest.Server, string) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/json/version":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Browser":              "Fake/1.0",
				"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/abc",
			})
		case r.URL.Path == "/json/list":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "page1", "type": "page", "webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/page/1"},
			})
		case strings.HasPrefix(r.URL.Path, "/devtools/"):
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				mt, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err := conn.WriteMessage(mt, append([]byte("echo:"), msg...)); err != nil {
					return
				}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, strings.TrimPrefix(srv.URL, "http://")
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitPort(t *testing.T, addr string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestForwardDrivesFakeCDPHost(t *testing.T) {
	srv, host := fakeCDPHost(t)
	port := freePort(t)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = Serve(ctx, Options{HTTPBase: srv.URL, WSBase: "ws://" + host, Bind: "127.0.0.1", Port: port, Logf: testLogf(t)})
	}()
	if !waitPort(t, addr) {
		t.Fatalf("forward listener never came up on %s", addr)
	}

	t.Logf("requested port=%d addr=%s", port, addr)
	localWS := "ws://" + addr

	t.Run("rewrites webSocketDebuggerUrl in /json/version", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/json/version")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()

		var version map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got, want := version["webSocketDebuggerUrl"], localWS+"/devtools/browser/abc"; got != want {
			t.Fatalf("webSocketDebuggerUrl = %v, want %v", got, want)
		}
		if version["Browser"] != "Fake/1.0" {
			t.Fatalf("Browser field not passed through: %v", version["Browser"])
		}
	})

	t.Run("rewrites every socket in /json/list", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/json/list")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()

		var targets []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("targets = %d, want 1", len(targets))
		}
		if got, want := targets[0]["webSocketDebuggerUrl"], localWS+"/devtools/page/1"; got != want {
			t.Fatalf("webSocketDebuggerUrl = %v, want %v", got, want)
		}
	})

	t.Run("proxies the /devtools WebSocket to the host", func(t *testing.T) {
		conn, _, err := websocket.DefaultDialer.Dial(localWS+"/devtools/browser/abc", nil)
		if err != nil {
			t.Fatalf("dial local ws: %v", err)
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("deadline: %v", err)
		}
		_, msg, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(msg) != "echo:hi" {
			t.Fatalf("echo = %q, want %q", msg, "echo:hi")
		}
	})

	t.Run("404s paths outside CDP", func(t *testing.T) {
		resp, err := http.Get("http://" + addr + "/secrets")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestCloseReasonMapsCDPCodes(t *testing.T) {
	cases := map[int]string{
		4014: "CDP grant expired or revoked; re-run `miosa browser forward` to mint a new one",
		4012: "browser not started",
		4009: "sandbox not running",
		4004: "sandbox not found",
		4013: "tenant stream limit reached",
		1000: "closed",
	}
	for code, want := range cases {
		if got := closeReason(code); got != want {
			t.Errorf("closeReason(%d) = %q, want %q", code, got, want)
		}
	}
}

// testLogf is t.Logf until the test's cleanup starts, then silent. Relay
// goroutines on hijacked WebSocket connections can outlive Serve's shutdown,
// and a t.Logf after the test completes panics ("Log in goroutine after ...
// has completed"), which failed the 2.3.14 release run.
func testLogf(t *testing.T) func(string, ...any) {
	var (
		mu   sync.Mutex
		done bool
	)
	t.Cleanup(func() {
		mu.Lock()
		done = true
		mu.Unlock()
	})
	return func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if !done {
			t.Logf(format, args...)
		}
	}
}
