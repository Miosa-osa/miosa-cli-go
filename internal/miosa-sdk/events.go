package miosa

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/gorilla/websocket"
)

// EventsService subscribes to real-time in-VM events over WebSocket.
// Accessed via Computer.Events.
type EventsService struct {
	client     *Client
	computerID string
}

// Subscribe opens a WebSocket connection for the specified event producers and
// returns an EventStream. The stream's channel yields Event values; the channel
// is closed when the connection closes or the context is cancelled.
//
// At least one producer must be specified.
//
//	stream, err := computer.Events.Subscribe(ctx, EventSubscribeOptions{
//	    Subscribe: []EventProducer{ProducerWindow, ProducerFile},
//	    Paths:     []string{"/home/user"},
//	})
//	if err != nil { ... }
//	defer stream.Close()
//	for ev := range stream.C {
//	    fmt.Println(ev.Type, ev.Payload)
//	}
func (s *EventsService) Subscribe(ctx context.Context, opts EventSubscribeOptions) (*EventStream, error) {
	const op = "EventsService.Subscribe"

	wsURL, err := s.buildWSURL(opts)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	header := make(map[string][]string)
	header["Authorization"] = []string{"Bearer " + s.client.apiKey}
	header["User-Agent"] = []string{"miosa-go/" + sdkVersion}

	dialer := websocket.DefaultDialer
	conn, _, err := dialer.DialContext(ctx, wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	ch := make(chan Event, 32)
	stream := &EventStream{conn: conn, ch: ch}
	go stream.readPump(ctx)
	return stream, nil
}

func (s *EventsService) buildWSURL(opts EventSubscribeOptions) (string, error) {
	base := s.client.baseURL
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)

	params := url.Values{}
	params.Set("subscribe", strings.Join(producersToStrings(opts.Subscribe), ","))
	if len(opts.Paths) > 0 {
		params.Set("paths", strings.Join(opts.Paths, ","))
	}
	if opts.IdleThresholdSec > 0 {
		params.Set("idle_threshold_sec", fmt.Sprintf("%d", opts.IdleThresholdSec))
	}

	return fmt.Sprintf("%s/computers/%s/events?%s", base, s.computerID, params.Encode()), nil
}

func producersToStrings(ps []EventProducer) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = string(p)
	}
	return out
}

// ─── EventStream ──────────────────────────────────────────────────────────────

// EventStream is a live WebSocket subscription to in-VM events.
// Drain the C channel or call Close to avoid goroutine leaks.
type EventStream struct {
	conn *websocket.Conn
	ch   chan Event
	// C is the read-only view of the channel exposed to callers.
	C <-chan Event
}

func (s *EventStream) init() {
	s.C = s.ch
}

// Close closes the WebSocket connection and drains the channel.
func (s *EventStream) Close() error {
	return s.conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client closed"))
}

// readPump reads frames until the connection closes or ctx is cancelled.
func (s *EventStream) readPump(ctx context.Context) {
	s.C = s.ch
	defer close(s.ch)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_, msg, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		var raw struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(msg, &raw); err != nil {
			continue
		}
		ev := Event{
			Type:      EventType(raw.Type),
			Timestamp: raw.Timestamp,
		}
		ev.Payload = decodeEventPayload(raw.Type, raw.Payload)
		select {
		case s.ch <- ev:
		case <-ctx.Done():
			return
		}
	}
}

// decodeEventPayload returns a typed payload struct for known event types.
// Unknown types return the raw json.RawMessage.
func decodeEventPayload(evType string, raw json.RawMessage) interface{} {
	switch evType {
	case "window.focus_changed":
		var p WindowFocusChangedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "window.opened":
		var p WindowOpenedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "window.closed":
		var p WindowClosedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "clipboard.changed":
		var p ClipboardChangedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "file.created":
		var p FileCreatedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "file.modified":
		var p FileModifiedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "file.deleted":
		var p FileDeletedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "process.started":
		var p ProcessStartedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "process.stopped":
		var p ProcessStoppedPayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "idle.inactive":
		var p IdleInactivePayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "idle.active":
		var p IdleActivePayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	case "producer.unavailable":
		var p ProducerUnavailablePayload
		if json.Unmarshal(raw, &p) == nil {
			return p
		}
	}
	return raw
}
