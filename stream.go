package owls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Davidgsilva/owls-insight-sdk-go/internal/sio"
)

// timing is every delay of the connection policy. Tests shrink it.
type timing struct {
	ackTimeout        time.Duration   // re-send an unacknowledged replayed subscription once after this
	connLimitDelays   []time.Duration // refusal retry schedule for CONNECTION_LIMIT
	refusalDelays     []time.Duration // refusal retry schedule for any other retryable code
	accountDelays     []time.Duration // refusal retry schedule for a non-retryable account refusal (accountCodes)
	retryWindow       time.Duration   // at most retryMaxPerWindow refusal retries per retryWindow
	retryMaxPerWindow int
	reconnectDelay    time.Duration // first delay after a dropped connection
	reconnectDelayMax time.Duration
	stableAfter       time.Duration // uptime after which the reconnect delay resets
	ipBlockWait       time.Duration // least wait after "Too many connection attempts" on the upgrade
	handshakeTimeout  time.Duration
	writeTimeout      time.Duration
	closeTimeout      time.Duration
}

var defaultTiming = timing{
	ackTimeout:        5 * time.Second,
	connLimitDelays:   []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second},
	refusalDelays:     []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second},
	accountDelays:     []time.Duration{30 * time.Second, 60 * time.Second},
	retryWindow:       60 * time.Second,
	retryMaxPerWindow: 4,
	reconnectDelay:    time.Second,
	reconnectDelayMax: 30 * time.Second,
	stableAfter:       60 * time.Second,
	ipBlockWait:       120 * time.Second,
	handshakeTimeout:  20 * time.Second,
	writeTimeout:      10 * time.Second,
	closeTimeout:      2 * time.Second,
}

const (
	// defaultReadLimit is the largest message accepted: full boards are several
	// MB and the server asks for at least 32 MB.
	defaultReadLimit = 64 << 20
	// defaultQueueSize is how many events wait for the handlers before the reader
	// stops reading.
	defaultQueueSize = 64
	// fallbackLiveness applies when the open packet carries no ping settings: the
	// server's pingInterval (25 s) plus pingTimeout (120 s).
	fallbackLiveness = 145 * time.Second
)

// StreamOption configures a [Stream].
type StreamOption func(*Stream)

// WithSubscription sets the subscription the stream sends on its first connect
// (in the handshake and as a subscribe message) and on every reconnect.
func WithSubscription(sub Subscription) StreamOption {
	return func(s *Stream) {
		b, err := json.Marshal(sub)
		if err != nil {
			s.initErr = fmt.Errorf("owls: encode subscription: %w", err)
			return
		}
		s.desired = b
	}
}

// Stream is one WebSocket connection to the API, kept open by [Stream.Run].
//
// The stream stores the last subscription it sent and the props subscriptions in
// force, and sends them again on every connect: in the handshake and as
// subscribe messages, and once more after 5 s if the server has not acknowledged
// them.
//
// Events are delivered in order, one at a time, from a queue of 64: a slow
// handler does not stop the stream from answering the server's pings until the
// queue is full. When it is full the stream stops reading, and the server closes
// a connection that falls too far behind.
//
// The stream remembers its reconnect delays across calls to Run, so a loop that
// calls Run again after it returns cannot connect faster than the stream would
// on its own.
type Stream struct {
	c         *Client
	t         timing
	readLimit int64
	queueSize int
	initErr   error

	running atomic.Bool
	// Connection policy state, kept across Runs. Only the one active Run reads
	// or writes it.
	shortDrops   int          // consecutive drops of connections younger than stableAfter, failed attempts included
	refused      refusalState // refusal retry schedule
	retryPending bool         // the next attempt is a refusal retry
	notBefore    time.Time    // no attempt before this

	// sendLock orders the replay on connect and the send methods: a
	// subscription sent while connecting is never overtaken by the older one,
	// and two sends reach the server in the order they were stored.
	sendLock chan struct{}
	mu       sync.Mutex
	conn     *sio.Conn       // nil while not connected
	desired  json.RawMessage // the last subscribe payload as sent; nil until one
	props    []propsSub      // props subscriptions in force, in first-subscribe order
	pending  map[string]bool // acknowledgements still awaited after a replay
	ackTimer *time.Timer

	hmu      sync.RWMutex
	handlers map[string][]*handler
}

type propsSub struct {
	event   string
	payload json.RawMessage
}

type handler struct{ fn func(json.RawMessage) }

type event struct {
	name string
	data json.RawMessage
}

// Stream returns a new, unconnected stream. Call Run to connect.
func (c *Client) Stream(opts ...StreamOption) *Stream {
	s := &Stream{
		c:         c,
		t:         defaultTiming,
		readLimit: defaultReadLimit,
		queueSize: defaultQueueSize,
		sendLock:  make(chan struct{}, 1),
		handlers:  map[string][]*handler{},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// On calls fn with the first argument of every event named name (JSON null when
// the event has none), in the order events arrive. Handlers run on one goroutine;
// keep them short. [Decode] turns a payload into one of the model types. It
// returns a function that removes fn.
func (s *Stream) On(name string, fn func(json.RawMessage)) (off func()) {
	h := &handler{fn: fn}
	s.hmu.Lock()
	s.handlers[name] = append(s.handlers[name], h)
	s.hmu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.hmu.Lock()
			defer s.hmu.Unlock()
			hs := s.handlers[name]
			for i, x := range hs {
				if x == h {
					s.handlers[name] = append(hs[:i:i], hs[i+1:]...)
					break
				}
			}
		})
	}
}

// OnOddsUpdate calls fn with every odds-update, decoded with [Decode]. Frames
// can be partial: after a subscribe the first frames are a snapshot, later ones
// carry only the games that changed, so merge by event id. A frame that cannot be
// decoded at all (not a JSON object) is passed as a nil payload and the error.
func (s *Stream) OnOddsUpdate(fn func(*OddsUpdatePayload, error)) (off func()) {
	return s.On(EventOddsUpdate, func(raw json.RawMessage) {
		fn(Decode[OddsUpdatePayload](raw))
	})
}

// Decode decodes an event payload (or any API JSON) into a model type the way
// the REST methods do: a value whose JSON type does not fit its field is left
// unset instead of failing, unknown fields are ignored, and only a payload that is
// not JSON, or whose top level does not fit T, is an error.
//
//	s.On(owls.EventServerNotice, func(raw json.RawMessage) {
//		n, err := owls.Decode[owls.ServerNotice](raw)
//		...
//	})
func Decode[T any](raw json.RawMessage) (*T, error) {
	out := new(T)
	if err := decodeBody(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}

// Connected reports whether the stream has an open connection.
func (s *Stream) Connected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn != nil
}

// Subscribe REPLACES the subscription: a field left out is cleared. The stream
// stores it and sends it again on every reconnect. It returns ErrNotConnected,
// and stores nothing, while the stream is not connected; use WithSubscription
// for the first connect.
//
// ctx bounds only the wait for a send already in progress; when it ends first,
// nothing is stored. Once stored, the subscription is written before Subscribe
// returns, within the stream's 10 s write timeout. A write error means the
// connection is failing: the subscription stays stored and is sent on the
// reconnect. The same holds for UpdateSubscription and the props calls.
func (s *Stream) Subscribe(ctx context.Context, sub Subscription) error {
	payload, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("owls: encode subscription: %w", err)
	}
	if err := s.lockSend(ctx); err != nil {
		return err
	}
	defer s.unlockSend()
	s.mu.Lock()
	conn := s.conn
	if conn == nil {
		s.mu.Unlock()
		return ErrNotConnected
	}
	s.desired = payload
	s.mu.Unlock()
	return s.send(conn, "subscribe", payload)
}

// UpdateSubscription MERGES the fields set in patch into the subscription, the
// way the server does: each top-level field replaces the stored one (a V2 you
// pass replaces the whole V2). The merged result is sent on every reconnect.
func (s *Stream) UpdateSubscription(ctx context.Context, patch Subscription) error {
	payload, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("owls: encode subscription: %w", err)
	}
	if err := s.lockSend(ctx); err != nil {
		return err
	}
	defer s.unlockSend()
	s.mu.Lock()
	conn := s.conn
	if conn == nil {
		s.mu.Unlock()
		return ErrNotConnected
	}
	merged, err := mergeSubscription(s.desired, payload)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("owls: merge subscription: %w", err)
	}
	s.desired = merged
	s.mu.Unlock()
	return s.send(conn, "update-subscription", payload)
}

// SubscribeProps subscribes to a book's player props stream ("" or "pinnacle"
// for player-props-update, else <book>-props-update). The subscription is kept
// and sent again on every reconnect until UnsubscribeProps.
func (s *Stream) SubscribeProps(ctx context.Context, book string, f PropsFilter) error {
	payload, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("owls: encode props filter: %w", err)
	}
	ev, _ := propsEvents(book)
	if err := s.lockSend(ctx); err != nil {
		return err
	}
	defer s.unlockSend()
	s.mu.Lock()
	conn := s.conn
	if conn == nil {
		s.mu.Unlock()
		return ErrNotConnected
	}
	replaced := false
	for i := range s.props {
		if s.props[i].event == ev {
			s.props[i].payload = payload
			replaced = true
		}
	}
	if !replaced {
		s.props = append(s.props, propsSub{event: ev, payload: payload})
	}
	s.mu.Unlock()
	return s.send(conn, ev, payload)
}

// UnsubscribeProps ends a book's props subscription.
func (s *Stream) UnsubscribeProps(ctx context.Context, book string) error {
	sub, unsub := propsEvents(book)
	if err := s.lockSend(ctx); err != nil {
		return err
	}
	defer s.unlockSend()
	s.mu.Lock()
	conn := s.conn
	if conn == nil {
		s.mu.Unlock()
		return ErrNotConnected
	}
	kept := s.props[:0:0]
	for _, p := range s.props {
		if p.event != sub {
			kept = append(kept, p)
		}
	}
	s.props = kept
	s.mu.Unlock()
	return s.send(conn, unsub, nil)
}

// lockSend takes the send lock, or returns ctx's error if ctx ends first.
func (s *Stream) lockSend(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case s.sendLock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Stream) unlockSend() { <-s.sendLock }

// send emits one event, holding the send lock. The write is bounded by the
// stream's write timeout and not by the caller's context: cancelling a write
// part-way would close the connection, and returning before it ends would let a
// later send reach the server first.
func (s *Stream) send(conn *sio.Conn, name string, arg json.RawMessage) error {
	ctx, cancel := context.WithTimeout(context.Background(), s.t.writeTimeout)
	defer cancel()
	if err := conn.Emit(ctx, name, arg); err != nil {
		return fmt.Errorf("owls: send %s: %w", name, err)
	}
	return nil
}

// Run connects and keeps the stream connected until ctx ends, then closes the
// connection with a Socket.IO disconnect and returns ctx.Err().
//
// If the FIRST connection attempt fails, Run returns that error without retrying:
// a *RefusalError (read Code, Retryable and RetryAfter), a *DialError, or a
// network error. After the stream has connected once, Run reconnects on its own:
//
//   - after a dropped connection, with a delay of 1 s that doubles, up to 30 s,
//     each time a connection drops before it has lasted 60 s (a failed attempt
//     counts as such a drop);
//   - after a refused reconnect the server marks retryable, on the refusal's own
//     schedule (CONNECTION_LIMIT 5/15/30/60 s, others 2/5/15/30/60 s);
//   - after a reconnect refused for the account (TIER_NO_WS,
//     SUBSCRIPTION_INACTIVE, PAYMENT_OVERDUE, TRIAL_EXPIRED, TRIAL_UNVERIFIABLE),
//     after 30 s and then every 60 s, so a reconnect after a network drop or a
//     server restart goes through once the invoice is paid or the plan upgraded;
//   - after an upgrade answered "Too many connection attempts", no sooner than
//     120 s.
//
// Every refusal retry is jittered x0.8-1.2, never sooner than the server's
// retryAfterMs, and at most 4 refusal retries of any kind fall in a minute. Each
// refusal is also delivered as an EventConnectError event.
//
// Run returns a *RefusalError when a reconnect is refused with retryable false
// and anything but an account code: a bad key (MISSING_API_KEY,
// INVALID_API_KEY, API_KEY_DEACTIVATED: waiting never fixes them, and each counts
// toward the server's per-IP block), a code this SDK does not know, or no code at
// all. An expired trial deactivates the key, so its retry meets
// API_KEY_DEACTIVATED and Run returns; so does a cancelled account until it
// subscribes again.
//
// Run returns ErrServerDisconnect when the server disconnects the stream. The
// server does that when its periodic check (every 5 minutes) finds the account no
// longer allows the connection, and the stream does not reconnect after it: call
// Run again once the account is fixed.
//
// The delay the stream would have waited before its next attempt is kept: calling
// Run again waits it out before connecting, so a supervisor loop around Run keeps
// to the same schedule.
//
// Only one Run may be active at a time. Once ctx ends, events still queued are
// dropped, and Run returns when the handler running at that moment (if any) has
// returned.
func (s *Stream) Run(ctx context.Context) error {
	if s.initErr != nil {
		return s.initErr
	}
	if !s.running.CompareAndSwap(false, true) {
		return errors.New("owls: Stream.Run is already running")
	}
	defer s.running.Store(false)

	queue := make(chan event, s.queueSize)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for ev := range queue {
			if ctx.Err() == nil {
				s.dispatch(ev)
			}
		}
	}()
	defer func() {
		close(queue)
		<-drained
	}()

	connected := false // at least one connection in this Run
	for {
		if err := sleepCtx(ctx, time.Until(s.notBefore)); err != nil {
			return err
		}
		if s.retryPending {
			s.refused.times = append(s.refused.times, time.Now())
			s.retryPending = false
		}
		conn, err := s.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var refusal *RefusalError
			isRefusal := errors.As(err, &refusal)
			if isRefusal {
				s.lifecycle(ctx, queue, EventConnectError, refusal.raw)
			}
			// A failed attempt counts as a drop of a young connection.
			s.shortDrops++
			delay := s.dropDelay(s.shortDrops)
			if isRefusal {
				delay = s.refused.delay(refusal, s.t, time.Now())
				s.retryPending = true
			} else if de := (*DialError)(nil); errors.As(err, &de) {
				delay = max(delay, de.RetryAfter)
			}
			s.notBefore = time.Now().Add(delay)
			if !connected || (isRefusal && stopsReconnecting(refusal)) {
				return err
			}
			continue
		}
		connected = true
		s.refused.attempt = 0
		started := time.Now()
		err = s.serve(ctx, conn, queue)
		if time.Since(started) >= s.t.stableAfter {
			s.shortDrops = 0
		} else {
			s.shortDrops++
		}
		s.notBefore = time.Now().Add(s.dropDelay(s.shortDrops))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		reason, _ := json.Marshal(map[string]string{"reason": err.Error()})
		s.lifecycle(ctx, queue, EventDisconnect, reason)
		if errors.Is(err, ErrServerDisconnect) {
			return err
		}
	}
}

// dropDelay is the delay before reconnecting after n consecutive short drops:
// reconnectDelay doubled n times, capped, with socket.io's ±50% jitter.
func (s *Stream) dropDelay(n int) time.Duration {
	d := s.t.reconnectDelay << min(n, 16)
	if d <= 0 || d > s.t.reconnectDelayMax {
		d = s.t.reconnectDelayMax
	}
	return min(time.Duration(float64(d)*(0.5+rand.Float64())), s.t.reconnectDelayMax)
}

// refusalState is the retry schedule of refused reconnects.
type refusalState struct {
	attempt int         // refusal retries since the last successful connect
	times   []time.Time // when recent refusal retries were made
}

// delay picks the wait before retrying a refused reconnect: the refusal's schedule
// (account refusals, CONNECTION_LIMIT, any other retryable code) with ×0.8-1.2
// jitter, floored at the server's RetryAfter, and pushed out so no more than
// retryMaxPerWindow retries fall in any retryWindow.
func (r *refusalState) delay(ref *RefusalError, t timing, now time.Time) time.Duration {
	ladder := t.refusalDelays
	switch {
	case !ref.Retryable:
		ladder = t.accountDelays
	case ref.Code == RefusalConnectionLimit:
		ladder = t.connLimitDelays
	}
	base := ladder[min(r.attempt, len(ladder)-1)]
	r.attempt++
	d := max(time.Duration(float64(base)*(0.8+rand.Float64()*0.4)), ref.RetryAfter)
	kept := r.times[:0]
	for _, at := range r.times {
		if now.Sub(at) < t.retryWindow {
			kept = append(kept, at)
		}
	}
	r.times = kept
	if n := len(r.times); n >= t.retryMaxPerWindow && t.retryMaxPerWindow > 0 {
		oldest := r.times[n-t.retryMaxPerWindow]
		d = max(d, oldest.Add(t.retryWindow).Sub(now)+time.Millisecond)
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dial opens a connection, sending the stored subscription in the handshake.
func (s *Stream) dial(ctx context.Context) (*sio.Conn, error) {
	auth := map[string]any{"sdk": SDKLabel}
	s.mu.Lock()
	if s.desired != nil {
		auth["subscription"] = s.desired
	}
	s.mu.Unlock()

	// The handshake context is not derived from ctx, so that a handshake that
	// runs out of time reports context.DeadlineExceeded whatever ctx is; ctx
	// still cancels it. coder/websocket drops the context once a read or write
	// returns, so ending it after Dial does not touch the open connection.
	hctx, cancel := context.WithTimeout(context.Background(), s.t.handshakeTimeout)
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	conn, err := sio.Dial(hctx, sio.Config{
		URL:        s.c.wsURL,
		APIKey:     s.c.apiKey,
		Header:     http.Header{"User-Agent": {SDKLabel}},
		HTTPClient: s.c.httpClient,
		Compress:   true,
		ReadLimit:  s.readLimit,
	}, auth)
	if err != nil {
		var ce *sio.ConnectError
		if errors.As(err, &ce) {
			return nil, refusalFrom(ce)
		}
		var he *sio.HTTPError
		if errors.As(err, &he) {
			return nil, s.dialError(he)
		}
		return nil, fmt.Errorf("owls: connect: %w", err)
	}
	return conn, nil
}

func (s *Stream) dialError(he *sio.HTTPError) *DialError {
	d := &DialError{StatusCode: he.StatusCode, Body: he.Body}
	if ra, ok := parseRetryAfter(he.Header, time.Now()); ok {
		d.RetryAfter = ra
	}
	if he.StatusCode == http.StatusBadRequest && strings.Contains(he.Body, "Too many connection attempts") {
		d.RetryAfter = max(d.RetryAfter, s.t.ipBlockWait)
	}
	return d
}

// accountCodes are the non-retryable refusals a running stream still retries,
// slowly: the account ones, which the customer fixes (pays, upgrades) without
// touching the client. Every other non-retryable refusal ends Run.
var accountCodes = map[string]bool{
	RefusalTierNoWS:             true,
	RefusalSubscriptionInactive: true,
	RefusalPaymentOverdue:       true,
	RefusalTrialExpired:         true,
	RefusalTrialUnverifiable:    true,
}

// stopsReconnecting reports whether a refused reconnect ends Run: any
// non-retryable refusal that is not an account refusal. That covers a bad key
// (waiting never fixes it, and each attempt counts toward the server's per-IP
// block, 15 in a row, then 2 minutes), a code this SDK does not know, and no code
// at all (an older server, where a bad key looks the same).
func stopsReconnecting(ref *RefusalError) bool {
	return !ref.Retryable && !accountCodes[ref.Code]
}

// retryableRefusal matches the refusals worth retrying from servers that send no
// retryable flag.
var retryableRefusal = regexp.MustCompile(`Connection limit reached|Authentication error|Unable to verify subscription status|subscription state lost|Too many connection attempts`)

func refusalFrom(ce *sio.ConnectError) *RefusalError {
	r := &RefusalError{Message: ce.Message}
	r.raw, _ = json.Marshal(ce)
	var d struct {
		Code           *string  `json:"code"`
		Retryable      *bool    `json:"retryable"`
		RetryAfterMs   *float64 `json:"retryAfterMs"`
		MaxConnections *float64 `json:"maxConnections"`
	}
	_ = json.Unmarshal(ce.Data, &d)
	if d.Retryable != nil {
		r.Retryable = *d.Retryable
	} else {
		r.Retryable = retryableRefusal.MatchString(ce.Message)
	}
	if d.Code != nil {
		r.Code = *d.Code
	} else if strings.Contains(ce.Message, "Connection limit reached") {
		r.Code = RefusalConnectionLimit
	}
	if d.RetryAfterMs != nil && *d.RetryAfterMs > 0 && *d.RetryAfterMs < 1e12 {
		r.RetryAfter = time.Duration(*d.RetryAfterMs * float64(time.Millisecond))
	}
	if d.MaxConnections != nil {
		r.MaxConnections = int(*d.MaxConnections)
	}
	return r
}

// serve runs one connection: it sends the stored subscriptions, then reads until
// the connection ends.
func (s *Stream) serve(ctx context.Context, conn *sio.Conn, queue chan<- event) error {
	// Reads use a context of their own: cancelling a read closes the connection,
	// and on ctx's end the disconnect below must still be sent.
	readCtx, cancelRead := context.WithCancel(context.Background())
	defer cancelRead()

	liveness := time.Duration(conn.Open.PingInterval+conn.Open.PingTimeout) * time.Millisecond
	if liveness <= 0 {
		liveness = fallbackLiveness
	}
	var silent atomic.Bool
	watchdog := time.AfterFunc(liveness, func() {
		silent.Store(true)
		conn.CloseNow()
	})
	defer watchdog.Stop()

	s.attach(conn)
	defer s.detach(conn)

	// On ctx's end: a Socket.IO disconnect, then a normal close. Deferred after
	// detach so it finishes before detach drops the connection.
	closed := make(chan struct{})
	stopWatch := context.AfterFunc(ctx, func() {
		defer close(closed)
		conn.Close(s.t.closeTimeout)
	})
	defer func() {
		if !stopWatch() {
			<-closed
		}
	}()

	sid, _ := json.Marshal(map[string]string{"sid": conn.SID})
	s.lifecycle(ctx, queue, EventConnect, sid)

	for {
		frame, err := conn.Read(readCtx)
		if err != nil {
			switch {
			case ctx.Err() != nil:
				return ctx.Err()
			case silent.Load():
				return fmt.Errorf("owls: no ping from the server in %v", liveness)
			}
			return fmt.Errorf("owls: connection lost: %w", err)
		}
		p, err := sio.Parse(frame)
		if err != nil {
			continue
		}
		switch p.Kind {
		case sio.KindPing:
			watchdog.Reset(liveness)
			wctx, cancel := context.WithTimeout(readCtx, s.t.writeTimeout)
			err := conn.Write(wctx, sio.Pong)
			cancel()
			if err != nil {
				return fmt.Errorf("owls: answer ping: %w", err)
			}
		case sio.KindClose:
			return errors.New("owls: the server closed the transport")
		case sio.KindDisconnect:
			return ErrServerDisconnect
		case sio.KindConnectError:
			return errors.New("owls: the server refused the connection after connecting")
		case sio.KindEvent:
			s.acked(conn, p.Name)
			select {
			case queue <- event{name: p.Name, data: p.Data}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// attach makes conn the stream's connection and sends the stored subscription and
// props subscriptions on it, then waits for their acknowledgements.
func (s *Stream) attach(conn *sio.Conn) {
	s.sendLock <- struct{}{}
	defer s.unlockSend()
	s.mu.Lock()
	s.conn = conn
	desired, props := s.desired, append([]propsSub(nil), s.props...)
	s.mu.Unlock()
	pending := s.sendStored(conn, desired, props, nil)
	if len(pending) == 0 {
		return
	}
	s.mu.Lock()
	s.pending = pending
	s.ackTimer = time.AfterFunc(s.t.ackTimeout, func() { s.resendUnacked(conn) })
	s.mu.Unlock()
}

// sendStored emits the stored subscriptions whose acknowledgement is in only (all
// of them when only is nil) and returns the acknowledgements they expect.
func (s *Stream) sendStored(conn *sio.Conn, desired json.RawMessage, props []propsSub, only map[string]bool) map[string]bool {
	acks := map[string]bool{}
	emit := func(name string, arg json.RawMessage) {
		ctx, cancel := context.WithTimeout(context.Background(), s.t.writeTimeout)
		defer cancel()
		_ = conn.Emit(ctx, name, arg) // a failed write surfaces as a failed read
	}
	if desired != nil && (only == nil || only[EventSubscribed]) {
		emit("subscribe", desired)
		acks[EventSubscribed] = true
	}
	for _, p := range props {
		ack := propsAckEvent(p.event)
		if only != nil && !only[ack] {
			continue
		}
		emit(p.event, p.payload)
		acks[ack] = true
	}
	return acks
}

// resendUnacked sends the subscriptions still unacknowledged, once.
func (s *Stream) resendUnacked(conn *sio.Conn) {
	s.sendLock <- struct{}{}
	defer s.unlockSend()
	s.mu.Lock()
	if s.conn != conn || len(s.pending) == 0 {
		s.mu.Unlock()
		return
	}
	waiting := s.pending
	s.pending, s.ackTimer = nil, nil
	desired, props := s.desired, append([]propsSub(nil), s.props...)
	s.mu.Unlock()
	s.sendStored(conn, desired, props, waiting)
}

// acked records an acknowledgement received on conn.
func (s *Stream) acked(conn *sio.Conn, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != conn || !s.pending[name] {
		return
	}
	delete(s.pending, name)
	if len(s.pending) == 0 {
		s.pending = nil
		if s.ackTimer != nil {
			s.ackTimer.Stop()
			s.ackTimer = nil
		}
	}
}

func (s *Stream) detach(conn *sio.Conn) {
	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.pending = nil
	if s.ackTimer != nil {
		s.ackTimer.Stop()
		s.ackTimer = nil
	}
	s.mu.Unlock()
	conn.CloseNow()
}

func (s *Stream) lifecycle(ctx context.Context, queue chan<- event, name string, data json.RawMessage) {
	select {
	case queue <- event{name: name, data: data}:
	case <-ctx.Done():
	}
}

func (s *Stream) dispatch(ev event) {
	s.hmu.RLock()
	hs := s.handlers[ev.name]
	s.hmu.RUnlock()
	for _, h := range hs {
		h.fn(ev.data)
	}
}
