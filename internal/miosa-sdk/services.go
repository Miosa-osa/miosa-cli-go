package miosa

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ServicesService manages long-running services on a computer.
// Accessed via Computer.Services.
type ServicesService struct {
	client     *Client
	computerID string
}

func (s *ServicesService) base() string {
	return fmt.Sprintf("/computers/%s/services", s.computerID)
}

// Create registers a new service on the computer.
func (s *ServicesService) Create(ctx context.Context, input CreateServiceInput) (*Service, error) {
	const op = "ServicesService.Create"
	var out struct {
		Data Service `json:"data"`
	}
	if err := s.client.postJSON(ctx, s.base(), input, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// List returns all services registered on this computer.
func (s *ServicesService) List(ctx context.Context) ([]Service, error) {
	const op = "ServicesService.List"
	var out struct {
		Data []Service `json:"data"`
	}
	if err := s.client.getJSON(ctx, s.base(), &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out.Data, nil
}

// Get fetches a single service by ID.
func (s *ServicesService) Get(ctx context.Context, id string) (*Service, error) {
	const op = "ServicesService.Get"
	var out struct {
		Data Service `json:"data"`
	}
	if err := s.client.getJSON(ctx, s.base()+"/"+id, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Start starts a stopped service.
func (s *ServicesService) Start(ctx context.Context, id string) (*Service, error) {
	const op = "ServicesService.Start"
	var out struct {
		Data Service `json:"data"`
	}
	if err := s.client.postJSON(ctx, fmt.Sprintf("%s/%s/start", s.base(), id), nil, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Stop stops a running service.
func (s *ServicesService) Stop(ctx context.Context, id string) (*Service, error) {
	const op = "ServicesService.Stop"
	var out struct {
		Data Service `json:"data"`
	}
	if err := s.client.postJSON(ctx, fmt.Sprintf("%s/%s/stop", s.base(), id), nil, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Restart restarts a service.
func (s *ServicesService) Restart(ctx context.Context, id string) (*Service, error) {
	const op = "ServicesService.Restart"
	var out struct {
		Data Service `json:"data"`
	}
	if err := s.client.postJSON(ctx, fmt.Sprintf("%s/%s/restart", s.base(), id), nil, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return &out.Data, nil
}

// Delete permanently removes a service.
func (s *ServicesService) Delete(ctx context.Context, id string) error {
	const op = "ServicesService.Delete"
	if err := s.client.deleteJSON(ctx, s.base()+"/"+id, nil); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// Logs opens a Server-Sent Events stream of log lines for the given service.
// Returns a *ServiceLogStream whose channel yields ServiceLogEvent values.
// The channel is closed when the stream ends or the context is cancelled.
func (s *ServicesService) Logs(ctx context.Context, id string) (*ServiceLogStream, error) {
	const op = "ServicesService.Logs"
	path := fmt.Sprintf("%s/%s/logs", s.base(), id)
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

	ch := make(chan ServiceLogEvent, 64)
	stream := &ServiceLogStream{ch: ch}
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		parseServiceLogSSE(ctx, resp, ch)
	}()
	return stream, nil
}

// parseServiceLogSSE reads an SSE stream and sends ServiceLogEvent values to ch.
func parseServiceLogSSE(ctx context.Context, resp *http.Response, ch chan<- ServiceLogEvent) {
	scanner := bufio.NewScanner(resp.Body)
	var dataBuf strings.Builder

	flush := func() {
		raw := strings.TrimSpace(dataBuf.String())
		dataBuf.Reset()
		if raw == "" {
			return
		}
		var ev ServiceLogEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			// Best-effort: treat raw line as log message.
			ev = ServiceLogEvent{Message: raw}
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
		}
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
			flush()
		case strings.HasPrefix(line, "data:"):
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
		}
	}
	flush()
}
