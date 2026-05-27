package miosa

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SnapshotsService manages Firecracker snapshots scoped to a single computer.
// Accessed via Computer.Snapshots.
type SnapshotsService struct {
	client     *Client
	computerID string
}

func (s *SnapshotsService) base() string {
	return fmt.Sprintf("/computers/%s/snapshots", s.computerID)
}

// Create captures a snapshot of the running computer.
// The returned Snapshot starts in "creating" status and progresses
// asynchronously to "ready". Poll Get or consume a SnapshotStream to track
// progress.
func (s *SnapshotsService) Create(ctx context.Context, input CreateSnapshotInput) (*Snapshot, error) {
	const op = "SnapshotsService.Create"
	var out struct {
		Data Snapshot `json:"data"`
	}
	if err := s.client.postJSON(ctx, s.base(), input, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// List returns all non-deleted snapshots for this computer.
func (s *SnapshotsService) List(ctx context.Context) ([]Snapshot, error) {
	const op = "SnapshotsService.List"
	var out struct {
		Data []Snapshot `json:"data"`
	}
	if err := s.client.getJSON(ctx, s.base(), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out.Data, nil
}

// Get fetches a single snapshot by ID.
func (s *SnapshotsService) Get(ctx context.Context, id string) (*Snapshot, error) {
	const op = "SnapshotsService.Get"
	var out struct {
		Data Snapshot `json:"data"`
	}
	if err := s.client.getJSON(ctx, s.base()+"/"+id, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Delete marks a snapshot as deleted and schedules storage cleanup.
func (s *SnapshotsService) Delete(ctx context.Context, id string) (*Snapshot, error) {
	const op = "SnapshotsService.Delete"
	var out struct {
		Data Snapshot `json:"data"`
	}
	if err := s.client.sendJSON(ctx, http.MethodDelete, s.base()+"/"+id, nil, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Events opens an SSE stream for snapshot progress and returns a SnapshotStream.
// The ticket is a short-lived token obtained from POST /api/v1/auth/sse-ticket.
// The channel is closed when the stream ends, the context is cancelled, or a
// terminal status (ready / failed / deleted) is received.
func (s *SnapshotsService) Events(ctx context.Context, id, ticket string) (*SnapshotStream, error) {
	const op = "SnapshotsService.Events"
	path := fmt.Sprintf("%s/%s/events?ticket=%s", s.base(), id, ticket)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.client.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.client.apiKey)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "miosa-go/"+sdkVersion)

	resp, err := s.client.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: %w", op, ctx.Err())
		}
		return nil, fmt.Errorf("%s: %w", op, &ConnectionError{Cause: err})
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %w", op, errorFromResponse(resp))
	}

	ch := make(chan SnapshotProgressEvent, 16)
	stream := &SnapshotStream{ch: ch, C: ch}
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		parseSnapshotSSE(ctx, resp, ch)
	}()
	return stream, nil
}

// parseSnapshotSSE reads the SSE stream and sends events to ch until a terminal
// state is reached, the context is cancelled, or the connection closes.
func parseSnapshotSSE(ctx context.Context, resp *http.Response, ch chan<- SnapshotProgressEvent) {
	scanner := bufio.NewScanner(resp.Body)
	terminalStatuses := map[string]bool{
		"ready":   true,
		"failed":  true,
		"deleted": true,
	}

	var dataBuf strings.Builder

	flush := func() bool {
		raw := strings.TrimSpace(dataBuf.String())
		dataBuf.Reset()
		if raw == "" {
			return false
		}
		var ev SnapshotProgressEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return false
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
			return false
		}
		return terminalStatuses[ev.Status]
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
			if flush() {
				return
			}
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		case strings.HasPrefix(line, "event:"), strings.HasPrefix(line, ":"),
			strings.HasPrefix(line, "id:"), strings.HasPrefix(line, "retry:"):
			// Ignore non-data SSE fields.
		}
	}
	flush()
}

// ─── Client-level restore ─────────────────────────────────────────────────────

// RestoreComputer provisions a fresh computer from the given snapshot ID.
// The new computer starts in "provisioning" status.
func (c *Client) RestoreComputer(ctx context.Context, snapshotID string) (*Computer, error) {
	const op = "Client.RestoreComputer"
	var out struct {
		Data ComputerData `json:"data"`
	}
	if err := c.postJSON(ctx, fmt.Sprintf("/snapshots/%s/restore", snapshotID), nil, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return c.Computers.wrap(out.Data), nil
}
