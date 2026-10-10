// Package api is the CLI's HTTP layer: authentication, retries, typed errors
// and streaming helpers. Every command that is not tied to the vendored SDK
// goes through it, so error codes, request ids and retry behavior are the same
// everywhere.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a minimal authenticated JSON client for the MIOSA API.
type Client struct {
	BaseURL   string
	Key       string
	UserAgent string
	// HTTP performs requests with a timeout. Stream uses StreamHTTP.
	HTTP *http.Client
	// StreamHTTP has no overall timeout; callers bound streams with a context.
	StreamHTTP *http.Client
	// MaxRetries is the number of retries after the first attempt.
	MaxRetries int
	// RetryBase is the first backoff delay. It doubles each attempt.
	RetryBase time.Duration
	// Headers are added to every request (for example X-Miosa-Org).
	Headers map[string]string
	// Sleep waits between attempts; tests replace it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Resolver resolves sandbox names and aliases found in request paths.
	Resolver *Resolver
}

// New returns a Client with sane defaults.
func New(baseURL, key string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Key:        key,
		UserAgent:  "miosa-cli",
		HTTP:       &http.Client{Timeout: 60 * time.Second},
		StreamHTTP: &http.Client{},
		MaxRetries: 3,
		RetryBase:  250 * time.Millisecond,
		Sleep:      sleepCtx,
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Error is an API error response.
type Error struct {
	Status    int
	Code      string
	Message   string
	RequestID string
	Retryable bool
	// RetryAfter is the server-suggested delay, if any.
	RetryAfter time.Duration
	// Details is the decoded "details" object, if the server sent one.
	Details map[string]any
	Body    []byte
}

func (e *Error) Error() string {
	var b strings.Builder
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	b.WriteString(msg)
	if e.Code != "" && !strings.EqualFold(e.Code, msg) {
		fmt.Fprintf(&b, " [%s]", e.Code)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " (request_id=%s)", e.RequestID)
	}
	return b.String()
}

// IsStatus reports whether err is an *Error with the given HTTP status.
func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

// IsCode reports whether err is an *Error with the given API code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && strings.EqualFold(e.Code, code)
}

// ParseError builds an *Error from a response. It reads and closes the body.
func ParseError(resp *http.Response) *Error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	e := &Error{Status: resp.StatusCode, Body: body, RequestID: resp.Header.Get("X-Request-Id")}
	var raw map[string]any
	parsed := json.Unmarshal(body, &raw) == nil
	if parsed {
		switch v := raw["error"].(type) {
		case map[string]any:
			e.Code, _ = v["code"].(string)
			e.Message, _ = v["message"].(string)
			if m, ok := v["details"].(map[string]any); ok {
				e.Details = m
			}
			if r, ok := v["retryable"].(bool); ok {
				e.Retryable = r
			}
			if rid, _ := v["request_id"].(string); rid != "" && e.RequestID == "" {
				e.RequestID = rid
			}
			if f, ok := v["retry_after"].(float64); ok && f > 0 {
				e.RetryAfter = time.Duration(f * float64(time.Second))
			}
		case string:
			e.Message = v
			e.Code, _ = raw["code"].(string)
		}
		if e.Message == "" {
			if m, ok := raw["message"].(string); ok {
				e.Message = m
			}
		}
		if e.Code == "" {
			if c, ok := raw["code"].(string); ok {
				e.Code = c
			}
		}
		if rid, _ := raw["request_id"].(string); rid != "" && e.RequestID == "" {
			e.RequestID = rid
		}
		if r, ok := raw["retryable"].(bool); ok && r {
			e.Retryable = true
		}
	}
	if e.Message == "" && !parsed {
		e.Message = strings.TrimSpace(string(clip(body, 200)))
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.ParseFloat(ra, 64); err == nil && secs >= 0 {
			e.RetryAfter = time.Duration(secs * float64(time.Second))
		}
	}
	return e
}

func clip(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

// TransportError wraps a network failure so callers can tell it from an API error.
type TransportError struct{ Err error }

func (e *TransportError) Error() string {
	return "cannot reach the MIOSA API: " + e.Err.Error()
}
func (e *TransportError) Unwrap() error { return e.Err }

// Request describes one call.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	// Body is marshalled to JSON unless it is an io.Reader.
	Body any
	// ContentType overrides the JSON content type when Body is an io.Reader.
	ContentType string
	// IdempotencyKey makes a POST safe to retry.
	IdempotencyKey string
	Headers        map[string]string
	// NoRetry disables retries for this call.
	NoRetry bool
}

func (c *Client) url(path string, q url.Values) string {
	u := c.BaseURL + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func idempotent(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// shouldRetry decides whether a failed attempt can be repeated safely.
func shouldRetry(r Request, err error) bool {
	if r.NoRetry {
		return false
	}
	safe := idempotent(r.Method) || r.IdempotencyKey != ""
	var te *TransportError
	if errors.As(err, &te) {
		return safe
	}
	var ae *Error
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.Status {
	case http.StatusTooManyRequests:
		return true // never processed
	case http.StatusServiceUnavailable:
		return true
	case http.StatusBadGateway, http.StatusGatewayTimeout:
		return safe
	case http.StatusInternalServerError:
		return safe && ae.Retryable
	}
	return false
}

func (c *Client) backoff(attempt int, err error) time.Duration {
	var ae *Error
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		if ae.RetryAfter > 30*time.Second {
			return 30 * time.Second
		}
		return ae.RetryAfter
	}
	d := c.RetryBase << uint(attempt)
	if d > 8*time.Second || d <= 0 {
		d = 8 * time.Second
	}
	return time.Duration(rand.Int63n(int64(d)/2+1)) + d/2
}

func (c *Client) build(ctx context.Context, r Request, body io.Reader, ctype string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, r.Method, c.url(r.Path, r.Query), body)
	if err != nil {
		return nil, err
	}
	if c.Key != "" {
		req.Header.Set("Authorization", "Bearer "+c.Key)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if r.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	return req, nil
}

// Do performs the request with retries and returns the successful response.
// The caller closes the body.
func (c *Client) Do(ctx context.Context, r Request) (*http.Response, error) {
	return c.do(ctx, c.HTTP, r)
}

// Stream is Do without the overall client timeout. Retries still apply to the
// initial connection.
func (c *Client) Stream(ctx context.Context, r Request) (*http.Response, error) {
	return c.do(ctx, c.StreamHTTP, r)
}

func (c *Client) do(ctx context.Context, hc *http.Client, r Request) (*http.Response, error) {
	path, err := c.RewritePath(ctx, r.Path)
	if err != nil {
		return nil, err
	}
	r.Path = path
	var payload []byte
	var reader io.Reader
	ctype := r.ContentType
	switch b := r.Body.(type) {
	case nil:
	case io.Reader:
		// A raw reader cannot be replayed; buffer it so retries work.
		data, err := io.ReadAll(b)
		if err != nil {
			return nil, err
		}
		payload = data
	default:
		data, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		payload = data
		if ctype == "" {
			ctype = "application/json"
		}
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := c.build(ctx, r, reader, ctype)
		if err != nil {
			return nil, err
		}
		resp, err := hc.Do(req)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = &TransportError{Err: err}
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			return resp, nil
		default:
			lastErr = ParseError(resp)
		}
		if attempt >= c.MaxRetries || !shouldRetry(r, lastErr) {
			return nil, lastErr
		}
		if err := c.Sleep(ctx, c.backoff(attempt, lastErr)); err != nil {
			return nil, err
		}
	}
}

// JSON performs a request and decodes a JSON response into out (may be nil).
func (c *Client) JSON(ctx context.Context, r Request, out any) error {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// Get is a GET returning decoded JSON.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.JSON(ctx, Request{Method: http.MethodGet, Path: path, Query: q}, out)
}

// Post is a POST with a JSON body.
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.JSON(ctx, Request{Method: http.MethodPost, Path: path, Body: body}, out)
}

// Put is a PUT with a JSON body.
func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.JSON(ctx, Request{Method: http.MethodPut, Path: path, Body: body}, out)
}

// Patch is a PATCH with a JSON body.
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.JSON(ctx, Request{Method: http.MethodPatch, Path: path, Body: body}, out)
}

// Delete is a DELETE.
func (c *Client) Delete(ctx context.Context, path string, q url.Values, out any) error {
	return c.JSON(ctx, Request{Method: http.MethodDelete, Path: path, Query: q}, out)
}

// JSONAny performs a request and decodes the response into generic JSON with
// numbers kept exact (json.Number), so ids and counters print as sent. An
// empty body yields nil.
func (c *Client) JSONAny(ctx context.Context, r Request) (any, error) {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		// Not JSON (a plain-text route): hand back the text.
		return string(body), nil
	}
	return out, nil
}

// Bytes performs a request and returns the raw successful body.
func (c *Client) Bytes(ctx context.Context, r Request) ([]byte, http.Header, error) {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.Header, err
}
