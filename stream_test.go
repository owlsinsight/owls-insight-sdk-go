package owls

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Davidgsilva/owls-insight-sdk-go/internal/sio"
)

// Every behaviour below has a CONTROL: a run of the same check that must come out
// the other way (usually a bare connection from internal/sio, without the
// Stream's policy), so a check that cannot fail is caught.

const (
	testPingInterval = 200 * time.Millisecond
	testPingTimeout  = 400 * time.Millisecond
)

// rawDial opens a bare connection with none of the Stream's behaviour.
func rawDial(t *testing.T, url, ua string, auth any, compress bool) *sio.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := sio.Dial(ctx, sio.Config{
		URL: url, APIKey: "test-key", Header: http.Header{"User-Agent": {ua}},
		Compress: compress, ReadLimit: 64 << 20,
	}, auth)
	if err != nil {
		t.Fatalf("raw dial: %v", err)
	}
	t.Cleanup(conn.CloseNow)
	return conn
}

// recorder collects events delivered to a Stream handler.
type recorder struct {
	mu  sync.Mutex
	got []json.RawMessage
}

func (r *recorder) add(raw json.RawMessage) {
	r.mu.Lock()
	r.got = append(r.got, append(json.RawMessage(nil), raw...))
	r.mu.Unlock()
}

func (r *recorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *recorder) at(i int) json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got[i]
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("bad JSON %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("bad JSON %s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

func eventsNamed(c serverConn, name string) []json.RawMessage {
	var out []json.RawMessage
	for _, e := range c.Events {
		if e.Name == name {
			if len(e.Args) > 0 {
				out = append(out, e.Args[0])
			} else {
				out = append(out, json.RawMessage("null"))
			}
		}
	}
	return out
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestStreamConnectSubscribeAndIdentity(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	c := newTestClient(t, srv.URL)
	sub := Subscription{Sports: []string{"nba"}, Books: []string{"pinnacle"}}
	s := c.Stream(WithSubscription(sub))
	s.t = fastTiming()
	var acks recorder
	s.On(EventSubscribed, acks.add)
	runStream(t, s)

	until(t, 5*time.Second, "the subscribed ack", func() bool { return acks.len() >= 1 })
	if !jsonEqual(t, acks.at(0), mustJSON(sub)) {
		t.Errorf("ack = %s, want %s", acks.at(0), mustJSON(sub))
	}
	st := srv.state()
	a := st.Attempts[0]
	if a.UA == nil || *a.UA != SDKLabel {
		t.Errorf("User-Agent = %v, want %q", a.UA, SDKLabel)
	}
	wantAuth := mustJSON(map[string]any{"sdk": SDKLabel, "subscription": sub})
	if !jsonEqual(t, a.Auth, wantAuth) {
		t.Errorf("handshake auth = %s, want %s", a.Auth, wantAuth)
	}
	if a.APIKey == nil || *a.APIKey != "test-key" {
		t.Errorf("apiKey query = %v", a.APIKey)
	}
	subs := eventsNamed(st.Connections[0], "subscribe")
	if len(subs) != 1 || !jsonEqual(t, subs[0], mustJSON(sub)) {
		t.Errorf("subscribe messages = %s, want one %s", subs, mustJSON(sub))
	}
	if SDKLabel != "owls-insight-go/"+Version {
		t.Errorf("SDKLabel = %q", SDKLabel)
	}
}

func TestStreamConnectSubscribeControl(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)

	// CONTROL: the server records whatever identity a client sends, so the check
	// above is not reading a constant.
	rawDial(t, srv.URL, "other/1", map[string]any{"sdk": "other/1"}, true)
	st := srv.state()
	if *st.Attempts[0].UA != "other/1" || !jsonEqual(t, st.Attempts[0].Auth, mustJSON(map[string]any{"sdk": "other/1"})) {
		t.Fatalf("server recorded %v / %s", *st.Attempts[0].UA, st.Attempts[0].Auth)
	}

	// CONTROL: without a subscription nothing is sent and no ack arrives, so the
	// wait above can time out.
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	var acks recorder
	s.On(EventSubscribed, acks.add)
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	if holds(500*time.Millisecond, func() bool { return acks.len() > 0 }) {
		t.Fatal("an ack arrived without a subscription")
	}
	if n := len(eventsNamed(srv.state().Connections[1], "subscribe")); n != 0 {
		t.Fatalf("%d subscribe messages without a subscription", n)
	}
}

func TestStreamSendWhileDisconnectedIsAnError(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	c := newTestClient(t, srv.URL)
	s := c.Stream()
	s.t = fastTiming()
	ctx := context.Background()
	for name, err := range map[string]error{
		"Subscribe":          s.Subscribe(ctx, Subscription{Sports: []string{"nba"}}),
		"UpdateSubscription": s.UpdateSubscription(ctx, Subscription{Books: []string{"x"}}),
		"SubscribeProps":     s.SubscribeProps(ctx, "fanduel", PropsFilter{}),
		"UnsubscribeProps":   s.UnsubscribeProps(ctx, "fanduel"),
	} {
		if !errors.Is(err, ErrNotConnected) {
			t.Errorf("%s before Run = %v, want ErrNotConnected", name, err)
		}
	}
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	// Nothing was stored while disconnected.
	if holds(300*time.Millisecond, func() bool { return len(srv.state().Connections[0].Events) > 0 }) {
		t.Fatalf("a refused call was stored and sent: %+v", srv.state().Connections[0].Events)
	}
	// CONTROL: the same call succeeds once connected, and reaches the server.
	if err := s.Subscribe(ctx, Subscription{Sports: []string{"nba"}}); err != nil {
		t.Fatalf("Subscribe while connected: %v", err)
	}
	until(t, 5*time.Second, "the subscribe", func() bool {
		return len(eventsNamed(srv.state().Connections[0], "subscribe")) == 1
	})
}

func TestStreamResubscribesAfterDrop(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	c := newTestClient(t, srv.URL)
	s := c.Stream(WithSubscription(Subscription{Sports: []string{"nba"}, Books: []string{"pinnacle"}}))
	s.t = fastTiming()
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)

	ctx := context.Background()
	if err := s.UpdateSubscription(ctx, Subscription{Books: []string{"fanduel"}, ProphetX: true}); err != nil {
		t.Fatal(err)
	}
	props := PropsFilter{Sports: []string{"nba"}, Categories: []string{"points"}}
	if err := s.SubscribeProps(ctx, "fanduel", props); err != nil {
		t.Fatal(err)
	}
	until(t, 5*time.Second, "the props subscribe", func() bool {
		return len(eventsNamed(srv.state().Connections[0], "subscribe-fanduel-props")) == 1
	})
	merged := mustJSON(map[string]any{"sports": []string{"nba"}, "books": []string{"fanduel"}, "prophetx": true})

	srv.control(map[string]any{"op": "drop"})
	until(t, 5*time.Second, "the reconnect's replay", func() bool {
		st := srv.state()
		return len(st.Connections) == 2 && len(eventsNamed(st.Connections[1], "subscribe-fanduel-props")) == 1
	})
	st := srv.state()
	second := st.Connections[1]
	if !jsonEqual(t, st.Attempts[1].Auth, mustJSON(map[string]any{"sdk": SDKLabel, "subscription": json.RawMessage(merged)})) {
		t.Errorf("reconnect handshake auth = %s", st.Attempts[1].Auth)
	}
	subs := eventsNamed(second, "subscribe")
	if len(subs) != 1 || !jsonEqual(t, subs[0], merged) {
		t.Errorf("reconnect subscribe = %s, want the stored %s", subs, merged)
	}
	s.mu.Lock()
	stored := s.desired
	s.mu.Unlock()
	if !jsonEqual(t, subs[0], stored) {
		t.Errorf("re-sent %s, stored %s", subs[0], stored)
	}
	if p := eventsNamed(second, "subscribe-fanduel-props"); !jsonEqual(t, p[0], mustJSON(props)) {
		t.Errorf("reconnect props subscribe = %s", p[0])
	}
	if second.Events[0].Name != "subscribe" {
		t.Errorf("first message after reconnect = %q, want subscribe", second.Events[0].Name)
	}
}

func TestStreamResubscribeControl(t *testing.T) {
	// CONTROL: the server keeps no subscription across connections: a bare client
	// that reconnects without re-sending has none on the new connection.
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	conn := rawDial(t, srv.URL, "raw/1", map[string]any{"sdk": "raw/1"}, true)
	if err := conn.Emit(context.Background(), "subscribe", mustJSON(map[string]any{"sports": []string{"nba"}})); err != nil {
		t.Fatal(err)
	}
	until(t, 5*time.Second, "the subscribe", func() bool { return len(srv.state().Connections[0].Events) == 1 })
	srv.control(map[string]any{"op": "drop"})
	rawDial(t, srv.URL, "raw/1", map[string]any{"sdk": "raw/1"}, true)
	time.Sleep(300 * time.Millisecond)
	if n := len(srv.state().Connections[1].Events); n != 0 {
		t.Fatalf("the second bare connection has %d messages", n)
	}
}

func TestStreamResendsUnacknowledgedSubscriptionOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		noAck int
		want  int
	}{
		{"server drops the ack", 1, 2},
		{"CONTROL: server acks", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			srv.control(map[string]any{"op": "noAck", "count": tc.noAck})
			s := newTestClient(t, srv.URL).Stream(WithSubscription(Subscription{Sports: []string{"mlb"}}))
			s.t = fastTiming() // ackTimeout 300 ms
			runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			time.Sleep(3 * s.t.ackTimeout)
			subs := eventsNamed(srv.state().Connections[0], "subscribe")
			if len(subs) != tc.want {
				t.Fatalf("server received %d subscribe messages, want %d", len(subs), tc.want)
			}
			if tc.want == 2 {
				ev := srv.state().Connections[0].Events
				gap := time.Duration(ev[1].At-ev[0].At) * time.Millisecond
				if gap < s.t.ackTimeout-20*time.Millisecond {
					t.Errorf("re-sent after %v, before the %v ack timeout", gap, s.t.ackTimeout)
				}
			}
		})
	}
}

func TestStreamNonRetryableRefusalStops(t *testing.T) {
	for _, tc := range []struct {
		name      string
		retryable bool
	}{
		{"retryable false stops", false},
		{"CONTROL: retryable true retries", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			var refusals recorder
			s.On(EventConnectError, refusals.add)
			run := runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			srv.control(map[string]any{"op": "refuse", "message": "Invalid API key",
				"data": map[string]any{"code": "INVALID_API_KEY", "retryable": tc.retryable}})
			srv.control(map[string]any{"op": "drop"})
			if !tc.retryable {
				err, ok := run.wait(5 * time.Second)
				if !ok {
					t.Fatal("Run did not return")
				}
				var ref *RefusalError
				if !errors.As(err, &ref) || ref.Code != "INVALID_API_KEY" || ref.Retryable || ref.Message != "Invalid API key" {
					t.Fatalf("Run = %v, want a non-retryable INVALID_API_KEY refusal", err)
				}
				time.Sleep(500 * time.Millisecond)
				if n := len(srv.state().Attempts); n != 2 {
					t.Fatalf("%d connection attempts, want 2 (no retry)", n)
				}
				until(t, 2*time.Second, "the connect_error event", func() bool { return refusals.len() == 1 })
				var ev struct {
					Message string `json:"message"`
					Data    struct {
						Code      string `json:"code"`
						Retryable bool   `json:"retryable"`
					} `json:"data"`
				}
				if json.Unmarshal(refusals.at(0), &ev); ev.Data.Code != "INVALID_API_KEY" || ev.Message != "Invalid API key" {
					t.Errorf("connect_error payload = %s", refusals.at(0))
				}
				return
			}
			until(t, 5*time.Second, "retries", func() bool { return len(srv.state().Attempts) >= 4 })
		})
	}
}

// runReturned reports Run's error if it has already returned.
func runReturned(r *running) (error, bool) {
	select {
	case <-r.done:
		return r.err, true
	default:
		return nil, false
	}
}

func TestStreamAccountRefusalRetriedUntilAdmitted(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  string
		stops bool
	}{
		{"PAYMENT_OVERDUE is retried and reconnects once admitted", RefusalPaymentOverdue, false},
		{"CONTROL: INVALID_API_KEY stops and never reconnects", RefusalInvalidAPIKey, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			s.t.accountDelays = []time.Duration{150 * time.Millisecond, 400 * time.Millisecond}
			// The other schedules are far shorter, so a retry on the wrong one shows.
			s.t.refusalDelays = []time.Duration{20 * time.Millisecond}
			s.t.connLimitDelays = []time.Duration{20 * time.Millisecond}
			var refusals recorder
			s.On(EventConnectError, refusals.add)
			run := runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			srv.control(map[string]any{"op": "refuse", "message": "refused for the account",
				"data": map[string]any{"code": tc.code, "retryable": false}})
			srv.control(map[string]any{"op": "drop"})
			if tc.stops {
				err, ok := run.wait(5 * time.Second)
				var ref *RefusalError
				if !ok || !errors.As(err, &ref) || ref.Code != tc.code {
					t.Fatalf("Run = %v (returned %t), want refusal %s", err, ok, tc.code)
				}
				srv.control(map[string]any{"op": "admit"})
				time.Sleep(700 * time.Millisecond)
				if st := srv.state(); len(st.Attempts) != 2 || len(st.Connections) != 1 {
					t.Fatalf("%d attempts and %d connections, want 2 and 1 (no retry)", len(st.Attempts), len(st.Connections))
				}
				return
			}
			// Attempts: first connect, refused reconnect, two refused retries.
			until(t, 5*time.Second, "two refusal retries", func() bool { return len(srv.state().Attempts) >= 4 })
			if err, done := runReturned(run); done {
				t.Fatalf("Run returned %v on an account refusal", err)
			}
			at := srv.state().Attempts
			if g := time.Duration(at[2].At-at[1].At) * time.Millisecond; g < 115*time.Millisecond || g > 260*time.Millisecond {
				t.Errorf("first retry after %v, want 150 ms x0.8-1.2", g)
			}
			if g := time.Duration(at[3].At-at[2].At) * time.Millisecond; g < 315*time.Millisecond || g > 560*time.Millisecond {
				t.Errorf("second retry after %v, want 400 ms x0.8-1.2", g)
			}
			if !at[1].Refused || !at[2].Refused || !at[3].Refused {
				t.Fatalf("attempts not refused as set up: %+v", at)
			}
			srv.control(map[string]any{"op": "admit"})
			until(t, 5*time.Second, "the reconnect", func() bool { return s.Connected() && len(srv.state().Connections) == 2 })
			if err, done := runReturned(run); done {
				t.Fatalf("Run returned %v after reconnecting", err)
			}
			if n := refusals.len(); n < 3 {
				t.Errorf("%d connect_error events, want one per refusal (at least 3)", n)
			}
		})
	}
}

// A non-retryable refusal that is not an account refusal stops a running stream:
// no code, no data at all, or a code the SDK does not know.
func TestStreamNonAccountNonRetryableRefusalStops(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		data    map[string]any
		code    string // the RefusalError.Code Run returns
		stops   bool
	}{
		{"retryable false and no code stops", "refused", map[string]any{"retryable": false}, "", true},
		{"no data at all (older server) stops", "Invalid API key", nil, "", true},
		{"retryable false with an unknown code stops", "refused", map[string]any{"code": "SOME_FUTURE_REFUSAL", "retryable": false}, "SOME_FUTURE_REFUSAL", true},
		{"CONTROL: retryable false with an account code is retried", "Payment overdue", map[string]any{"code": RefusalPaymentOverdue, "retryable": false}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			run := runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			refuse := map[string]any{"op": "refuse", "message": tc.message}
			if tc.data != nil {
				refuse["data"] = tc.data
			}
			srv.control(refuse)
			srv.control(map[string]any{"op": "drop"})
			if !tc.stops {
				until(t, 5*time.Second, "retries", func() bool { return len(srv.state().Attempts) >= 4 })
				if err, done := runReturned(run); done {
					t.Fatalf("Run returned %v", err)
				}
				return
			}
			err, ok := run.wait(5 * time.Second)
			var ref *RefusalError
			if !ok || !errors.As(err, &ref) || ref.Code != tc.code || ref.Retryable || ref.Message != tc.message {
				t.Fatalf("Run = %v (returned %t), want a non-retryable refusal with code %q", err, ok, tc.code)
			}
			time.Sleep(500 * time.Millisecond)
			if n := len(srv.state().Attempts); n != 2 {
				t.Fatalf("%d connection attempts, want 2 (no retry)", n)
			}
		})
	}
}

// An expired trial deactivates the key, so the account retry after TRIAL_EXPIRED
// meets API_KEY_DEACTIVATED, and that ends Run.
func TestStreamExpiredTrialStopsOnDeactivatedKey(t *testing.T) {
	expired := map[string]any{"message": "Trial expired", "data": map[string]any{"code": RefusalTrialExpired, "retryable": false}}
	deactivated := map[string]any{"message": "API key deactivated", "data": map[string]any{"code": RefusalAPIKeyDeactivated, "retryable": false}}
	for _, tc := range []struct {
		name     string
		sequence []any
		stops    bool
	}{
		{"TRIAL_EXPIRED, then API_KEY_DEACTIVATED on the retry, stops", []any{expired, deactivated}, true},
		{"CONTROL: TRIAL_EXPIRED on every attempt keeps retrying", []any{expired}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			run := runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			srv.control(map[string]any{"op": "refuse", "sequence": tc.sequence})
			srv.control(map[string]any{"op": "drop"})
			if !tc.stops {
				until(t, 5*time.Second, "retries", func() bool { return len(srv.state().Attempts) >= 5 })
				if err, done := runReturned(run); done {
					t.Fatalf("Run returned %v", err)
				}
				return
			}
			err, ok := run.wait(5 * time.Second)
			var ref *RefusalError
			if !ok || !errors.As(err, &ref) || ref.Code != RefusalAPIKeyDeactivated {
				t.Fatalf("Run = %v (returned %t), want API_KEY_DEACTIVATED", err, ok)
			}
			time.Sleep(500 * time.Millisecond)
			// The first connect, the refused reconnect (TRIAL_EXPIRED), one retry.
			if n := len(srv.state().Attempts); n != 3 {
				t.Fatalf("%d connection attempts, want 3", n)
			}
		})
	}
}

// The server's periodic account check closes a connection with an error event and
// a Socket.IO disconnect (41). The stream does not reconnect after it, even once
// the account would be admitted; calling Run again does.
func TestStreamAccountCheckDisconnectNeedsANewRun(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	var errs recorder
	s.On(EventError, errs.add)
	run := runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	srv.control(map[string]any{"op": "emit", "event": EventError, "payload": map[string]any{"message": "Payment overdue. Disconnecting."}})
	srv.control(map[string]any{"op": "disconnect"})
	err, ok := run.wait(5 * time.Second)
	if !ok || !errors.Is(err, ErrServerDisconnect) {
		t.Fatalf("Run = %v (returned %t), want ErrServerDisconnect", err, ok)
	}
	if errs.len() != 1 {
		t.Fatalf("%d error events before the disconnect, want 1", errs.len())
	}
	time.Sleep(500 * time.Millisecond)
	if n := len(srv.state().Attempts); n != 1 {
		t.Fatalf("%d attempts after the account check closed the stream, want 1 (no reconnect)", n)
	}
	// CONTROL: a new Run connects: the server admits the key, so only the stream
	// was holding back.
	runStream(t, s)
	until(t, 5*time.Second, "the new Run to connect", func() bool { return s.Connected() && len(srv.state().Connections) == 2 })
}

func TestStreamFirstConnectRefusalFailsRun(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	srv.control(map[string]any{"op": "refuse", "count": 1, "message": "Connection limit reached (1)",
		"data": map[string]any{"code": "CONNECTION_LIMIT", "retryable": true, "retryAfterMs": 1500, "maxConnections": 1}})
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	err := s.Run(context.Background())
	var ref *RefusalError
	if !errors.As(err, &ref) {
		t.Fatalf("Run = %v, want *RefusalError", err)
	}
	want := RefusalError{Message: "Connection limit reached (1)", Code: "CONNECTION_LIMIT", Retryable: true,
		RetryAfter: 1500 * time.Millisecond, MaxConnections: 1}
	got := *ref
	got.raw = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("refusal = %+v, want %+v", got, want)
	}
	if n := len(srv.state().Attempts); n != 1 {
		t.Fatalf("%d attempts, want 1", n)
	}
	// CONTROL: once admitted, the same stream connects and Run keeps running.
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
}

// A first connect refused for the account fails Run at once, like any other first
// refusal, although the same refusal of a reconnect is retried.
func TestStreamFirstConnectAccountRefusalFailsRun(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	overdue := map[string]any{"code": RefusalPaymentOverdue, "retryable": false}
	srv.control(map[string]any{"op": "refuse", "message": "Payment overdue", "data": overdue})
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.Run(ctx)
	var ref *RefusalError
	if !errors.As(err, &ref) || ref.Code != RefusalPaymentOverdue || ref.Retryable || ref.Message != "Payment overdue" {
		t.Fatalf("Run = %v, want a non-retryable PAYMENT_OVERDUE refusal", err)
	}
	time.Sleep(300 * time.Millisecond) // five times the account retry delay
	if n := len(srv.state().Attempts); n != 1 {
		t.Fatalf("%d attempts, want 1 (no retry of a first connect)", n)
	}
	// CONTROL: once connected, the same refusal of a reconnect is retried and Run
	// keeps running.
	srv.control(map[string]any{"op": "admit"})
	run := runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	srv.control(map[string]any{"op": "refuse", "message": "Payment overdue", "data": overdue})
	srv.control(map[string]any{"op": "drop"})
	until(t, 5*time.Second, "account retries", func() bool { return len(srv.state().Attempts) >= 5 })
	if err, done := runReturned(run); done {
		t.Fatalf("Run returned %v on a refused reconnect", err)
	}
}

// refusedReconnectGaps drops the connection while the server refuses the next
// `refusals` attempts with data, and returns the gaps between attempts from the
// first refused one on.
func refusedReconnectGaps(t *testing.T, tm timing, refusals int, data map[string]any, wantAttempts int) []time.Duration {
	t.Helper()
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = tm
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	srv.control(map[string]any{"op": "refuse", "count": refusals, "message": "refused", "data": data})
	srv.control(map[string]any{"op": "drop"})
	until(t, 10*time.Second, "the attempts", func() bool { return len(srv.state().Attempts) >= wantAttempts })
	at := srv.state().Attempts
	var gaps []time.Duration
	for i := 2; i < len(at); i++ {
		gaps = append(gaps, time.Duration(at[i].At-at[i-1].At)*time.Millisecond)
	}
	return gaps
}

func TestStreamRetryableRefusalSchedule(t *testing.T) {
	tm := fastTiming()
	tm.connLimitDelays = []time.Duration{150 * time.Millisecond, 400 * time.Millisecond}
	tm.refusalDelays = []time.Duration{40 * time.Millisecond}
	limit := map[string]any{"code": "CONNECTION_LIMIT", "retryable": true, "maxConnections": 1}
	// Attempts: first connect, refused reconnect, refused retry, admitted retry.
	gaps := refusedReconnectGaps(t, tm, 2, limit, 4)
	if gaps[0] < 115*time.Millisecond || gaps[0] > 260*time.Millisecond {
		t.Errorf("first retry after %v, want 150 ms x0.8-1.2", gaps[0])
	}
	if gaps[1] < 315*time.Millisecond || gaps[1] > 560*time.Millisecond {
		t.Errorf("second retry after %v, want 400 ms x0.8-1.2", gaps[1])
	}

	// CONTROL: another retryable code follows the other schedule.
	other := map[string]any{"code": "SUBSCRIPTION_CHECK_FAILED", "retryable": true}
	if g := refusedReconnectGaps(t, tm, 1, other, 3); g[0] > 110*time.Millisecond {
		t.Errorf("SUBSCRIPTION_CHECK_FAILED retry after %v, want 40 ms x0.8-1.2", g[0])
	}
}

func TestStreamRefusalRetryHonoursRetryAfterFloor(t *testing.T) {
	tm := fastTiming() // 60 ms schedule
	floored := map[string]any{"code": "CONNECTION_LIMIT", "retryable": true, "retryAfterMs": 600}
	if g := refusedReconnectGaps(t, tm, 1, floored, 3); g[0] < 590*time.Millisecond {
		t.Errorf("retry after %v, want at least the server's 600 ms", g[0])
	}
	// CONTROL: without retryAfterMs the same refusal is retried on the 60 ms schedule.
	plain := map[string]any{"code": "CONNECTION_LIMIT", "retryable": true}
	if g := refusedReconnectGaps(t, tm, 1, plain, 3); g[0] > 300*time.Millisecond {
		t.Errorf("control retry after %v, want about 60 ms", g[0])
	}
}

func TestStreamRefusalRetriesCappedPerWindow(t *testing.T) {
	limit := map[string]any{"message": "Connection limit reached (1)",
		"data": map[string]any{"code": "CONNECTION_LIMIT", "retryable": true}}
	// Account refusals (billing, plan) alternate with CONNECTION_LIMIT: they count
	// toward the same cap.
	overdue := map[string]any{"message": "Payment overdue",
		"data": map[string]any{"code": "PAYMENT_OVERDUE", "retryable": false}}
	mixed := []any{overdue, limit}
	for _, tc := range []struct {
		name  string
		max   int
		mixed bool
	}{
		{"at most 4 in the window", 4, false},
		{"CONTROL: without the cap", 1000, false},
		{"at most 4 in the window with account refusals mixed in", 4, true},
		{"CONTROL: account refusals mixed in, without the cap", 1000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			s.t.retryWindow = 1500 * time.Millisecond
			s.t.retryMaxPerWindow = tc.max
			var refusals recorder
			s.On(EventConnectError, refusals.add)
			runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			if tc.mixed {
				srv.control(map[string]any{"op": "refuse", "sequence": mixed})
			} else {
				srv.control(map[string]any{"op": "refuse", "message": limit["message"], "data": limit["data"]})
			}
			srv.control(map[string]any{"op": "drop"})
			time.Sleep(time.Second)
			// Attempts: the first connect, the reconnect after the drop, then retries.
			retries := len(srv.state().Attempts) - 2
			if tc.mixed {
				// Both kinds were refused, so the mix is real.
				codes := map[string]int{}
				for i := 0; i < refusals.len(); i++ {
					var ev struct {
						Data struct {
							Code string `json:"code"`
						} `json:"data"`
					}
					_ = json.Unmarshal(refusals.at(i), &ev)
					codes[ev.Data.Code]++
				}
				if codes["PAYMENT_OVERDUE"] < 2 || codes["CONNECTION_LIMIT"] < 2 {
					t.Fatalf("refusals seen by the stream: %v, want both codes at least twice", codes)
				}
			}
			if tc.max == 4 {
				if retries != 4 {
					t.Fatalf("%d refusal retries in 1 s, want 4", retries)
				}
				until(t, 3*time.Second, "the fifth retry", func() bool { return len(srv.state().Attempts)-2 == 5 })
				at := srv.state().Attempts[2:]
				if span := time.Duration(at[4].At-at[0].At) * time.Millisecond; span < 1500*time.Millisecond-30*time.Millisecond {
					t.Fatalf("5 retries within %v, want the fifth at least a window after the first", span)
				}
				return
			}
			if retries <= 8 {
				t.Fatalf("only %d retries in 1 s without the cap", retries)
			}
		})
	}
}

// acceptGaps returns the gaps between consecutive admitted connections.
func acceptGaps(st serverState) []time.Duration {
	var gaps []time.Duration
	for i := 1; i < len(st.Connections); i++ {
		gaps = append(gaps, time.Duration(st.Connections[i].At-st.Connections[i-1].At)*time.Millisecond)
	}
	return gaps
}

func TestStreamBackoffGrowsForYoungDropsAndResets(t *testing.T) {
	tm := fastTiming()
	tm.reconnectDelay = 50 * time.Millisecond
	tm.reconnectDelayMax = 2 * time.Second
	tm.stableAfter = 300 * time.Millisecond

	srv := startWSServer(t, testPingInterval, testPingTimeout)
	srv.control(map[string]any{"op": "dropAfter", "ms": 10})
	s := newTestClient(t, srv.URL).Stream()
	s.t = tm
	runStream(t, s)
	// Connection 0 is the first connect; 1-4 follow young drops: 100, 200, 400, 800 ms (x0.5-1.5).
	until(t, 10*time.Second, "five connections", func() bool { return len(srv.state().Connections) >= 5 })
	g := acceptGaps(srv.state())
	if g[0] > 350*time.Millisecond {
		t.Errorf("first reconnect after %v, want about 100 ms", g[0])
	}
	if g[3] < 400*time.Millisecond {
		t.Errorf("fourth reconnect after %v, want at least 400 ms (800 ms x0.5-1.5)", g[3])
	}

	// A connection that lives past stableAfter resets the delay to the base.
	srv.control(map[string]any{"op": "dropAfter", "ms": 450})
	before := len(srv.state().Connections)
	until(t, 10*time.Second, "two more connections", func() bool { return len(srv.state().Connections) >= before+2 })
	g = acceptGaps(srv.state())
	last := g[len(g)-1]
	if last < 450*time.Millisecond || last > 450*time.Millisecond+350*time.Millisecond {
		t.Errorf("reconnect after a stable connection came %v after the accept, want 450 ms uptime + about 50 ms", last)
	}
}

func TestStreamBackoffControlFlatWhenEveryConnectionIsStable(t *testing.T) {
	// CONTROL: with stableAfter 0 every drop resets the delay, so it stays flat:
	// the growth above comes from the young-drop rule.
	tm := fastTiming()
	tm.reconnectDelay = 50 * time.Millisecond
	tm.reconnectDelayMax = 2 * time.Second
	tm.stableAfter = 0
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	srv.control(map[string]any{"op": "dropAfter", "ms": 10})
	s := newTestClient(t, srv.URL).Stream()
	s.t = tm
	runStream(t, s)
	until(t, 10*time.Second, "five connections", func() bool { return len(srv.state().Connections) >= 5 })
	if g := acceptGaps(srv.state()); g[3] > 350*time.Millisecond {
		t.Fatalf("fourth reconnect after %v with every connection stable", g[3])
	}
}

func TestStreamRefusedReconnectCountsAsYoungDrop(t *testing.T) {
	gapAfterSecondConnection := func(t *testing.T, refusals int) time.Duration {
		tm := fastTiming()
		tm.reconnectDelay = 50 * time.Millisecond
		tm.reconnectDelayMax = 5 * time.Second
		tm.stableAfter = time.Minute
		tm.connLimitDelays = []time.Duration{10 * time.Millisecond}
		srv := startWSServer(t, testPingInterval, testPingTimeout)
		s := newTestClient(t, srv.URL).Stream()
		s.t = tm
		runStream(t, s)
		until(t, 5*time.Second, "the stream to connect", s.Connected)
		srv.control(map[string]any{"op": "dropAfter", "ms": 10})
		if refusals > 0 {
			srv.control(map[string]any{"op": "refuse", "count": refusals, "message": "limit",
				"data": map[string]any{"code": "CONNECTION_LIMIT", "retryable": true}})
		}
		srv.control(map[string]any{"op": "drop"})
		until(t, 10*time.Second, "three connections", func() bool { return len(srv.state().Connections) >= 3 })
		return acceptGaps(srv.state())[1]
	}
	// Drop, 2 refusals, admitted, drop: 4 short drops, 800 ms x0.5-1.5.
	if g := gapAfterSecondConnection(t, 2); g < 400*time.Millisecond {
		t.Errorf("with 2 refused reconnects the next delay was %v, want at least 400 ms", g)
	}
	// CONTROL: without the refusals it is 2 short drops, 200 ms x0.5-1.5.
	if g := gapAfterSecondConnection(t, 0); g > 350*time.Millisecond {
		t.Errorf("without refusals the next delay was %v, want at most 300 ms", g)
	}
}

func TestStreamNoReconnectAfterServerDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name string
		op   string
	}{
		{"server disconnect (41) ends Run", "disconnect"},
		{"CONTROL: a transport drop reconnects", "drop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			run := runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			srv.control(map[string]any{"op": tc.op})
			if tc.op == "drop" {
				until(t, 5*time.Second, "a reconnect", func() bool { return len(srv.state().Attempts) == 2 })
				return
			}
			err, ok := run.wait(5 * time.Second)
			if !ok {
				t.Fatal("Run did not return after the server disconnect")
			}
			if !errors.Is(err, ErrServerDisconnect) {
				t.Fatalf("Run = %v, want ErrServerDisconnect", err)
			}
			time.Sleep(400 * time.Millisecond)
			if n := len(srv.state().Attempts); n != 1 {
				t.Fatalf("%d attempts after a server disconnect, want 1", n)
			}
		})
	}
}

func TestStreamNegotiatesDeflate(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	if a := srv.state().Attempts[0]; !a.DeflateOffered || !a.DeflateNegotiated {
		t.Fatalf("stream: offered=%v negotiated=%v", a.DeflateOffered, a.DeflateNegotiated)
	}
	// CONTROL: a connection that does not offer it gets none.
	rawDial(t, srv.URL, "raw/1", map[string]any{"sdk": "raw/1"}, false)
	if a := srv.state().Attempts[1]; a.DeflateOffered || a.DeflateNegotiated {
		t.Fatalf("control: offered=%v negotiated=%v", a.DeflateOffered, a.DeflateNegotiated)
	}
}

func TestStreamReceives8MBMessage(t *testing.T) {
	const size = 8 << 20
	for _, tc := range []struct {
		name      string
		readLimit int64
	}{
		{"default limit", 0},
		{"CONTROL: a 1 MiB limit", 1 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Production-like ping slack: reading 8 MiB under -race takes longer than
			// the short test pings allow.
			srv := startWSServer(t, 2*time.Second, 10*time.Second)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			if tc.readLimit > 0 {
				s.readLimit = tc.readLimit
			}
			var got atomic.Int64
			s.On("big", func(raw json.RawMessage) {
				var p struct {
					Blob string `json:"blob"`
				}
				if json.Unmarshal(raw, &p) == nil {
					got.Store(int64(len(p.Blob)))
				}
			})
			runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			srv.control(map[string]any{"op": "emit", "event": "big", "size": size})
			arrived := holds(10*time.Second, func() bool { return got.Load() == size })
			if tc.readLimit == 0 && !arrived {
				t.Fatalf("the 8 MiB message did not arrive (got %d bytes)", got.Load())
			}
			if tc.readLimit > 0 {
				if arrived {
					t.Fatal("control: the message arrived through a 1 MiB limit")
				}
				// The oversized message closed the connection (1009) and the stream reconnected.
				until(t, 5*time.Second, "a reconnect", func() bool { return len(srv.state().Connections) >= 2 })
			}
		})
	}
}

func TestStreamAnswersPings(t *testing.T) {
	srv := startWSServer(t, 100*time.Millisecond, 200*time.Millisecond)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	time.Sleep(1500 * time.Millisecond)
	st := srv.state()
	if len(st.Connections) != 1 || !st.Connections[0].Connected {
		t.Fatalf("stream lost its connection: %d connections, connected=%v", len(st.Connections), st.Connections[0].Connected)
	}
	// CONTROL: a connection that never answers is closed by the server's ping timeout.
	rawDial(t, srv.URL, "raw/1", map[string]any{"sdk": "raw/1"}, true) // never read, never pong
	until(t, 3*time.Second, "the server to close the silent client", func() bool {
		st := srv.state()
		return len(st.Connections) == 2 && !st.Connections[1].Connected
	})
	if r := srv.state().Connections[1].Reason; r == nil || *r != "ping timeout" {
		t.Fatalf("control closed with %v, want ping timeout", r)
	}
}

func TestStreamDetectsServerThatStopsPinging(t *testing.T) {
	// pingInterval + pingTimeout = 600 ms: the stream treats 600 ms without a ping
	// as a dead connection.
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)

	// CONTROL: a bare connection whose server stops pinging stays open on the
	// server side, so only the client can notice.
	raw := rawDial(t, srv.URL, "raw/1", map[string]any{"sdk": "raw/1"}, true)
	go func() {
		for {
			f, err := raw.Read(context.Background())
			if err != nil {
				return
			}
			if p, _ := sio.Parse(f); p.Kind == sio.KindPing {
				_ = raw.Write(context.Background(), sio.Pong)
			}
		}
	}()
	srv.control(map[string]any{"op": "stallPings"})
	stalled := time.Now()
	until(t, 3*time.Second, "the stream to reconnect", func() bool { return len(srv.state().Connections) >= 3 })
	if waited := time.Since(stalled); waited < 400*time.Millisecond {
		t.Errorf("reconnected %v after the pings stopped, before the 600 ms liveness window", waited)
	}
	time.Sleep(time.Second)
	if !srv.state().Connections[1].Connected {
		t.Fatal("control: the server closed the bare connection itself")
	}
}

func TestStreamSlowHandlerDoesNotStopPongs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		queueSize int
		wantDrop  bool
	}{
		{"queued events", defaultQueueSize, false},
		{"CONTROL: no queue", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, 100*time.Millisecond, 200*time.Millisecond)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			s.queueSize = tc.queueSize
			var handled atomic.Int32
			s.On("tick", func(json.RawMessage) {
				time.Sleep(150 * time.Millisecond)
				handled.Add(1)
			})
			runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			srv.control(map[string]any{"op": "emit", "event": "tick", "payload": 1, "times": 8})
			time.Sleep(1500 * time.Millisecond)
			dropped := len(srv.state().Connections) > 1 || !srv.state().Connections[0].Connected
			if dropped != tc.wantDrop {
				t.Fatalf("connection dropped = %v, want %v (handled %d)", dropped, tc.wantDrop, handled.Load())
			}
		})
	}
}

func TestStreamUpgradeBlockedFirstConnect(t *testing.T) {
	for _, tc := range []struct {
		message string
		wait    time.Duration
	}{
		{"Too many connection attempts", 120 * time.Second},
		{"CONTROL: another 400", 0},
	} {
		t.Run(tc.message, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			srv.control(map[string]any{"op": "blockUpgrades", "count": 1, "message": tc.message})
			s := newTestClient(t, srv.URL).Stream()
			err := s.Run(context.Background())
			var de *DialError
			if !errors.As(err, &de) || de.StatusCode != http.StatusBadRequest {
				t.Fatalf("Run = %v, want a 400 *DialError", err)
			}
			if de.RetryAfter != tc.wait {
				t.Fatalf("RetryAfter = %v, want %v (body %q)", de.RetryAfter, tc.wait, de.Body)
			}
		})
	}
}

func TestStreamUpgradeBlockedReconnectWaits(t *testing.T) {
	gap := func(t *testing.T, message string) time.Duration {
		srv := startWSServer(t, testPingInterval, testPingTimeout)
		s := newTestClient(t, srv.URL).Stream()
		s.t = fastTiming()
		s.t.ipBlockWait = 700 * time.Millisecond
		runStream(t, s)
		until(t, 5*time.Second, "the stream to connect", s.Connected)
		srv.control(map[string]any{"op": "blockUpgrades", "count": 1, "message": message})
		srv.control(map[string]any{"op": "drop"})
		until(t, 5*time.Second, "the reconnect", func() bool { return len(srv.state().Attempts) == 2 })
		st := srv.state()
		if len(st.Blocked) != 1 {
			t.Fatalf("%d blocked upgrades, want 1", len(st.Blocked))
		}
		return time.Duration(st.Attempts[1].At-st.Blocked[0]) * time.Millisecond
	}
	if g := gap(t, "Too many connection attempts"); g < 690*time.Millisecond {
		t.Errorf("reconnected %v after the IP block, want at least the 700 ms (120 s) wait", g)
	}
	// CONTROL: another 400 only gets the dropped-connection delay.
	if g := gap(t, "Bad request"); g > 400*time.Millisecond {
		t.Errorf("control reconnected %v after a plain 400", g)
	}
}

func TestStreamCancelSendsDisconnect(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	run := runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	if err := run.stop(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run after cancel = %v, want context.Canceled", err)
	}
	until(t, 3*time.Second, "the server to see the disconnect", func() bool { return !srv.state().Connections[0].Connected })
	if r := srv.state().Connections[0].Reason; r == nil || *r != "client namespace disconnect" {
		t.Fatalf("server disconnect reason = %v, want client namespace disconnect", r)
	}
	// CONTROL: closing without the Socket.IO disconnect reads as a transport close.
	raw := rawDial(t, srv.URL, "raw/1", map[string]any{"sdk": "raw/1"}, true)
	raw.CloseNow()
	until(t, 3*time.Second, "the server to see the close", func() bool { return !srv.state().Connections[1].Connected })
	if r := srv.state().Connections[1].Reason; r == nil || *r == "client namespace disconnect" {
		t.Fatalf("control reason = %v", r)
	}
}

func TestStreamRunIsExclusive(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("a second concurrent Run was allowed")
	}
	if n := len(srv.state().Attempts); n != 1 {
		t.Fatalf("%d connection attempts, want 1", n)
	}
}

func TestStreamOnOddsUpdateDecodes(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	got := make(chan *OddsUpdatePayload, 1)
	errs := make(chan error, 1)
	off := s.OnOddsUpdate(func(p *OddsUpdatePayload, err error) {
		if err != nil {
			errs <- err
			return
		}
		got <- p
	})
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	srv.control(map[string]any{"op": "emit", "event": "odds-update", "payload": map[string]any{
		"sports":        map[string]any{"nba": []any{map[string]any{"id": "e1", "home_team": "A", "future_field": 1}}},
		"timestamp":     "2026-09-25T00:00:00Z",
		"brand_new_key": map[string]any{"x": nil},
	}})
	select {
	case p := <-got:
		if len(p.Sports["nba"]) != 1 || *p.Sports["nba"][0].ID != "e1" || *p.Timestamp != "2026-09-25T00:00:00Z" {
			t.Fatalf("decoded %+v", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no odds-update")
	}
	// A frame that is not an object reaches the handler as an error, not silence.
	srv.control(map[string]any{"op": "emit", "event": "odds-update", "payload": "not an object"})
	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "decode") {
			t.Fatalf("decode error = %v", err)
		}
	case p := <-got:
		t.Fatalf("a string frame decoded to %+v", p)
	case <-time.After(5 * time.Second):
		t.Fatal("the undecodable frame was dropped silently")
	}
	// CONTROL: after off() the handler is not called.
	off()
	srv.control(map[string]any{"op": "emit", "event": "odds-update", "payload": map[string]any{}})
	select {
	case <-got:
		t.Fatal("handler called after off()")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestStreamDeliversServerErrorEvent(t *testing.T) {
	// The server's application-level "error" event (a props TIER_REQUIRED
	// refusal, a revoked key, a reduced connection limit) is an ordinary event:
	// it reaches On(EventError) and does not end the connection.
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	var errs recorder
	s.On(EventError, errs.add)
	var notices recorder
	s.On(EventServerNotice, notices.add)
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)

	// CONTROL: another event does not reach the error handler.
	srv.control(map[string]any{"op": "emit", "event": EventServerNotice, "payload": map[string]any{"message": "hello"}})
	until(t, 5*time.Second, "the server-notice", func() bool { return notices.len() == 1 })
	if errs.len() != 0 {
		t.Fatalf("the error handler got %s", errs.at(0))
	}

	srv.control(map[string]any{"op": "emit", "event": EventError, "payload": map[string]any{
		"code": "TIER_REQUIRED", "message": "Player props require the Rookie plan", "newField": 1,
	}})
	until(t, 5*time.Second, "the error event", func() bool { return errs.len() == 1 })
	e, err := Decode[WSError](errs.at(0))
	if err != nil || e.Code == nil || *e.Code != "TIER_REQUIRED" || e.Message == nil || *e.Message != "Player props require the Rookie plan" {
		t.Fatalf("decoded %+v (%v) from %s", e, err, errs.at(0))
	}
	time.Sleep(3 * testPingInterval)
	if !s.Connected() {
		t.Fatal("the error event closed the stream")
	}
}

func TestStreamUpdateSubscriptionClearsAList(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream(WithSubscription(Subscription{Sports: []string{"nba"}, EventIDs: []string{"e1"}}))
	s.t = fastTiming()
	var acks recorder
	s.On(EventSubscribed, acks.add)
	runStream(t, s)
	until(t, 5*time.Second, "the first ack", func() bool { return acks.len() == 1 })
	ctx := context.Background()

	// CONTROL: a patch that leaves EventIDs nil does not touch the server's filter.
	if err := s.UpdateSubscription(ctx, Subscription{Books: []string{"pinnacle"}}); err != nil {
		t.Fatal(err)
	}
	until(t, 5*time.Second, "the second ack", func() bool { return acks.len() == 2 })
	if want := mustJSON(map[string]any{"sports": []string{"nba"}, "eventIds": []string{"e1"}, "books": []string{"pinnacle"}}); !jsonEqual(t, acks.at(1), want) {
		t.Fatalf("server state %s, want %s", acks.at(1), want)
	}

	// An empty, non-nil list clears it, on the server and in the stored subscription.
	if err := s.UpdateSubscription(ctx, Subscription{EventIDs: []string{}}); err != nil {
		t.Fatal(err)
	}
	until(t, 5*time.Second, "the third ack", func() bool { return acks.len() == 3 })
	want := mustJSON(map[string]any{"sports": []string{"nba"}, "eventIds": []string{}, "books": []string{"pinnacle"}})
	if !jsonEqual(t, acks.at(2), want) {
		t.Fatalf("server state %s, want %s", acks.at(2), want)
	}
	s.mu.Lock()
	stored := s.desired
	s.mu.Unlock()
	if !jsonEqual(t, stored, want) {
		t.Fatalf("stored %s, want %s", stored, want)
	}
}

func TestStreamSendHonoursContextBeforeStoring(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Subscribe(done, Subscription{Sports: []string{"nba"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Subscribe with an ended context = %v", err)
	}
	s.mu.Lock()
	stored := s.desired
	s.mu.Unlock()
	if stored != nil {
		t.Fatalf("stored %s from a call that returned an error", stored)
	}
	// CONTROL: the same call with a live context stores and sends.
	if err := s.Subscribe(context.Background(), Subscription{Sports: []string{"nba"}}); err != nil {
		t.Fatal(err)
	}
	until(t, 5*time.Second, "the subscribe", func() bool { return len(eventsNamed(srv.state().Connections[0], "subscribe")) == 1 })
}

func TestStreamResendsUnacknowledgedPropsOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		drop  int
		props int
	}{
		{"server drops the props ack", 1, 2},
		{"CONTROL: server acks", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			s := newTestClient(t, srv.URL).Stream(WithSubscription(Subscription{Sports: []string{"mlb"}}))
			s.t = fastTiming() // ackTimeout 300 ms
			runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			filter := PropsFilter{Sports: []string{"mlb"}}
			if err := s.SubscribeProps(context.Background(), "fanduel", filter); err != nil {
				t.Fatal(err)
			}
			srv.control(map[string]any{"op": "noAck", "count": tc.drop, "event": "fanduel-props-subscribed"})
			srv.control(map[string]any{"op": "drop"})
			until(t, 5*time.Second, "the reconnect", func() bool { return len(srv.state().Connections) == 2 })
			time.Sleep(3 * s.t.ackTimeout)
			second := srv.state().Connections[1]
			props := eventsNamed(second, "subscribe-fanduel-props")
			if len(props) != tc.props {
				t.Fatalf("server received %d props subscribes on the reconnect, want %d", len(props), tc.props)
			}
			for _, p := range props {
				if !jsonEqual(t, p, mustJSON(filter)) {
					t.Errorf("props payload %s, want %s", p, mustJSON(filter))
				}
			}
			// The subscription was acknowledged, so it is not re-sent.
			if n := len(eventsNamed(second, "subscribe")); n != 1 {
				t.Errorf("%d subscribe messages, want 1", n)
			}
			if tc.props == 2 {
				var at []int64
				for _, e := range second.Events {
					if e.Name == "subscribe-fanduel-props" {
						at = append(at, e.At)
					}
				}
				if gap := time.Duration(at[1]-at[0]) * time.Millisecond; gap < s.t.ackTimeout-20*time.Millisecond {
					t.Errorf("re-sent after %v, before the %v ack timeout", gap, s.t.ackTimeout)
				}
			}
		})
	}
}

func TestStreamReconnectHonoursUpgradeRetryAfter(t *testing.T) {
	gap := func(t *testing.T, retryAfter any) time.Duration {
		srv := startWSServer(t, testPingInterval, testPingTimeout)
		s := newTestClient(t, srv.URL).Stream()
		s.t = fastTiming() // dropped-connection delays of at most 50 ms
		runStream(t, s)
		until(t, 5*time.Second, "the stream to connect", s.Connected)
		srv.control(map[string]any{"op": "rejectUpgrades", "count": 1, "status": 429, "retryAfter": retryAfter, "body": "rate limited"})
		srv.control(map[string]any{"op": "drop"})
		until(t, 5*time.Second, "the reconnect", func() bool { return len(srv.state().Attempts) == 2 })
		st := srv.state()
		if len(st.Rejected) != 1 {
			t.Fatalf("%d rejected upgrades, want 1", len(st.Rejected))
		}
		return time.Duration(st.Attempts[1].At-st.Rejected[0]) * time.Millisecond
	}
	if g := gap(t, "0.7"); g < 690*time.Millisecond {
		t.Errorf("reconnected %v after a 429 with Retry-After 0.7, want at least 700 ms", g)
	}
	// CONTROL: without Retry-After only the dropped-connection delay applies.
	if g := gap(t, nil); g > 400*time.Millisecond {
		t.Errorf("control reconnected %v after a 429 without Retry-After", g)
	}
}

func TestStreamFirstConnect429IsADialError(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	srv.control(map[string]any{"op": "rejectUpgrades", "count": 1, "status": 429, "retryAfter": "30", "body": "slow down"})
	err := newTestClient(t, srv.URL).Stream().Run(context.Background())
	var de *DialError
	if !errors.As(err, &de) || de.StatusCode != http.StatusTooManyRequests || de.RetryAfter != 30*time.Second || de.Body != "slow down" {
		t.Fatalf("Run = %#v", err)
	}
}

func TestStreamRememberedDelayAcrossRuns(t *testing.T) {
	refused := map[string]any{"op": "refuse", "count": 1, "message": "Connection limit reached (1)",
		"data": map[string]any{"code": "CONNECTION_LIMIT", "retryable": true, "retryAfterMs": 500}}
	for _, tc := range []struct {
		name      string
		sameState bool
	}{
		{"the same stream waits the pending delay", true},
		{"CONTROL: a new stream connects at once", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, testPingInterval, testPingTimeout)
			srv.control(refused)
			c := newTestClient(t, srv.URL)
			s := c.Stream()
			s.t = fastTiming()
			var ref *RefusalError
			if err := s.Run(context.Background()); !errors.As(err, &ref) {
				t.Fatalf("first Run = %v", err)
			}
			if !tc.sameState {
				s = c.Stream()
				s.t = fastTiming()
			}
			runStream(t, s)
			until(t, 5*time.Second, "the second attempt", func() bool { return len(srv.state().Attempts) == 2 })
			at := srv.state().Attempts
			g := time.Duration(at[1].At-at[0].At) * time.Millisecond
			if tc.sameState && g < 490*time.Millisecond {
				t.Fatalf("the second Run connected %v after a refusal asking for 500 ms", g)
			}
			if !tc.sameState && g > 300*time.Millisecond {
				t.Fatalf("control: a new stream waited %v", g)
			}
		})
	}
}

func TestStreamSupervisorLoopBacksOff(t *testing.T) {
	// A loop that calls Run again after every refused first connect keeps to the
	// refusal schedule instead of hammering the server.
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	srv.control(map[string]any{"op": "refuse", "message": "Connection limit reached (1)",
		"data": map[string]any{"code": "CONNECTION_LIMIT", "retryable": true}})
	s := newTestClient(t, srv.URL).Stream()
	s.t = fastTiming()
	s.t.connLimitDelays = []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	for i := 0; i < 4; i++ {
		var ref *RefusalError
		if err := s.Run(context.Background()); !errors.As(err, &ref) {
			t.Fatalf("Run %d = %v", i, err)
		}
	}
	at := srv.state().Attempts
	if len(at) != 4 {
		t.Fatalf("%d attempts, want 4", len(at))
	}
	for i, base := range []time.Duration{100, 200, 400} {
		g := time.Duration(at[i+1].At-at[i].At) * time.Millisecond
		if lo := time.Duration(float64(base*time.Millisecond) * 0.8); g < lo-10*time.Millisecond {
			t.Errorf("attempt %d came %v after the previous, want at least %v", i+1, g, lo)
		}
	}
	// CONTROL: the same loop over fresh streams hits the server back to back.
	srv2 := startWSServer(t, testPingInterval, testPingTimeout)
	srv2.control(map[string]any{"op": "refuse", "message": "Connection limit reached (1)",
		"data": map[string]any{"code": "CONNECTION_LIMIT", "retryable": true}})
	c2 := newTestClient(t, srv2.URL)
	start := time.Now()
	for i := 0; i < 4; i++ {
		fresh := c2.Stream()
		fresh.t = s.t
		_ = fresh.Run(context.Background())
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("control: 4 fresh streams took %v", d)
	}
}

func TestStreamRunReturnsOnceTheRunningHandlerReturns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cancel bool
	}{
		{"cancel drops the queued events", true},
		{"CONTROL: without cancel every event is delivered", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, time.Second, 5*time.Second)
			s := newTestClient(t, srv.URL).Stream()
			s.t = fastTiming()
			release := make(chan struct{})
			var calls atomic.Int32
			s.On("tick", func(json.RawMessage) {
				if calls.Add(1) == 1 {
					<-release
				}
			})
			run := runStream(t, s)
			until(t, 5*time.Second, "the stream to connect", s.Connected)
			srv.control(map[string]any{"op": "emit", "event": "tick", "payload": 1, "times": 5})
			until(t, 5*time.Second, "the first handler call", func() bool { return calls.Load() == 1 })
			time.Sleep(200 * time.Millisecond) // the other four are queued behind it
			if tc.cancel {
				run.cancel()
				if _, returned := run.wait(300 * time.Millisecond); returned {
					t.Fatal("Run returned while a handler was still running")
				}
			}
			close(release)
			if tc.cancel {
				if _, returned := run.wait(2 * time.Second); !returned {
					t.Fatal("Run did not return after the handler did")
				}
				time.Sleep(100 * time.Millisecond)
				if n := calls.Load(); n != 1 {
					t.Fatalf("%d handler calls after cancel, want 1", n)
				}
				return
			}
			until(t, 5*time.Second, "all five events", func() bool { return calls.Load() == 5 })
		})
	}
}

// streamGoroutines lists the goroutines running or started by the SDK's own
// (non-test) code, or by coder/websocket.
func streamGoroutines() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var out []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		for _, line := range strings.Split(g, "\n") {
			line = strings.TrimSpace(line)
			sdkFile := strings.Contains(line, "/owls-insight-sdk-go/") && strings.Contains(line, ".go:") && !strings.Contains(line, "_test.go:")
			if sdkFile || strings.Contains(line, "github.com/coder/websocket") {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

func TestStreamLeavesNoGoroutines(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	until(t, 5*time.Second, "earlier tests' goroutines to end", func() bool { return len(streamGoroutines()) == 0 })
	c := newTestClient(t, srv.URL)
	for i := 0; i < 3; i++ {
		s := c.Stream(WithSubscription(Subscription{Sports: []string{"nba"}}))
		s.t = fastTiming()
		before := len(srv.state().Connections) // read before Run can connect
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.Run(ctx) }()
		until(t, 5*time.Second, "the connect", func() bool { return len(srv.state().Connections) == before+1 })
		// CONTROL: while connected, the check sees the stream's goroutines.
		if i == 0 && len(streamGoroutines()) == 0 {
			t.Fatal("control: no stream goroutine found while the stream is connected")
		}
		srv.control(map[string]any{"op": "drop"})
		until(t, 5*time.Second, "the reconnect", func() bool { return len(srv.state().Connections) == before+2 })
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v", err)
		}
	}
	if !holds(5*time.Second, func() bool { return len(streamGoroutines()) == 0 }) {
		t.Fatalf("goroutines left behind:\n\n%s", strings.Join(streamGoroutines(), "\n\n"))
	}
}

// replayedState folds a connection's subscribe and update-subscription messages
// the way the server does, and returns the last payload of each props subscribe.
func replayedState(t *testing.T, c serverConn) (json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	var state json.RawMessage
	props := map[string]json.RawMessage{}
	for _, e := range c.Events {
		arg := json.RawMessage("null")
		if len(e.Args) > 0 {
			arg = e.Args[0]
		}
		switch {
		case e.Name == "subscribe":
			state = arg
		case e.Name == "update-subscription":
			merged, err := mergeSubscription(state, arg)
			if err != nil {
				t.Fatal(err)
			}
			state = merged
		case strings.HasPrefix(e.Name, "subscribe-") && strings.HasSuffix(e.Name, "-props"):
			props[e.Name] = arg
		}
	}
	return state, props
}

func TestStreamConcurrentSendsDuringReconnects(t *testing.T) {
	srv := startWSServer(t, testPingInterval, testPingTimeout)
	s := newTestClient(t, srv.URL).Stream(WithSubscription(Subscription{Sports: []string{"nba"}}))
	s.t = fastTiming()
	runStream(t, s)
	until(t, 5*time.Second, "the stream to connect", s.Connected)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var sent atomic.Int32
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				ctx := context.Background()
				var err error
				switch (w + i) % 3 {
				case 0:
					err = s.Subscribe(ctx, Subscription{Sports: []string{"nba"}, Books: []string{fmt.Sprintf("b%d-%d", w, i)}})
				case 1:
					err = s.UpdateSubscription(ctx, Subscription{EventIDs: []string{fmt.Sprintf("e%d-%d", w, i)}})
				case 2:
					err = s.SubscribeProps(ctx, "fanduel", PropsFilter{Games: []string{fmt.Sprintf("g%d-%d", w, i)}})
				}
				if err == nil {
					sent.Add(1)
				}
				time.Sleep(time.Millisecond)
			}
		}(w)
	}
	for d := 0; d < 5; d++ {
		time.Sleep(60 * time.Millisecond)
		before := len(srv.state().Connections)
		srv.control(map[string]any{"op": "drop"})
		until(t, 5*time.Second, "the reconnect", func() bool { return len(srv.state().Connections) > before })
	}
	time.Sleep(60 * time.Millisecond)
	close(stop)
	wg.Wait()
	if sent.Load() < 20 {
		t.Fatalf("only %d sends succeeded: the churn did not exercise anything", sent.Load())
	}
	until(t, 5*time.Second, "the stream to settle", s.Connected)
	time.Sleep(200 * time.Millisecond) // let the last writes reach the server

	st := srv.state()
	live := st.Connections[len(st.Connections)-1]
	gotState, gotProps := replayedState(t, live)
	s.mu.Lock()
	desired := s.desired
	var props json.RawMessage
	for _, p := range s.props {
		if p.event == "subscribe-fanduel-props" {
			props = p.payload
		}
	}
	s.mu.Unlock()
	if !jsonEqual(t, gotState, desired) {
		t.Errorf("the server holds %s, the stream stored %s", gotState, desired)
	}
	if props == nil || !jsonEqual(t, gotProps["subscribe-fanduel-props"], props) {
		t.Errorf("the server holds props %s, the stream stored %s", gotProps["subscribe-fanduel-props"], props)
	}

	// CONTROL: the fold is order-sensitive, so a send that overtook an older one
	// would show as a difference.
	a := serverEvent{Name: "subscribe", Args: []json.RawMessage{mustJSON(map[string]any{"books": []string{"a"}})}}
	b := serverEvent{Name: "subscribe", Args: []json.RawMessage{mustJSON(map[string]any{"books": []string{"b"}})}}
	x, _ := replayedState(t, serverConn{Events: []serverEvent{a, b}})
	y, _ := replayedState(t, serverConn{Events: []serverEvent{b, a}})
	if jsonEqual(t, x, y) {
		t.Fatal("control: the fold does not depend on order")
	}
}

func TestStreamErrorsNeverContainTheKey(t *testing.T) {
	const key = "owlsinsight_SECRET0123456789"
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	silent := l.Addr().String()
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	l2, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := l2.Addr().String()
	l2.Close()

	run := func(addr string, ctx context.Context) error {
		c, err := NewClient(key, WithBaseURL("http://"+addr))
		if err != nil {
			t.Fatal(err)
		}
		s := c.Stream()
		s.t = fastTiming()
		s.t.handshakeTimeout = 200 * time.Millisecond
		return s.Run(ctx)
	}
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: Run succeeded", name)
		}
		for _, text := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%v", errors.Unwrap(err))} {
			if strings.Contains(text, key) {
				t.Fatalf("%s: the key is in the error: %s", name, text)
			}
		}
	}

	err = run(closed, context.Background())
	check("refused port", err)
	var op *net.OpError
	if !errors.As(err, &op) {
		t.Errorf("refused port: errors.As(*net.OpError) failed on %v", err)
	}

	err = run(silent, context.Background())
	check("handshake timeout", err)
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Errorf("handshake timeout = %v, want context.DeadlineExceeded", err)
	}

	// CONTROL: a caller that cancels during the handshake gets context.Canceled,
	// not a deadline, so the check above tells the two apart.
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	err = run(silent, ctx)
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("cancelled handshake = %v, want context.Canceled", err)
	}
}
