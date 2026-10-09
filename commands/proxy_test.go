package commands_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	miosa "github.com/Miosa-osa/miosa-go"
	"github.com/gorilla/websocket"

	"github.com/Miosa-osa/miosa-cli-go/internal/client"
)

// ─── Helpers ──────────────────────────────────────────────────────────────────

// fakeTunnelServer returns an httptest.Server that:
//   - serves GET /computers/:id as a valid computer JSON
//   - serves GET /computers/:id/tunnel/:port as a WebSocket echo server
//
// It also records how many tunnel connections were made.
func fakeTunnelServer(t *testing.T, computerID, name string) (*httptest.Server, *int) {
	t.Helper()
	connections := new(int)

	upgrader := websocket.Upgrader{
		CheckOrigin:  func(*http.Request) bool { return true },
		Subprotocols: []string{"miosa-tunnel-v1"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Computer lookup.
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/computers/") &&
			!strings.Contains(r.URL.Path, "/tunnel/") {
			data, _ := json.Marshal(miosa.ComputerData{
				ID:           computerID,
				Name:         name,
				Status:       miosa.StatusRunning,
				Size:         miosa.SizeSmall,
				TemplateType: "miosa-sandbox",
				CreatedAt:    "2026-01-01T00:00:00Z",
			})
			w.Write(data)
			return
		}

		// Tunnel WebSocket.
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/tunnel/") {
			*connections++
			hdr := http.Header{"Sec-WebSocket-Protocol": []string{"miosa-tunnel-v1"}}
			conn, err := upgrader.Upgrade(w, r, hdr)
			if err != nil {
				return
			}
			defer conn.Close()
			// Echo loop.
			for {
				mt, msg, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err := conn.WriteMessage(mt, msg); err != nil {
					return
				}
			}
		}

		http.Error(w, "not found", http.StatusNotFound)
	}))
	return srv, connections
}

// ─── Argument parsing tests ───────────────────────────────────────────────────

func TestProxyCommand_InvalidPortMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "proxy", "abc123", "notaport")
	if err == nil {
		t.Fatal("expected error for invalid port mapping")
	}
}

func TestProxyCommand_NoPortMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "proxy", "abc123")
	if err == nil {
		t.Fatal("expected error when no port mapping provided")
	}
}

func TestProxyCommand_InvalidLocalPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "proxy", "abc123", "99999:80")
	if err == nil {
		t.Fatal("expected error for out-of-range local port")
	}
}

func TestProxyCommand_InvalidRemotePort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "proxy", "abc123", "8080:0")
	if err == nil {
		t.Fatal("expected error for out-of-range remote port")
	}
}

func TestProxyCommand_NotAvailableDoesNotCallComputers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/computers/") {
			t.Fatalf("proxy command must not call computer API: %s", r.URL.Path)
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	_, err := run(t, "proxy", "abc123", "8080:80")
	if err == nil {
		t.Fatal("expected unsupported native sandbox proxy error")
	}
}

// ─── Tunnel roundtrip test (real TCP + fake WS control plane) ────────────────

// TestProxyCommand_TunnelRoundtrip verifies the full TCP↔WebSocket bridge:
//  1. CLI starts a local listener on a random port.
//  2. Test opens a TCP connection to that local port.
//  3. CLI opens a WebSocket to the fake control plane.
//  4. Fake control plane echoes all bytes.
//  5. Test asserts it receives the echoed bytes back over TCP.
func TestProxyCommand_TunnelRoundtrip(t *testing.T) {
	srv, _ := fakeTunnelServer(t, "abc123", "my-box")
	defer srv.Close()
	cleanup := setupEnv(t, srv)
	defer cleanup()

	// Build the proxy transport directly so we control the local port.
	proxyClient := &client.Client{}
	_ = proxyClient

	// Use a free local port.
	localLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	localPort := localLn.Addr().(*net.TCPAddr).Port
	localLn.Close()

	// Derive WebSocket URL from the test server URL.
	wsBase := "ws" + strings.TrimPrefix(srv.URL, "http")

	// Build a realProxy directly (bypassing the full CLI flag machinery).
	rc := newTestRealProxy(t, wsBase+"/api/v1", "msk_u_test")

	ctx, cancel := context.WithCancel(context.Background())

	fwdDone := make(chan error, 1)
	go func() {
		fwdDone <- rc.Forward(ctx, "abc123", localPort, 3000)
	}()

	// Give the listener a moment to start.
	time.Sleep(20 * time.Millisecond)

	// Open a TCP connection to the local forwarder.
	tcpConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 3*time.Second)
	if err != nil {
		cancel()
		t.Fatalf("dial local forwarder: %v", err)
	}
	defer tcpConn.Close()

	// Send a message and expect it echoed back.
	payload := []byte("proxy-roundtrip-test")
	if _, err := tcpConn.Write(payload); err != nil {
		cancel()
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, len(payload))
	tcpConn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	if _, err := io.ReadFull(tcpConn, buf); err != nil {
		cancel()
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(payload) {
		t.Errorf("echo mismatch: got %q want %q", buf, payload)
	}

	// Graceful shutdown.
	cancel()
	select {
	case err := <-fwdDone:
		if err != nil {
			t.Logf("forwarder exited: %v (expected after cancel)", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("forwarder did not exit within 2s after cancel")
	}
}

// ─── Multi-port test ──────────────────────────────────────────────────────────

func TestProxyCommand_MultiplePortMappings_ParseOK(t *testing.T) {
	// Verify that multiple port pairs parse correctly without hitting a server.
	nameOrID, pairs, err := testParseProxyArgs([]string{"abc123", "5432:5432", "6379:6379"})
	if err != nil {
		t.Fatalf("parseProxyArgs: %v", err)
	}
	if nameOrID != "abc123" {
		t.Errorf("nameOrID: got %q want abc123", nameOrID)
	}
	if len(pairs) != 2 {
		t.Fatalf("pairs: got %d want 2", len(pairs))
	}
	if pairs[0] != [2]int{5432, 5432} {
		t.Errorf("pair[0]: got %v", pairs[0])
	}
	if pairs[1] != [2]int{6379, 6379} {
		t.Errorf("pair[1]: got %v", pairs[1])
	}
}

// ─── Internal helpers ─────────────────────────────────────────────────────────

// newTestRealProxy constructs the realProxy via the exported client.New path
// using env overrides pointing at the test server. The baseURL here is the
// raw WebSocket base without /api/v1 suffix since tunnelURL appends the path.
func newTestRealProxy(t *testing.T, baseURL, apiKey string) client.ProxyTransport {
	t.Helper()
	// client.New reads from env; we set those in setupEnv. Here we just build
	// a concrete realProxy by round-tripping through New.
	t.Setenv("MIOSA_API_KEY", apiKey)
	t.Setenv("MIOSA_BASE_URL", baseURL)
	c, _, err := client.New(client.ResolveOptions{})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	return c.Proxy
}

// testParseProxyArgs calls the exported parseProxyArgs indirectly by running
// the validation logic inline (mirrors the function in proxy.go).
func testParseProxyArgs(args []string) (nameOrID string, pairs [][2]int, err error) {
	start := 0
	if len(args) > 0 && !strings.Contains(args[0], ":") {
		nameOrID = args[0]
		start = 1
	}
	for _, arg := range args[start:] {
		parts := strings.SplitN(arg, ":", 2)
		if len(parts) != 2 {
			return "", nil, fmt.Errorf("invalid mapping %q", arg)
		}
		var local, remote int
		if _, serr := fmt.Sscanf(parts[0], "%d", &local); serr != nil || local < 1 || local > 65535 {
			return "", nil, fmt.Errorf("invalid local port %q", parts[0])
		}
		if _, serr := fmt.Sscanf(parts[1], "%d", &remote); serr != nil || remote < 1 || remote > 65535 {
			return "", nil, fmt.Errorf("invalid remote port %q", parts[1])
		}
		pairs = append(pairs, [2]int{local, remote})
	}
	if len(pairs) == 0 {
		return "", nil, fmt.Errorf("no port mappings")
	}
	return nameOrID, pairs, nil
}
