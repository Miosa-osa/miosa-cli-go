// Package cdpforward serves a sandbox's Chrome DevTools Protocol endpoint on
// loopback, so tools that insist on a local browser -- Selenium
// (debuggerAddress=127.0.0.1:9222), chromedp with an http:// base -- work
// without exposing the plaintext CDP port.
//
// It proxies HTTP discovery (/json, /json/version, /json/list, /json/new,
// /json/protocol) to the cdp host's https base, rewrites every
// webSocketDebuggerUrl in the response to this local listener, and proxies the
// /devtools/<browser|page>/<id> WebSocket the client then dials to the cdp
// host's wss endpoint. The scoped, expiring CDP grant stays in the host label,
// so nothing is ever exposed in plaintext.
package cdpforward

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

// Options configure the forward listener.
type Options struct {
	// HTTPBase is the cdp host https base, e.g. https://cdp-<id8>-<token>.sandbox.miosa.ai
	HTTPBase string
	// WSBase is the cdp host wss base, e.g. wss://cdp-<id8>-<token>.sandbox.miosa.ai
	WSBase string
	// Bind is the local address; empty means 127.0.0.1.
	Bind string
	// Port is the local port. 0 lets the OS choose (tests).
	Port int
	// Logf receives one line per connection event. Nil is silent.
	Logf func(format string, args ...any)
}

type forwarder struct {
	ctx      context.Context
	httpBase string
	wsBase   string
	bind     string
	port     int
	client   *http.Client
	logf     func(string, ...any)
}

// Serve listens on Bind:Port and proxies CDP until ctx is cancelled.
func Serve(ctx context.Context, opts Options) error {
	bind := opts.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}

	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}

	f := &forwarder{
		ctx:      ctx,
		httpBase: strings.TrimRight(opts.HTTPBase, "/"),
		wsBase:   strings.TrimRight(opts.WSBase, "/"),
		bind:     bind,
		port:     opts.Port,
		client:   &http.Client{},
		logf:     logf,
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(opts.Port)))
	if err != nil {
		return err
	}
	f.port = ln.Addr().(*net.TCPAddr).Port

	srv := &http.Server{Handler: f}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// LocalBase is the loopback base clients should dial, e.g. ws://127.0.0.1:9222.
func (f *forwarder) localBase() string {
	return "ws://" + net.JoinHostPort(f.bind, strconv.Itoa(f.port))
}

// LocalAddr is the loopback host:port, e.g. 127.0.0.1:9222.
func (f *forwarder) LocalAddr() string {
	return net.JoinHostPort(f.bind, strconv.Itoa(f.port))
}

func (f *forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/json" || strings.HasPrefix(r.URL.Path, "/json/"):
		f.proxyHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, "/devtools/"):
		f.proxyWS(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *forwarder) proxyHTTP(w http.ResponseWriter, r *http.Request) {
	target := f.httpBase + r.URL.RequestURI()

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	if err != nil {
		http.Error(w, "bad upstream request", http.StatusBadGateway)
		return
	}
	if accept := r.Header.Get("Accept"); accept != "" {
		req.Header.Set("Accept", accept)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		f.logf("%s %s -> upstream error: %v", r.Method, r.URL.Path, err)
		http.Error(w, "cdp host unreachable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "cdp host read error", http.StatusBadGateway)
		return
	}

	// CDP discovery is JSON; rewrite whenever it parses, regardless of the
	// upstream Content-Type header (a body that does not parse is returned
	// untouched).
	body = f.rewriteSocketURLs(body)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func (f *forwarder) proxyWS(w http.ResponseWriter, r *http.Request) {
	target := f.wsBase + r.URL.RequestURI()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	client, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	dialer := websocket.Dialer{}
	upstream, _, err := dialer.DialContext(f.ctx, target, nil)
	if err != nil {
		f.logf("devtools %s -> upstream dial error: %v", r.URL.Path, err)
		_ = client.Close()
		return
	}

	f.logf("devtools %s connected", r.URL.Path)
	f.pipe(client, upstream)
}

// pipe relays WebSocket messages in both directions until either side closes.
// A close code from the cdp host is surfaced, so an expired grant (4014), a
// browser that was never started (4012), or a stopped sandbox (4009) reads as
// itself instead of a bare disconnect.
func (f *forwarder) pipe(client, upstream *websocket.Conn) {
	done := make(chan struct{}, 2)

	relay := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, msg, err := src.ReadMessage()
			if err != nil {
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) && closeErr.Code != websocket.CloseNormalClosure {
					f.logf("devtools closed: %d %s", closeErr.Code, closeReason(closeErr.Code))
				}
				return
			}
			if err := dst.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	}

	go relay(client, upstream)
	go relay(upstream, client)
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

// closeReason maps the cdp host's WebSocket close codes to a sentence.
func closeReason(code int) string {
	switch code {
	case 4014:
		return "CDP grant expired or revoked; re-run `miosa browser forward` to mint a new one"
	case 4012:
		return "browser not started"
	case 4009:
		return "sandbox not running"
	case 4004:
		return "sandbox not found"
	case 4013:
		return "tenant stream limit reached"
	default:
		return "closed"
	}
}

// rewriteSocketURLs points every webSocketDebuggerUrl at this local listener,
// keeping its /devtools/... path, so a client that follows it stays local and
// the grant never leaves the host label.
func (f *forwarder) rewriteSocketURLs(body []byte) []byte {
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return body
	}

	rewritten, err := json.Marshal(rewriteTerm(decoded, f.localBase()))
	if err != nil {
		return body
	}
	return rewritten
}

func rewriteTerm(v any, base string) any {
	switch value := v.(type) {
	case []any:
		for i, item := range value {
			value[i] = rewriteTerm(item, base)
		}
		return value
	case map[string]any:
		for key, item := range value {
			if key == "webSocketDebuggerUrl" {
				if s, ok := item.(string); ok {
					value[key] = rewriteSocketURL(s, base)
					continue
				}
			}
			value[key] = rewriteTerm(item, base)
		}
		return value
	default:
		return v
	}
}

func rewriteSocketURL(raw, base string) string {
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Path, "/devtools/") {
		return raw
	}
	out := base + u.Path
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out
}
