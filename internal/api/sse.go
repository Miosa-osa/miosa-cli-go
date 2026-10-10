package api

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Event is one server-sent event.
type Event struct {
	ID    string
	Name  string
	Data  string
	Retry string
}

// ReadSSE parses a text/event-stream body and calls fn for each event until
// the stream ends, ctx is cancelled, or fn returns an error. Comment lines
// (": keepalive") are skipped.
func ReadSSE(ctx context.Context, r io.Reader, fn func(Event) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var ev Event
	var data []string
	flush := func() error {
		if len(data) == 0 && ev.Name == "" && ev.ID == "" {
			return nil
		}
		ev.Data = strings.Join(data, "\n")
		out := ev
		ev, data = Event{}, nil
		return fn(out)
	}
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.Name = value
		case "data":
			data = append(data, value)
		case "id":
			ev.ID = value
		case "retry":
			ev.Retry = value
		}
	}
	if err := flush(); err != nil {
		return err
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		return err
	}
	return ctx.Err()
}

// Events opens an SSE endpoint and streams events to fn.
func (c *Client) Events(ctx context.Context, path string, q url.Values, fn func(Event) error) error {
	resp, err := c.Stream(ctx, Request{Method: http.MethodGet, Path: path, Query: q, Headers: map[string]string{"Accept": "text/event-stream"}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return ReadSSE(ctx, resp.Body, fn)
}
