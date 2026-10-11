package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// BrowserCDP is the CDP grant for one sandbox's live browser, minted by
// POST /sandboxes/:id/browser/cdp-url. Every URL embeds a scoped, expiring,
// revocable token.
//
// Two forms are returned: the api.miosa.ai URLs (cdp_url / cdp_ws_url /
// cdp_http_url, token in the path or a header) and the token-in-the-host-label
// form (cdp_host / cdp_host_http_url / cdp_host_ws_url) that clients which take
// only host:port - Selenium's debuggerAddress, chromedp's base URL - can use.
type BrowserCDP struct {
	State          string `json:"state"`
	Epoch          int    `json:"epoch"`
	CdpURL         string `json:"cdp_url"`
	CdpWsURL       string `json:"cdp_ws_url"`
	CdpHTTPURL     string `json:"cdp_http_url"`
	CdpTokenID     string `json:"cdp_token_id"`
	CdpExpiresAt   string `json:"cdp_expires_at"`
	CdpHost        string `json:"cdp_host"`
	CdpHostHTTPURL string `json:"cdp_host_http_url"`
	CdpHostWSURL   string `json:"cdp_host_ws_url"`
	// CdpDebuggerAddress is host:port for a Selenium debuggerAddress, present
	// only on the start (/browser) payload where the edge serves plain HTTP
	// on :80 for the cdp host. The cdp-url grant omits it.
	CdpDebuggerAddress string `json:"cdp_debugger_address"`
}

// BrowserViewport is the live browser's viewport size in pixels.
type BrowserViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// BrowserStatusInfo is returned by GET /browser and by the stop response. It
// never starts anything. State reads "sandbox_not_running" when the sandbox is
// not running. External lists user-launched / external Chromes attached to the
// same browser, each as its raw JSON object.
type BrowserStatusInfo struct {
	State    string           `json:"state"`
	Epoch    int              `json:"epoch"`
	Viewport BrowserViewport  `json:"viewport"`
	External []map[string]any `json:"external,omitempty"`
}

// BrowserConnect is the connect payload from POST /browser. It starts the
// browser lazily and returns the live view stream plus the CDP grant forms.
type BrowserConnect struct {
	State               string          `json:"state"`
	Epoch               int             `json:"epoch"`
	Viewport            BrowserViewport `json:"viewport"`
	LiveURL             string          `json:"live_url"`
	StreamURL           string          `json:"stream_url"`
	StreamAuth          string          `json:"stream_auth"`
	StreamAuthExpiresAt string          `json:"stream_auth_expires_at"`
	CdpURL              string          `json:"cdp_url"`
	CdpWsURL            string          `json:"cdp_ws_url"`
	CdpHTTPURL          string          `json:"cdp_http_url"`
	CdpTokenID          string          `json:"cdp_token_id"`
	CdpExpiresAt        string          `json:"cdp_expires_at"`
	CdpHost             string          `json:"cdp_host"`
	CdpHostHTTPURL      string          `json:"cdp_host_http_url"`
	CdpHostWSURL        string          `json:"cdp_host_ws_url"`
	CdpDebuggerAddress  string          `json:"cdp_debugger_address"`
}

// BrowserShareInfo is the payload from POST /browser/share. The server shares
// view-only mode; Mode reads "view".
type BrowserShareInfo struct {
	Mode       string `json:"mode"`
	ShareID    string `json:"share_id"`
	ShareToken string `json:"share_token"`
	ExpiresAt  string `json:"expires_at"`
	StreamURL  string `json:"stream_url"`
	ViewURL    string `json:"view_url"`
}

// BrowserRevokeResult is returned by the share revoke endpoints. Revoke-all
// carries Epoch; revoke-one carries ShareID.
type BrowserRevokeResult struct {
	Revoked bool   `json:"revoked"`
	Epoch   int    `json:"epoch,omitempty"`
	ShareID string `json:"share_id,omitempty"`
}

// browserTTL is the optional request body for the endpoints that accept a
// session lifetime.
type browserTTL struct {
	TTLSeconds int `json:"ttl_seconds"`
}

// ttlBody returns the optional {"ttl_seconds":N} body, or nil when ttl is not
// positive so the server default applies.
func ttlBody(ttlSeconds int) any {
	if ttlSeconds > 0 {
		return browserTTL{TTLSeconds: ttlSeconds}
	}
	return nil
}

type browserCDPResponse struct {
	Data BrowserCDP `json:"data"`
}

// BrowserCDPURL starts the sandbox's browser lazily if needed and returns the
// CDP grant. It needs the sandboxes:write scope.
func (c *Client) BrowserCDPURL(ctx context.Context, sandboxID string) (*BrowserCDP, error) {
	return c.BrowserCDPURLWithTTL(ctx, sandboxID, 0)
}

// BrowserCDPURLWithTTL is BrowserCDPURL with an explicit grant lifetime in
// seconds (0 keeps the server default).
func (c *Client) BrowserCDPURLWithTTL(ctx context.Context, sandboxID string, ttlSeconds int) (*BrowserCDP, error) {
	var out browserCDPResponse
	if err := c.RC.postJSON(ctx, "/sandboxes/"+sandboxID+"/browser/cdp-url", ttlBody(ttlSeconds), &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// BrowserStatus reads the live browser state without starting anything. It
// needs the sandboxes:read scope.
func (c *Client) BrowserStatus(ctx context.Context, sandboxID string) (*BrowserStatusInfo, error) {
	var out struct {
		Data BrowserStatusInfo `json:"data"`
	}
	if err := c.RC.getJSON(ctx, "/sandboxes/"+sandboxID+"/browser", &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// BrowserStart starts the sandbox's browser lazily and returns the connect
// payload. It needs the sandboxes:write scope. A stopped sandbox answers 409
// SANDBOX_NOT_RUNNING.
func (c *Client) BrowserStart(ctx context.Context, sandboxID string, ttlSeconds int) (*BrowserConnect, error) {
	var out struct {
		Data BrowserConnect `json:"data"`
	}
	if err := c.RC.postJSON(ctx, "/sandboxes/"+sandboxID+"/browser", ttlBody(ttlSeconds), &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// BrowserStop stops the sandbox's browser. It needs the sandboxes:write scope.
func (c *Client) BrowserStop(ctx context.Context, sandboxID string) (*BrowserStatusInfo, error) {
	var out struct {
		Data BrowserStatusInfo `json:"data"`
	}
	if err := c.deleteJSON(ctx, "/sandboxes/"+sandboxID+"/browser", &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// BrowserShare creates a view-only share link for the live browser and returns
// its view URL. It needs the sandboxes:write scope.
func (c *Client) BrowserShare(ctx context.Context, sandboxID string, ttlSeconds int) (*BrowserShareInfo, error) {
	var out struct {
		Data BrowserShareInfo `json:"data"`
	}
	if err := c.RC.postJSON(ctx, "/sandboxes/"+sandboxID+"/browser/share", ttlBody(ttlSeconds), &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// BrowserRevokeAll revokes every share and CDP link for the browser.
func (c *Client) BrowserRevokeAll(ctx context.Context, sandboxID string) (*BrowserRevokeResult, error) {
	var out struct {
		Data BrowserRevokeResult `json:"data"`
	}
	if err := c.deleteJSON(ctx, "/sandboxes/"+sandboxID+"/browser/share", &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// BrowserRevokeOne revokes one share by id.
func (c *Client) BrowserRevokeOne(ctx context.Context, sandboxID, shareID string) (*BrowserRevokeResult, error) {
	var out struct {
		Data BrowserRevokeResult `json:"data"`
	}
	if err := c.deleteJSON(ctx, "/sandboxes/"+sandboxID+"/browser/share/"+url.PathEscape(shareID), &out); err != nil {
		return nil, err
	}
	return &out.Data, nil
}

// BrowserScreenshot captures the live browser. With encoding="" the server
// answers raw image bytes with an image/png or image/jpeg Content-Type; with
// encoding="base64" it answers {"data":{content_type,image_base64}}. The method
// returns the decoded image bytes and its content type either way.
func (c *Client) BrowserScreenshot(ctx context.Context, sandboxID, format, tab, encoding string) ([]byte, string, error) {
	q := url.Values{}
	if format != "" {
		q.Set("format", format)
	}
	if tab != "" {
		q.Set("tab", tab)
	}
	if encoding != "" {
		q.Set("encoding", encoding)
	}
	data, hdr, err := c.RC.api.Bytes(ctx, api.Request{
		Method: http.MethodGet,
		Path:   "/sandboxes/" + sandboxID + "/browser/screenshot",
		Query:  q,
	})
	if err != nil {
		return nil, "", err
	}
	ctype := hdr.Get("Content-Type")
	if strings.HasPrefix(ctype, "application/json") {
		var out struct {
			Data struct {
				ContentType string `json:"content_type"`
				ImageBase64 string `json:"image_base64"`
			} `json:"data"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, "", err
		}
		decoded, err := base64.StdEncoding.DecodeString(out.Data.ImageBase64)
		if err != nil {
			return nil, "", err
		}
		if out.Data.ContentType != "" {
			ctype = out.Data.ContentType
		}
		return decoded, ctype, nil
	}
	return data, ctype, nil
}

// BrowserAction sends one browser action (click, type, navigate, ...) and
// returns the daemon result. The action is wrapped as {"actions":[action]}.
func (c *Client) BrowserAction(ctx context.Context, sandboxID string, action map[string]any) (any, error) {
	body := map[string]any{"actions": []map[string]any{action}}
	var out struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.RC.postJSON(ctx, "/sandboxes/"+sandboxID+"/browser/actions", body, &out); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out.Data)) == 0 {
		return nil, nil
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(out.Data))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// deleteJSON is DELETE with a decoded JSON response. restClient.delete
// discards the body, which the browser routes need.
func (c *Client) deleteJSON(ctx context.Context, path string, out any) error {
	return c.RC.api.JSON(ctx, api.Request{Method: http.MethodDelete, Path: path}, out)
}
