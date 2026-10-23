package commands_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func feedbackServer(t *testing.T, status int, got *map[string]any, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/feedback" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		*calls++
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Fatalf("body is not JSON: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusCreated {
			w.Write([]byte(`{"id":"fb_123","status":"new"}`))
		} else {
			w.Write([]byte(`{"error":{"code":"RATE_LIMIT_EXCEEDED","message":"Too many requests"}}`))
		}
	}))
}

func TestFeedbackCommand_SendsMessageWithCLIContext(t *testing.T) {
	var got map[string]any
	calls := 0
	srv := feedbackServer(t, http.StatusCreated, &got, &calls)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := run(t, "feedback", "--type", "bug", "deploy", "hangs", "after", "build")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if got["message"] != "deploy hangs after build" || got["type"] != "bug" || got["source"] != "cli" {
		t.Fatalf("unexpected body: %v", got)
	}
	if !strings.HasPrefix(got["app_version"].(string), "cli-") {
		t.Fatalf("app_version should carry the CLI version: %v", got["app_version"])
	}
	meta := got["metadata"].(map[string]any)
	if meta["os"] != runtime.GOOS || meta["arch"] != runtime.GOARCH || meta["source"] != "cli" {
		t.Fatalf("metadata should carry the OS: %v", meta)
	}
	if !strings.Contains(got["user_agent"].(string), runtime.GOOS+"/"+runtime.GOARCH) {
		t.Fatalf("user_agent should name the platform: %v", got["user_agent"])
	}
	if !strings.Contains(out, "fb_123") {
		t.Fatalf("expected the feedback id in the output, got: %s", out)
	}
}

func TestFeedbackCommand_ReadsStdin(t *testing.T) {
	var got map[string]any
	calls := 0
	srv := feedbackServer(t, http.StatusCreated, &got, &calls)
	defer srv.Close()
	defer setupEnv(t, srv)()

	if out, err := runIn(t, "  line one\nline two\n", "feedback", "--type", "general", "-"); err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, out)
	}
	if got["message"] != "line one\nline two" {
		t.Fatalf("expected the trimmed stdin message, got: %q", got["message"])
	}
}

func TestFeedbackCommand_RefusesBadInputWithoutCalling(t *testing.T) {
	var got map[string]any
	calls := 0
	srv := feedbackServer(t, http.StatusCreated, &got, &calls)
	defer srv.Close()
	defer setupEnv(t, srv)()

	if _, err := run(t, "feedback", "--type", "rant", "hello"); err == nil {
		t.Fatal("expected an unknown --type to fail")
	}
	if _, err := run(t, "feedback", "--type", "general", strings.Repeat("x", 5001)); err == nil {
		t.Fatal("expected a message over 5000 characters to fail")
	}
	if _, err := run(t, "feedback", "--type", "general", "   "); err == nil {
		t.Fatal("expected an empty message to fail")
	}
	if calls != 0 {
		t.Fatalf("expected no API call, got %d", calls)
	}
}

func TestFeedbackCommand_RateLimitIsNotRetried(t *testing.T) {
	var got map[string]any
	calls := 0
	srv := feedbackServer(t, http.StatusTooManyRequests, &got, &calls)
	defer srv.Close()
	defer setupEnv(t, srv)()

	out, err := run(t, "feedback", "--type", "general", "hello")
	if err == nil {
		t.Fatalf("expected the rate limit to fail the command, got: %s", out)
	}
	if !strings.Contains(err.Error()+out, "a lot of feedback") {
		t.Fatalf("expected a readable rate-limit message, got: %v %s", err, out)
	}
	if calls != 1 {
		t.Fatalf("expected exactly one attempt, got %d", calls)
	}
}
