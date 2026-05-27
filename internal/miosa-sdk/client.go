package miosa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultBaseURL    = "https://api.miosa.ai/api/v1"
	defaultTimeout    = 60 * time.Second
	defaultMaxRetries = 3
	sdkVersion        = "0.2.0"
)

// ClientOption is a functional option for configuring a Client.
type ClientOption func(*Client)

// WithBaseURL overrides the default API base URL.
func WithBaseURL(u string) ClientOption {
	return func(c *Client) { c.baseURL = u }
}

// WithHTTPClient replaces the default HTTP client.
func WithHTTPClient(hc *http.Client) ClientOption {
	return func(c *Client) { c.httpClient = hc }
}

// WithTimeout sets the per-request timeout.
func WithTimeout(d time.Duration) ClientOption {
	return func(c *Client) { c.httpClient.Timeout = d }
}

// WithMaxRetries sets the maximum number of retry attempts for retryable errors.
// Set to 0 to disable retries.
func WithMaxRetries(n int) ClientOption {
	return func(c *Client) { c.maxRetries = n }
}

// Client is the root MIOSA API client.
// Use NewClient to construct one.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	maxRetries int

	// Services — populated by NewClient.
	Computers *ComputersService
	Sandboxes *SandboxesService
	Agent     *AgentService
	Files     *FilesService
	Credits   *CreditsService
	Admin     *AdminService
}

// NewClient creates a new Client authenticated with the given API key.
// Options are applied in order after defaults.
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		maxRetries: defaultMaxRetries,
	}
	for _, o := range opts {
		o(c)
	}
	c.Computers = &ComputersService{client: c}
	c.Sandboxes = &SandboxesService{client: c}
	c.Agent = &AgentService{client: c}
	c.Files = &FilesService{client: c}
	c.Credits = &CreditsService{client: c}
	c.Admin = &AdminService{client: c}
	return c
}

// ─── Core HTTP helpers ────────────────────────────────────────────────────────

// do executes an HTTP request with retry logic for retryable errors.
// The response body is the caller's responsibility to close.
func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoff(attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		// Buffer the body so it can be re-read on retry.
		var bodyReader io.Reader
		if body != nil {
			if seeker, ok := body.(io.ReadSeeker); ok {
				if _, err := seeker.Seek(0, io.SeekStart); err != nil {
					return nil, fmt.Errorf("failed to rewind request body: %w", err)
				}
				bodyReader = seeker
			} else {
				bodyReader = body
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
		if err != nil {
			return nil, fmt.Errorf("failed to build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("User-Agent", "miosa-go/"+sdkVersion)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = &ConnectionError{Cause: err}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			apiErr := errorFromResponse(resp)
			if isRetryable(apiErr) && attempt < c.maxRetries {
				lastErr = apiErr
				continue
			}
			return nil, apiErr
		}
		return resp, nil
	}
	return nil, lastErr
}

// getJSON issues a GET request and JSON-decodes the response into out.
func (c *Client) getJSON(ctx context.Context, path string, out interface{}) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// postJSON issues a POST request with a JSON body and decodes the response into out.
// out may be nil when the caller does not need the response body.
func (c *Client) postJSON(ctx context.Context, path string, in, out interface{}) error {
	return c.sendJSON(ctx, http.MethodPost, path, in, out)
}

// deleteJSON issues a DELETE request.
func (c *Client) deleteJSON(ctx context.Context, path string, out interface{}) error {
	return c.sendJSON(ctx, http.MethodDelete, path, nil, out)
}

// patchJSON issues a PATCH request with a JSON body and decodes the response into out.
func (c *Client) patchJSON(ctx context.Context, path string, in, out interface{}) error {
	return c.sendJSON(ctx, http.MethodPatch, path, in, out)
}

// putJSON issues a PUT request with a JSON body and decodes the response into out.
func (c *Client) putJSON(ctx context.Context, path string, in, out interface{}) error {
	return c.sendJSON(ctx, http.MethodPut, path, in, out)
}

// sendJSON is the common implementation for postJSON/deleteJSON.
func (c *Client) sendJSON(ctx context.Context, method, path string, in, out interface{}) error {
	var bodyReader io.ReadSeeker
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}
	resp, err := c.do(ctx, method, path, bodyReader)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil || resp.ContentLength == 0 {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// getRaw issues a GET request and returns the raw response body bytes.
func (c *Client) getRaw(ctx context.Context, path string) ([]byte, string, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response body: %w", err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// postMultipart issues a POST with a prebuilt multipart body.
func (c *Client) postMultipart(ctx context.Context, path string, body io.ReadSeeker, contentType string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("User-Agent", "miosa-go/"+sdkVersion)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &ConnectionError{Cause: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errorFromResponse(resp)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ─── Query string helpers ─────────────────────────────────────────────────────

// buildQuery converts a map to a URL-encoded query string including "?".
// Returns "" if the map is empty.
func buildQuery(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	q := url.Values{}
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// ─── Retry helpers ────────────────────────────────────────────────────────────

// backoff returns the wait duration before attempt n (1-indexed).
// Strategy: capped exponential backoff with full jitter.
func backoff(attempt int) time.Duration {
	cap := 30 * time.Second
	base := 500 * time.Millisecond
	exp := time.Duration(math.Pow(2, float64(attempt-1))) * base
	if exp > cap {
		exp = cap
	}
	// Full jitter: [0, exp)
	jitter := time.Duration(rand.Int63n(int64(exp) + 1))
	return jitter
}

// ─── Credits service ──────────────────────────────────────────────────────────

// CreditsService provides access to credit-related API endpoints.
type CreditsService struct {
	client *Client
}

// Balance returns the current credit balance for the authenticated tenant.
func (s *CreditsService) Balance(ctx context.Context) (*CreditBalance, error) {
	var out CreditBalance
	if err := s.client.getJSON(ctx, "/credits/balance", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Usage returns credit consumption for the current billing period.
func (s *CreditsService) Usage(ctx context.Context) (*CreditUsage, error) {
	var out CreditUsage
	if err := s.client.getJSON(ctx, "/credits/usage", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Transactions returns a paginated list of credit transactions.
func (s *CreditsService) Transactions(ctx context.Context, page, perPage int) (*CreditTransactionListResponse, error) {
	params := map[string]string{}
	if page > 0 {
		params["page"] = strconv.Itoa(page)
	}
	if perPage > 0 {
		params["per_page"] = strconv.Itoa(perPage)
	}
	var out CreditTransactionListResponse
	if err := s.client.getJSON(ctx, "/credits/transactions"+buildQuery(params), &out); err != nil {
		return nil, err
	}
	return &out, nil
}
