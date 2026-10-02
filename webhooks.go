package owls

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Customer webhooks (beta; MVP and Hall of Fame): the management methods, their
// request inputs, and the check of a delivery's signature. The response and delivery
// models (WebhookEndpoint, WebhookLineMovedEvent, ...) are generated in models_gen.go.

// The webhook event types. WebhookEventTest is sent only by [Client.TestWebhook].
const (
	WebhookEventLineMoved   = "line.moved"
	WebhookEventEvFound     = "ev.found"
	WebhookEventGameStarted = "game.started"
	WebhookEventGameFinal   = "game.final"
	WebhookEventPropsGraded = "props.graded"
	WebhookEventTest        = "webhook.test"
)

// WebhookSignatureHeader is the header that carries a delivery's signature.
const WebhookSignatureHeader = "Owls-Signature"

// DefaultWebhookTolerance is how far a delivery's timestamp may be from your clock,
// either side, for [VerifyWebhookSignature].
const DefaultWebhookTolerance = 5 * time.Minute

// CreateWebhookParams is the body of [Client.CreateWebhook].
type CreateWebhookParams struct {
	// URL must be https on the default port with a public hostname. IP addresses
	// and private or internal hosts are refused.
	URL string `json:"url"`
	// EventTypes are one or more of the WebhookEvent* constants (not
	// WebhookEventTest). A type that is not open yet is refused with a 400
	// (event_type_unavailable).
	EventTypes []string `json:"event_types"`
	// Description is up to 200 characters; empty sends none.
	Description string `json:"description,omitempty"`
	// Filters narrows what is sent; nil takes every default.
	Filters *WebhookFiltersInput `json:"filters,omitempty"`
}

// UpdateWebhookParams is the body of [Client.UpdateWebhook]. Only the fields you set
// are sent.
type UpdateWebhookParams struct {
	// URL replaces the endpoint's url; empty leaves it.
	URL string `json:"url,omitempty"`
	// EventTypes replaces the list; nil or empty leaves it.
	EventTypes []string `json:"event_types,omitempty"`
	// Description replaces the description; nil leaves it, and Ptr("") stores an
	// empty one.
	Description *string `json:"description,omitempty"`
	// Filters merge into the stored filters (see [WebhookFiltersInput]); nil
	// leaves them.
	Filters *WebhookFiltersInput `json:"filters,omitempty"`
	// Enabled: Ptr(false) stops deliveries and cancels the undelivered ones;
	// Ptr(true) resumes an endpoint that was disabled or auto-disabled after
	// failing, and resets its failure count. nil leaves it.
	Enabled *bool `json:"enabled,omitempty"`
}

// RotateWebhookSecretParams is the optional body of [Client.RotateWebhookSecret].
type RotateWebhookSecretParams struct {
	// ExpirePreviousAfterHours is how long the previous secret keeps signing beside
	// the new one: 0 to 168 hours. nil takes the default, 24; Ptr(0.0) retires it
	// at once.
	ExpirePreviousAfterHours *float64 `json:"expire_previous_after_hours,omitempty"`
}

// WebhookFiltersInput is an endpoint's filters as sent. On create an omitted field
// takes its default. On update the filters merge into the stored ones: a section
// you send (LineMoved, EvFound) changes only the fields you set, and Sports
// replaces the list. A sport that one of the endpoint's event types does not cover
// is refused with a 400 (sport_not_covered).
type WebhookFiltersInput struct {
	// Sports are sport keys. nil sends nothing; a non-nil empty slice sends [],
	// which means every sport the endpoint's event types cover.
	Sports    []string                      `json:"sports,omitempty"`
	LineMoved *WebhookLineMovedFiltersInput `json:"line_moved,omitempty"`
	EvFound   *WebhookEvFoundFiltersInput   `json:"ev_found,omitempty"`
}

// MarshalJSON sends only the sections that are set, and Sports when it is non-nil
// (an empty list included).
func (f WebhookFiltersInput) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if f.Sports != nil {
		m["sports"] = f.Sports
	}
	if f.LineMoved != nil {
		m["line_moved"] = f.LineMoved
	}
	if f.EvFound != nil {
		m["ev_found"] = f.EvFound
	}
	return json.Marshal(m)
}

// WebhookLineMovedFiltersInput is the line.moved section of [WebhookFiltersInput].
// Its zero fields are not sent.
type WebhookLineMovedFiltersInput struct {
	// PriceStepPp is the implied-probability move that fires, in percentage
	// points: 0.5, 1, 2, 3 or 5 (default 2).
	PriceStepPp float64 `json:"price_step_pp,omitempty"`
	// PointStep is the spread or total line move that fires: 0.5, 1, 1.5, 2 or 3
	// (default 1).
	PointStep float64 `json:"point_step,omitempty"`
	// Markets are moneyline, spread and total (default all three).
	Markets []string `json:"markets,omitempty"`
}

// WebhookEvFoundFiltersInput is the ev.found section of [WebhookFiltersInput].
type WebhookEvFoundFiltersInput struct {
	// MinEv is the least EV that fires, in percent: 1, 2, 3, 5 or 8 (default 3).
	// 0 is not sent.
	MinEv float64 `json:"min_ev,omitempty"`
	// Kinds are book (sportsbooks) and venue (exchanges and prediction markets);
	// nil or empty is not sent (default both).
	Kinds []string `json:"kinds,omitempty"`
	// Venues are sportsbook or venue keys the EV board scores. nil sends nothing;
	// a non-nil empty slice sends [], which means every one.
	Venues []string `json:"venues,omitempty"`
}

// MarshalJSON leaves out the zero fields, but sends Venues when it is non-nil (an
// empty list included).
func (f WebhookEvFoundFiltersInput) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if f.MinEv != 0 {
		m["min_ev"] = f.MinEv
	}
	if len(f.Kinds) > 0 {
		m["kinds"] = f.Kinds
	}
	if f.Venues != nil {
		m["venues"] = f.Venues
	}
	return json.Marshal(m)
}

// WebhookDeliveriesParams filters [Client.ListWebhookDeliveries]. Its zero fields are
// not sent.
type WebhookDeliveriesParams struct {
	// Status is pending, delivered, failed, expired, cancelled or shadow.
	Status string
	// Type is one event type, WebhookEventTest included.
	Type string
	// Limit is the page size, 1 to 50 (default 20).
	Limit int
	// StartingAfter is an event id (evt_...) from the previous page: the
	// deliveries older than it are returned.
	StartingAfter string
}

func (p *WebhookDeliveriesParams) query() url.Values {
	q := url.Values{}
	if p == nil {
		return q
	}
	setStr(q, "status", p.Status)
	setStr(q, "type", p.Type)
	setInt(q, "limit", p.Limit)
	setStr(q, "starting_after", p.StartingAfter)
	return q
}

// sendInto sends one request with a JSON body (nil: none) and decodes a 2xx answer.
// It is never retried: these calls change something, and a retried create could
// register an endpoint twice.
func sendInto[T any](ctx context.Context, c *Client, method, path string, body any) (*T, error) {
	status, header, data, err := c.roundTrip(ctx, method, path, nil, body)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, newAPIError(status, header, data)
	}
	out := new(T)
	if err := decodeBody(data, out); err != nil {
		return nil, err
	}
	return out, nil
}

// webhookPath is /api/v1/webhooks/{id} with the id as one escaped path segment. An
// empty id, "." or ".." is refused before anything is sent: a URL resolves those to
// another route (the endpoint list, or a parent path).
func webhookPath(id string) (string, error) {
	if id == "" || id == "." || id == ".." {
		return "", fmt.Errorf("owls: invalid webhook id %q: pass the endpoint's id (whk_...)", id)
	}
	return "/api/v1/webhooks/" + esc(id), nil
}

// CreateWebhook registers an HTTPS endpoint for signed event notifications:
// line.moved, ev.found, game.started, game.final and props.graded (beta; MVP and
// Hall of Fame; MVP 3 endpoints, Hall of Fame 10). Data.Secret, the signing secret,
// is returned ONCE: store it, then check every delivery with [VerifyWebhookSignature].
//
// The calls that change something (create, update, delete, test, rotate-secret)
// are never retried, whatever [RetryPolicy] says. If one times out, list your
// endpoints before creating again, and rotate the secret of one whose secret you
// never saw. A 400 or 409 is an [*APIError] whose Code says why, such as
// invalid_body, https_required, url_not_allowed, url_unresolvable,
// sport_not_covered, filter_never_matches, event_type_unavailable (400),
// endpoint_limit or endpoint_disabled (409), and whose Details is the body. A 429
// carries the code rate_limited. Other plans get a 403 ([ErrForbidden]).
func (c *Client) CreateWebhook(ctx context.Context, p CreateWebhookParams) (*WebhookCreateResponse, error) {
	return sendInto[WebhookCreateResponse](ctx, c, http.MethodPost, "/api/v1/webhooks", p)
}

// ListWebhooks returns your webhook endpoints, oldest first, never their secrets.
// Meta.Limit is how many endpoints your plan allows.
func (c *Client) ListWebhooks(ctx context.Context) (*WebhookListResponse, error) {
	return getInto[WebhookListResponse](ctx, c, "/api/v1/webhooks", nil)
}

// GetWebhook returns one endpoint with its delivery health (ConsecutiveFailures,
// FailingSince, LastSuccessAt). An id that is not one of your endpoints is a 404
// ([ErrNotFound]).
func (c *Client) GetWebhook(ctx context.Context, id string) (*WebhookResponse, error) {
	path, err := webhookPath(id)
	if err != nil {
		return nil, err
	}
	return getInto[WebhookResponse](ctx, c, path, nil)
}

// UpdateWebhook changes an endpoint; only the fields set in p are sent. Never
// retried.
func (c *Client) UpdateWebhook(ctx context.Context, id string, p UpdateWebhookParams) (*WebhookResponse, error) {
	path, err := webhookPath(id)
	if err != nil {
		return nil, err
	}
	return sendInto[WebhookResponse](ctx, c, http.MethodPatch, path, p)
}

// DeleteWebhook deletes an endpoint. Its undelivered events are cancelled and
// nothing more is sent. Never retried.
func (c *Client) DeleteWebhook(ctx context.Context, id string) (*WebhookDeleteResponse, error) {
	path, err := webhookPath(id)
	if err != nil {
		return nil, err
	}
	return sendInto[WebhookDeleteResponse](ctx, c, http.MethodDelete, path, nil)
}

// TestWebhook queues a webhook.test event to the endpoint, signed like every other
// event (202). One per endpoint every 10 seconds; the endpoint must be enabled.
// Follow it with [Client.ListWebhookDeliveries]. Never retried.
func (c *Client) TestWebhook(ctx context.Context, id string) (*WebhookTestResponse, error) {
	path, err := webhookPath(id)
	if err != nil {
		return nil, err
	}
	return sendInto[WebhookTestResponse](ctx, c, http.MethodPost, path+"/test", nil)
}

// RotateWebhookSecret issues a new signing secret, returned ONCE. The previous one
// keeps signing beside it for p.ExpirePreviousAfterHours (nil p: 24 hours), so a
// receiver holding either secret verifies meanwhile. Never retried.
func (c *Client) RotateWebhookSecret(ctx context.Context, id string, p *RotateWebhookSecretParams) (*WebhookRotateSecretResponse, error) {
	path, err := webhookPath(id)
	if err != nil {
		return nil, err
	}
	body := RotateWebhookSecretParams{}
	if p != nil {
		body = *p
	}
	return sendInto[WebhookRotateSecretResponse](ctx, c, http.MethodPost, path+"/rotate-secret", body)
}

// ListWebhookDeliveries returns recent deliveries to one endpoint, newest first:
// status, attempts, your receiver's last answer and the exact payload sent. Page
// with StartingAfter (the last ID of the previous page) while HasMore. Deliveries
// are kept for 14 days. A delivery's Payload is the body as stored; to type it,
// pass its JSON to [ParseWebhookEvent].
func (c *Client) ListWebhookDeliveries(ctx context.Context, id string, p *WebhookDeliveriesParams) (*WebhookDeliveriesResponse, error) {
	path, err := webhookPath(id)
	if err != nil {
		return nil, err
	}
	return getInto[WebhookDeliveriesResponse](ctx, c, path+"/deliveries", p.query())
}

// WebhookSignature is a parsed Owls-Signature header.
type WebhookSignature struct {
	// Timestamp is the t value, unix seconds; meaningful only when HasTimestamp.
	Timestamp    int64
	HasTimestamp bool
	// V1 holds every well-formed v1 value, lowercase, in header order.
	V1 []string
}

// ParseWebhookSignature reads t=<unix seconds>,v1=<hex>[,v1=<hex>]. Whitespace
// around a part is ignored, and so are parts it does not know (another scheme such
// as a future v2) and malformed values. The last valid t wins; a t has 1 to 12
// digits.
func ParseWebhookSignature(header string) WebhookSignature {
	var s WebhookSignature
	for _, part := range strings.Split(header, ",") {
		i := strings.IndexByte(part, '=')
		if i <= 0 {
			continue
		}
		k := strings.TrimFunc(part[:i], isJSWhitespace)
		v := strings.TrimFunc(part[i+1:], isJSWhitespace)
		switch {
		case k == "t" && len(v) >= 1 && len(v) <= 12 && allBytes(v, isDigit):
			n, err := strconv.ParseInt(v, 10, 64)
			if err == nil {
				s.Timestamp, s.HasTimestamp = n, true
			}
		case k == "v1" && len(v) == 64 && allBytes(v, isHex):
			s.V1 = append(s.V1, strings.ToLower(v))
		}
	}
	return s
}

// isJSWhitespace reports the characters JavaScript's String.prototype.trim removes,
// which is how the API's own parser trims each part. (unicode.IsSpace differs: it
// includes U+0085 and leaves out U+FEFF.)
func isJSWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', '\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func isHex(b byte) bool { return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') }

func allBytes(s string, ok func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

// ComputeWebhookSignature returns hex HMAC-SHA256 of timestamp, a period and the raw
// body, keyed with the whole secret (whsec_ included) as UTF-8.
func ComputeWebhookSignature(secret string, timestamp int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10) + "."))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSignature reports whether header (the Owls-Signature value) carries
// a v1 signature of payload (the RAW body, exactly as received) made with secret,
// with a timestamp within [DefaultWebhookTolerance] of the clock. Reject the
// delivery when it is false, and dedupe accepted ones on the event id (delivery is
// at least once).
//
//	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
//	if err != nil || !owls.VerifyWebhookSignature(body, r.Header.Get(owls.WebhookSignatureHeader), secret) {
//		http.Error(w, "bad signature", http.StatusBadRequest)
//		return
//	}
func VerifyWebhookSignature(payload []byte, header, secret string) bool {
	return VerifyWebhookSignatureAt(payload, header, secret, DefaultWebhookTolerance, time.Now())
}

// VerifyWebhookSignatureAt is [VerifyWebhookSignature] with the tolerance and the
// clock given. During a secret rotation the header carries one v1 per live secret,
// and a match with any of them is enough; every candidate is compared, each in
// constant time. An empty secret, or a negative tolerance, gives false.
func VerifyWebhookSignatureAt(payload []byte, header, secret string, tolerance time.Duration, now time.Time) bool {
	if secret == "" {
		return false
	}
	s := ParseWebhookSignature(header)
	if !s.HasTimestamp || len(s.V1) == 0 {
		return false
	}
	diff := now.Unix() - s.Timestamp
	if diff < 0 {
		diff = -diff
	}
	// diff stays negative only when it overflowed (a clock near the minimum time).
	if diff < 0 || float64(diff) > tolerance.Seconds() {
		return false
	}
	expected := []byte(ComputeWebhookSignature(secret, s.Timestamp, payload))
	ok := false
	for _, candidate := range s.V1 {
		if subtle.ConstantTimeCompare([]byte(candidate), expected) == 1 {
			ok = true
		}
	}
	return ok
}

// WebhookEvent is a delivery body as [ParseWebhookEvent] returns it:
// *WebhookLineMovedEvent, *WebhookEvFoundEvent, *WebhookGameStartedEvent,
// *WebhookGameFinalEvent, *WebhookPropsGradedEvent, *WebhookTestEvent, or
// *WebhookDeliveryPayload for an event type this version does not know.
type WebhookEvent interface {
	// EventID is the event id (evt_...), identical on every retry: dedupe on it.
	EventID() string
	// EventType is the event type, such as WebhookEventLineMoved.
	EventType() string
}

func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// EventID is the event id ("" when absent).
func (e *WebhookLineMovedEvent) EventID() string { return strOrEmpty(e.ID) }

// EventType is the event type ("" when absent).
func (e *WebhookLineMovedEvent) EventType() string { return strOrEmpty(e.Type) }

// EventID is the event id ("" when absent).
func (e *WebhookEvFoundEvent) EventID() string { return strOrEmpty(e.ID) }

// EventType is the event type ("" when absent).
func (e *WebhookEvFoundEvent) EventType() string { return strOrEmpty(e.Type) }

// EventID is the event id ("" when absent).
func (e *WebhookGameStartedEvent) EventID() string { return strOrEmpty(e.ID) }

// EventType is the event type ("" when absent).
func (e *WebhookGameStartedEvent) EventType() string { return strOrEmpty(e.Type) }

// EventID is the event id ("" when absent).
func (e *WebhookGameFinalEvent) EventID() string { return strOrEmpty(e.ID) }

// EventType is the event type ("" when absent).
func (e *WebhookGameFinalEvent) EventType() string { return strOrEmpty(e.Type) }

// EventID is the event id ("" when absent).
func (e *WebhookPropsGradedEvent) EventID() string { return strOrEmpty(e.ID) }

// EventType is the event type ("" when absent).
func (e *WebhookPropsGradedEvent) EventType() string { return strOrEmpty(e.Type) }

// EventID is the event id ("" when absent).
func (e *WebhookTestEvent) EventID() string { return strOrEmpty(e.ID) }

// EventType is the event type ("" when absent).
func (e *WebhookTestEvent) EventType() string { return strOrEmpty(e.Type) }

// EventID is the event id ("" when absent).
func (e *WebhookDeliveryPayload) EventID() string { return strOrEmpty(e.ID) }

// EventType is the event type ("" when absent).
func (e *WebhookDeliveryPayload) EventType() string { return strOrEmpty(e.Type) }

// ParseWebhookEvent decodes a delivery body, after [VerifyWebhookSignature] accepted
// it, into the model of its type (see [WebhookEvent]). Decoding is lenient, like
// [Decode]. It returns an error when the body is not a JSON object with a type.
//
//	ev, err := owls.ParseWebhookEvent(body)
//	if err != nil {
//		return err
//	}
//	if seen(ev.EventID()) {
//		return nil // a retry of an event already handled
//	}
//	switch ev := ev.(type) {
//	case *owls.WebhookLineMovedEvent:
//		fmt.Println(*ev.Data.Market, ev.Data.Current.Home)
//	case *owls.WebhookEvFoundEvent:
//		fmt.Println(*ev.Data.Signal.Venue, *ev.Data.Signal.EvPercent)
//	}
func ParseWebhookEvent(payload []byte) (WebhookEvent, error) {
	var head struct {
		Type *string `json:"type"`
	}
	if err := json.Unmarshal(payload, &head); err != nil {
		return nil, fmt.Errorf("owls: webhook body: %w", err)
	}
	if head.Type == nil {
		return nil, errors.New("owls: webhook body has no type")
	}
	switch *head.Type {
	case WebhookEventLineMoved:
		return decodeAs[WebhookLineMovedEvent](payload)
	case WebhookEventEvFound:
		return decodeAs[WebhookEvFoundEvent](payload)
	case WebhookEventGameStarted:
		return decodeAs[WebhookGameStartedEvent](payload)
	case WebhookEventGameFinal:
		return decodeAs[WebhookGameFinalEvent](payload)
	case WebhookEventPropsGraded:
		return decodeAs[WebhookPropsGradedEvent](payload)
	case WebhookEventTest:
		return decodeAs[WebhookTestEvent](payload)
	}
	return decodeAs[WebhookDeliveryPayload](payload)
}

// decodeAs is [Decode] returning an untyped nil on error (not a nil *T in an
// interface).
func decodeAs[T any, P interface {
	*T
	WebhookEvent
}](payload []byte) (WebhookEvent, error) {
	v, err := Decode[T](payload)
	if err != nil {
		return nil, err
	}
	return P(v), nil
}
