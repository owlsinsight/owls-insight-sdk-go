package owls

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

// TestLive calls the PRODUCTION API. It is off unless OWLS_INSIGHT_LIVE=1 and
// OWLS_INSIGHT_API_KEY are both set, and must not run more than once a minute: the
// API blocks an IP address that makes too many WebSocket connection attempts in a
// minute, REST included, for several minutes. This test makes 2 (one connect, one
// reconnect), and other processes behind the same IP share the budget.
func TestLive(t *testing.T) {
	key := os.Getenv("OWLS_INSIGHT_API_KEY")
	if os.Getenv("OWLS_INSIGHT_LIVE") != "1" || key == "" {
		t.Skip("SKIPPED: live tests call the production API; set OWLS_INSIGHT_LIVE=1 and OWLS_INSIGHT_API_KEY to run them")
	}
	c, err := NewClient(key, WithBaseURL(firstNonEmpty(os.Getenv("OWLS_INSIGHT_BASE_URL"), defaultBaseURL)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pause := func(d time.Duration) { time.Sleep(d) }

	// One REST call first: an invalid key must not reach the WebSocket, where
	// failed key checks count toward the per-IP block.
	if _, err := c.GetHistoryCoverage(ctx); errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) {
		t.Fatalf("the key was refused, stopping before any WebSocket attempt: %v", err)
	} else if err != nil {
		t.Fatalf("GetHistoryCoverage: %v", err)
	}
	pause(time.Second)
	odds, err := c.GetOdds(ctx, "mlb", nil)
	if err != nil || odds.Success == nil || !*odds.Success {
		t.Fatalf("GetOdds: %+v %v", odds, err)
	}
	pause(time.Second)
	if _, err := c.GetV2Leagues(ctx, "fanduel", "mlb"); err != nil {
		t.Fatalf("GetV2Leagues: %v", err)
	}
	pause(time.Second)
	games, err := c.GetHistoryGames(ctx, &HistoryGamesParams{Sport: "mlb", Limit: 1})
	if err != nil {
		t.Fatalf("GetHistoryGames: %v", err)
	}
	if games.Data != nil && len(games.Data.Games) > 0 && games.Data.Games[0].EventID != nil {
		pause(time.Second)
		n := 0
		for _, err := range c.IterHistoryOdds(ctx, HistoryOddsParams{EventID: *games.Data.Games[0].EventID}, &PageOptions{PageSize: 50, MaxPages: 1}) {
			if err != nil {
				t.Fatalf("IterHistoryOdds: %v", err)
			}
			n++
		}
		t.Logf("history page: %d rows", n)
	}

	pause(10 * time.Second)
	s := c.Stream(WithSubscription(Subscription{Sports: []string{"mlb"}, Books: []string{"pinnacle"}}))
	var subscribed, updates, connects atomic.Int32
	s.On(EventSubscribed, func(json.RawMessage) { subscribed.Add(1) })
	s.On(EventOddsUpdate, func(json.RawMessage) { updates.Add(1) })
	s.On(EventConnect, func(json.RawMessage) { connects.Add(1) })
	sctx, scancel := context.WithTimeout(ctx, 90*time.Second)
	defer scancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(sctx) }()
	wait := func(what string, cond func() bool) {
		for !cond() {
			select {
			case err := <-done:
				var ref *RefusalError
				if errors.As(err, &ref) && ref.Code == "CONNECTION_LIMIT" {
					t.Skipf("SKIPPED: the key's WebSocket slot is held elsewhere: %v", err)
				}
				t.Fatalf("Run ended while waiting for %s: %v", what, err)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	wait("the first ack and odds-update", func() bool { return subscribed.Load() >= 1 && updates.Load() >= 1 })
	// Drop the connection once; the stream must reconnect and subscribe again.
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	acks := subscribed.Load()
	conn.CloseNow()
	wait("the reconnect's ack", func() bool { return subscribed.Load() > acks })
	if n := connects.Load(); n != 2 {
		t.Errorf("%d connections, want exactly 2", n)
	}
	scancel()
	if err := <-done; !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v", err)
	}
}
