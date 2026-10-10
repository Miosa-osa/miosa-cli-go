package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Miosa-osa/miosa-cli-go/internal/api"
	"github.com/Miosa-osa/miosa-cli-go/internal/client"
	miosa "github.com/Miosa-osa/miosa-go"
)

// Exit codes. A script can branch on them without parsing messages.
const (
	ExitOK       = 0
	ExitGeneral  = 1
	ExitUsage    = 2
	ExitAuth     = 3 // not signed in, bad key, missing scope
	ExitNotFound = 4
	ExitConflict = 5 // validation, conflict, precondition
	ExitRetry    = 6 // rate limited
	ExitServer   = 7 // 5xx or network
)

// Failure is a command error that has already been turned into a message.
// die() returns one; Execute prints it exactly once.
type Failure struct {
	Err error
	// quiet means the failure was already reported (per-item errors); only the
	// exit status remains.
	quiet bool
}

func (f *Failure) Error() string { return friendlyMessage(f.Err) }
func (f *Failure) Unwrap() error { return f.Err }

// die wraps err so Execute can print it and pick an exit code.
func die(err error) error {
	if err == nil {
		return nil
	}
	var f *Failure
	if errors.As(err, &f) {
		return err
	}
	return &Failure{Err: err}
}

// usageErr marks an error as a usage mistake (exit code 2).
type usageErr struct{ error }

func (u usageErr) Unwrap() error { return u.error }

func usagef(format string, args ...any) error {
	return die(usageErr{fmt.Errorf(format, args...)})
}

// asAPIError converts any API-shaped error (the CLI's own client or the
// vendored SDK) into *api.Error.
func asAPIError(err error) (*api.Error, bool) {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae, true
	}
	var base *miosa.MiosaError
	var ra *miosa.RateLimitError
	switch {
	case errors.As(err, &ra):
		return &api.Error{Status: ra.StatusCode, Code: ra.Code, Message: ra.Message, RequestID: ra.RequestID,
			RetryAfter: time.Duration(ra.RetryAfter * float64(time.Second)), Body: ra.Body}, true
	case errors.As(err, &base):
		return &api.Error{Status: base.StatusCode, Code: base.Code, Message: base.Message, RequestID: base.RequestID, Body: base.Body}, true
	}
	// The SDK's typed errors embed MiosaError; reach it through each wrapper.
	var (
		a  *miosa.AuthenticationError
		pe *miosa.PermissionError
		nf *miosa.NotFoundError
		ve *miosa.ValidationError
		ic *miosa.InsufficientCreditsError
		se *miosa.ServerError
	)
	var m miosa.MiosaError
	switch {
	case errors.As(err, &a):
		m = a.MiosaError
	case errors.As(err, &pe):
		m = pe.MiosaError
	case errors.As(err, &nf):
		m = nf.MiosaError
	case errors.As(err, &ve):
		m = ve.MiosaError
	case errors.As(err, &ic):
		m = ic.MiosaError
	case errors.As(err, &se):
		m = se.MiosaError
	default:
		return nil, false
	}
	return &api.Error{Status: m.StatusCode, Code: m.Code, Message: m.Message, RequestID: m.RequestID, Body: m.Body}, true
}

// friendlyMessage renders an error for a person: the server's message, its
// code and request id, and a hint for the common failures.
func friendlyMessage(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, client.ErrNotAuthenticated) {
		return err.Error()
	}
	if ae, ok := asAPIError(err); ok {
		msg := contextPrefix(err) + ae.Error()
		if strings.EqualFold(ae.Code, "INVALID_ID") && (ae.Message == "" || strings.HasPrefix(ae.Message, "request failed with status")) {
			return "not a valid id, and no sandbox has that name - 'miosa list' shows names and ids" + requestSuffix(ae)
		}
		switch ae.Status {
		case 401:
			return msg + " - sign in with 'miosa login' or check MIOSA_API_KEY"
		case 402:
			return msg + " - add credits at https://miosa.ai/billing"
		case 403:
			if !strings.Contains(strings.ToLower(ae.Message), "scope") {
				return msg + " - this key may lack the required scope or role"
			}
		case 429:
			if ae.RetryAfter > 0 {
				return fmt.Sprintf("%s - retry in %s", msg, ae.RetryAfter.Round(time.Second))
			}
			return msg + " - retry shortly"
		}
		return msg
	}
	var te *api.TransportError
	if errors.As(err, &te) {
		return te.Error()
	}
	var ce *miosa.ConnectionError
	if errors.As(err, &ce) {
		return "cannot reach the MIOSA API: " + ce.Cause.Error()
	}
	var pn *client.ErrPhaseNotReady
	if errors.As(err, &pn) {
		return pn.Error()
	}
	msg := err.Error()
	return strings.TrimPrefix(msg, "miosa: ")
}

// contextPrefix returns the text a caller wrapped around the API error
// ("key validation failed: "), so context survives the friendly rendering.
func contextPrefix(err error) string {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if _, leaf := e.(*api.Error); leaf {
			return strings.TrimSuffix(err.Error(), e.Error())
		}
		if isSDKLeaf(e) {
			return strings.TrimSuffix(err.Error(), e.Error())
		}
	}
	return ""
}

func isSDKLeaf(e error) bool {
	switch e.(type) {
	case *miosa.MiosaError, *miosa.AuthenticationError, *miosa.PermissionError, *miosa.NotFoundError,
		*miosa.ValidationError, *miosa.InsufficientCreditsError, *miosa.RateLimitError, *miosa.ServerError:
		return true
	}
	return false
}

func requestSuffix(ae *api.Error) string {
	if ae.RequestID == "" {
		return ""
	}
	return " (request_id=" + ae.RequestID + ")"
}

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) && ee.Code > 0 && ee.Code < 256 {
		return ee.Code
	}
	var uu usageErr
	if errors.As(err, &uu) || looksLikeUsageError(err) {
		return ExitUsage
	}
	if errors.Is(err, client.ErrNotAuthenticated) {
		return ExitAuth
	}
	if ae, ok := asAPIError(err); ok {
		switch {
		case ae.Status == 401 || ae.Status == 403:
			return ExitAuth
		case ae.Status == 404 || ae.Status == 410:
			return ExitNotFound
		case ae.Status == 429:
			return ExitRetry
		case ae.Status >= 500:
			return ExitServer
		case ae.Status >= 400:
			return ExitConflict
		}
	}
	var te *api.TransportError
	var ce *miosa.ConnectionError
	if errors.As(err, &te) || errors.As(err, &ce) {
		return ExitServer
	}
	return ExitGeneral
}

var usagePrefixes = []string{
	"unknown command", "unknown flag", "unknown shorthand flag", "flag needs an argument",
	"accepts ", "requires at least", "requires at most", "required flag(s)",
	"invalid argument", "if any flags in the group", "none of the flags in the group",
}

// looksLikeUsageError recognizes cobra's own argument and flag errors.
func looksLikeUsageError(err error) bool {
	var f *Failure
	if errors.As(err, &f) {
		return false
	}
	msg := err.Error()
	for _, p := range usagePrefixes {
		if strings.HasPrefix(msg, p) {
			return true
		}
	}
	return false
}

// errorBody is the JSON written to stderr for a failed command in JSON mode.
type errorBody struct {
	Error struct {
		Code      string `json:"code,omitempty"`
		Message   string `json:"message"`
		Status    int    `json:"status,omitempty"`
		RequestID string `json:"request_id,omitempty"`
		Retryable bool   `json:"retryable,omitempty"`
		ExitCode  int    `json:"exit_code"`
	} `json:"error"`
}

// writeError prints err to w as a single line of text, or as one JSON object
// when asJSON is set.
func writeError(w io.Writer, err error, asJSON bool) {
	var qf *Failure
	if errors.As(err, &qf) && qf.quiet {
		return
	}
	var ee *ExitError
	if errors.As(err, &ee) && !asJSON {
		// The remote command already wrote its own output; the exit code says the rest.
		return
	}
	if !asJSON {
		fmt.Fprintf(w, "miosa: %s\n", friendlyMessage(err))
		return
	}
	var b errorBody
	b.Error.Message = friendlyMessage(err)
	b.Error.ExitCode = ExitCode(err)
	if ae, ok := asAPIError(err); ok {
		b.Error.Code, b.Error.Status, b.Error.RequestID = ae.Code, ae.Status, ae.RequestID
		b.Error.Retryable = ae.Retryable || ae.Status == 429 || ae.Status == 503
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(b)
}
