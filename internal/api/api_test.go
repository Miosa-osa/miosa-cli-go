package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(srv *httptest.Server) *Client {
	c := New(srv.URL, "msk_u_test")
	c.RetryBase = time.Millisecond
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestParseErrorShapes(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		header     map[string]string
		wantCode   string
		wantMsg    string
		wantRID    string
		wantRetry  bool
		wantAfter  time.Duration
		wantString string
	}{
		{"nested", 400, `{"error":{"code":"INVALID_PATH","message":"bad path"}}`, nil, "INVALID_PATH", "bad path", "", false, 0, "bad path [INVALID_PATH]"},
		{"flat", 401, `{"error":"Token is invalid","code":"INVALID_TOKEN"}`, nil, "INVALID_TOKEN", "Token is invalid", "", false, 0, "Token is invalid [INVALID_TOKEN]"},
		{"code only", 404, `{"error":{"code":"NOT_FOUND"}}`, nil, "NOT_FOUND", "", "", false, 0, "Not Found [NOT_FOUND]"},
		{"request id header", 500, `{"error":{"code":"X","message":"boom","retryable":true}}`, map[string]string{"X-Request-Id": "rid1"}, "X", "boom", "rid1", true, 0, "boom [X] (request_id=rid1)"},
		{"retry after", 429, `{"error":{"code":"RATE_LIMITED","message":"slow down"}}`, map[string]string{"Retry-After": "2"}, "RATE_LIMITED", "slow down", "", false, 2 * time.Second, "slow down [RATE_LIMITED]"},
		{"plain text", 502, `upstream down`, nil, "", "upstream down", "", false, 0, "upstream down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			for k, v := range tc.header {
				rec.Header().Set(k, v)
			}
			rec.WriteHeader(tc.status)
			rec.WriteString(tc.body)
			e := ParseError(rec.Result())
			if e.Status != tc.status || e.Code != tc.wantCode || e.Message != tc.wantMsg || e.RequestID != tc.wantRID || e.Retryable != tc.wantRetry || e.RetryAfter != tc.wantAfter {
				t.Fatalf("got %+v", e)
			}
			if e.Error() != tc.wantString {
				t.Fatalf("Error() = %q, want %q", e.Error(), tc.wantString)
			}
		})
	}
}

func TestJSONSendsAuthAndBody(t *testing.T) {
	var gotAuth, gotCT, gotBody, gotUA, gotOrg string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCT, gotUA, gotOrg = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("User-Agent"), r.Header.Get("X-Miosa-Org")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	c.UserAgent = "miosa-cli/1.2.3"
	c.Headers = map[string]string{"X-Miosa-Org": "acme"}
	var out struct{ OK bool }
	if err := c.Post(context.Background(), "/x", map[string]any{"a": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || gotAuth != "Bearer msk_u_test" || gotCT != "application/json" || gotBody != `{"a":1}` || gotUA != "miosa-cli/1.2.3" || gotOrg != "acme" {
		t.Fatalf("auth=%q ct=%q body=%q ua=%q org=%q out=%+v", gotAuth, gotCT, gotBody, gotUA, gotOrg, out)
	}
}

func TestQueryEncoding(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	q := url.Values{}
	q.Set("path", "/home/user/a b")
	if err := testClient(srv).Get(context.Background(), "/fs", q, nil); err != nil {
		t.Fatal(err)
	}
	if got != "path=%2Fhome%2Fuser%2Fa+b" {
		t.Fatalf("query = %q", got)
	}
}

func TestRetryPolicy(t *testing.T) {
	cases := []struct {
		name      string
		method    string
		key       string
		statuses  []int
		body      string
		wantCalls int
		wantErr   bool
	}{
		{"GET 503 then ok", "GET", "", []int{503, 200}, `{}`, 2, false},
		{"GET 429 then ok", "GET", "", []int{429, 429, 200}, `{}`, 3, false},
		{"POST 429 retried", "POST", "", []int{429, 200}, `{}`, 2, false},
		{"POST 502 not retried", "POST", "", []int{502, 200}, `{}`, 1, true},
		{"POST 502 retried with idempotency key", "POST", "k1", []int{502, 200}, `{}`, 2, false},
		{"GET 500 retryable", "GET", "", []int{500, 200}, `{"error":{"code":"E","retryable":true}}`, 2, false},
		{"POST 500 retryable not retried", "POST", "", []int{500, 200}, `{"error":{"code":"E","retryable":true}}`, 1, true},
		{"GET 404 never", "GET", "", []int{404, 200}, `{}`, 1, true},
		{"GET 400 never", "GET", "", []int{400, 200}, `{}`, 1, true},
		{"retries exhausted", "GET", "", []int{503, 503, 503, 503, 503}, `{}`, 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := int(atomic.AddInt32(&calls, 1)) - 1
				st := tc.statuses[len(tc.statuses)-1]
				if i < len(tc.statuses) {
					st = tc.statuses[i]
				}
				if st >= 300 {
					w.WriteHeader(st)
				}
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			err := testClient(srv).JSON(context.Background(), Request{Method: tc.method, Path: "/x", IdempotencyKey: tc.key}, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if int(calls) != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

func TestRetryAfterHonoured(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := testClient(srv)
	var slept time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error { slept = d; return nil }
	if err := c.Get(context.Background(), "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if slept != 3*time.Second {
		t.Fatalf("slept %v, want 3s", slept)
	}
}

func TestTransportErrorRetriesIdempotentOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now unreachable
	c := New(url, "k")
	c.RetryBase = time.Millisecond
	var sleeps int
	c.Sleep = func(context.Context, time.Duration) error { sleeps++; return nil }
	err := c.Get(context.Background(), "/x", nil, nil)
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("want TransportError, got %T %v", err, err)
	}
	if sleeps != 3 {
		t.Fatalf("GET sleeps = %d, want 3", sleeps)
	}
	sleeps = 0
	if err := c.Post(context.Background(), "/x", nil, nil); !errors.As(err, &te) {
		t.Fatalf("want TransportError, got %v", err)
	}
	if sleeps != 0 {
		t.Fatalf("POST must not retry a transport error, slept %d", sleeps)
	}
}

func TestContextCancelStopsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer srv.Close()
	c := testClient(srv)
	ctx, cancel := context.WithCancel(context.Background())
	c.Sleep = func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }
	err := c.Get(ctx, "/x", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestIsStatusAndCode(t *testing.T) {
	err := &Error{Status: 404, Code: "NOT_FOUND"}
	wrapped := errors.Join(errors.New("ctx"), err)
	if !IsStatus(wrapped, 404) || IsStatus(wrapped, 400) || !IsCode(wrapped, "not_found") || IsCode(nil, "x") {
		t.Fatal("IsStatus/IsCode")
	}
}

func TestReadSSE(t *testing.T) {
	stream := ": keepalive\n\nevent: run.created\nid: 1\ndata: {\"a\":1}\n\ndata: line1\ndata: line2\n\nevent: last\ndata: x"
	var got []Event
	err := ReadSSE(context.Background(), strings.NewReader(stream), func(e Event) error {
		got = append(got, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "run.created" || got[0].ID != "1" || got[0].Data != `{"a":1}` || got[1].Data != "line1\nline2" || got[2].Name != "last" || got[2].Data != "x" {
		t.Fatalf("events = %+v", got)
	}
}

func TestEventsStopsOnCallbackError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: a\n\ndata: b\n\n")
	}))
	defer srv.Close()
	stop := errors.New("stop")
	var n int
	err := testClient(srv).Events(context.Background(), "/events", nil, func(Event) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("err=%v n=%d", err, n)
	}
}
