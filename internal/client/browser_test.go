package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// browserSID is a UUID shaped sandbox id, so RewritePath passes it through
// without a by-name lookup.
const browserSID = "11111111-1111-1111-1111-111111111111"

func TestBrowserCDPURL(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"data":{
			"state":"running","epoch":2,
			"cdp_url":"wss://api.miosa.ai/api/v1/sandboxes/s1/browser/cdp/tok",
			"cdp_ws_url":"wss://api.miosa.ai/api/v1/sandboxes/s1/browser/cdp/tok",
			"cdp_http_url":"https://api.miosa.ai/api/v1/sandboxes/s1/browser/cdp/tok",
			"cdp_token_id":"tid","cdp_expires_at":"2026-10-11T03:10:00Z",
			"cdp_host":"cdp-abc1234-tok.sandbox.miosa.ai",
			"cdp_host_http_url":"https://cdp-abc1234-tok.sandbox.miosa.ai",
			"cdp_host_ws_url":"wss://cdp-abc1234-tok.sandbox.miosa.ai",
			"cdp_debugger_address":"cdp-abc1234-tok.sandbox.miosa.ai:80"
		}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}

	grant, err := c.BrowserCDPURL(context.Background(), "s1")
	if err != nil {
		t.Fatalf("BrowserCDPURL: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sandboxes/s1/browser/cdp-url" {
		t.Fatalf("request %s %s, want POST /sandboxes/s1/browser/cdp-url", gotMethod, gotPath)
	}
	if grant.CdpHostHTTPURL != "https://cdp-abc1234-tok.sandbox.miosa.ai" {
		t.Fatalf("cdp_host_http_url = %q", grant.CdpHostHTTPURL)
	}
	if grant.CdpHostWSURL != "wss://cdp-abc1234-tok.sandbox.miosa.ai" {
		t.Fatalf("cdp_host_ws_url = %q", grant.CdpHostWSURL)
	}
	if grant.CdpDebuggerAddress != "cdp-abc1234-tok.sandbox.miosa.ai:80" {
		t.Fatalf("cdp_debugger_address = %q", grant.CdpDebuggerAddress)
	}
}

func TestBrowserStatus(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"data":{
			"state":"running","epoch":7,
			"viewport":{"width":1280,"height":720},
			"external":[{"id":"ext-1","kind":"chrome"}]
		}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	st, err := c.BrowserStatus(context.Background(), browserSID)
	if err != nil {
		t.Fatalf("BrowserStatus: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/sandboxes/"+browserSID+"/browser" {
		t.Fatalf("request %s %s, want GET /sandboxes/%s/browser", gotMethod, gotPath, browserSID)
	}
	if st.State != "running" || st.Epoch != 7 {
		t.Fatalf("state/epoch = %q/%d", st.State, st.Epoch)
	}
	if st.Viewport.Width != 1280 || st.Viewport.Height != 720 {
		t.Fatalf("viewport = %dx%d", st.Viewport.Width, st.Viewport.Height)
	}
	if len(st.External) != 1 || st.External[0]["id"] != "ext-1" {
		t.Fatalf("external = %#v", st.External)
	}
}

func TestBrowserStart(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"data":{
			"state":"running","epoch":3,
			"viewport":{"width":1024,"height":768},
			"live_url":"https://view.sandbox.miosa.ai/abc",
			"stream_url":"wss://stream.sandbox.miosa.ai/abc",
			"stream_auth":"tok","stream_auth_expires_at":"2026-10-11T04:00:00Z",
			"cdp_url":"wss://api.miosa.ai/api/v1/sandboxes/s1/browser/cdp/tok",
			"cdp_ws_url":"wss://api.miosa.ai/api/v1/sandboxes/s1/browser/cdp/tok",
			"cdp_http_url":"https://api.miosa.ai/api/v1/sandboxes/s1/browser/cdp/tok",
			"cdp_token_id":"tid","cdp_expires_at":"2026-10-11T03:10:00Z",
			"cdp_host":"cdp-abc1234-tok.sandbox.miosa.ai",
			"cdp_host_http_url":"https://cdp-abc1234-tok.sandbox.miosa.ai",
			"cdp_host_ws_url":"wss://cdp-abc1234-tok.sandbox.miosa.ai"
		}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	conn, err := c.BrowserStart(context.Background(), browserSID, 300)
	if err != nil {
		t.Fatalf("BrowserStart: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sandboxes/"+browserSID+"/browser" {
		t.Fatalf("request %s %s, want POST /sandboxes/%s/browser", gotMethod, gotPath, browserSID)
	}
	if got, ok := gotBody["ttl_seconds"].(float64); !ok || got != 300 {
		t.Fatalf("ttl_seconds = %#v, want 300", gotBody["ttl_seconds"])
	}
	if conn.LiveURL != "https://view.sandbox.miosa.ai/abc" {
		t.Fatalf("live_url = %q", conn.LiveURL)
	}
	if conn.CdpExpiresAt != "2026-10-11T03:10:00Z" {
		t.Fatalf("cdp_expires_at = %q, want the ISO-8601 string", conn.CdpExpiresAt)
	}
	if conn.Viewport.Height != 768 {
		t.Fatalf("viewport height = %d", conn.Viewport.Height)
	}
}

func TestBrowserStart_NoTTL(t *testing.T) {
	var raw map[string]any
	var hasBody bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hasBody = len(b) > 0
		_ = json.Unmarshal(b, &raw)
		_, _ = w.Write([]byte(`{"data":{"state":"running"}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	if _, err := c.BrowserStart(context.Background(), browserSID, 0); err != nil {
		t.Fatalf("BrowserStart: %v", err)
	}
	if hasBody && raw != nil {
		if _, ok := raw["ttl_seconds"]; ok {
			t.Fatalf("ttl 0 must omit ttl_seconds: %#v", raw)
		}
	}
}

func TestBrowserStop(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"data":{"state":"stopped","epoch":8,"viewport":{"width":1280,"height":720}}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	st, err := c.BrowserStop(context.Background(), browserSID)
	if err != nil {
		t.Fatalf("BrowserStop: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/sandboxes/"+browserSID+"/browser" {
		t.Fatalf("request %s %s, want DELETE /sandboxes/%s/browser", gotMethod, gotPath, browserSID)
	}
	if st.State != "stopped" {
		t.Fatalf("state = %q", st.State)
	}
}

func TestBrowserShare(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"data":{
			"mode":"view","share_id":"shr_1","share_token":"tok",
			"expires_at":"2026-10-11T05:00:00Z",
			"stream_url":"wss://stream/1","view_url":"https://view/1"
		}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	share, err := c.BrowserShare(context.Background(), browserSID, 600)
	if err != nil {
		t.Fatalf("BrowserShare: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sandboxes/"+browserSID+"/browser/share" {
		t.Fatalf("request %s %s, want POST /sandboxes/%s/browser/share", gotMethod, gotPath, browserSID)
	}
	if got, ok := gotBody["ttl_seconds"].(float64); !ok || got != 600 {
		t.Fatalf("ttl_seconds = %#v, want 600", gotBody["ttl_seconds"])
	}
	if share.ViewURL != "https://view/1" || share.ShareID != "shr_1" || share.Mode != "view" {
		t.Fatalf("share = %#v", share)
	}
	if share.ExpiresAt != "2026-10-11T05:00:00Z" {
		t.Fatalf("expires_at = %q", share.ExpiresAt)
	}
}

func TestBrowserRevokeAll(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"data":{"revoked":true,"epoch":9}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	res, err := c.BrowserRevokeAll(context.Background(), browserSID)
	if err != nil {
		t.Fatalf("BrowserRevokeAll: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/sandboxes/"+browserSID+"/browser/share" {
		t.Fatalf("request %s %s, want DELETE /sandboxes/%s/browser/share", gotMethod, gotPath, browserSID)
	}
	if !res.Revoked || res.Epoch != 9 {
		t.Fatalf("revoke-all = %#v", res)
	}
}

func TestBrowserRevokeOne(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_, _ = w.Write([]byte(`{"data":{"revoked":true,"share_id":"shr_1"}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	res, err := c.BrowserRevokeOne(context.Background(), browserSID, "shr_1")
	if err != nil {
		t.Fatalf("BrowserRevokeOne: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/sandboxes/"+browserSID+"/browser/share/shr_1" {
		t.Fatalf("request %s %s, want DELETE /sandboxes/%s/browser/share/shr_1", gotMethod, gotPath, browserSID)
	}
	if !res.Revoked || res.ShareID != "shr_1" {
		t.Fatalf("revoke-one = %#v", res)
	}
}

func TestBrowserScreenshotRaw(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	var gotPath, gotMethod, gotFormat, gotTab, gotEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotFormat = r.URL.Query().Get("format")
		gotTab = r.URL.Query().Get("tab")
		gotEncoding = r.URL.Query().Get("encoding")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	data, ctype, err := c.BrowserScreenshot(context.Background(), browserSID, "png", "tab-2", "")
	if err != nil {
		t.Fatalf("BrowserScreenshot: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/sandboxes/"+browserSID+"/browser/screenshot" {
		t.Fatalf("request %s %s, want GET /sandboxes/%s/browser/screenshot", gotMethod, gotPath, browserSID)
	}
	if gotFormat != "png" || gotTab != "tab-2" || gotEncoding != "" {
		t.Fatalf("query format=%q tab=%q encoding=%q", gotFormat, gotTab, gotEncoding)
	}
	if string(data) != string(png) {
		t.Fatalf("bytes = %#v, want %#v", data, png)
	}
	if ctype != "image/png" {
		t.Fatalf("content type = %q", ctype)
	}
}

func TestBrowserScreenshotBase64(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G'}
	var gotEncoding string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEncoding = r.URL.Query().Get("encoding")
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{"data": map[string]string{
			"content_type": "image/jpeg",
			"image_base64": base64.StdEncoding.EncodeToString(png),
		}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	data, ctype, err := c.BrowserScreenshot(context.Background(), browserSID, "", "", "base64")
	if err != nil {
		t.Fatalf("BrowserScreenshot: %v", err)
	}
	if gotEncoding != "base64" {
		t.Fatalf("query encoding = %q, want base64", gotEncoding)
	}
	if string(data) != string(png) {
		t.Fatalf("decoded bytes = %#v, want %#v", data, png)
	}
	if ctype != "image/jpeg" {
		t.Fatalf("content type = %q, want image/jpeg", ctype)
	}
}

func TestBrowserAction(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"data":{"ok":true,"url":"https://example.com"}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	res, err := c.BrowserAction(context.Background(), browserSID, map[string]any{"type": "navigate", "url": "https://example.com"})
	if err != nil {
		t.Fatalf("BrowserAction: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sandboxes/"+browserSID+"/browser/actions" {
		t.Fatalf("request %s %s, want POST /sandboxes/%s/browser/actions", gotMethod, gotPath, browserSID)
	}
	actions, ok := gotBody["actions"].([]any)
	if !ok || len(actions) != 1 {
		t.Fatalf("body.actions = %#v, want a one element array", gotBody["actions"])
	}
	first, _ := actions[0].(map[string]any)
	if first["type"] != "navigate" || first["url"] != "https://example.com" {
		t.Fatalf("action = %#v", first)
	}
	m, ok := res.(map[string]any)
	if !ok || m["ok"] != true {
		t.Fatalf("result = %#v", res)
	}
}

func TestBrowserCDPURLWithTTL(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"data":{
			"state":"running","epoch":2,
			"cdp_ws_url":"wss://api.miosa.ai/api/v1/sandboxes/s1/browser/cdp/tok",
			"cdp_token_id":"tid","cdp_expires_at":"2026-10-11T03:10:00Z",
			"cdp_host":"cdp-abc1234-tok.sandbox.miosa.ai",
			"cdp_host_http_url":"https://cdp-abc1234-tok.sandbox.miosa.ai",
			"cdp_host_ws_url":"wss://cdp-abc1234-tok.sandbox.miosa.ai"
		}}`))
	}))
	defer srv.Close()

	c := &Client{RC: newRestClient(srv.URL, "key")}
	grant, err := c.BrowserCDPURLWithTTL(context.Background(), browserSID, 120)
	if err != nil {
		t.Fatalf("BrowserCDPURLWithTTL: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/sandboxes/"+browserSID+"/browser/cdp-url" {
		t.Fatalf("request %s %s, want POST /sandboxes/%s/browser/cdp-url", gotMethod, gotPath, browserSID)
	}
	if got, ok := gotBody["ttl_seconds"].(float64); !ok || got != 120 {
		t.Fatalf("ttl_seconds = %#v, want 120", gotBody["ttl_seconds"])
	}
	if grant.CdpExpiresAt != "2026-10-11T03:10:00Z" {
		t.Fatalf("cdp_expires_at = %q, want a string", grant.CdpExpiresAt)
	}
	if grant.CdpHostHTTPURL != "https://cdp-abc1234-tok.sandbox.miosa.ai" {
		t.Fatalf("cdp_host_http_url = %q", grant.CdpHostHTTPURL)
	}
}
