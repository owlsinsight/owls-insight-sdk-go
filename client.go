package owls

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL            = "https://api.owlsinsight.com"
	defaultTimeout            = 30 * time.Second
	defaultHistoryConcurrency = 3
)

// Client calls the Owls Insight REST API and opens WebSocket streams. It is safe
// for concurrent use.
type Client struct {
	apiKey     string
	baseURL    string
	wsURL      string
	httpClient *http.Client
	timeout    time.Duration
	retry      RetryPolicy
	// historyGate holds one token per history request in flight; nil disables it.
	historyGate chan struct{}
}

// RetryPolicy says which failed GET requests are sent again. Only 429 and 503
// responses are retried, a 429 is never retried once the monthly quota is used
// up (X-RateLimit-Remaining-Month: 0), and a POST is never retried.
type RetryPolicy struct {
	// MaxRetries is the number of attempts after the first. Default 0: no retry.
	MaxRetries int
	// BaseDelay is the backoff base when the server sent no Retry-After; it
	// doubles per attempt with full jitter. Default 2 s.
	BaseDelay time.Duration
	// MaxDelay caps the computed backoff. Default 30 s.
	MaxDelay time.Duration
	// MaxRetryAfter is the longest Retry-After the client waits for; a longer one
	// returns the error instead. Default 120 s.
	MaxRetryAfter time.Duration
}

func (p RetryPolicy) withDefaults() RetryPolicy {
	if p.BaseDelay <= 0 {
		p.BaseDelay = 2 * time.Second
	}
	if p.MaxDelay <= 0 {
		p.MaxDelay = 30 * time.Second
	}
	if p.MaxRetryAfter <= 0 {
		p.MaxRetryAfter = 120 * time.Second
	}
	if p.MaxRetries < 0 {
		p.MaxRetries = 0
	}
	return p
}

type config struct {
	baseURL            string
	wsURL              string
	httpClient         *http.Client
	timeout            time.Duration
	retry              RetryPolicy
	historyConcurrency int
}

// Option configures a [Client].
type Option func(*config)

// WithBaseURL sets the REST base URL (default https://api.owlsinsight.com). The
// WebSocket URL follows it unless [WithWSURL] is also given.
func WithBaseURL(u string) Option { return func(c *config) { c.baseURL = u } }

// WithWSURL sets the WebSocket base URL, without the /socket.io/ path.
func WithWSURL(u string) Option { return func(c *config) { c.wsURL = u } }

// WithHTTPClient sets the http.Client used for REST calls and the WebSocket
// upgrade. Leave its Timeout at zero: timeouts come from [WithTimeout], per attempt.
func WithHTTPClient(hc *http.Client) Option { return func(c *config) { c.httpClient = hc } }

// WithTimeout sets how long one REST attempt may take, response body included
// (default 30 s). It is applied through the request context; 0 disables it.
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }

// WithRetry enables retries of 429 and 503 responses on GET requests. Zero
// fields take their defaults.
func WithRetry(p RetryPolicy) Option { return func(c *config) { c.retry = p } }

// WithHistoryConcurrency sets how many /api/v1/history/* and /api/odds/history
// requests the client keeps in flight at once (default 3, the per-key allowance
// of the MVP plan). Requests beyond it wait for a slot instead of being refused
// by the API. 0 disables the gate.
func WithHistoryConcurrency(n int) Option { return func(c *config) { c.historyConcurrency = n } }

// NewClient returns a client for apiKey.
func NewClient(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		return nil, errors.New("owls: an API key is required")
	}
	cfg := config{timeout: defaultTimeout, historyConcurrency: defaultHistoryConcurrency}
	for _, o := range opts {
		o(&cfg)
	}
	base := firstNonEmpty(cfg.baseURL, defaultBaseURL)
	ws := firstNonEmpty(cfg.wsURL, cfg.baseURL, defaultBaseURL)
	if !validURL(base, "http", "https") {
		return nil, fmt.Errorf("owls: invalid base URL %q", base)
	}
	if !validURL(ws, "http", "https", "ws", "wss") {
		return nil, fmt.Errorf("owls: invalid WebSocket URL %q", ws)
	}
	c := &Client{
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(base, "/"),
		wsURL:      strings.TrimRight(ws, "/"),
		httpClient: cfg.httpClient,
		timeout:    max(cfg.timeout, 0),
		retry:      cfg.retry.withDefaults(),
	}
	if c.httpClient == nil {
		c.httpClient = http.DefaultClient
	}
	if cfg.historyConcurrency > 0 {
		c.historyGate = make(chan struct{}, cfg.historyConcurrency)
	}
	return c, nil
}

func validURL(raw string, schemes ...string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return true
		}
	}
	return false
}

// Ptr returns a pointer to v, for the optional pointer fields of the models and
// of [Subscription].
func Ptr[T any](v T) *T { return &v }
