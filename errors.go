package owls

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors an [*APIError] matches with errors.Is, one per HTTP status the
// API gives a meaning to.
var (
	ErrUnauthorized = errors.New("owls: unauthorized (401)")
	ErrForbidden    = errors.New("owls: forbidden (403)")
	ErrNotFound     = errors.New("owls: not found (404)")
	ErrRateLimited  = errors.New("owls: rate limited (429)")
	ErrServiceBusy  = errors.New("owls: service busy (503)")
)

// SgpNoPriceCode is the Code of the SGP builder's 503 when the price request
// produced no usable answer. Retry after RetryAfter (2 s when the server sent no
// Retry-After header).
const SgpNoPriceCode = "no-price"

const sgpNoPriceRetryAfter = 2 * time.Second

// APIError is a response the API answered with a non-2xx status.
type APIError struct {
	// Status is the HTTP status code.
	Status int
	// Code is the API's own code when it sent one. 401, 403 and 404 always carry
	// UNAUTHORIZED, FORBIDDEN and NOT_FOUND; a 429 without a code is RATE_LIMITED
	// (HISTORY_CONCURRENCY and SGP_RATE_LIMIT are the API's own), a 503 without
	// one is SERVICE_BUSY.
	Code string
	// Message is the body's message, else its error, else the HTTP status text.
	Message string
	// RetryAfter is how long the server asked the client to wait, from the
	// Retry-After header (seconds or an HTTP date). Set on 429 (60 s when the
	// header is missing) and 503 (0 when it is missing).
	RetryAfter time.Duration
	// RemainingMinute and RemainingMonth are the X-RateLimit-Remaining-Minute and
	// X-RateLimit-Remaining-Month headers of a 429; nil when absent or "unlimited".
	RemainingMinute *int
	RemainingMonth  *int
	// Details is the body's details field, else the whole body when it was a JSON
	// object. Nil when the body was not JSON (an edge error page).
	Details json.RawMessage
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("owls: HTTP %d %s: %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("owls: HTTP %d: %s", e.Status, e.Message)
}

// Is reports whether target is the sentinel error for e's status.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrRateLimited:
		return e.Status == http.StatusTooManyRequests
	case ErrServiceBusy:
		return e.Status == http.StatusServiceUnavailable
	}
	return false
}

// newAPIError maps a non-2xx response the way the TypeScript SDK does.
func newAPIError(status int, header http.Header, body []byte) *APIError {
	e := &APIError{Status: status}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil {
		obj = nil
	}
	str := func(key string) string {
		var s string
		if raw, ok := obj[key]; ok && json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	e.Message = firstNonEmpty(str("message"), str("error"), http.StatusText(status), fmt.Sprintf("HTTP %d", status))
	code := str("code")
	if d, ok := obj["details"]; ok && string(d) != "null" {
		e.Details = d
	} else if len(obj) > 0 {
		e.Details = json.RawMessage(strings.TrimSpace(string(body)))
	}
	switch status {
	case http.StatusUnauthorized:
		e.Code = "UNAUTHORIZED"
	case http.StatusForbidden:
		e.Code = "FORBIDDEN"
	case http.StatusNotFound:
		e.Code = "NOT_FOUND"
	case http.StatusTooManyRequests:
		e.Code = firstNonEmpty(code, "RATE_LIMITED")
		e.RetryAfter = 60 * time.Second
		if d, ok := parseRetryAfter(header, time.Now()); ok {
			e.RetryAfter = d
		}
		e.RemainingMinute = headerInt(header, "X-RateLimit-Remaining-Minute")
		e.RemainingMonth = headerInt(header, "X-RateLimit-Remaining-Month")
	case http.StatusServiceUnavailable:
		e.Code = firstNonEmpty(code, "SERVICE_BUSY")
		if d, ok := parseRetryAfter(header, time.Now()); ok {
			e.RetryAfter = d
		}
	default:
		e.Code = code
	}
	return e
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// headerInt parses an integer header; nil when it is absent, "unlimited" or not a number.
func headerInt(h http.Header, name string) *int {
	raw := strings.TrimSpace(h.Get(name))
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &n
}

// ErrNotConnected is returned by the Stream methods that send to the server while
// the stream has no open connection.
var ErrNotConnected = errors.New("owls: stream is not connected")

// ErrServerDisconnect is returned by [Stream.Run] when the server closed the
// stream with a Socket.IO disconnect ("io server disconnect"). The server does that
// when its periodic check (every 5 minutes) finds that the account no longer allows
// the connection (plan, payment, trial) or that the connection limit was lowered,
// after an [EventError] event that says why. The stream does not reconnect after
// it: call Run again once the account is fixed.
var ErrServerDisconnect = errors.New("owls: the server disconnected the stream (io server disconnect)")

// Refusal codes: the RefusalError.Code values the server sends. More can be added;
// RefusalError.Retryable says whether waiting helps, and [Stream.Run] says what a
// running stream does with each.
const (
	// Not retryable: the key is missing, wrong or deactivated. Each of these
	// counts toward the server's per-IP block, so Stream.Run stops on them.
	RefusalMissingAPIKey     = "MISSING_API_KEY"
	RefusalInvalidAPIKey     = "INVALID_API_KEY"
	RefusalAPIKeyDeactivated = "API_KEY_DEACTIVATED"
	// Not retryable until the account changes (plan, payment, trial). A running
	// Stream still retries a reconnect refused with one of these five, after 30 s
	// and then every 60 s. Any other non-retryable code, including one added
	// later, ends Stream.Run.
	RefusalTierNoWS             = "TIER_NO_WS"
	RefusalSubscriptionInactive = "SUBSCRIPTION_INACTIVE"
	RefusalPaymentOverdue       = "PAYMENT_OVERDUE"
	RefusalTrialExpired         = "TRIAL_EXPIRED"
	RefusalTrialUnverifiable    = "TRIAL_UNVERIFIABLE"
	// Retryable.
	RefusalSubscriptionCheckFailed = "SUBSCRIPTION_CHECK_FAILED"
	RefusalInternal                = "INTERNAL"
	RefusalIPBlocked               = "IP_BLOCKED"
	RefusalConnectionLimit         = "CONNECTION_LIMIT" // every connection the plan allows is in use
	RefusalClientClosed            = "CLIENT_CLOSED"
)

// RefusalError is a WebSocket connection the server refused (a Socket.IO
// connect_error).
type RefusalError struct {
	// Message is the server's message, unchanged.
	Message string
	// Code is the reason, one of the Refusal* constants (for example
	// RefusalConnectionLimit). More codes can be added. An older server sends
	// none: then it is RefusalConnectionLimit when the message says so, else empty.
	Code string
	// Retryable is true when waiting alone can let the same key connect. The
	// non-retryable account codes (RefusalTierNoWS, RefusalSubscriptionInactive,
	// RefusalPaymentOverdue, RefusalTrialExpired, RefusalTrialUnverifiable) clear
	// once the account is fixed (see [Stream.Run]).
	Retryable bool
	// RetryAfter is the least time to wait before trying again, when the server said.
	RetryAfter time.Duration
	// MaxConnections is how many connections the plan allows (CONNECTION_LIMIT only).
	MaxConnections int

	raw json.RawMessage // the connect_error payload, for the connect_error event
}

func (e *RefusalError) Error() string {
	code := e.Code
	if code == "" {
		code = "REFUSED"
	}
	return fmt.Sprintf("owls: connection refused (%s, retryable=%t): %s", code, e.Retryable, e.Message)
}

// DialError is a WebSocket upgrade the server answered with an HTTP status other
// than 101, before any Socket.IO exchange.
type DialError struct {
	StatusCode int
	// Body is the start of the response body (at most 1 KiB).
	Body string
	// RetryAfter is the least time to wait before the next attempt: the
	// Retry-After header, or 120 s for the API's per-IP block ("Too many
	// connection attempts").
	RetryAfter time.Duration
}

func (e *DialError) Error() string {
	return fmt.Sprintf("owls: websocket upgrade answered HTTP %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}
