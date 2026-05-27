package miosa

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// AgentService manages CUA (Computer-Use Agent) sessions.
// When accessed via Client.Agent it is unscoped; when accessed via
// Computer.Agent it is scoped to that computer's ID.
type AgentService struct {
	client     *Client
	computerID string
}

// For returns an AgentService scoped to the given computer ID.
func (s *AgentService) For(computerID string) *AgentService {
	return &AgentService{client: s.client, computerID: computerID}
}

func (s *AgentService) base() string {
	return fmt.Sprintf("/computers/%s/cua", s.computerID)
}

// ─── Session lifecycle ────────────────────────────────────────────────────────

// Run creates and starts a new agent session.
// The session begins executing immediately on the server.
func (s *AgentService) Run(ctx context.Context, input RunAgentInput) (*AgentSessionData, error) {
	var out AgentSessionData
	if err := s.client.postJSON(ctx, s.base()+"/sessions", input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Get returns the current state of a session.
func (s *AgentService) Get(ctx context.Context, sessionID string) (*AgentSessionData, error) {
	var out AgentSessionData
	if err := s.client.getJSON(ctx, fmt.Sprintf("/cua/sessions/%s", sessionID), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List returns all sessions. When s is computer-scoped the list is filtered to
// that computer; otherwise all sessions are returned.
func (s *AgentService) List(ctx context.Context) (*AgentSessionListResponse, error) {
	var path string
	if s.computerID != "" {
		path = s.base() + "/sessions"
	} else {
		path = "/cua/sessions"
	}
	var out AgentSessionListResponse
	if err := s.client.getJSON(ctx, path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Cancel requests cancellation of a running session.
func (s *AgentService) Cancel(ctx context.Context, sessionID string) error {
	return s.client.deleteJSON(ctx, fmt.Sprintf("/cua/sessions/%s", sessionID), nil)
}

// ─── SSE streaming ────────────────────────────────────────────────────────────

// Stream opens an SSE connection to the session event stream and returns a
// channel of AgentEvents. The channel is closed when the stream ends, the
// context is cancelled, or an unrecoverable error occurs.
//
// Any parse errors are sent as events with Type == EventError.
// Callers must drain or discard the channel; it will not block the server.
func (s *AgentService) Stream(ctx context.Context, sessionID string) (<-chan AgentEvent, error) {
	path := fmt.Sprintf("/cua/sessions/%s/events", sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.client.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("miosa: failed to build SSE request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.client.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "miosa-go/"+sdkVersion)

	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &ConnectionError{Cause: err}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, errorFromResponse(resp)
	}

	ch := make(chan AgentEvent, 32)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		parseSSE(ctx, resp, ch)
	}()
	return ch, nil
}

// parseSSE reads the SSE stream, decoding events and sending them to ch.
// It returns when the stream ends, a "done" sentinel event is dispatched,
// or ctx is cancelled.
func parseSSE(ctx context.Context, resp *http.Response, ch chan<- AgentEvent) {
	scanner := bufio.NewScanner(resp.Body)

	var eventType string
	var dataBuf strings.Builder
	var done bool

	// flush dispatches the current buffered event to ch.
	// Returns true if a "done" event was dispatched.
	flush := func() bool {
		raw := strings.TrimSpace(dataBuf.String())
		dataBuf.Reset()
		evTypeName := eventType
		eventType = ""

		// No data — nothing to emit (e.g. a bare empty line).
		if raw == "" {
			return false
		}

		evType := AgentEventType(evTypeName)
		if evType == "" {
			evType = "message"
		}

		// Attempt to JSON-decode the data payload.
		var decoded interface{}
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			decoded = raw
		}

		ev := AgentEvent{
			Type: evType,
			Data: decoded,
		}

		// Extract well-known top-level fields from JSON payloads.
		if m, ok := decoded.(map[string]interface{}); ok {
			if sid, ok := m["session_id"].(string); ok {
				ev.SessionID = sid
			}
			if ts, ok := m["timestamp"].(string); ok {
				ev.Timestamp = ts
			}
		}

		select {
		case ch <- ev:
		case <-ctx.Done():
			return false
		}

		return evType == EventDone
	}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()

		switch {
		case line == "":
			// Empty line → dispatch buffered event.
			done = flush()
			if done {
				return
			}

		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))

		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)

		case strings.HasPrefix(line, ":"):
			// SSE comment — ignore.

		case strings.HasPrefix(line, "id:"), strings.HasPrefix(line, "retry:"):
			// Last-event-ID and retry hints — ignore for now.

		default:
			// Unrecognized field — ignore per SSE spec.
		}
	}

	// Flush any pending event buffered before EOF.
	flush()
}
