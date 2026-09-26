package owls

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI is a local HTTP server standing in for the REST API.
type fakeAPI struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*recordedRequest
	respond  func(r *http.Request, n int) (int, http.Header, string)
}

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
	At     time.Time
}

func newFakeAPI(t *testing.T, respond func(r *http.Request, n int) (int, http.Header, string)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{respond: respond}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.requests = append(f.requests, &recordedRequest{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), string(body), time.Now()})
		n := len(f.requests)
		f.mu.Unlock()
		status, h, out := f.respond(r, n)
		for k, v := range h {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeAPI) req(i int) *recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func ok(body string) func(*http.Request, int) (int, http.Header, string) {
	return func(*http.Request, int) (int, http.Header, string) { return 200, nil, body }
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func testClient(t *testing.T, url string, opts ...Option) *Client {
	t.Helper()
	c, err := NewClient("test-key", append([]Option{WithBaseURL(url)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewClient(t *testing.T) {
	if _, err := NewClient(""); err == nil {
		t.Error("an empty key was accepted")
	}
	if _, err := NewClient("k", WithBaseURL("not a url")); err == nil {
		t.Error("an invalid base URL was accepted")
	}
	c, err := NewClient("k", WithBaseURL("http://example.test/"))
	if err != nil {
		t.Fatal(err)
	}
	if c.baseURL != "http://example.test" || c.wsURL != "http://example.test" {
		t.Errorf("base %q ws %q: the WebSocket URL should follow the base URL", c.baseURL, c.wsURL)
	}
	c, _ = NewClient("k", WithBaseURL("http://a.test"), WithWSURL("wss://b.test"))
	if c.wsURL != "wss://b.test" {
		t.Errorf("ws %q", c.wsURL)
	}
	c, _ = NewClient("k")
	if c.baseURL != defaultBaseURL || c.timeout != 30*time.Second || cap(c.historyGate) != 3 || c.retry.MaxRetries != 0 {
		t.Errorf("defaults: %+v", c)
	}
}

func TestRequestHeaders(t *testing.T) {
	api := newFakeAPI(t, ok(`{"success":true}`))
	c := testClient(t, api.URL)
	_, err := c.GetOdds(context.Background(), "nba", &OddsParams{
		Books: []string{"pinnacle", "fanduel"}, Market: "h2h", Alternates: true, ExcludeExchanges: true, League: "NBA",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := api.req(0)
	if got := r.Header.Get("User-Agent"); got != "owls-insight-go/"+Version {
		t.Errorf("User-Agent = %q, want %q", got, SDKLabel)
	}
	if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
		t.Errorf("Authorization = %q", got)
	}
	if r.Header.Get("X-Api-Key") != "" {
		t.Error("the key was sent as x-api-key")
	}
	if r.Method != "GET" || r.Path != "/api/v1/nba/odds" ||
		r.Query != "alternates=true&books=pinnacle%2Cfanduel&exclude_exchanges=true&league=NBA&market=h2h" {
		t.Errorf("request = %s %s?%s", r.Method, r.Path, r.Query)
	}
	// CONTROL: the fake server records what a client sends, not a constant.
	resp, err := http.Get(api.URL + "/x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if r := api.req(1); r.Header.Get("User-Agent") == SDKLabel || r.Header.Get("Authorization") != "" {
		t.Errorf("control request recorded UA %q auth %q", r.Header.Get("User-Agent"), r.Header.Get("Authorization"))
	}
}

func TestRequestPaths(t *testing.T) {
	api := newFakeAPI(t, ok(`{}`))
	c := testClient(t, api.URL)
	ctx := context.Background()
	calls := []struct {
		call func() error
		want string
	}{
		{func() error { _, err := c.GetV2(ctx, "fanduel", "soccer", "epl"); return err }, "/api/v2/fanduel/soccer?league=epl"},
		{func() error { _, err := c.GetV2(ctx, "4casters", "nba", ""); return err }, "/api/v2/4casters/nba"},
		{func() error { _, err := c.GetV2Leagues(ctx, "draftkings", "soccer"); return err }, "/api/v2/draftkings/soccer/leagues"},
		{func() error { _, err := c.GetScores(ctx, ""); return err }, "/api/v1/scores/live"},
		{func() error { _, err := c.GetScores(ctx, "mlb"); return err }, "/api/v1/mlb/scores/live"},
		{func() error {
			_, err := c.GetOddsHistory(ctx, OddsHistoryParams{EventID: "e", Book: "pinnacle", Market: "h2h", Side: "home", Hours: 6})
			return err
		}, "/api/odds/history?book=pinnacle&eventId=e&hours=6&market=h2h&side=home"},
		{func() error {
			_, err := c.GetHistoryOdds(ctx, HistoryOddsParams{EventID: "e", Opening: true, Limit: 10})
			return err
		}, "/api/v1/history/odds?eventId=e&limit=10&opening=true"},
		{func() error {
			_, err := c.GetInjuries(ctx, "nfl", &InjuriesParams{Status: []string{"out", "doubtful"}, IncludeActive: true})
			return err
		}, "/api/v1/nfl/injuries?includeActive=true&status=out%2Cdoubtful"},
		{func() error {
			_, err := c.GetPropResults(ctx, "mlb", PropResultsParams{Date: "2026-09-01"})
			return err
		}, "/api/v1/mlb/props/results?date=2026-09-01"},
		{func() error {
			_, err := c.GetBookProps(ctx, "nba", "betmgm", &PropsParams{Player: "X", Books: []string{"y"}})
			return err
		}, "/api/v1/nba/props/betmgm?player=X"},
		{func() error { _, err := c.GetHardRockEvents(ctx, "az", "ICE_HOCKEY", "nhl"); return err }, "/api/v2/hardrock/az/ICE_HOCKEY?league=nhl"},
		{func() error {
			_, err := c.GetProphetxOdds(ctx, ProphetXOddsParams{Sports: []string{"basketball", "golf"}, Kind: "prop"})
			return err
		}, "/api/v1/prophetx/odds?kind=prop&sport=basketball%2Cgolf"},
		{func() error { _, err := c.GetCS2Match(ctx, "2374324"); return err }, "/api/v1/history/cs2/matches/2374324"},
		{func() error {
			_, err := c.GetMoneylineHistory(ctx, LineHistoryParams{EventID: "e", Book: "pinnacle", Side: "draw"})
			return err
		}, "/api/odds/history?book=pinnacle&eventId=e&market=h2h&side=draw"},
		{func() error {
			_, err := c.GetSpreadHistory(ctx, LineHistoryParams{EventID: "e", Book: "pinnacle", Side: "home", StartTime: 1700000000000})
			return err
		}, "/api/odds/history?book=pinnacle&eventId=e&market=spreads&side=home&startTime=1700000000000"},
		{func() error {
			_, err := c.GetTotalsHistory(ctx, LineHistoryParams{EventID: "e", Book: "pinnacle", Side: "over", Hours: 3})
			return err
		}, "/api/odds/history?book=pinnacle&eventId=e&hours=3&market=totals&side=over"},
		{func() error { _, err := c.GetEsportsRealtime(ctx, "cs2", " BLAST "); return err }, "/api/v1/cs2/realtime?league=BLAST"},
	}
	for i, tc := range calls {
		if err := tc.call(); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		r := api.req(i)
		got := r.Path
		if r.Query != "" {
			got += "?" + r.Query
		}
		if got != tc.want {
			t.Errorf("call %d requested %s, want %s", i, got, tc.want)
		}
	}
}

func TestEsportsRealtime(t *testing.T) {
	// The server's answer: Pinnacle matchups, not OddsEvent rows.
	const body = `{"success": true,
		"data": [{"id": 1603451234, "league": {"name": "BLAST Premier"}, "participants": [{"name": "NAVI", "alignment": "home"}],
			"markets": [{"type": "moneyline", "prices": [{"designation": "home", "price": -150}], "limits": [{"amount": 500}]}],
			"freshness": {"ageSeconds": 2, "stale": false}}],
		"meta": {"game": "cs2", "source": "pinnacle", "league": "BLAST", "available": true, "events": 1,
			"timestamp": "2026-09-25T00:00:00Z", "freshness": {"ageSeconds": 2, "stale": false}, "cacheAgeSeconds": null, "newField": 1}}`
	api := newFakeAPI(t, ok(body))
	c := testClient(t, api.URL)
	ctx := context.Background()

	res, err := c.GetEsportsRealtime(ctx, "cs2", "BLAST")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != 1 || res.Meta == nil || *res.Meta.League != "BLAST" || *res.Meta.Events != 1 ||
		res.Meta.Freshness == nil || *res.Meta.Freshness.Stale || *res.Meta.Freshness.AgeSeconds != 2 {
		t.Fatalf("decoded %+v / %+v", res, res.Meta)
	}
	// Every field of the matchup survives, as Pinnacle sent it.
	var row map[string]any
	if err := json.Unmarshal(res.Data[0], &row); err != nil || row["participants"] == nil || row["markets"] == nil || row["league"] == nil {
		t.Fatalf("row = %s (%v)", res.Data[0], err)
	}
	// CONTROL: the same body through GetRealtime's type loses the Pinnacle fields,
	// which is why the esports keys need their own method.
	var lossy RealtimeResponse
	if err := decodeBody([]byte(body), &lossy); err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(lossy.Data[0]); strings.Contains(string(b), "participants") {
		t.Fatalf("control: OddsEvent kept the Pinnacle fields: %s", b)
	}

	before := api.count()
	for _, league := range []string{"", "   "} {
		if _, err := c.GetEsportsRealtime(ctx, "lol", league); err == nil || !strings.Contains(err.Error(), "league is required") {
			t.Errorf("GetEsportsRealtime(lol, %q) = %v, want a league error", league, err)
		}
	}
	for _, game := range []string{"cs2", "lol", "valorant", "dota2"} {
		if _, err := c.GetRealtime(ctx, game, "BLAST"); err == nil || !strings.Contains(err.Error(), "GetEsportsRealtime") {
			t.Errorf("GetRealtime(%s) = %v, want it to point at GetEsportsRealtime", game, err)
		}
	}
	if n := api.count() - before; n != 0 {
		t.Fatalf("%d requests for refused calls, want none", n)
	}
	// CONTROL: a traditional sport goes through GetRealtime, with or without a league.
	if _, err := c.GetRealtime(ctx, "nba", ""); err != nil || api.count() != before+1 {
		t.Fatalf("GetRealtime(nba) = %v after %d requests", err, api.count()-before)
	}
}

func TestUnionResultsPickTheRightType(t *testing.T) {
	api := newFakeAPI(t, ok(`{"success":true,"data":[]}`))
	c := testClient(t, api.URL)
	ctx := context.Background()
	if r, _ := c.GetScores(ctx, ""); r.All == nil || r.Sport != nil {
		t.Errorf("GetScores(all) = %+v", r)
	}
	if r, _ := c.GetScores(ctx, "nba"); r.Sport == nil || r.All != nil {
		t.Errorf("GetScores(nba) = %+v", r)
	}
	if r, _ := c.GetBookProps(ctx, "nba", "betmgm", nil); r.BetMGM == nil || r.Props != nil {
		t.Errorf("GetBookProps(betmgm) = %+v", r)
	}
	if r, _ := c.GetBookProps(ctx, "nba", "fanduel", nil); r.Props == nil || r.BetMGM != nil {
		t.Errorf("GetBookProps(fanduel) = %+v", r)
	}
	if r, _ := c.GetPropResults(ctx, "mlb", PropResultsParams{Date: "2026-09-01"}); r.Day == nil || r.Game != nil {
		t.Errorf("GetPropResults(date) = %+v", r)
	}
	if r, _ := c.GetPropResults(ctx, "mlb", PropResultsParams{GameID: "g", Date: "2026-09-01"}); r.Game == nil || r.Day != nil {
		t.Errorf("GetPropResults(game) = %+v", r)
	}
}

func TestErrorMapping(t *testing.T) {
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	cases := []struct {
		name      string
		status    int
		header    http.Header
		body      string
		sentinel  error
		code      string
		message   string
		retry     time.Duration // exact, or approx when approx is set
		approx    bool
		month     *int
		minute    *int
		details   string // "" means nil
		notSentry []error
	}{
		{name: "401 message", status: 401, body: `{"error":"Unauthorized","message":"Invalid API key"}`, sentinel: ErrUnauthorized,
			code: "UNAUTHORIZED", message: "Invalid API key", details: `{"error":"Unauthorized","message":"Invalid API key"}`,
			notSentry: []error{ErrForbidden, ErrNotFound, ErrRateLimited, ErrServiceBusy}},
		{name: "403 error only", status: 403, body: `{"error":"Requires MVP"}`, sentinel: ErrForbidden, code: "FORBIDDEN",
			message: "Requires MVP", details: `{"error":"Requires MVP"}`, notSentry: []error{ErrUnauthorized}},
		{name: "404 empty body", status: 404, body: ``, sentinel: ErrNotFound, code: "NOT_FOUND", message: "Not Found"},
		{name: "429 seconds", status: 429, header: http.Header{"Retry-After": {"7"}, "X-Ratelimit-Remaining-Minute": {"0"},
			"X-Ratelimit-Remaining-Month": {"unlimited"}}, body: `{"error":"Too many","code":"HISTORY_CONCURRENCY"}`,
			sentinel: ErrRateLimited, code: "HISTORY_CONCURRENCY", message: "Too many", retry: 7 * time.Second,
			minute: Ptr(0), details: `{"error":"Too many","code":"HISTORY_CONCURRENCY"}`},
		{name: "429 fractional seconds", status: 429, header: http.Header{"Retry-After": {"1.5"}}, body: `{}`,
			sentinel: ErrRateLimited, code: "RATE_LIMITED", message: "Too Many Requests", retry: 1500 * time.Millisecond},
		{name: "429 HTTP date", status: 429, header: http.Header{"Retry-After": {future}, "X-Ratelimit-Remaining-Month": {"0"}},
			body: `{"message":"Monthly quota used"}`, sentinel: ErrRateLimited, code: "RATE_LIMITED", message: "Monthly quota used",
			retry: 90 * time.Second, approx: true, month: Ptr(0), details: `{"message":"Monthly quota used"}`},
		{name: "429 no header defaults to 60s", status: 429, body: `{"error":"x"}`, sentinel: ErrRateLimited, code: "RATE_LIMITED",
			message: "x", retry: 60 * time.Second, details: `{"error":"x"}`},
		{name: "503 code and details", status: 503, header: http.Header{"Retry-After": {"2"}},
			body: `{"error":"busy","code":"SGP_BUSY","details":{"queue":3}}`, sentinel: ErrServiceBusy, code: "SGP_BUSY",
			message: "busy", retry: 2 * time.Second, details: `{"queue":3}`},
		{name: "503 no hint", status: 503, body: `{"error":"down"}`, sentinel: ErrServiceBusy, code: "SERVICE_BUSY", message: "down",
			details: `{"error":"down"}`},
		{name: "502 HTML", status: 502, body: `<html>bad gateway</html>`, code: "", message: "Bad Gateway",
			notSentry: []error{ErrServiceBusy}},
		{name: "410 message beats error", status: 410, body: `{"error":"Gone","message":"Use /api/v2/kalshi instead","code":"RETIRED"}`,
			code: "RETIRED", message: "Use /api/v2/kalshi instead", details: `{"error":"Gone","message":"Use /api/v2/kalshi instead","code":"RETIRED"}`},
		{name: "400 details null uses body", status: 400, body: `{"error":"bad","details":null}`, message: "bad",
			details: `{"error":"bad","details":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) { return tc.status, tc.header, tc.body })
			_, err := testClient(t, api.URL).GetOdds(context.Background(), "nba", nil)
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if ae.Status != tc.status || ae.Code != tc.code || ae.Message != tc.message {
				t.Errorf("got status %d code %q message %q", ae.Status, ae.Code, ae.Message)
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Errorf("errors.Is(%v) = false", tc.sentinel)
			}
			for _, s := range tc.notSentry {
				if errors.Is(err, s) {
					t.Errorf("CONTROL: errors.Is(%v) = true", s)
				}
			}
			if tc.approx {
				if ae.RetryAfter < tc.retry-3*time.Second || ae.RetryAfter > tc.retry {
					t.Errorf("RetryAfter = %v, want about %v", ae.RetryAfter, tc.retry)
				}
			} else if ae.RetryAfter != tc.retry {
				t.Errorf("RetryAfter = %v, want %v", ae.RetryAfter, tc.retry)
			}
			if !reflect2(ae.RemainingMonth, tc.month) || !reflect2(ae.RemainingMinute, tc.minute) {
				t.Errorf("remaining minute %v month %v", deref(ae.RemainingMinute), deref(ae.RemainingMonth))
			}
			if tc.details == "" && ae.Details != nil {
				t.Errorf("Details = %s, want nil", ae.Details)
			}
			if tc.details != "" && string(ae.Details) != tc.details {
				t.Errorf("Details = %s, want %s", ae.Details, tc.details)
			}
		})
	}
}

func reflect2(a, b *int) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestNoRetryByDefault(t *testing.T) {
	api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) {
		return 503, http.Header{"Retry-After": {"0.01"}}, `{}`
	})
	if _, err := testClient(t, api.URL).GetOdds(context.Background(), "nba", nil); !errors.Is(err, ErrServiceBusy) {
		t.Fatal(err)
	}
	if n := api.count(); n != 1 {
		t.Fatalf("%d requests without WithRetry, want 1", n)
	}
}

func TestRetryHonoursRetryAfter(t *testing.T) {
	// Each case builds its header when the first request arrives and returns the
	// least wait it asks for, measured from then. An HTTP date has whole-second
	// precision, so its wait is the parsed date minus the arrival time, not 2 s.
	cases := []struct {
		name   string
		header func(now time.Time) (http.Header, time.Duration)
	}{
		{"seconds", func(time.Time) (http.Header, time.Duration) {
			return http.Header{"Retry-After": {"0.2"}}, 200 * time.Millisecond
		}},
		{"HTTP date", func(now time.Time) (http.Header, time.Duration) {
			date := now.Add(2 * time.Second).UTC().Format(http.TimeFormat)
			parsed, err := http.ParseTime(date)
			if err != nil {
				panic(err)
			}
			return http.Header{"Retry-After": {date}}, parsed.Sub(now)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				mu    sync.Mutex
				first time.Time
				want  time.Duration
			)
			api := newFakeAPI(t, func(_ *http.Request, n int) (int, http.Header, string) {
				if n == 1 {
					mu.Lock()
					defer mu.Unlock()
					first = time.Now()
					h, least := tc.header(first)
					want = least
					return 429, h, `{"error":"slow down"}`
				}
				return 200, nil, `{"success":true}`
			})
			c := testClient(t, api.URL, WithRetry(RetryPolicy{MaxRetries: 2}))
			res, err := c.GetOdds(context.Background(), "nba", nil)
			if err != nil || res.Success == nil || !*res.Success {
				t.Fatalf("GetOdds = %+v, %v", res, err)
			}
			if api.count() != 2 {
				t.Fatalf("%d requests, want 2", api.count())
			}
			mu.Lock()
			defer mu.Unlock()
			if want <= 100*time.Millisecond {
				t.Fatalf("the case asks for only %v: it cannot tell a wait from none", want)
			}
			if waited := api.req(1).At.Sub(first); waited < want-10*time.Millisecond {
				t.Errorf("retried after %v, want at least %v", waited, want)
			}
		})
	}
}

func TestRetryRules(t *testing.T) {
	respondThenOK := func(status int, header http.Header) func(*http.Request, int) (int, http.Header, string) {
		return func(_ *http.Request, n int) (int, http.Header, string) {
			if n == 1 {
				return status, header, `{}`
			}
			return 200, nil, `{}`
		}
	}
	policy := WithRetry(RetryPolicy{MaxRetries: 3, BaseDelay: 5 * time.Millisecond, MaxRetryAfter: time.Second})
	cases := []struct {
		name     string
		status   int
		header   http.Header
		requests int
	}{
		{"503 without a hint backs off and retries", 503, nil, 2},
		{"429 with quota left retries", 429, http.Header{"Retry-After": {"0.01"}, "X-Ratelimit-Remaining-Month": {"5"}}, 2},
		{"429 with the monthly quota used up is not retried", 429, http.Header{"Retry-After": {"0.01"}, "X-Ratelimit-Remaining-Month": {"0"}}, 1},
		{"Retry-After beyond MaxRetryAfter is not retried", 503, http.Header{"Retry-After": {"30"}}, 1},
		{"500 is not retried", 500, nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t, respondThenOK(tc.status, tc.header))
			start := time.Now()
			_, _ = testClient(t, api.URL, policy).GetOdds(context.Background(), "nba", nil)
			if n := api.count(); n != tc.requests {
				t.Fatalf("%d requests, want %d", n, tc.requests)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatalf("took %v", time.Since(start))
			}
		})
	}
}

func TestPostIsNeverRetried(t *testing.T) {
	api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) {
		return 503, http.Header{"Retry-After": {"0.01"}}, `{"error":"no price","code":"no-price","reasonCode":"no-price"}`
	})
	c := testClient(t, api.URL, WithRetry(RetryPolicy{MaxRetries: 3}))
	_, err := c.BuildSgp(context.Background(), "mlb", SgpBuildParams{EventID: "1", Book: "fanduel"})
	if !errors.Is(err, ErrServiceBusy) || api.count() != 1 {
		t.Fatalf("BuildSgp: err %v after %d requests, want one 503", err, api.count())
	}
	// CONTROL: the same response to a GET is retried.
	_, _ = c.GetOdds(context.Background(), "nba", nil)
	if n := api.count() - 1; n != 4 {
		t.Fatalf("GET made %d requests, want 4 (1 + 3 retries)", n)
	}
}

func TestBuildSgp(t *testing.T) {
	t.Run("request", func(t *testing.T) {
		api := newFakeAPI(t, ok(`{"success":true,"sgp":{"american":450,"decimal":5.5,"isSGM":true},"legs":[]}`))
		res, err := testClient(t, api.URL).BuildSgp(context.Background(), "wnba", SgpBuildParams{
			EventID: "35826592", Book: "fanduel",
			Legs: []SgpLeg{
				{Type: "moneyline", Selection: "Chicago Sky"},
				{Type: "spread", Selection: "Chicago Sky", Point: Ptr(0.0)},
				{Type: "player_prop", Player: "N. Ogwumike", Category: "points", Side: "over", Line: Ptr(16.5)},
			},
		})
		if err != nil || *res.Sgp.American != 450 {
			t.Fatalf("BuildSgp = %+v, %v", res, err)
		}
		r := api.req(0)
		want := `{"eventId":"35826592","book":"fanduel","legs":[{"type":"moneyline","selection":"Chicago Sky"},` +
			`{"type":"spread","selection":"Chicago Sky","point":0},` +
			`{"type":"player_prop","player":"N. Ogwumike","category":"points","side":"over","line":16.5}]}`
		if r.Method != "POST" || r.Path != "/api/v1/wnba/sgp/build" || r.Body != want || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %s %q body %s", r.Method, r.Path, r.Header.Get("Content-Type"), r.Body)
		}
	})
	t.Run("422 envelope is returned", func(t *testing.T) {
		api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) {
			return 422, nil, fixture(t, "sgp-422-unresolved.json")
		})
		res, err := testClient(t, api.URL).BuildSgp(context.Background(), "wnba", SgpBuildParams{})
		if err != nil {
			t.Fatalf("err = %v, want the 422 returned", err)
		}
		if *res.Success || *res.ReasonCode != "unresolved" || len(res.Legs) != 2 || *res.Legs[1].Resolved ||
			string(*res.Meta.EventID) != "35826592" {
			t.Fatalf("result %+v", res)
		}
	})
	for _, tc := range []struct{ name, body, code, message string }{
		{"422 without legs is an error", `{"success":false,"error":"Invalid legs","code":"BAD_LEGS"}`, "BAD_LEGS", "Invalid legs"},
		{"422 claiming success is an error", `{"success":true,"legs":[]}`, "", "Unprocessable Entity"},
		{"422 HTML is an error", `<html>edge</html>`, "", "Unprocessable Entity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) { return 422, nil, tc.body })
			_, err := testClient(t, api.URL).BuildSgp(context.Background(), "wnba", SgpBuildParams{})
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != 422 || ae.Code != tc.code || ae.Message != tc.message {
				t.Fatalf("err = %#v", err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		header http.Header
		body   string
		code   string
		want   time.Duration
	}{
		{"no-price without a header waits 2s", nil, `{"success":false,"code":"no-price","reasonCode":"no-price"}`, "no-price", 2 * time.Second},
		{"no-price keeps the server's header", http.Header{"Retry-After": {"5"}}, `{"code":"no-price"}`, "no-price", 5 * time.Second},
		{"CONTROL: SGP_BUSY without a header has no default", nil, `{"code":"SGP_BUSY"}`, "SGP_BUSY", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) { return 503, tc.header, tc.body })
			_, err := testClient(t, api.URL).BuildSgp(context.Background(), "mlb", SgpBuildParams{})
			var ae *APIError
			if !errors.As(err, &ae) || ae.Code != tc.code || ae.RetryAfter != tc.want {
				t.Fatalf("err = %#v", err)
			}
		})
	}
}

func TestTimeoutIsPerAttemptThroughContext(t *testing.T) {
	api := newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) {
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
		}
		return 200, nil, `{}`
	})
	_, err := testClient(t, api.URL, WithTimeout(50*time.Millisecond)).GetOdds(context.Background(), "nba", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	// CONTROL: without the timeout the slow response succeeds.
	if _, err := testClient(t, api.URL, WithTimeout(0)).GetOdds(context.Background(), "nba", nil); err != nil {
		t.Fatalf("control: %v", err)
	}
}

// gatedAPI holds every request until release is closed and records the peak in flight.
func gatedAPI(t *testing.T) (*fakeAPI, *atomic.Int32, *atomic.Int32, chan struct{}) {
	var inFlight, peak atomic.Int32
	release := make(chan struct{})
	api := newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		inFlight.Add(-1)
		return 200, nil, `{}`
	})
	return api, &inFlight, &peak, release
}

func TestHistoryGate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  []Option
		path  func(c *Client) error
		limit int32
	}{
		{"history calls wait for a slot", nil, func(c *Client) error {
			_, err := c.GetHistoryGames(context.Background(), nil)
			return err
		}, 3},
		{"the legacy history path is gated too", []Option{WithHistoryConcurrency(2)}, func(c *Client) error {
			_, err := c.GetOddsHistory(context.Background(), OddsHistoryParams{EventID: "e"})
			return err
		}, 2},
		{"CONTROL: 0 disables the gate", []Option{WithHistoryConcurrency(0)}, func(c *Client) error {
			_, err := c.GetHistoryGames(context.Background(), nil)
			return err
		}, 10},
		{"CONTROL: other paths are not gated", nil, func(c *Client) error {
			_, err := c.GetOdds(context.Background(), "nba", nil)
			return err
		}, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, inFlight, peak, release := gatedAPI(t)
			c := testClient(t, api.URL, tc.opts...)
			var wg sync.WaitGroup
			for i := 0; i < 10; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); _ = tc.path(c) }()
			}
			until(t, 5*time.Second, "requests in flight", func() bool { return inFlight.Load() == tc.limit })
			time.Sleep(100 * time.Millisecond)
			if p := peak.Load(); p != tc.limit {
				t.Errorf("peak in flight %d, want %d", p, tc.limit)
			}
			close(release)
			wg.Wait()
			if api.count() != 10 {
				t.Errorf("%d requests, want 10", api.count())
			}
		})
	}
}

func TestHistoryGateHonoursContextWhileQueued(t *testing.T) {
	api, inFlight, _, release := gatedAPI(t)
	defer close(release)
	c := testClient(t, api.URL, WithHistoryConcurrency(1))
	go func() { _, _ = c.GetHistoryCoverage(context.Background()) }()
	until(t, 5*time.Second, "the first request", func() bool { return inFlight.Load() == 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.GetHistoryCoverage(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued call = %v, want DeadlineExceeded", err)
	}
	if api.count() != 1 {
		t.Fatalf("the queued call reached the server")
	}
}

// pagedAPI serves `total` history rows, paging on limit/offset.
func pagedAPI(t *testing.T, total int, fail func(n int) bool) *fakeAPI {
	return newFakeAPI(t, func(r *http.Request, n int) (int, http.Header, string) {
		if fail != nil && fail(n) {
			return 503, http.Header{"Retry-After": {"0.01"}}, `{"error":"busy"}`
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		var rows []map[string]any
		for i := offset; i < min(offset+limit, total); i++ {
			rows = append(rows, map[string]any{"recordedAt": strconv.Itoa(i), "playerName": "p" + strconv.Itoa(i)})
		}
		b, _ := json.Marshal(map[string]any{"success": true, "data": map[string]any{"snapshots": rows}})
		return 200, nil, string(b)
	})
}

func TestIterHistoryOddsPages(t *testing.T) {
	api := pagedAPI(t, 2500, nil)
	c := testClient(t, api.URL)
	var got []string
	for s, err := range c.IterHistoryOdds(context.Background(), HistoryOddsParams{EventID: "e", Book: "pinnacle", Limit: 7, Offset: 9}, nil) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, *s.RecordedAt)
	}
	if len(got) != 2500 || got[0] != "0" || got[2499] != "2499" {
		t.Fatalf("got %d rows (%v...)", len(got), got[:3])
	}
	if api.count() != 3 {
		t.Fatalf("%d requests, want 3 (1000, 1000, 500)", api.count())
	}
	for i, off := range []string{"0", "1000", "2000"} {
		if q := api.req(i).Query; q != "book=pinnacle&eventId=e&limit=1000&offset="+off {
			t.Errorf("page %d query %s", i, q)
		}
	}
}

func TestIterHistoryStopsAndOptions(t *testing.T) {
	t.Run("break stops paging", func(t *testing.T) {
		api := pagedAPI(t, 5000, nil)
		n := 0
		for _, err := range testClient(t, api.URL).IterHistoryOdds(context.Background(), HistoryOddsParams{EventID: "e"}, &PageOptions{PageSize: 10}) {
			if err != nil {
				t.Fatal(err)
			}
			if n++; n == 15 {
				break
			}
		}
		if api.count() != 2 {
			t.Fatalf("%d requests after breaking in the second page", api.count())
		}
	})
	t.Run("MaxPages and the page size clamp", func(t *testing.T) {
		api := pagedAPI(t, 100000, nil)
		n := 0
		for _, err := range testClient(t, api.URL).IterHistoryProps(context.Background(), HistoryPropsParams{EventID: "e"}, &PageOptions{PageSize: 99999, MaxPages: 2}) {
			if err != nil {
				t.Fatal(err)
			}
			n++
		}
		if n != 10000 || api.count() != 2 || api.req(0).Query != "eventId=e&limit=5000&offset=0" {
			t.Fatalf("%d rows in %d requests (%s)", n, api.count(), api.req(0).Query)
		}
	})
	t.Run("CONTROL: an endless full-page server only stops at MaxPages", func(t *testing.T) {
		api := pagedAPI(t, 1<<30, nil)
		for range testClient(t, api.URL).IterHistoryOdds(context.Background(), HistoryOddsParams{EventID: "e"}, &PageOptions{PageSize: 5, MaxPages: 3}) {
		}
		if api.count() != 3 {
			t.Fatalf("%d requests", api.count())
		}
	})
	t.Run("per-page retry by default", func(t *testing.T) {
		api := pagedAPI(t, 3, func(n int) bool { return n <= 2 })
		n := 0
		for _, err := range testClient(t, api.URL).IterHistoryOdds(context.Background(), HistoryOddsParams{EventID: "e"}, nil) {
			if err != nil {
				t.Fatal(err)
			}
			n++
		}
		if n != 3 || api.count() != 3 {
			t.Fatalf("%d rows in %d requests, want 3 rows after 2 retries", n, api.count())
		}
	})
	t.Run("an error is yielded once, then the sequence ends", func(t *testing.T) {
		api := pagedAPI(t, 3, func(int) bool { return true })
		var errs, rows int
		for _, err := range testClient(t, api.URL).IterHistoryOdds(context.Background(), HistoryOddsParams{EventID: "e"}, &PageOptions{MaxRetries: -1}) {
			if err != nil {
				errs++
				if !errors.Is(err, ErrServiceBusy) {
					t.Errorf("err = %v", err)
				}
				continue
			}
			rows++
		}
		if errs != 1 || rows != 0 || api.count() != 1 {
			t.Fatalf("%d errors, %d rows, %d requests", errs, rows, api.count())
		}
	})
}

func TestLenientDecoding(t *testing.T) {
	t.Run("unknown fields, new enum values, nulls and a mistyped field", func(t *testing.T) {
		body := fixture(t, "odds-lenient.json")
		api := newFakeAPI(t, ok(body))
		res, err := testClient(t, api.URL).GetOdds(context.Background(), "nba", nil)
		if err != nil {
			t.Fatalf("GetOdds: %v", err)
		}
		ev := res.Data["pinnacle"][0]
		if *ev.Status != "a_status_added_next_year" || ev.Phase != nil || ev.IsLive != nil || *ev.ID != "4001" {
			t.Errorf("event %+v", ev)
		}
		oc := ev.Bookmakers[0].Markets[0].Outcomes
		if *ev.Bookmakers[0].Markets[0].Key != "a_new_market_key" || *oc[0].Price != -110 || *oc[0].Point != -2.5 {
			t.Errorf("first outcome %+v", oc[0])
		}
		if oc[1].Price != nil || oc[1].Point != nil || *oc[1].Name != "Away" {
			t.Errorf("mistyped outcome %+v: the bad price should be left unset, the rest decoded", oc[1])
		}
		if string(*oc[0].SelectionID) != "29172" || string(*oc[1].SelectionID) != `"sel-abc"` {
			t.Errorf("selection ids %s %s", *oc[0].SelectionID, *oc[1].SelectionID)
		}
		if *res.Meta.Sport != "nba" || res.Meta.IgnoredParams[0] != "foo" {
			t.Errorf("meta %+v", res.Meta)
		}
		// CONTROL: a strict decode of the same body fails, so the fixture does
		// exercise the leniency.
		var strict OddsResponse
		if err := json.Unmarshal([]byte(body), &strict); err == nil {
			t.Error("control: strict decoding accepted the mistyped field")
		}
	})
	t.Run("nulls", func(t *testing.T) {
		res, err := testClient(t, newFakeAPI(t, ok(fixture(t, "odds-nulls.json"))).URL).GetOdds(context.Background(), "nba", nil)
		if err != nil || res.Success != nil || res.Data != nil || res.Meta != nil {
			t.Fatalf("%+v %v", res, err)
		}
	})
	t.Run("CONTROL: a body of the wrong shape is an error", func(t *testing.T) {
		for _, body := range []string{`[1,2]`, `<html>`, ``} {
			if _, err := testClient(t, newFakeAPI(t, ok(body)).URL).GetOdds(context.Background(), "nba", nil); err == nil {
				t.Errorf("body %q decoded without error", body)
			}
		}
	})
	t.Run("v2 data stays raw", func(t *testing.T) {
		res, err := testClient(t, newFakeAPI(t, ok(fixture(t, "v2-book.json"))).URL).GetV2(context.Background(), "fanduel", "mlb", "")
		if err != nil {
			t.Fatal(err)
		}
		if res.Data == nil {
			t.Fatal("data is nil")
		}
		var markets map[string]json.RawMessage
		if err := json.Unmarshal(*res.Data, &markets); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{
			"m1": `{"odds": [1.5, "2/1", null], "deep": {"x": {"y": [true, {"z": 1e3}]}}}`,
			"m2": `[1, 2]`, "m3": `"a string"`, "m4": `null`,
		}
		for k, v := range want {
			if string(markets[k]) != v {
				t.Errorf("data[%s] = %s, want %s verbatim", k, markets[k], v)
			}
		}
		if *res.Meta.Status != "a_new_status" || *res.Meta.AgeSeconds != 3.5 || *res.MarketCount != 4 {
			t.Errorf("envelope %+v", res)
		}
	})
	t.Run("v2 data that is not an object", func(t *testing.T) {
		// Some books send [] or null when they have nothing; the envelope still decodes.
		for _, data := range []string{`[]`, `null`, `[{"id": 1}]`} {
			body := `{"success": true, "sport": "mlb", "marketCount": 0, "data": ` + data + `, "meta": {"status": "no-data"}}`
			res, err := testClient(t, newFakeAPI(t, ok(body)).URL).GetV2(context.Background(), "bet105", "mlb", "")
			if err != nil || !*res.Success || *res.Meta.Status != "no-data" {
				t.Fatalf("data %s: %+v %v", data, res, err)
			}
			got := "<absent>"
			if res.Data != nil {
				got = string(*res.Data)
			}
			if data == `null` && res.Data != nil || data != `null` && got != data {
				t.Errorf("data %s decoded as %s", data, got)
			}
		}
		// CONTROL: the envelope this replaced (data as a map of raw markets) refuses [].
		var old struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(`{"data": []}`), &old); err == nil {
			t.Error("control: a map accepted []")
		}
	})
	t.Run("realtime frame with a sport the SDK does not name", func(t *testing.T) {
		frame := json.RawMessage(fixture(t, "realtime-frame.json"))
		var p RealtimeOddsPayload
		if err := decodeBody(frame, &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Nba) != 1 || !*p.Delta || p.Resync != nil || *p.Stream.Seq != 7 || p.Removed["curling"][0] != "c0" {
			t.Errorf("frame %+v", p)
		}
		// RealtimeSportEvents reads a sport the model has no field for.
		curling, err := RealtimeSportEvents(frame, "curling")
		if err != nil || len(curling) != 1 || *curling[0].ID != "c1" || *curling[0].HomeTeam != "A" {
			t.Errorf("curling = %+v (%v)", curling, err)
		}
		// CONTROL: the model drops that sport, which is why the helper exists.
		if b, _ := json.Marshal(p); strings.Contains(string(b), `"c1"`) || !strings.Contains(string(b), `"n1"`) {
			t.Errorf("control: the model kept curling or lost nba: %s", b)
		}
		if nba, err := RealtimeSportEvents(frame, "nba"); err != nil || len(nba) != 1 || *nba[0].ID != "n1" {
			t.Errorf("nba = %+v (%v)", nba, err)
		}
		// Keys that are not event lists, and absent sports, give nil.
		for _, key := range []string{"removed", "stream", "timestamp", "resync", "delta", "mlb"} {
			if ev, err := RealtimeSportEvents(frame, key); err != nil || ev != nil {
				t.Errorf("RealtimeSportEvents(%q) = %+v, %v; want nil, nil", key, ev, err)
			}
		}
		// Events decode leniently: a mistyped id is dropped, the rest kept.
		ev, err := RealtimeSportEvents(json.RawMessage(`{"nba": [{"id": 5, "home_team": "H"}]}`), "nba")
		if err != nil || len(ev) != 1 || ev[0].ID != nil || *ev[0].HomeTeam != "H" {
			t.Errorf("lenient events = %+v (%v)", ev, err)
		}
		if ev, err := RealtimeSportEvents(json.RawMessage(`null`), "nba"); err != nil || ev != nil {
			t.Errorf("null frame = %+v, %v", ev, err)
		}
		for _, bad := range []string{`[]`, `"x"`, `not json`} {
			if _, err := RealtimeSportEvents(json.RawMessage(bad), "nba"); err == nil {
				t.Errorf("frame %s: no error", bad)
			}
		}
		// A mistyped field of the frame is dropped too.
		var q RealtimeOddsPayload
		mistyped := []byte(`{"delta": "yes", "nba": [{"id": "n1"}], "curling": []}`)
		if err := decodeBody(mistyped, &q); err != nil || q.Delta != nil || len(q.Nba) != 1 {
			t.Errorf("mistyped frame: %+v %v", q, err)
		}
		if err := json.Unmarshal(mistyped, &q); err == nil {
			t.Error("control: encoding/json accepted the mistyped frame")
		}
	})
	t.Run("live ProphetX markets from the TypeScript SDK's fixture", func(t *testing.T) {
		var fx struct {
			Markets map[string]struct {
				Event  ProphetXRawEvent `json:"event"`
				Market ProphetXMarket   `json:"market"`
			} `json:"markets"`
		}
		if err := json.Unmarshal([]byte(fixture(t, "prophetx-markets.json")), &fx); err != nil {
			t.Fatalf("strict decode of the live payloads: %v", err)
		}
		ml := fx.Markets["moneyline"]
		if *ml.Event.Name != "Chicago White Sox at Houston Astros" || string(*ml.Market.Line) != "0" || len(ml.Market.Selections) != 2 {
			t.Errorf("moneyline %+v", ml.Market)
		}
		ld := fx.Markets["line_delta"]
		if ld.Market.Selections != nil || len(ld.Market.MarketLines) == 0 {
			t.Errorf("line_delta: selections %v, marketLines %d", ld.Market.Selections, len(ld.Market.MarketLines))
		}
		if fx.Markets["prop"].Market.TotalStake == nil {
			t.Errorf("prop: totalStake missing")
		}
	})
}

func TestV2EventName(t *testing.T) {
	for book, want := range map[string]string{
		"fanduel": "fanduel-v2-update", "4casters": "4casters-v2-update", "betonline": "betonline-realtime",
	} {
		if got := V2EventName(book); got != want {
			t.Errorf("V2EventName(%q) = %q, want %q", book, got, want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	for raw, want := range map[string]time.Duration{
		"3": 3 * time.Second, "0.25": 250 * time.Millisecond, "-4": 0,
		now.Add(10 * time.Second).Format(http.TimeFormat):  10 * time.Second,
		now.Add(-10 * time.Second).Format(http.TimeFormat): 0,
	} {
		got, ok := parseRetryAfter(http.Header{"Retry-After": {raw}}, now)
		if !ok || got != want {
			t.Errorf("parseRetryAfter(%q) = %v %v, want %v", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "soon", "NaN"} {
		if _, ok := parseRetryAfter(http.Header{"Retry-After": {raw}}, now); ok {
			t.Errorf("parseRetryAfter(%q) parsed", raw)
		}
	}
	if d, _ := parseRetryAfter(http.Header{"Retry-After": {"1e306"}}, now); d <= 0 {
		t.Errorf("a huge Retry-After overflowed to %v", d)
	}
}
