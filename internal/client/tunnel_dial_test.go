package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// A sandbox moving between hosts refuses a tunnel dial for a moment: the dial
// is retried, and a refusal that will not change is not.
func TestDialTunnelRetriesWhileTheSandboxMoves(t *testing.T) {
	old := tunnelRetryPause
	tunnelRetryPause = time.Millisecond
	defer func() { tunnelRetryPause = old }()

	var calls int32
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			http.Error(w, "moving", http.StatusServiceUnavailable)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err == nil {
			c.Close()
		}
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	var failures int
	conn, err := dialTunnel(context.Background(), &websocket.Dialer{}, url, nil, func(int, *http.Response, error) { failures++ })
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	conn.Close()
	if calls != 3 || failures != 2 {
		t.Fatalf("calls=%d failures=%d", calls, failures)
	}
}

func TestDialTunnelDoesNotRetryAForbiddenTunnel(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "no", http.StatusForbidden)
	}))
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	if _, err := dialTunnel(context.Background(), &websocket.Dialer{}, url, nil, func(int, *http.Response, error) {}); err == nil {
		t.Fatal("expected an error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}
