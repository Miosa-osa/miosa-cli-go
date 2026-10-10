package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
)

// snapshotAPI is the thin layer behind `miosa snapshot`. Every request goes
// through the CLI's shared client (internal/api), so authentication, the
// organization header, retries, request ids and typed errors behave exactly
// like every other command, and a failure maps to the same exit code.
type snapshotAPI struct {
	c *api.Client
}

func newSnapshotAPI() (*snapshotAPI, error) {
	cl, _, err := buildClient()
	if err != nil {
		return nil, err
	}
	return &snapshotAPI{c: cl.API}, nil
}

// call performs one JSON request and decodes the body into out (may be nil).
// It returns the HTTP status so callers can tell 200 from 202 (a snapshot
// whose disk is still being unpacked).
func (s *snapshotAPI) call(ctx context.Context, method, path string, body, out interface{}) (int, error) {
	return s.callRequest(ctx, api.Request{Method: method, Path: path, Body: body}, out)
}

// callKeyed is call for a request that creates a machine. The idempotency key
// makes a retry after a lost response create nothing twice.
func (s *snapshotAPI) callKeyed(ctx context.Context, method, path string, body, out interface{}) (int, error) {
	return s.callRequest(ctx, api.Request{Method: method, Path: path, Body: body, IdempotencyKey: snapIdemKey()}, out)
}

func (s *snapshotAPI) callRequest(ctx context.Context, req api.Request, out interface{}) (int, error) {
	resp, err := s.c.Do(ctx, req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if err := decodeSnapshotBody(resp, out); err != nil {
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

// callAllowing is like call, but also decodes the body of the listed non-2xx
// statuses instead of treating them as failures. A 409 from bulk delete means
// everything asked for was blocked, and its body still says why.
func (s *snapshotAPI) callAllowing(ctx context.Context, method, path string, body, out interface{}, allowed ...int) (int, error) {
	status, err := s.callRequest(ctx, api.Request{Method: method, Path: path, Body: body, NoRetry: true}, out)
	var ae *api.Error
	if err == nil || !errors.As(err, &ae) {
		return status, err
	}
	for _, s := range allowed {
		if ae.Status == s {
			if out != nil && len(ae.Body) > 0 {
				if jerr := json.Unmarshal(ae.Body, out); jerr != nil {
					return ae.Status, fmt.Errorf("decoding response: %w", jerr)
				}
			}
			return ae.Status, nil
		}
	}
	return status, err
}

func decodeSnapshotBody(resp *http.Response, out interface{}) error {
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

// open sends a GET that returns a body to stream (a file listing, a download).
// It has no overall timeout, so a large download is not cut off; retries still
// cover the initial connection.
func (s *snapshotAPI) open(ctx context.Context, path string) (*http.Response, error) {
	return s.c.Stream(ctx, api.Request{Method: http.MethodGet, Path: path})
}

// snapPollInterval is how often a still-warming snapshot is asked again.
var snapPollInterval = 2 * time.Second

// pollWarming repeats a request while the server answers 202 "warming" (the
// snapshot's disk is being unpacked in the background). The first call can
// take a while for a large disk, so it waits up to `wait`.
func (s *snapshotAPI) pollWarming(ctx context.Context, wait time.Duration, do func() (*http.Response, error)) (*http.Response, error) {
	deadline := time.Now().Add(wait)
	for {
		resp, err := do()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusAccepted {
			return resp, nil
		}
		resp.Body.Close()
		if time.Now().After(deadline) {
			return nil, &api.Error{
				Status:    http.StatusServiceUnavailable,
				Code:      "BROWSE_UNAVAILABLE",
				Message:   "the snapshot is still being prepared for browsing; try again in a minute",
				Retryable: true,
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(snapPollInterval):
		}
	}
}

func snapQuery(pairs map[string]string) string {
	v := url.Values{}
	for k, val := range pairs {
		if val != "" {
			v.Set(k, val)
		}
	}
	if len(v) == 0 {
		return ""
	}
	return "?" + v.Encode()
}

func snapIdemKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("snap-%d", time.Now().UnixNano())
	}
	return "snap-" + hex.EncodeToString(b)
}
