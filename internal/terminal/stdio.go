package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocketURL turns an API base URL (https://host/api/v1) and a path
// (/sandboxes/ID/ssh-tunnel) into a wss:// or ws:// URL.
func WebSocketURL(apiBase, path string) (string, error) {
	u, err := url.Parse(apiBase)
	if err != nil {
		return "", fmt.Errorf("parsing the API URL: %w", err)
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(path, "/")
	return u.String(), nil
}

// DialStream opens an authenticated WebSocket for a raw byte stream (the SSH
// tunnel). The key goes in the Authorization header, never in the URL.
func DialStream(ctx context.Context, wsURL, apiKey string) (*websocket.Conn, error) {
	d := websocket.Dialer{HandshakeTimeout: 20 * time.Second}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+apiKey)
	conn, resp, err := d.DialContext(ctx, wsURL, hdr)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("tunnel handshake failed: %s", resp.Status)
		}
		return nil, fmt.Errorf("tunnel connection failed: %w", err)
	}
	return conn, nil
}

// BridgeStdio copies in to the connection as binary frames and binary frames to
// out, until either side ends. It is the body of an SSH ProxyCommand: ssh
// writes its bytes to our stdin and reads the server's from our stdout. A
// server close code is returned as a *CloseError.
func BridgeStdio(ctx context.Context, conn *websocket.Conn, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { <-ctx.Done(); conn.Close() }()

	readErr := make(chan error, 1)
	writeErr := make(chan error, 1)
	go func() { // stdin -> websocket
		buf := make([]byte, 32*1024)
		for {
			n, err := in.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					writeErr <- werr
					return
				}
			}
			if err != nil {
				// ssh closed its end: tell the server, then let it finish sending.
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				if !errors.Is(err, io.EOF) {
					writeErr <- err
					return
				}
				time.AfterFunc(5*time.Second, cancel)
				return
			}
		}
	}()
	go func() { // websocket -> stdout
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				var ce *websocket.CloseError
				if errors.As(err, &ce) {
					if ce.Code == websocket.CloseNormalClosure || ce.Code == websocket.CloseGoingAway {
						readErr <- nil
						return
					}
					readErr <- &CloseError{Code: ce.Code, Text: ce.Text}
					return
				}
				readErr <- err
				return
			}
			if mt != websocket.BinaryMessage {
				continue
			}
			if _, werr := out.Write(data); werr != nil {
				readErr <- werr
				return
			}
		}
	}()
	select {
	case err := <-readErr:
		if ctx.Err() != nil && err != nil && !errors.As(err, new(*CloseError)) {
			return nil // the stdin-EOF grace period ended
		}
		return err
	case err := <-writeErr:
		return err
	}
}
