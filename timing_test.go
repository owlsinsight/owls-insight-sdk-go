package owls

import (
	"reflect"
	"testing"
	"time"
)

// The conformance tests run with shrunken timings; this pins the real ones, which
// every Owls Insight SDK shares.
func TestDefaultTimingMatchesContract(t *testing.T) {
	s := time.Second
	want := timing{
		ackTimeout:        5 * s,
		connLimitDelays:   []time.Duration{5 * s, 15 * s, 30 * s, 60 * s},
		refusalDelays:     []time.Duration{2 * s, 5 * s, 15 * s, 30 * s, 60 * s},
		accountDelays:     []time.Duration{30 * s, 60 * s},
		retryWindow:       60 * s,
		retryMaxPerWindow: 4,
		reconnectDelay:    s,
		reconnectDelayMax: 30 * s,
		stableAfter:       60 * s,
		ipBlockWait:       120 * s,
		handshakeTimeout:  20 * s,
		writeTimeout:      10 * s,
		closeTimeout:      2 * s,
	}
	if !reflect.DeepEqual(defaultTiming, want) {
		t.Fatalf("defaultTiming = %+v", defaultTiming)
	}
	if defaultReadLimit != 64<<20 || fallbackLiveness != 145*time.Second {
		t.Fatalf("read limit %d, liveness %v", defaultReadLimit, fallbackLiveness)
	}
	c, _ := NewClient("k")
	if st := c.Stream(); st.readLimit != 64<<20 || !reflect.DeepEqual(st.t, want) {
		t.Fatal("a new Stream does not use the default timing")
	}
}

func TestRefusalScheduleWithRealTiming(t *testing.T) {
	now := time.Now()
	var r refusalState
	limit := &RefusalError{Code: "CONNECTION_LIMIT", Retryable: true}
	for i, base := range []time.Duration{5, 15, 30, 60, 60} {
		d := r.delay(limit, defaultTiming, now)
		lo, hi := time.Duration(float64(base*time.Second)*0.8), time.Duration(float64(base*time.Second)*1.2)
		if d < lo || d > hi {
			t.Errorf("CONNECTION_LIMIT retry %d waits %v, want %v-%v", i, d, lo, hi)
		}
	}
	var o refusalState
	other := &RefusalError{Code: "SUBSCRIPTION_CHECK_FAILED", Retryable: true}
	if d := o.delay(other, defaultTiming, now); d < 1600*time.Millisecond || d > 2400*time.Millisecond {
		t.Errorf("first other retry waits %v, want 2 s x0.8-1.2", d)
	}
	// An account refusal waits 30 s, then 60 s for every later attempt.
	var a refusalState
	overdue := &RefusalError{Code: RefusalPaymentOverdue}
	for i, base := range []time.Duration{30, 60, 60, 60} {
		d := a.delay(overdue, defaultTiming, now)
		lo, hi := time.Duration(float64(base*time.Second)*0.8), time.Duration(float64(base*time.Second)*1.2)
		if d < lo || d > hi {
			t.Errorf("PAYMENT_OVERDUE retry %d waits %v, want %v-%v", i, d, lo, hi)
		}
	}
	// CONTROL: the same code marked retryable follows the 2 s schedule.
	var ac refusalState
	if d := ac.delay(&RefusalError{Code: RefusalPaymentOverdue, Retryable: true}, defaultTiming, now); d > 2400*time.Millisecond {
		t.Errorf("retryable PAYMENT_OVERDUE waits %v, want 2 s x0.8-1.2", d)
	}
	// The floor applies to account refusals too.
	var af refusalState
	if d := af.delay(&RefusalError{Code: RefusalTierNoWS, RetryAfter: 90 * time.Second}, defaultTiming, now); d != 90*time.Second {
		t.Errorf("floored account retry waits %v, want 90 s", d)
	}
	// The server's retryAfterMs is a floor under the jittered schedule.
	var f refusalState
	if d := f.delay(&RefusalError{Code: "CONNECTION_LIMIT", Retryable: true, RetryAfter: 45 * time.Second}, defaultTiming, now); d != 45*time.Second {
		t.Errorf("floored retry waits %v, want 45 s", d)
	}
	// A fifth retry inside the minute waits for the oldest of the last four to age out.
	w := refusalState{times: []time.Time{now.Add(-50 * time.Second), now.Add(-40 * time.Second), now.Add(-30 * time.Second), now.Add(-20 * time.Second)}}
	if d := w.delay(other, defaultTiming, now); d < 10*time.Second || d > 11*time.Second {
		t.Errorf("fifth retry in the window waits %v, want about 10 s", d)
	}
	// CONTROL: with the oldest retry already out of the window there is no extra wait.
	w = refusalState{times: []time.Time{now.Add(-61 * time.Second), now.Add(-40 * time.Second), now.Add(-30 * time.Second), now.Add(-20 * time.Second)}}
	if d := w.delay(other, defaultTiming, now); d > 2400*time.Millisecond {
		t.Errorf("control retry waits %v", d)
	}
}

func TestDropDelayWithRealTiming(t *testing.T) {
	s := &Stream{t: defaultTiming}
	for n, base := range map[int]time.Duration{0: time.Second, 1: 2 * time.Second, 3: 8 * time.Second} {
		for i := 0; i < 50; i++ {
			if d := s.dropDelay(n); d < base/2 || d >= base*3/2 {
				t.Fatalf("dropDelay(%d) = %v, want %v x0.5-1.5", n, d, base)
			}
		}
	}
	for i := 0; i < 50; i++ {
		if d := s.dropDelay(40); d < 15*time.Second || d > 30*time.Second {
			t.Fatalf("dropDelay(40) = %v, want capped at 30 s", d)
		}
	}
}

// The refusal policy: a non-retryable refusal stops a running stream unless its
// code is one of the five account codes, which are retried slowly. That includes
// the bad-key codes, a code this SDK does not know, and no code at all. A
// retryable refusal is retried whatever its code.
func TestRefusalStopPolicy(t *testing.T) {
	retried := map[string]bool{
		RefusalTierNoWS:             true,
		RefusalSubscriptionInactive: true,
		RefusalPaymentOverdue:       true,
		RefusalTrialExpired:         true,
		RefusalTrialUnverifiable:    true,
	}
	codes := append([]string{"", "SOME_FUTURE_REFUSAL"}, refusalCodes...)
	sawStop, sawRetry := false, false
	for _, code := range codes {
		got := stopsReconnecting(&RefusalError{Code: code})
		if got != !retried[code] {
			t.Errorf("non-retryable %q: stops = %t, want %t", code, got, !retried[code])
		}
		sawStop = sawStop || got
		sawRetry = sawRetry || !got
		if stopsReconnecting(&RefusalError{Code: code, Retryable: true}) {
			t.Errorf("retryable %q stops", code)
		}
	}
	// CONTROL: the table holds both outcomes, so a policy that always stops or
	// never stops fails above, and the allowlist is exactly the five account codes.
	if !sawStop || !sawRetry || len(accountCodes) != len(retried) {
		t.Fatalf("control: stops seen %t, retries seen %t, %d account codes", sawStop, sawRetry, len(accountCodes))
	}
	for code := range accountCodes {
		if !retried[code] {
			t.Errorf("accountCodes has %q, not an account code", code)
		}
	}
}

// The production schedule of an account refusal: 30 s, then 60 s for every later
// attempt, x0.8-1.2, for each of the five account codes.
func TestAccountRefusalProductionSchedule(t *testing.T) {
	if want := []time.Duration{30 * time.Second, 60 * time.Second}; !reflect.DeepEqual(defaultTiming.accountDelays, want) {
		t.Fatalf("defaultTiming.accountDelays = %v, want %v", defaultTiming.accountDelays, want)
	}
	now := time.Now()
	for code := range accountCodes {
		var r refusalState
		ref := &RefusalError{Code: code}
		if stopsReconnecting(ref) {
			t.Fatalf("%s stops", code)
		}
		for i, base := range []time.Duration{30, 60, 60} {
			d := r.delay(ref, defaultTiming, now)
			lo, hi := time.Duration(float64(base*time.Second)*0.8), time.Duration(float64(base*time.Second)*1.2)
			if d < lo || d > hi {
				t.Errorf("%s retry %d waits %v, want %v-%v", code, i, d, lo, hi)
			}
		}
	}
	// CONTROL: an unknown non-retryable code stops instead of entering the schedule.
	if !stopsReconnecting(&RefusalError{Code: "SOME_FUTURE_REFUSAL"}) {
		t.Fatal("control: an unknown non-retryable code is retried")
	}
}
