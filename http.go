package owls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Davidgsilva/owls-insight-sdk-go/internal/lenient"
)

// isHistoryPath reports whether a request goes through the history gate.
func isHistoryPath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/history/") || path == "/api/odds/history"
}

// get is a GET with the client's retry policy.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	return c.request(ctx, http.MethodGet, path, q, out, c.retry.MaxRetries)
}

// request sends one GET, retrying up to maxRetries times. A history request holds a
// gate slot for the whole call, retries included, so a burst queues here instead of
// being refused by the API's per-key history limit.
func (c *Client) request(ctx context.Context, method, path string, q url.Values, out any, maxRetries int) error {
	if c.historyGate != nil && isHistoryPath(path) {
		select {
		case c.historyGate <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-c.historyGate }()
	}
	for attempt := 0; ; attempt++ {
		status, header, body, err := c.roundTrip(ctx, method, path, q, nil)
		if err == nil {
			if status >= 200 && status < 300 {
				return decodeBody(body, out)
			}
			err = newAPIError(status, header, body)
		}
		delay, ok := c.retryDelay(err, method, attempt, maxRetries)
		if !ok {
			return err
		}
		t := time.NewTimer(delay)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
}

// retryDelay says whether err is retried and after how long: 429 (unless the
// monthly quota is used up) and 503 only, GET only, honouring Retry-After up to
// MaxRetryAfter, else exponential backoff with full jitter.
func (c *Client) retryDelay(err error, method string, attempt, maxRetries int) (time.Duration, bool) {
	var ae *APIError
	if method != http.MethodGet || attempt >= maxRetries || !errors.As(err, &ae) {
		return 0, false
	}
	switch {
	case ae.Status == http.StatusTooManyRequests && !(ae.RemainingMonth != nil && *ae.RemainingMonth == 0):
	case ae.Status == http.StatusServiceUnavailable:
	default:
		return 0, false
	}
	if ae.RetryAfter > 0 {
		if ae.RetryAfter > c.retry.MaxRetryAfter {
			return 0, false
		}
		return ae.RetryAfter, true
	}
	ceiling := c.retry.MaxDelay
	if attempt < 30 {
		ceiling = min(c.retry.MaxDelay, c.retry.BaseDelay<<attempt)
	}
	if ceiling <= 0 {
		return 0, true
	}
	return time.Duration(rand.Int64N(int64(ceiling))), true
}

// roundTrip performs one HTTP exchange under the per-attempt timeout and returns
// the status, headers and whole body.
func (c *Client) roundTrip(ctx context.Context, method, path string, q url.Values, body any) (int, http.Header, []byte, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, fmt.Errorf("owls: encode request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("owls: %s %s: %w", method, path, err)
	}
	req.Header.Set("User-Agent", SDKLabel)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("owls: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("owls: %s %s: read body: %w", method, path, err)
	}
	return resp.StatusCode, resp.Header, data, nil
}

// decodeBody decodes a successful response leniently: a value whose JSON type
// does not fit its model field is dropped (the field stays unset) instead of
// failing the call, and the rest of the response still decodes. A body that is
// not JSON, or whose top level has the wrong shape, is an error.
func decodeBody(body []byte, out any) error {
	if out == nil {
		return nil
	}
	if err := lenient.Unmarshal(body, out); err != nil {
		return fmt.Errorf("owls: decode response: %w", err)
	}
	return nil
}

// parseRetryAfter reads Retry-After as seconds (fractions allowed) or an HTTP date.
func parseRetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil && !math.IsNaN(secs) && !math.IsInf(secs, 0) {
		if secs <= 0 {
			return 0, true
		}
		if secs >= float64(math.MaxInt64/int64(time.Second)) {
			return time.Duration(math.MaxInt64), true
		}
		return time.Duration(secs * float64(time.Second)), true
	}
	if t, err := http.ParseTime(raw); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}
