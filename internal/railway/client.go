// Package railway reads a Railway project through Railway's public GraphQL API
// and turns it into a MIOSA import plan. It never calls MIOSA: the plan is the
// only seam, so a plan can be reviewed, saved, diffed and applied later.
package railway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint is Railway's public GraphQL API.
const DefaultEndpoint = "https://backboard.railway.com/graphql/v2"

// Client is a Railway GraphQL client that holds a token only in memory.
type Client struct {
	Endpoint string
	Token    string
	HTTP     *http.Client
	// MaxRetries is the number of retries after the first attempt for 429 and 5xx.
	MaxRetries int
	// RetryBase is the first backoff; it doubles each attempt.
	RetryBase time.Duration
	// DumpDir, when set, writes every query and answer there so fixtures can be
	// re-recorded when Railway's schema moves. Tokens and secret values are not
	// written: only the query, its variables and the answer body.
	DumpDir string
	dumps   int
}

// NewClient returns a client for Railway's public API.
func NewClient(token string) *Client {
	return &Client{
		Endpoint:   DefaultEndpoint,
		Token:      token,
		HTTP:       &http.Client{Timeout: 30 * time.Second},
		MaxRetries: 3,
		RetryBase:  500 * time.Millisecond,
	}
}

type graphQLBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Query runs one GraphQL document and decodes its data into out.
// Railway answers 200 with an errors array for a rejected query, so both the
// status and the errors are checked.
func (c *Client) Query(ctx context.Context, query string, variables map[string]any, out any) error {
	payload, err := json.Marshal(graphQLBody{Query: query, Variables: variables})
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.RetryBase << (attempt - 1)):
			}
		}
		status, body, retryAfter, err := c.post(ctx, payload)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		if status == http.StatusTooManyRequests || status >= 500 {
			lastErr = fmt.Errorf("railway: HTTP %d", status)
			if retryAfter > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryAfter):
				}
			}
			continue
		}

		var resp graphQLResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return fmt.Errorf("railway: unexpected answer (HTTP %d)", status)
		}
		if len(resp.Errors) > 0 {
			return fmt.Errorf("railway: %s", joinErrors(resp.Errors))
		}
		if status != http.StatusOK {
			return fmt.Errorf("railway: HTTP %d", status)
		}
		c.dump(query, variables, resp.Data)
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return fmt.Errorf("railway: unexpected answer: %w", err)
		}
		return nil
	}
	return lastErr
}

// dump writes one exchange for fixture recording.
func (c *Client) dump(query string, variables map[string]any, data json.RawMessage) {
	dump(c.DumpDir, &c.dumps, query, variables, data)
}

// dump is shared by the token client and the Railway CLI client.
func dump(dir string, counter *int, query string, variables map[string]any, data json.RawMessage) {
	if dir == "" {
		return
	}
	*counter++
	name := fmt.Sprintf("%02d-%s.json", *counter, operationName(query))
	body, err := json.MarshalIndent(map[string]any{
		"query":     query,
		"variables": variables,
		"data":      redactVariables(data),
	}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, name), body, 0o600)
}

// redactVariables keeps a recorded fixture useful without recording secrets: a
// variable reference is kept, a literal value becomes "<redacted>".
func redactVariables(data json.RawMessage) json.RawMessage {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return data
	}
	redactIn(decoded)
	body, err := json.Marshal(decoded)
	if err != nil {
		return data
	}
	return body
}

func redactIn(value any) {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			if key == "variables" {
				if values, ok := child.(map[string]any); ok {
					for name, v := range values {
						text, isText := v.(string)
						if !isText || !strings.Contains(text, "${{") {
							values[name] = "<redacted>"
						}
					}
					continue
				}
			}
			redactIn(child)
		}
	case []any:
		for _, child := range node {
			redactIn(child)
		}
	}
}

// operationName is the query's name (the word after "query"), for the file name.
func operationName(query string) string {
	fields := strings.Fields(query)
	if len(fields) >= 2 && fields[0] == "query" {
		name := fields[1]
		if i := strings.IndexByte(name, '('); i > 0 {
			name = name[:i]
		}
		return strings.ToLower(name)
	}
	return "query"
}

func (c *Client) post(ctx context.Context, payload []byte) (int, []byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, 0, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+c.Token)
	req.Header.Set("user-agent", "miosa-cli (import railway)")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, 0, err
	}
	var retryAfter time.Duration
	if v := resp.Header.Get("retry-after"); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil && seconds > 0 && seconds <= 60 {
			retryAfter = time.Duration(seconds) * time.Second
		}
	}
	return resp.StatusCode, body, retryAfter, nil
}

func joinErrors(errs []struct {
	Message string `json:"message"`
}) string {
	messages := make([]string, 0, len(errs))
	for _, e := range errs {
		if e.Message != "" {
			messages = append(messages, e.Message)
		}
	}
	if len(messages) == 0 {
		return "the request was rejected"
	}
	return strings.Join(messages, "; ")
}

// ErrNotFound is returned when a project, service or environment does not exist
// or the token cannot see it.
var ErrNotFound = errors.New("not found")
