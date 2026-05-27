package miosa

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ─── Error types ─────────────────────────────────────────────────────────────

// MiosaError is the base error type for all API errors returned by this SDK.
type MiosaError struct {
	// StatusCode is the HTTP status code, or 0 for transport-level errors.
	StatusCode int
	// Message is a human-readable description of the error.
	Message string
	// RequestID is the X-Request-ID header from the API response, if present.
	RequestID string
	// Body is the raw response body for further inspection.
	Body []byte
}

func (e *MiosaError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("miosa: %s (status=%d, request_id=%s)", e.Message, e.StatusCode, e.RequestID)
	}
	return fmt.Sprintf("miosa: %s (status=%d)", e.Message, e.StatusCode)
}

// AuthenticationError is returned for 401 responses.
type AuthenticationError struct{ MiosaError }

// PermissionError is returned for 403 responses.
type PermissionError struct{ MiosaError }

// NotFoundError is returned for 404 responses.
type NotFoundError struct{ MiosaError }

// ValidationError is returned for 422 responses.
type ValidationError struct{ MiosaError }

// InsufficientCreditsError is returned for 402 responses.
type InsufficientCreditsError struct{ MiosaError }

// RateLimitError is returned for 429 responses.
// RetryAfter holds the server-suggested delay in seconds (0 if not provided).
type RateLimitError struct {
	MiosaError
	RetryAfter float64
}

// ServerError is returned for 5xx responses.
type ServerError struct{ MiosaError }

// ConnectionError is returned when the SDK cannot reach the API.
type ConnectionError struct {
	Cause error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("miosa: connection error: %v", e.Cause)
}

func (e *ConnectionError) Unwrap() error { return e.Cause }

// ─── Error construction ───────────────────────────────────────────────────────

// errorFromResponse builds the appropriate typed error from an HTTP response.
// It reads and closes the body.
func errorFromResponse(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB cap
	resp.Body.Close()

	requestID := resp.Header.Get("X-Request-ID")
	message := extractMessage(body, resp.StatusCode)

	base := MiosaError{
		StatusCode: resp.StatusCode,
		Message:    message,
		RequestID:  requestID,
		Body:       body,
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return &AuthenticationError{base}
	case http.StatusPaymentRequired:
		return &InsufficientCreditsError{base}
	case http.StatusForbidden:
		return &PermissionError{base}
	case http.StatusNotFound:
		return &NotFoundError{base}
	case http.StatusUnprocessableEntity:
		return &ValidationError{base}
	case http.StatusTooManyRequests:
		ra := parseRetryAfter(body)
		return &RateLimitError{MiosaError: base, RetryAfter: ra}
	default:
		if resp.StatusCode >= 500 {
			return &ServerError{base}
		}
		return &base
	}
}

// extractMessage tries to pull a human-readable message from a JSON body.
func extractMessage(body []byte, statusCode int) string {
	body = []byte(strings.TrimSpace(string(body)))
	if len(body) == 0 {
		return fmt.Sprintf("request failed with status %d", statusCode)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err == nil {
		for _, key := range []string{"message", "error", "detail"} {
			if raw, ok := obj[key]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil && s != "" {
					return s
				}
			}
		}
		// "errors" key may be a list
		if raw, ok := obj["errors"]; ok {
			var list []string
			if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 {
				return list[0]
			}
		}
	}
	// Fall back to the raw body if it's a plain string.
	if s := strings.Trim(string(body), `"`); s != "" {
		return s
	}
	return fmt.Sprintf("request failed with status %d", statusCode)
}

// parseRetryAfter extracts retry_after from a JSON body, returning 0 if absent.
func parseRetryAfter(body []byte) float64 {
	var obj struct {
		RetryAfter float64 `json:"retry_after"`
	}
	_ = json.Unmarshal(body, &obj)
	return obj.RetryAfter
}

// isRetryable reports whether the error should trigger a retry.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	switch err.(type) {
	case *RateLimitError, *ServerError:
		return true
	}
	return false
}
