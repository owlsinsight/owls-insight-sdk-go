package owls

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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
		{func() error { _, err := c.GetV2Markets(ctx, "draftkings", "nfl", "", "props-*"); return err }, "/api/v2/draftkings/nfl?marketIds=props-%2A"},
		{func() error { _, err := c.GetV2Markets(ctx, "draftkings", "soccer", "epl", "main"); return err }, "/api/v2/draftkings/soccer?league=epl&marketIds=main"},
		{func() error { _, err := c.GetV2Markets(ctx, "draftkings", "nfl", "", ""); return err }, "/api/v2/draftkings/nfl"},
		{func() error { _, err := c.GetV2Leagues(ctx, "draftkings", "soccer"); return err }, "/api/v2/draftkings/soccer/leagues"},
		// Prop Builder: every sport is one league; marketIds selects entry families.
		{func() error { _, err := c.GetV2(ctx, "propbuilder", "nfl", ""); return err }, "/api/v2/propbuilder/nfl"},
		{func() error { _, err := c.GetV2Markets(ctx, "propbuilder", "nba", "", "ou-*,ss-*"); return err },
			"/api/v2/propbuilder/nba?marketIds=ou-%2A%2Css-%2A"},
		{func() error { _, err := c.GetV2Leagues(ctx, "propbuilder", "mlb"); return err }, "/api/v2/propbuilder/mlb/leagues"},
		// 888sport: a book key that starts with a digit, single-league and multi-league sports.
		{func() error { _, err := c.GetV2(ctx, "888sport", "nfl", ""); return err }, "/api/v2/888sport/nfl"},
		{func() error { _, err := c.GetV2(ctx, "888sport", "soccer", "england-premier-league"); return err },
			"/api/v2/888sport/soccer?league=england-premier-league"},
		{func() error { _, err := c.GetV2Leagues(ctx, "888sport", "soccer"); return err }, "/api/v2/888sport/soccer/leagues"},
		{func() error {
			_, err := c.GetHistorySplits(ctx, HistorySplitsParams{Sport: "nfl", StartDate: "2026-09-29", EndDate: "2026-09-30", Limit: 5})
			return err
		}, "/api/v1/history/splits?endDate=2026-09-30&limit=5&sport=nfl&startDate=2026-09-29"},
		{func() error {
			_, err := c.GetHistorySplits(ctx, HistorySplitsParams{EventID: "nfl:Dallas Cowboys@Green Bay Packers-20261004", Book: "betmgm", Offset: 100})
			return err
		}, "/api/v1/history/splits?book=betmgm&eventId=nfl%3ADallas+Cowboys%40Green+Bay+Packers-20261004&offset=100"},
		// The multi-sport day schedule: the zero params send nothing; sports are one comma list under "sport".
		{func() error { _, err := c.GetDaySchedule(ctx, DayScheduleParams{}); return err }, "/api/v1/schedule"},
		{func() error {
			_, err := c.GetDaySchedule(ctx, DayScheduleParams{Date: "2026-10-04", TZ: "America/Los_Angeles", Days: 3, Sports: []string{"nfl", "ncaaf"}})
			return err
		}, "/api/v1/schedule?date=2026-10-04&days=3&sport=nfl%2Cncaaf&tz=America%2FLos_Angeles"},
		{func() error { _, err := c.GetSchedule(ctx, "nfl"); return err }, "/api/v1/nfl/schedule"},
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
		// EV: venues=true only when Venues is set; nil params send nothing.
		{func() error {
			_, err := c.GetEV(ctx, "nfl", &EVParams{MinEV: 2, Book: "kalshi", Venues: true})
			return err
		}, "/api/v1/nfl/ev?book=kalshi&min_ev=2&venues=true"},
		{func() error { _, err := c.GetEV(ctx, "nfl", &EVParams{Book: "fanduel"}); return err }, "/api/v1/nfl/ev?book=fanduel"},
		{func() error { _, err := c.GetEV(ctx, "nfl", nil); return err }, "/api/v1/nfl/ev"},
		// EV lines: market defaults to both; min_ev and book only when given; each
		// market is trimmed and lower-cased, blanks and repeats dropped.
		{func() error { _, err := c.GetEVLines(ctx, "nfl", nil); return err }, "/api/v1/nfl/ev?market=spreads%2Ctotals"},
		{func() error { _, err := c.GetEVLines(ctx, "nfl", &EVLinesParams{}); return err }, "/api/v1/nfl/ev?market=spreads%2Ctotals"},
		{func() error {
			_, err := c.GetEVLines(ctx, "nfl", &EVLinesParams{Markets: []string{" Totals ", "", "spreads", "TOTALS"}})
			return err
		}, "/api/v1/nfl/ev?market=totals%2Cspreads"},
		{func() error {
			_, err := c.GetEVLines(ctx, "nfl", &EVLinesParams{Markets: []string{"spreads, totals,"}})
			return err
		},
			"/api/v1/nfl/ev?market=spreads%2Ctotals"},
		{func() error {
			_, err := c.GetEVLines(ctx, "nfl", &EVLinesParams{Markets: []string{"totals"}, MinEV: 2, Book: "fanduel"})
			return err
		}, "/api/v1/nfl/ev?book=fanduel&market=totals&min_ev=2"},
		{func() error { _, err := c.GetFair(ctx, "nfl"); return err }, "/api/v1/nfl/fair"},
		// Props EV: all=true only when All is set.
		{func() error { _, err := c.GetPropsEV(ctx, "nba", nil); return err }, "/api/v1/nba/props/ev"},
		{func() error {
			_, err := c.GetPropsEV(ctx, "nba", &PropsEVParams{MinEV: 2.5, Book: "fanduel", Player: "Tatum", Category: "points", All: true})
			return err
		}, "/api/v1/nba/props/ev?all=true&book=fanduel&category=points&min_ev=2.5&player=Tatum"},
		{func() error { _, err := c.GetPropsEV(ctx, "nba", &PropsEVParams{All: false}); return err }, "/api/v1/nba/props/ev"},
		// Play-by-play: no event id lists the games; since=0 is sent, a nil Since is not.
		{func() error { _, err := c.GetPlays(ctx, "nfl", "", &PlaysParams{}); return err }, "/api/v1/nfl/plays"},
		{func() error { _, err := c.GetPlays(ctx, "nhl", "  ", nil); return err }, "/api/v1/nhl/plays"},
		{func() error {
			_, err := c.GetPlays(ctx, "nfl", "nfl:Cincinnati Bengals@Baltimore Ravens-20261025", &PlaysParams{Since: Ptr(0.0), Limit: 50})
			return err
		}, "/api/v1/nfl/plays?eventId=nfl%3ACincinnati+Bengals%40Baltimore+Ravens-20261025&limit=50&since=0"},
		{func() error { _, err := c.GetPlays(ctx, "nba", "e1", &PlaysParams{Since: Ptr(1234567.0)}); return err }, "/api/v1/nba/plays?eventId=e1&since=1234567"},
		{func() error { _, err := c.GetPlays(ctx, "nba", "e1", &PlaysParams{}); return err }, "/api/v1/nba/plays?eventId=e1"},
		// FanDuel state marketIds on the odds routes and the props routes.
		{func() error {
			_, err := c.GetOdds(ctx, "nfl", &OddsParams{State: "co", ExcludeExchanges: true})
			return err
		}, "/api/v1/nfl/odds?exclude_exchanges=true&state=co"},
		{func() error { _, err := c.GetSpreads(ctx, "nfl", &OddsParams{State: "ks"}); return err }, "/api/v1/nfl/spreads?state=ks"},
		{func() error { _, err := c.GetMoneyline(ctx, "nfl", &OddsParams{ExcludeExchanges: true}); return err }, "/api/v1/nfl/moneyline?exclude_exchanges=true"},
		{func() error {
			_, err := c.GetTotals(ctx, "nfl", &OddsParams{ExcludeExchanges: false, State: "nj"})
			return err
		}, "/api/v1/nfl/totals?state=nj"},
		{func() error {
			_, err := c.GetProps(ctx, "nba", &PropsParams{Books: []string{"fanduel"}, State: "ks"})
			return err
		}, "/api/v1/nba/props?books=fanduel&state=ks"},
		{func() error {
			_, err := c.GetBookProps(ctx, "nba", "fanduel", &PropsParams{Books: []string{"x"}, State: "ks"})
			return err
		}, "/api/v1/nba/props/fanduel?state=ks"},
		// Default-excluded v2 families: Polymarket US full-*, Fanatics Markets books-*.
		{func() error { _, err := c.GetV2(ctx, "polymarketus", "nfl", "nfl"); return err }, "/api/v2/polymarketus/nfl?league=nfl"},
		{func() error { _, err := c.GetV2Markets(ctx, "polymarketus", "nfl", "nfl", "full-*"); return err },
			"/api/v2/polymarketus/nfl?league=nfl&marketIds=full-%2A"},
		{func() error { _, err := c.GetV2Leagues(ctx, "polymarketus", "nfl"); return err }, "/api/v2/polymarketus/nfl/leagues"},
		{func() error { _, err := c.GetV2Markets(ctx, "fanaticsmarkets", "nfl", "", "books-*"); return err },
			"/api/v2/fanaticsmarkets/nfl?marketIds=books-%2A"},
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
	if r, _ := c.GetPlays(ctx, "nfl", "", nil); r.Games == nil || r.Game != nil {
		t.Errorf("GetPlays(list) = %+v", r)
	}
	if r, _ := c.GetPlays(ctx, "nfl", "e1", nil); r.Game == nil || r.Games != nil {
		t.Errorf("GetPlays(game) = %+v", r)
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
		{name: "403 data-only key keeps its code", status: 403,
			body:     `{"error":"data_key_forbidden","message":"This is a read-only data key."}`,
			sentinel: ErrForbidden, code: DataKeyForbiddenCode, message: "This is a read-only data key.",
			details: `{"error":"data_key_forbidden","message":"This is a read-only data key."}`},
		// CONTROL: the code is read from error alone, as the TypeScript SDK does; a
		// code field of that value does not change a 403's FORBIDDEN.
		{name: "403 code field is not the data-key refusal", status: 403, body: `{"error":"Requires MVP","code":"data_key_forbidden"}`,
			sentinel: ErrForbidden, code: "FORBIDDEN", message: "Requires MVP", details: `{"error":"Requires MVP","code":"data_key_forbidden"}`},
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

func TestHistorySplits(t *testing.T) {
	t.Run("rows with absent markets and fields", func(t *testing.T) {
		api := newFakeAPI(t, ok(fixture(t, "history-splits.json")))
		res, err := testClient(t, api.URL).GetHistorySplits(context.Background(),
			HistorySplitsParams{Sport: "nfl", StartDate: "2026-09-29", EndDate: "2026-09-30", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if !isHistoryPath(api.req(0).Path) {
			t.Errorf("%s does not go through the history gate", api.req(0).Path)
		}
		d := res.Data
		if !*res.Success || d == nil || *d.Sport != "nfl" || d.EventID != nil || len(d.Splits) != 3 {
			t.Fatalf("decoded %+v / %+v", res, d)
		}
		if *d.Window.Start != "2026-09-29" || *d.Window.End != "2026-09-30" ||
			*d.Pagination.Total != 3 || *d.Pagination.Limit != 5 || *d.Pagination.HasMore {
			t.Errorf("window %+v pagination %+v", d.Window, d.Pagination)
		}

		dk := d.Splits[0]
		if *dk.Book != "dk" || *dk.Title != "DraftKings" || *dk.RecordedAt != "2026-09-29T14:00:00.000Z" ||
			*dk.EventID != "nfl:Dallas Cowboys@Green Bay Packers-20261004" || *dk.Sport != "nfl" {
			t.Errorf("dk row %+v", dk)
		}
		if *dk.Spread.AwayLine != 3.5 || *dk.Spread.HomeHandlePct != 59 || *dk.Total.Line != 47.5 ||
			*dk.Total.UnderBetsPct != 30 || *dk.Moneyline.HomePrice != -175 || *dk.Moneyline.AwayHandlePct != 22 {
			t.Errorf("dk markets %+v %+v %+v", dk.Spread, dk.Total, dk.Moneyline)
		}

		// BetMGM publishes tickets only: no handle fields, and no total at that read.
		mgm := d.Splits[1]
		if *mgm.Book != "betmgm" || *mgm.Title != "BetMGM" || mgm.Total != nil {
			t.Errorf("betmgm row %+v", mgm)
		}
		if mgm.Spread.AwayHandlePct != nil || mgm.Spread.HomeHandlePct != nil ||
			mgm.Moneyline.AwayHandlePct != nil || mgm.Moneyline.HomeHandlePct != nil {
			t.Errorf("betmgm handle fields were invented: %+v %+v", mgm.Spread, mgm.Moneyline)
		}
		if *mgm.Spread.AwayBetsPct != 44 || *mgm.Spread.HomeBetsPct != 56 || *mgm.Moneyline.HomeBetsPct != 100 {
			t.Errorf("betmgm ticket fields %+v %+v", mgm.Spread, mgm.Moneyline)
		}
		// CONTROL: a 0 the API sent is kept as a 0, so nil above means absent, not zero.
		if mgm.Moneyline.AwayBetsPct == nil || *mgm.Moneyline.AwayBetsPct != 0 {
			t.Errorf("an explicit 0 was lost: %v", mgm.Moneyline.AwayBetsPct)
		}

		// A row with one market, and only some of its fields.
		circa := d.Splits[2]
		if circa.Spread != nil || circa.Moneyline != nil || *circa.Total.Line != 47 || *circa.Total.OverHandlePct != 55 ||
			circa.Total.UnderHandlePct != nil || circa.Total.OverBetsPct != nil || circa.Total.UnderBetsPct != nil {
			t.Errorf("circa row %+v total %+v", circa, circa.Total)
		}
	})
	t.Run("an eventId query echoes the id and has no window", func(t *testing.T) {
		const id = "nfl:Dallas Cowboys@Green Bay Packers-20261004"
		api := newFakeAPI(t, ok(`{"success": true, "data": {"eventId": "`+id+`", "splits": [],
			"pagination": {"total": 0, "limit": 100, "offset": 0, "hasMore": false}}}`))
		res, err := testClient(t, api.URL).GetHistorySplits(context.Background(), HistorySplitsParams{EventID: id})
		if err != nil {
			t.Fatal(err)
		}
		if q, _ := url.ParseQuery(api.req(0).Query); q.Get("eventId") != id || len(q) != 1 {
			t.Errorf("query %s", api.req(0).Query)
		}
		if *res.Data.EventID != id || res.Data.Sport != nil || res.Data.Window != nil || len(res.Data.Splits) != 0 {
			t.Errorf("data %+v", res.Data)
		}
	})
	t.Run("refusals are APIErrors", func(t *testing.T) {
		cases := []struct {
			status   int
			header   http.Header
			body     string
			sentinel error
			code     string
			retry    time.Duration
		}{
			{429, http.Header{"Retry-After": {"4"}},
				`{"success":false,"retryAfterSeconds":4,"scope":"history","tier":"mvp","error":"History concurrency limit exceeded","code":"HISTORY_CONCURRENCY","limit":3,"current":3}`,
				ErrRateLimited, "HISTORY_CONCURRENCY", 4 * time.Second},
			{503, http.Header{"Retry-After": {"2"}},
				`{"success":false,"error":"Temporarily unavailable: rate limit system","retryAfterSeconds":2}`,
				ErrServiceBusy, "SERVICE_BUSY", 2 * time.Second},
			{403, nil, `{"error":"Upgrade required","message":"Historical betting splits require an MVP subscription.","requiredTier":"mvp"}`,
				ErrForbidden, "FORBIDDEN", 0},
			{400, nil, `{"success":false,"error":"Querying by sport alone is too broad. Add startDate to narrow the query."}`,
				nil, "", 0},
		}
		for _, tc := range cases {
			api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) { return tc.status, tc.header, tc.body })
			_, err := testClient(t, api.URL).GetHistorySplits(context.Background(), HistorySplitsParams{Sport: "nfl"})
			var ae *APIError
			if !errors.As(err, &ae) || ae.Status != tc.status || ae.Code != tc.code || ae.RetryAfter != tc.retry {
				t.Errorf("%d: err %v (%+v)", tc.status, err, ae)
				continue
			}
			if tc.sentinel != nil && !errors.Is(err, tc.sentinel) {
				t.Errorf("%d: errors.Is(%v) = false", tc.status, tc.sentinel)
			}
			if tc.status == 400 && !strings.Contains(ae.Message, "too broad") {
				t.Errorf("400 message %q", ae.Message)
			}
		}
	})
	t.Run("WithRetry retries a history 429 after Retry-After", func(t *testing.T) {
		api := newFakeAPI(t, func(_ *http.Request, n int) (int, http.Header, string) {
			if n == 1 {
				return 429, http.Header{"Retry-After": {"0.01"}}, `{"code":"HISTORY_CONCURRENCY"}`
			}
			return 200, nil, `{"success": true, "data": {"splits": []}}`
		})
		c := testClient(t, api.URL, WithRetry(RetryPolicy{MaxRetries: 1}))
		if res, err := c.GetHistorySplits(context.Background(), HistorySplitsParams{EventID: "e"}); err != nil || !*res.Success || api.count() != 2 {
			t.Fatalf("%+v %v after %d requests", res, err, api.count())
		}
	})
}

func TestDaySchedule(t *testing.T) {
	t.Run("games of every shape", func(t *testing.T) {
		api := newFakeAPI(t, ok(fixture(t, "day-schedule.json")))
		res, err := testClient(t, api.URL).GetDaySchedule(context.Background(), DayScheduleParams{TZ: "America/New_York"})
		if err != nil {
			t.Fatal(err)
		}
		if r := api.req(0); r.Path != "/api/v1/schedule" || r.Query != "tz=America%2FNew_York" || isHistoryPath(r.Path) {
			t.Errorf("requested %s?%s", r.Path, r.Query)
		}
		if !*res.Success || *res.Date != "2026-10-01" || *res.Days != 1 || *res.Tz != "America/New_York" ||
			len(res.Sports) != 9 || res.Sports[8] != "ufc" || *res.Count != 4 || len(res.Games) != 4 {
			t.Fatalf("decoded %+v", res)
		}
		m := res.Meta
		if *m.Source != "kalshi" || *m.Status != "ok" || *m.CacheTimestamp != "2026-10-01T18:59:40.400Z" ||
			*m.AgeSeconds != 40 || *m.LiveAgeSeconds != 11 || *m.Timestamp != "2026-10-01T19:00:20.114Z" {
			t.Errorf("meta %+v", m)
		}
		if *m.BuildWindow.From != "2026-09-28T00:00:00.000Z" || *m.BuildWindow.To != "2026-10-10T00:00:00.000Z" ||
			*m.Window.Start != "2026-10-01T04:00:00.000Z" || *m.Window.End != "2026-10-02T04:00:00.000Z" {
			t.Errorf("windows %+v %+v", m.BuildWindow, m.Window)
		}

		// A soccer game won on penalties: level scores, a winner, closed markets.
		final := res.Games[0]
		if *final.ID != "71be7f1a-8847-4189-85e0-c692f4f2199e" || *final.Sport != "soccer" || *final.Status != "final" ||
			*final.StatusDetail != "FT, pens 5-4" || final.Competition != nil || final.Round != nil || final.DrawPrice != nil ||
			*final.EventID != "soccer:Ecuador@Japan-20261001" || *final.Volume != 0 {
			t.Errorf("final %+v", final)
		}
		away, home := final.Competitors[0], final.Competitors[1]
		if *away.Side != "away" || *away.Code != "ECU" || *away.Score != 0 || *away.Winner || away.WinPrice != nil ||
			*home.Side != "home" || *home.Name != "Japan" || !*home.Winner || home.Sets != nil {
			t.Errorf("final sides %+v %+v", away, home)
		}
		if *final.StartCheck.Sportsbooks != "missing" || *final.StartCheck.Flashscore != "agree" || final.StartCheck.DiffersByMinutes != nil {
			t.Errorf("startCheck %+v", final.StartCheck)
		}

		// Tennis in play: no side, a score in sets, a competition and a round.
		tennis := res.Games[1]
		first, second := tennis.Competitors[0], tennis.Competitors[1]
		if *tennis.Competition != "ATP Challenger Columbus" || *tennis.Round != "Round Of 16" || *tennis.StatusDetail != "Set 2" ||
			*tennis.LiveUpdatedAt != "2026-10-01T19:00:08.997Z" || first.Side != nil || first.Winner != nil ||
			len(first.Sets) != 2 || first.Sets[0] != 6 || *first.Score != 1 || *first.WinPrice != 0.68 ||
			*second.Short != "C. Smith" || second.Sets[1] != 5 {
			t.Errorf("tennis %+v %+v %+v", tennis, first, second)
		}

		if soccer := res.Games[2]; *soccer.DrawPrice != 0.26 || *soccer.Competitors[0].WinPrice != 0.52 || *soccer.League != "UEFA Nations League" {
			t.Errorf("soccer in play %+v", soccer)
		}

		// Not started: no score, no live data yet, the /odds eventId to join on.
		nfl := res.Games[3]
		if *nfl.Status != "scheduled" || nfl.StatusDetail != nil || nfl.LiveUpdatedAt != nil || nfl.Competitors[0].Score != nil ||
			*nfl.Competitors[0].Name != "Pittsburgh Steelers" || *nfl.Competitors[1].Short != "Browns" ||
			*nfl.EventID != "nfl:Pittsburgh Steelers@Cleveland Browns-20261002" || *nfl.KalshiEventTicker != "KXNFLGAME-26OCT01PITCLE" ||
			*nfl.StartTime != "2026-10-02T00:15:00.000Z" || *nfl.Volume != 1963946 {
			t.Errorf("nfl %+v", nfl)
		}
		// CONTROL: a 0 the API sent is kept as a 0, so the nil scores above mean absent.
		if final.Competitors[0].Score == nil || nfl.Competitors[0].Score != nil {
			t.Errorf("control: scores %v / %v", final.Competitors[0].Score, nfl.Competitors[0].Score)
		}

		// The exported names are the ones the spec generates (and the JS and Python
		// SDKs use): a spec vendoring that would rename one stops compiling here.
		var (
			_ []DayScheduleGame            = res.Games
			_ []DayScheduleCompetitor      = final.Competitors
			_ *DayScheduleMeta             = res.Meta
			_ *DayScheduleBuildWindow      = m.BuildWindow
			_ *DayScheduleWindow           = m.Window
			_ *DayScheduleStartCheck       = final.StartCheck
			_ *DayScheduleStatus           = final.Status
			_ *DayScheduleStartCheckResult = final.StartCheck.Flashscore
		)
	})
	t.Run("a start time that differs, no data and unknown fields", func(t *testing.T) {
		const body = `{"success": true, "date": "2026-10-20", "days": 2, "tz": "Europe/London", "sports": ["ufc"], "count": 1,
			"games": [{"id": "g1", "sport": "ufc", "status": "time_tbd", "competitors": [{"side": null, "name": "A"}, {"side": null, "name": "B"}],
				"startCheck": {"sportsbooks": "differs", "flashscore": "missing", "differsByMinutes": 30, "sportsbooksMinutes": 30, "flashscoreMinutes": null},
				"newGameField": {"x": 1}}],
			"meta": {"source": "kalshi", "status": "no-data", "cacheTimestamp": null, "ageSeconds": null, "liveAgeSeconds": null,
				"buildWindow": null, "window": {"start": "2026-10-19T23:00:00.000Z", "end": "2026-10-21T23:00:00.000Z"}, "newMetaField": true}}`
		api := newFakeAPI(t, ok(body))
		res, err := testClient(t, api.URL).GetDaySchedule(context.Background(),
			DayScheduleParams{Date: "2026-10-20", TZ: "Europe/London", Days: 2, Sports: []string{"ufc"}})
		if err != nil {
			t.Fatal(err)
		}
		if q, _ := url.ParseQuery(api.req(0).Query); q.Get("date") != "2026-10-20" || q.Get("tz") != "Europe/London" ||
			q.Get("days") != "2" || q.Get("sport") != "ufc" || len(q) != 4 {
			t.Errorf("query %s", api.req(0).Query)
		}
		sc := res.Games[0].StartCheck
		if *sc.Sportsbooks != "differs" || *sc.DiffersByMinutes != 30 || *sc.SportsbooksMinutes != 30 || sc.FlashscoreMinutes != nil {
			t.Errorf("startCheck %+v", sc)
		}
		if *res.Games[0].Status != "time_tbd" || len(res.Games[0].Competitors) != 2 || res.Games[0].Competitors[1].Side != nil {
			t.Errorf("game %+v", res.Games[0])
		}
		m := res.Meta
		if *m.Status != "no-data" || m.CacheTimestamp != nil || m.AgeSeconds != nil || m.LiveAgeSeconds != nil ||
			m.BuildWindow != nil || *m.Window.Start != "2026-10-19T23:00:00.000Z" {
			t.Errorf("meta %+v", m)
		}
	})
	t.Run("a bad query is an APIError", func(t *testing.T) {
		api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) {
			return 400, nil, `{"success":false,"error":"Unknown sport \"cricket\". Valid: nfl, ncaaf, mlb, nba, wnba, nhl, tennis, soccer, ufc."}`
		})
		_, err := testClient(t, api.URL).GetDaySchedule(context.Background(), DayScheduleParams{Sports: []string{"cricket"}})
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != 400 || !strings.Contains(ae.Message, "Unknown sport") {
			t.Fatalf("err %v (%+v)", err, ae)
		}
		if api.req(0).Query != "sport=cricket" {
			t.Errorf("query %s", api.req(0).Query)
		}
	})
}

// TestEV decodes a Fair v1 answer of GET /api/v1/{sport}/ev (testdata/fixtures/ev-fair-v1.json,
// shared with the other SDKs: a consensus-fair game with a Polymarket entry, and a
// Pinnacle-anchored game with Novig, Kalshi, DraftKings and FanDuel entries).
func TestEV(t *testing.T) {
	t.Run("Fair v1 fields", func(t *testing.T) {
		api := newFakeAPI(t, ok(fixture(t, "ev-fair-v1.json")))
		res, err := testClient(t, api.URL).GetEV(context.Background(), "nfl", &EVParams{Venues: true})
		if err != nil {
			t.Fatal(err)
		}
		if r := api.req(0); r.Path != "/api/v1/nfl/ev" || r.Query != "venues=true" {
			t.Errorf("requested %s?%s", r.Path, r.Query)
		}
		m := res.Meta
		if !*res.Success || *m.FairMethod != "fair_v1" || *m.Sport != "nfl" || *m.Events != 2 || *m.Opportunities != 5 ||
			*m.Freshness.AgeSeconds != 4 || *m.Freshness.Stale || m.Book != nil || *m.Timestamp != "2026-10-02T00:39:01.480Z" {
			t.Errorf("meta %+v", m)
		}
		if b := m.Board; *b.BuiltAt != "2026-10-02T00:39:03.912Z" || *b.AgeMs != 1567 || *b.Stale || !*b.Venues || !*b.VenueQuotes {
			t.Errorf("board %+v", b)
		}

		// No Pinnacle line: a consensus fair, and only a prediction market scored against it.
		consensus := res.Data[0]
		if f := consensus.Fair; *f.Method != "consensus" || f.Anchor != nil || *f.Sources != 3 || f.Reason != nil ||
			*f.Home != 0.6012 || *f.Away != 0.3895 || f.Draw != nil {
			t.Errorf("consensus fair %+v", f)
		}
		if *consensus.CanonicalEventID != "nfl:Chicago Bears@Green Bay Packers-20261011" || *consensus.EventID != "1637790121" ||
			*consensus.FairProbability.Home != 0.5912 || consensus.FairProbability.Draw != nil {
			t.Errorf("consensus event %+v", consensus)
		}
		poly := consensus.Opportunities[0]
		if *poly.Book != "polymarket" || *poly.Kind != "venue" || *poly.Route != "yes" || poly.Size != nil || *poly.Tradeable ||
			*poly.Reason != "size unknown" || *poly.Fee != 0.007425 || *poly.QuoteAgeMs != 48210 {
			t.Errorf("polymarket %+v", poly)
		}

		// Pinnacle-anchored: Novig (always listed), Kalshi (venues), two sportsbooks, best EV first.
		anchored := res.Data[1]
		if f := anchored.Fair; *f.Method != "pinnacle" || *f.Anchor != "realtime" || *f.Sources != 5 || *f.Home != 0.5714 {
			t.Errorf("pinnacle fair %+v", f)
		}
		var books []string
		for _, o := range anchored.Opportunities {
			books = append(books, *o.Book)
		}
		if strings.Join(books, ",") != "novig,kalshi,draftkings,fanduel" {
			t.Errorf("opportunities %v", books)
		}
		kalshi := anchored.Opportunities[1]
		if *kalshi.Route != "no:BUF" || *kalshi.Size != 94.905 || !*kalshi.Tradeable || kalshi.Reason != nil ||
			*kalshi.FairUnadjusted != 0.5714 || *kalshi.FairProbability != 0.5614 || *kalshi.EvPct != 4.45 ||
			*kalshi.Roi != 0.04454185520361991 || *kalshi.Link != "https://kalshi.com/events/KXNFLGAME-26OCT12BUFLAR" {
			t.Errorf("kalshi %+v", kalshi)
		}
		dk, fd := anchored.Opportunities[2], anchored.Opportunities[3]
		if *dk.Kind != "book" || *dk.Fee != 0 || dk.Route != nil || dk.Size != nil || dk.Link != nil || !*dk.Tradeable || *dk.EvPct != 2.15 {
			t.Errorf("draftkings %+v", dk)
		}
		if *fd.Tradeable || *fd.Reason != "EV under 2%" {
			t.Errorf("fanduel %+v", fd)
		}
		// A sportsbook's own feed never counts toward the fair it is scored against.
		if *anchored.BooksInConsensus != 5 || *dk.BooksInConsensus != 4 || *kalshi.BooksInConsensus != 5 {
			t.Errorf("booksInConsensus event %v draftkings %v kalshi %v", *anchored.BooksInConsensus, *dk.BooksInConsensus, *kalshi.BooksInConsensus)
		}
		// CONTROL: a 0 the API sent is kept as a 0 (Fee), so the nil pointers above mean null.
		if dk.Fee == nil || dk.Size != nil {
			t.Errorf("control: fee %v size %v", dk.Fee, dk.Size)
		}

		// The exported names are the ones the spec generates (and the JS and Python
		// SDKs use): a spec vendoring that would rename one stops compiling here.
		var (
			_ *EVBoard        = m.Board
			_ *EVFair         = anchored.Fair
			_ []EVOpportunity = anchored.Opportunities
			_ []EVEvent       = res.Data
		)
	})
	t.Run("an answer without the Fair v1 fields", func(t *testing.T) {
		const body = `{"success": true, "data": [{"eventId": "e1", "sport": "nfl", "market": "h2h",
			"fairProbability": {"home": 0.55, "away": 0.45, "draw": null}, "booksInConsensus": 4,
			"opportunities": [{"book": "fanduel", "side": "home", "bookPrice": 120, "evPct": 3.1}]}],
			"meta": {"fair_method": "consensus_devig_median", "freshness": {"ageSeconds": 12, "stale": false}}}`
		api := newFakeAPI(t, ok(body))
		res, err := testClient(t, api.URL).GetEV(context.Background(), "nfl", &EVParams{Book: "fanduel"})
		if err != nil {
			t.Fatal(err)
		}
		if api.req(0).Query != "book=fanduel" {
			t.Errorf("query %s", api.req(0).Query)
		}
		e, o := res.Data[0], res.Data[0].Opportunities[0]
		if res.Meta.Board != nil || e.Fair != nil || e.CanonicalEventID != nil || o.Kind != nil || o.Tradeable != nil ||
			o.FairUnadjusted != nil || *o.EvPct != 3.1 || *res.Meta.FairMethod != "consensus_devig_median" {
			t.Errorf("decoded %+v / %+v / %+v", res.Meta, e, o)
		}
	})
}

func TestSplitsBetMGMTicketsOnly(t *testing.T) {
	res, err := testClient(t, newFakeAPI(t, ok(fixture(t, "splits-board.json"))).URL).GetSplits(context.Background(), "nfl")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != 1 || len(res.Data[0].Splits) != 2 || len(res.Meta.Books) != 3 || res.Meta.Books[2] != "betmgm" || *res.Meta.Status != "ok" {
		t.Fatalf("decoded %+v / %+v", res.Data, res.Meta)
	}
	dk, mgm := res.Data[0].Splits[0], res.Data[0].Splits[1]
	if *mgm.Book != "betmgm" || *mgm.Title != "BetMGM" || *mgm.AsOf != "2026-09-30T17:58:00.000Z" {
		t.Errorf("betmgm entry %+v", mgm)
	}
	if mgm.Spread.AwayHandlePct != nil || mgm.Spread.HomeHandlePct != nil || mgm.Total.OverHandlePct != nil ||
		mgm.Total.UnderHandlePct != nil || mgm.Moneyline.AwayHandlePct != nil || mgm.Moneyline.HomeHandlePct != nil {
		t.Errorf("betmgm handle fields were invented: %+v %+v %+v", mgm.Spread, mgm.Total, mgm.Moneyline)
	}
	if *mgm.Spread.AwayBetsPct != 44 || *mgm.Total.OverBetsPct != 61 || *mgm.Total.Line != 47.5 || *mgm.Moneyline.HomeBetsPct != 70 {
		t.Errorf("betmgm ticket fields %+v %+v %+v", mgm.Spread, mgm.Total, mgm.Moneyline)
	}
	// CONTROL: the DraftKings entry next to it does carry handle percentages.
	if dk.Spread.AwayHandlePct == nil || *dk.Spread.AwayHandlePct != 41 || dk.Total.OverHandlePct == nil || dk.Moneyline.HomeHandlePct == nil {
		t.Errorf("control: dk handle fields %+v %+v %+v", dk.Spread, dk.Total, dk.Moneyline)
	}
}

func TestV2EightEightEightSport(t *testing.T) {
	// 888sport keys data by its own event id; each value is the event as 888sport sent it.
	const event = `{"id": 1025011234, "name": "Dallas Cowboys @ Green Bay Packers", "start_time": "2026-10-04T20:25:00Z",
		"markets": [{"name": "Money Line", "selections": [{"name": "Dallas Cowboys", "decimal_price": 2.5, "fraction_price": "6/4"}]}]}`
	board := `{"success": true, "sport": "nfl", "league": "nfl", "marketCount": 1, "etag": "\"e1\"",
		"data": {"1025011234": ` + event + `},
		"meta": {"source": "888sport-v2", "cached": true, "status": "stale", "ageSeconds": 151, "cacheTimestamp": "2026-09-30T18:00:00.000Z", "timestamp": "2026-09-30T18:02:31.000Z"}}`
	api := newFakeAPI(t, ok(board))
	res, err := testClient(t, api.URL).GetV2(context.Background(), "888sport", "nfl", "")
	if err != nil {
		t.Fatal(err)
	}
	if api.req(0).Path != "/api/v2/888sport/nfl" || api.req(0).Query != "" {
		t.Errorf("requested %s?%s", api.req(0).Path, api.req(0).Query)
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(*res.Data, &events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || string(events["1025011234"]) != event {
		t.Errorf("data = %s, want the event verbatim under its id", *res.Data)
	}
	if *res.Meta.Source != "888sport-v2" || *res.Meta.Status != "stale" || *res.Meta.AgeSeconds != 151 || *res.Etag != `"e1"` {
		t.Errorf("envelope %+v / %+v", res, res.Meta)
	}

	leagues := `{"success": true, "sport": "soccer", "leagueCount": 2,
		"leagues": [{"leagueKey": "england-premier-league", "leagueName": "Premier League", "marketCount": 10, "updatedAt": 1759255351000},
			{"leagueKey": "spain-la-liga", "leagueName": "La Liga", "marketCount": 9, "updatedAt": 1759255351000}],
		"meta": {"source": "888sport-v2"}}`
	api = newFakeAPI(t, ok(leagues))
	idx, err := testClient(t, api.URL).GetV2Leagues(context.Background(), "888sport", "soccer")
	if err != nil {
		t.Fatal(err)
	}
	if api.req(0).Path != "/api/v2/888sport/soccer/leagues" || *idx.LeagueCount != 2 || len(idx.Leagues) != 2 ||
		*idx.Leagues[0].LeagueKey != "england-premier-league" || *idx.Leagues[1].MarketCount != 9 {
		t.Errorf("leagues %+v from %s", idx, api.req(0).Path)
	}
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
		// The sport's feed state; pinnacle-realtime-sync carries the same object.
		if f := p.Feed; f == nil || *f.UpstreamAgeSeconds != 41.5 || *f.Resubscribing || !*f.Stalled {
			t.Errorf("feed %+v", p.Feed)
		}
		rs, err := Decode[PinnacleRealtimeSyncPayload](json.RawMessage(`{"sport": "nfl",
			"feed": {"upstreamAgeSeconds": null, "resubscribing": null, "stalled": false}}`))
		if err != nil || rs.Feed == nil || rs.Feed.UpstreamAgeSeconds != nil || rs.Feed.Resubscribing != nil || *rs.Feed.Stalled {
			t.Errorf("sync feed %+v (%v)", rs, err)
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
		for _, key := range []string{"removed", "stream", "timestamp", "resync", "delta", "feed", "mlb"} {
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
		"propbuilder": "propbuilder-v2-update",
	} {
		if got := V2EventName(book); got != want {
			t.Errorf("V2EventName(%q) = %q, want %q", book, got, want)
		}
	}
	// The event layer knows the events added since the manifest check last ran,
	// without a manifest; CONTROL: an unknown name is not known.
	known := knownEvents()
	for _, name := range []string{"propbuilder-v2-update", EventScoresUpdate, "plays-update", "fanaticsmarkets-books-v2-update"} {
		if !known[name] {
			t.Errorf("knownEvents lacks %q", name)
		}
	}
	if known["propbuilder-v3-update"] {
		t.Error("control: knownEvents accepts any name")
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

// TestFairAndEVLines decodes the answers of GET /api/v1/{sport}/fair and of
// GET /api/v1/{sport}/ev?market=spreads (the bodies of the TypeScript SDK's tests).
func TestFairAndEVLines(t *testing.T) {
	t.Run("EV lines", func(t *testing.T) {
		const body = `{"success": true,
			"data": [{"eventId": "g1", "canonicalEventId": "nfl:Denver Broncos@Arizona Cardinals-20261025", "sport": "nfl",
				"homeTeam": "Arizona Cardinals", "awayTeam": "Denver Broncos", "commenceTime": "2026-10-25T20:05:00.000Z",
				"market": "spreads", "point": 4, "pushPossible": true, "fairProbability": {"home": 0.4836, "away": 0.4862},
				"booksInConsensus": 2, "fair": {"method": "pinnacle", "anchor": "odds", "sources": 2, "home": 0.4936, "away": 0.4962},
				"opportunities": [{"book": "fanduel", "market": "spreads", "side": "home", "team": "Arizona Cardinals", "point": 4,
					"bookPrice": 120, "bookImplied": 0.4545, "fairProbability": 0.4836, "fairPrice": 107, "evPct": 6.39, "edgePp": 2.91,
					"kellyFraction": 0.0533, "booksInConsensus": 2, "kind": "book", "fairUnadjusted": 0.4936, "roi": 0.0639,
					"tradeable": true, "reason": null, "quoteAgeMs": 4100}]}],
			"meta": {"sport": "nfl", "market": "spreads", "odds_format": "american", "min_ev": 0, "max_ev": 25, "fair_method": "fair_v1",
				"line": "pinnacle_main", "pregame_only": true, "events": 1, "opportunities": 1, "timestamp": "2026-10-19T17:00:00.000Z",
				"freshness": {"ageSeconds": 3, "stale": false}, "board": {"builtAt": "2026-10-19T17:00:01.000Z", "ageMs": 1200, "stale": false}}}`
		api := newFakeAPI(t, ok(body))
		res, err := testClient(t, api.URL).GetEVLines(context.Background(), "nfl", &EVLinesParams{Markets: []string{"spreads"}})
		if err != nil {
			t.Fatal(err)
		}
		if r := api.req(0); r.Path != "/api/v1/nfl/ev" || r.Query != "market=spreads" {
			t.Errorf("requested %s?%s", r.Path, r.Query)
		}
		ev := res.Data[0]
		if *ev.Market != "spreads" || *ev.Point != 4 || !*ev.PushPossible || *ev.FairProbability.Home != 0.4836 ||
			ev.FairProbability.Over != nil || *ev.Fair.Anchor != "odds" || *ev.BooksInConsensus != 2 {
			t.Errorf("event %+v", ev)
		}
		o := ev.Opportunities[0]
		if *o.Book != "fanduel" || *o.Side != "home" || *o.Point != 4 || *o.BookPrice != 120 || *o.EvPct != 6.39 ||
			!*o.Tradeable || o.Reason != nil || *o.QuoteAgeMs != 4100 {
			t.Errorf("opportunity %+v", o)
		}
		if m := res.Meta; *m.Line != "pinnacle_main" || *m.Market != "spreads" || *m.MaxEv != 25 || *m.Board.AgeMs != 1200 || m.Unsupported != nil {
			t.Errorf("meta %+v", m)
		}
	})
	t.Run("anything the route would answer with the moneyline is refused without a request", func(t *testing.T) {
		api := newFakeAPI(t, ok(`{}`))
		c := testClient(t, api.URL)
		for _, markets := range [][]string{{"h2h"}, {"H2H"}, {"spreads", "h2h"}, {"spread"}, {"moneyline"}, {"totals", "moneyline"},
			{""}, {"", " "}, {" , "}, {"spreads,h2h"}} {
			_, err := c.GetEVLines(context.Background(), "nfl", &EVLinesParams{Markets: markets})
			if err == nil || !strings.Contains(err.Error(), "GetEVLines") {
				t.Errorf("GetEVLines(%q) = %v, want an error", markets, err)
			}
		}
		if api.count() != 0 {
			t.Fatalf("%d requests for refused calls", api.count())
		}
		// CONTROL: spreads and totals go through.
		if _, err := c.GetEVLines(context.Background(), "nfl", &EVLinesParams{Markets: []string{"totals", "spreads"}}); err != nil || api.count() != 1 ||
			api.req(0).Query != "market=totals%2Cspreads" {
			t.Fatalf("GetEVLines(totals, spreads) = %v after %d requests", err, api.count())
		}
	})
	t.Run("a moneyline answer is an error", func(t *testing.T) {
		api := newFakeAPI(t, ok(`{"success": true, "data": [], "meta": {"sport": "nfl", "market": "h2h"}}`))
		c := testClient(t, api.URL)
		if _, err := c.GetEVLines(context.Background(), "nfl", nil); err == nil || !strings.Contains(err.Error(), "moneyline board") {
			t.Fatalf("GetEVLines on an h2h answer = %v, want an error", err)
		}
		// CONTROL: the same body through GetEV is the moneyline board it is.
		if _, err := c.GetEV(context.Background(), "nfl", nil); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("fair line", func(t *testing.T) {
		const body = `{"success": true,
			"data": [{"eventId": "g1", "canonicalEventId": null, "sport": "nfl", "league": null, "homeTeam": "Arizona Cardinals",
				"awayTeam": "Denver Broncos", "commenceTime": "2026-10-25T20:05:00.000Z",
				"moneyline": {"method": "pinnacle", "sources": 3, "home": {"probability": 0.3333, "american": 200, "booksDisagree": false},
					"away": {"probability": 0.65, "american": -186, "booksDisagree": false}, "draw": null},
				"spread": {"point": 4, "pushPossible": true, "sources": 2,
					"home": {"point": 4, "probability": 0.4936, "american": 103, "booksDisagree": false},
					"away": {"point": -4, "probability": 0.4962, "american": 102, "booksDisagree": false}},
				"total": null}],
			"meta": {"sport": "nfl", "fair_method": "fair_v1", "anchor": "pinnacle", "odds_format": "american", "pregame_only": true,
				"markets": {"moneyline": true, "spreads": true, "totals": true}, "events": 1, "notes": [],
				"boards": {"moneyline": {"builtAt": "2026-10-19T17:00:01.000Z", "ageMs": 900, "stale": false},
					"lines": {"builtAt": "2026-10-19T17:00:01.000Z", "ageMs": 900, "stale": false}}}}`
		api := newFakeAPI(t, ok(body))
		res, err := testClient(t, api.URL).GetFair(context.Background(), "nfl")
		if err != nil {
			t.Fatal(err)
		}
		if r := api.req(0); r.Path != "/api/v1/nfl/fair" || r.Query != "" {
			t.Errorf("requested %s?%s", r.Path, r.Query)
		}
		g := res.Data[0]
		if g.CanonicalEventID != nil || g.League != nil || g.Total != nil || g.Moneyline.Draw != nil {
			t.Errorf("nulls %+v", g)
		}
		if ml := g.Moneyline; *ml.Method != "pinnacle" || *ml.Home.American != 200 || *ml.Away.Probability != 0.65 || *ml.Away.BooksDisagree {
			t.Errorf("moneyline %+v", ml)
		}
		if sp := g.Spread; *sp.Point != 4 || !*sp.PushPossible || *sp.Away.Point != -4 || *sp.Away.American != 102 {
			t.Errorf("spread %+v", sp)
		}
		if m := res.Meta; *m.Anchor != "pinnacle" || !*m.Markets.Totals || *m.Boards.Lines.AgeMs != 900 || m.Notes == nil || len(m.Notes) != 0 {
			t.Errorf("meta %+v", m)
		}
	})
}

func TestPropsEV(t *testing.T) {
	const body = `{"success": true,
		"data": [{"gameId": "nba:Boston Celtics@New York Knicks-20261025", "sport": "nba", "homeTeam": "New York Knicks",
			"awayTeam": "Boston Celtics", "commenceTime": "2026-10-25T23:30:00.000Z", "player": "Jayson Tatum", "category": "points",
			"line": 27.5, "fair": {"method": "pinnacle", "sources": 3, "over": {"probability": 0.5123, "american": -105, "booksDisagree": false},
				"under": {"probability": 0.4791, "american": 109, "booksDisagree": true}},
			"opportunities": [{"book": "draftkings", "side": "over", "bookPrice": 110, "bookImplied": 0.4762, "fairProbability": 0.5023,
				"fairPrice": -101, "evPct": 5.48, "edgePp": 2.61, "kellyFraction": 0.0498, "booksInConsensus": 3, "kind": "book",
				"fairUnadjusted": 0.5123, "roi": 0.0548, "tradeable": true, "reason": null, "quoteAgeMs": 21000}]}],
		"meta": {"sport": "nba", "fair_method": "fair_v1", "anchor": "pinnacle", "odds_format": "american", "pregame_only": true,
			"all": false, "min_ev": 0, "max_ev": 25, "rows": 1, "truncated": false, "opportunities": 1,
			"coverage": {"games": 9, "pinnacleProps": 412, "scoredPrices": 3120, "positive": 37}, "timestamp": "2026-10-25T18:00:00.000Z"}}`
	api := newFakeAPI(t, ok(body))
	res, err := testClient(t, api.URL).GetPropsEV(context.Background(), "nba", &PropsEVParams{Player: "Tatum"})
	if err != nil {
		t.Fatal(err)
	}
	if r := api.req(0); r.Path != "/api/v1/nba/props/ev" || r.Query != "player=Tatum" {
		t.Errorf("requested %s?%s", r.Path, r.Query)
	}
	row := res.Data[0]
	if *row.Player != "Jayson Tatum" || *row.Category != "points" || *row.Line != 27.5 || *row.Fair.Over.American != -105 ||
		!*row.Fair.Under.BooksDisagree || *row.Fair.Sources != 3 {
		t.Errorf("row %+v", row)
	}
	if o := row.Opportunities[0]; *o.Book != "draftkings" || *o.Side != "over" || *o.EvPct != 5.48 || o.Reason != nil || !*o.Tradeable {
		t.Errorf("opportunity %+v", o)
	}
	if m := res.Meta; *m.All || *m.Rows != 1 || *m.Truncated || *m.Coverage.PinnacleProps != 412 || *m.Coverage.Positive != 37 {
		t.Errorf("meta %+v", m)
	}
}

// TestPlays decodes both answers of GET /api/v1/{sport}/plays, the sport's own play
// detail, and a plays-update payload.
func TestPlays(t *testing.T) {
	const game = `{"success": true, "sport": "nfl", "eventId": "nfl:Cincinnati Bengals@Baltimore Ravens-20261025",
		"status": "ok", "state": "in", "home": "Baltimore Ravens", "away": "Cincinnati Bengals", "count": 112, "lastSeq": 4410000123,
		"plays": [{"id": "4017728371234", "seq": 4410000123, "period": 2, "clock": "5:32", "secondsRemaining": 332, "team": "away",
			"type": "Pass Reception", "text": "J.Burrow pass short right to J.Chase for 14 yards", "scoringPlay": false,
			"homeScore": 10, "awayScore": 7, "wallclock": "2026-10-25T18:41:07Z", "updatedAt": "2026-10-25T18:41:15.120Z",
			"detail": {"drive": "8", "down": 2, "distance": 6, "yardLine": 35, "yardsToEndzone": 65, "downDistanceText": "2nd & 6 at CIN 35",
				"possessionText": "CIN 35", "endDown": 1, "endDistance": 10, "endYardLine": 49, "endYardsToEndzone": 51,
				"endDownDistanceText": "1st & 10 at CIN 49", "yards": 14, "scoreValue": 0, "isPenalty": false, "isTurnover": false}},
			{"id": "p2", "seq": 4410000124, "period": null, "clock": null, "secondsRemaining": null, "team": null, "type": "Timeout",
			"text": "Timeout", "scoringPlay": false, "homeScore": null, "awayScore": null, "wallclock": null, "detail": {}}],
		"meta": {"source": "play-by-play", "asOf": "2026-10-25T18:41:16.000Z", "ageSeconds": 2, "sourceAgeSeconds": null,
			"since": 4410000100, "amendedWindowSeconds": 120, "returned": 2, "truncated": false, "timestamp": "2026-10-25T18:41:18.000Z"}}`
	const games = `{"success": true, "sport": "nfl", "count": 2,
		"games": [{"eventId": "nfl:Cincinnati Bengals@Baltimore Ravens-20261025", "home": "Baltimore Ravens", "away": "Cincinnati Bengals",
			"state": "in", "playCount": 112, "asOf": "2026-10-25T18:41:16.000Z"},
			{"eventId": "e2", "home": "A", "away": "B", "state": "pre", "playCount": 0, "asOf": null}],
		"meta": {"timestamp": "2026-10-25T18:41:18.000Z"}}`
	api := newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) {
		if r.URL.Query().Get("eventId") == "" {
			return 200, nil, games
		}
		return 200, nil, game
	})
	c := testClient(t, api.URL)
	ctx := context.Background()

	res, err := c.GetPlays(ctx, "nfl", "nfl:Cincinnati Bengals@Baltimore Ravens-20261025", &PlaysParams{Since: Ptr(4410000100.0)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Games != nil || res.Game == nil {
		t.Fatalf("GetPlays(event) = %+v", res)
	}
	g := res.Game
	if *g.State != "in" || *g.Status != "ok" || *g.Count != 112 || *g.LastSeq != 4410000123 || len(g.Plays) != 2 ||
		*g.Meta.AmendedWindowSeconds != 120 || g.Meta.SourceAgeSeconds != nil || *g.Meta.Returned != 2 {
		t.Errorf("game %+v / %+v", g, g.Meta)
	}
	// The poll parameter is the response's own LastSeq, unconverted.
	if q, _ := url.ParseQuery(api.req(0).Query); q.Get("since") != "4410000100" {
		t.Errorf("since = %q", q.Get("since"))
	}
	if next := (&PlaysParams{Since: g.LastSeq}); next.Since == nil || strconv.FormatFloat(*next.Since, 'f', -1, 64) != "4410000123" {
		t.Errorf("LastSeq does not feed Since: %v", next.Since)
	}
	p := g.Plays[0]
	if *p.ID != "4017728371234" || *p.Clock != "5:32" || *p.Team != "away" || *p.Type != "Pass Reception" || *p.HomeScore != 10 || p.Detail == nil {
		t.Errorf("play %+v", p)
	}
	d, err := Decode[FootballPlayDetail](*p.Detail)
	if err != nil || *d.Down != 2 || *d.Yards != 14 || *d.EndDownDistanceText != "1st & 10 at CIN 49" || *d.IsTurnover {
		t.Errorf("detail %+v (%v)", d, err)
	}
	neutral := g.Plays[1]
	if neutral.Period != nil || neutral.Clock != nil || neutral.Team != nil || neutral.Wallclock != nil || neutral.UpdatedAt != nil {
		t.Errorf("neutral play %+v", neutral)
	}

	// Since and Limit without an event id are refused, not dropped in favour of the list.
	before := api.count()
	for _, p := range []*PlaysParams{{Since: Ptr(0.0)}, {Limit: 5}, {Since: Ptr(9.0), Limit: 1}} {
		for _, id := range []string{"", "   "} {
			if _, err := c.GetPlays(ctx, "nfl", id, p); err == nil || !strings.Contains(err.Error(), "event id") {
				t.Errorf("GetPlays(%q, %+v) = %v, want an error", id, p, err)
			}
		}
	}
	if api.count() != before {
		t.Fatalf("%d requests for refused calls", api.count()-before)
	}

	// CONTROL: nil or zero params list the games.
	if _, err := c.GetPlays(ctx, "nfl", "", &PlaysParams{}); err != nil {
		t.Fatal(err)
	}
	list, err := c.GetPlays(ctx, "nfl", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.Game != nil || list.Games == nil || *list.Games.Count != 2 || len(list.Games.Games) != 2 {
		t.Fatalf("GetPlays(list) = %+v", list)
	}
	if first, second := list.Games.Games[0], list.Games.Games[1]; *first.PlayCount != 112 || *first.State != "in" ||
		*second.State != "pre" || second.AsOf != nil || *list.Games.Meta.Timestamp != "2026-10-25T18:41:18.000Z" {
		t.Errorf("games %+v", list.Games.Games)
	}
	// CONTROL: the game list decoded as one game's answer has no plays and no event id,
	// which is why GetPlays picks the type by the event id it was given.
	var wrong PlaysResponse
	if err := decodeBody([]byte(games), &wrong); err != nil || wrong.EventID != nil || wrong.Plays != nil {
		t.Fatalf("control: %+v (%v)", wrong, err)
	}

	update, err := Decode[PlaysUpdate](json.RawMessage(`{"type": "delta", "timestamp": "2026-10-25T18:41:19.000Z", "sport": "nhl",
		"events": [{"eventId": "nhl:Boston Bruins@Toronto Maple Leafs-20261025", "home": "Toronto Maple Leafs", "away": "Boston Bruins",
			"state": "in", "count": 201, "plays": [{"id": "251", "seq": 251, "period": 1, "clock": "12:04", "secondsRemaining": 724,
				"team": "home", "type": "goal", "text": "Goal", "scoringPlay": true, "homeScore": 1, "awayScore": 0, "wallclock": null,
				"detail": {"situationCode": "1551", "zone": "O", "shotType": "wrist",
					"players": [{"role": "scorer", "id": 8479318, "name": "Auston Matthews", "sweater": 34}]}}]}]}`))
	if err != nil || *update.Type != "delta" || *update.Sport != "nhl" || len(update.Events) != 1 || *update.Events[0].Count != 201 {
		t.Fatalf("plays-update %+v (%v)", update, err)
	}
	// The subscribed acknowledgement echoes the plays opt-in as the server kept it.
	ack, err := Decode[ClientSubscription](json.RawMessage(`{"sports": ["nfl"], "plays": ["nfl", "nba"], "scores": true}`))
	if err != nil || ack.Plays == nil || string(*ack.Plays) != `["nfl", "nba"]` {
		t.Errorf("subscribed ack plays %v (%v)", ack.Plays, err)
	}
	goal := update.Events[0].Plays[0]
	hd, err := Decode[HockeyPlayDetail](*goal.Detail)
	if err != nil || !*goal.ScoringPlay || *hd.ShotType != "wrist" || len(hd.Players) != 1 || *hd.Players[0].Name != "Auston Matthews" {
		t.Errorf("goal %+v / %+v (%v)", goal, hd, err)
	}
}

// fillParams sets every field of the struct p points to, so a field a method does
// not send shows up as a missing query key.
func fillParams(t *testing.T, p any) {
	t.Helper()
	v := reflect.ValueOf(p).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x" + strconv.Itoa(i))
		case reflect.Int, reflect.Int64:
			f.SetInt(int64(i + 1))
		case reflect.Float64:
			f.SetFloat(float64(i) + 1.5)
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Slice:
			f.Set(reflect.ValueOf([]string{"a", "b"}))
		case reflect.Pointer:
			if f.Type().Elem().Kind() != reflect.Float64 {
				t.Fatalf("fillParams: field %s of type %s", v.Type().Field(i).Name, f.Type())
			}
			f.Set(reflect.ValueOf(Ptr(float64(i + 7))))
		default:
			t.Fatalf("fillParams: field %s of kind %s", v.Type().Field(i).Name, f.Kind())
		}
	}
}

// Every field of a params struct reaches the wire: a field added to the struct but
// not to its query builder would type-check and never be sent.
func TestParamsReachTheWire(t *testing.T) {
	api := newFakeAPI(t, ok(`{}`))
	c := testClient(t, api.URL)
	ctx := context.Background()
	var (
		odds    OddsParams
		props   PropsParams
		lines   EVLinesParams
		propsEV PropsEVParams
		plays   PlaysParams
	)
	for _, p := range []any{&odds, &props, &lines, &propsEV, &plays} {
		fillParams(t, p)
	}
	lines.Markets = []string{"spreads", "totals"}
	numFields := func(p any) int { return reflect.TypeOf(p).Elem().NumField() }
	for _, tc := range []struct {
		name string
		call func() error
		want int
	}{
		{"GetOdds", func() error { _, err := c.GetOdds(ctx, "nfl", &odds); return err }, numFields(&odds)},
		{"GetSpreads", func() error { _, err := c.GetSpreads(ctx, "nfl", &odds); return err }, numFields(&odds)},
		{"GetProps", func() error { _, err := c.GetProps(ctx, "nba", &props); return err }, numFields(&props)},
		// CONTROL: GetBookProps leaves Books out on purpose, and the count shows it.
		{"GetBookProps", func() error { _, err := c.GetBookProps(ctx, "nba", "fanduel", &props); return err }, numFields(&props) - 1},
		{"GetEVLines", func() error { _, err := c.GetEVLines(ctx, "nfl", &lines); return err }, numFields(&lines)},
		{"GetPropsEV", func() error { _, err := c.GetPropsEV(ctx, "nba", &propsEV); return err }, numFields(&propsEV)},
		// eventId plus every field.
		{"GetPlays", func() error { _, err := c.GetPlays(ctx, "nfl", "e1", &plays); return err }, numFields(&plays) + 1},
	} {
		n := api.count()
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		q, err := url.ParseQuery(api.req(n).Query)
		if err != nil {
			t.Fatal(err)
		}
		if len(q) != tc.want {
			t.Errorf("%s sent %d query keys (%s), want %d", tc.name, len(q), api.req(n).Query, tc.want)
		}
	}
	if odds.State == "" || props.State == "" {
		t.Fatal("control: fillParams left State empty")
	}
}

// TestV2MarketFilterMeta decodes the meta a v2 book adds for the marketIds filter: the
// default-excluded entries on a default request, the echo and count on a filtered one.
func TestV2MarketFilterMeta(t *testing.T) {
	const plain = `{"success": true, "sport": "nfl", "league": "nfl", "marketCount": 1, "etag": "\"e1\"",
		"data": {"main": {"x": 1}},
		"meta": {"source": "draftkings-v2", "status": "ok", "ageSeconds": 4, "timestamp": "2026-10-25T18:00:00.000Z",
			"defaultExcluded": {"marketIds": "props-*", "count": 41, "include": "?marketIds=props-*"},
			"marketAges": {"main": 4, "live": 1.5}}}`
	const filtered = `{"success": true, "sport": "nfl", "league": "nfl", "marketCount": 2, "data": {"props-1": {}, "props-2": {}},
		"meta": {"source": "draftkings-v2", "status": "ok", "marketIds": "props-*", "marketsFilteredOut": 3,
			"marketAges": {"props-1": 120, "props-2": 95}}}`
	api := newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) {
		if r.URL.Query().Get("marketIds") != "" {
			return 200, nil, filtered
		}
		return 200, nil, plain
	})
	c := testClient(t, api.URL)
	ctx := context.Background()

	res, err := c.GetV2(ctx, "draftkings", "nfl", "")
	if err != nil {
		t.Fatal(err)
	}
	m := res.Meta
	if m.DefaultExcluded == nil || *m.DefaultExcluded.MarketIds != "props-*" || *m.DefaultExcluded.Count != 41 ||
		*m.DefaultExcluded.Include != "?marketIds=props-*" || m.MarketIds != nil || m.MarketsFilteredOut != nil ||
		m.MarketAges["main"] != 4 || m.MarketAges["live"] != 1.5 {
		t.Errorf("default meta %+v", m)
	}

	res, err = c.GetV2Markets(ctx, "draftkings", "nfl", "", "props-*")
	if err != nil {
		t.Fatal(err)
	}
	m = res.Meta
	if m.DefaultExcluded != nil || *m.MarketIds != "props-*" || *m.MarketsFilteredOut != 3 || len(m.MarketAges) != 2 ||
		m.MarketAges["props-1"] != 120 {
		t.Errorf("filtered meta %+v", m)
	}
	// CONTROL: the filter fields are read from the body, not invented: a body without
	// them leaves them unset.
	var bare V2BookResponse
	if err := decodeBody([]byte(`{"success": true, "meta": {"source": "x"}}`), &bare); err != nil ||
		bare.Meta.DefaultExcluded != nil || bare.Meta.MarketIds != nil || bare.Meta.MarketAges != nil {
		t.Fatalf("control: %+v (%v)", bare.Meta, err)
	}
}

// TestBoardMetaFields decodes meta fields the API already sends: the exchange echo and
// league list of an odds board, the retired props books, and the odds line history's
// data quality when every snapshot was withheld.
func TestBoardMetaFields(t *testing.T) {
	ctx := context.Background()
	api := newFakeAPI(t, ok(`{"success": true, "data": {}, "meta": {"sport": "nfl", "excludeExchanges": true,
		"exchangeBooks": ["kalshi", "polymarket", "novig"], "available_leagues": ["nfl"],
		"fanduelMarketIds": {"state": "co", "available": false, "mapped": 0, "unmapped": 0, "timestamp": null}}}`))
	odds, err := testClient(t, api.URL).GetOdds(ctx, "nfl", &OddsParams{ExcludeExchanges: true, State: "co"})
	if err != nil {
		t.Fatal(err)
	}
	if m := odds.Meta; !*m.ExcludeExchanges || len(m.ExchangeBooks) != 3 || m.AvailableLeagues[0] != "nfl" ||
		*m.FanduelMarketIds.State != "co" || *m.FanduelMarketIds.Available || m.FanduelMarketIds.Timestamp != nil {
		t.Errorf("odds meta %+v", m)
	}

	api = newFakeAPI(t, ok(`{"success": true, "data": [], "meta": {"sport": "nba",
		"retiredBooks": {"bet365": "GET /api/v2/bet365/nba"},
		"fanduelMarketIds": {"state": "ks", "mapped": 812, "unmapped": 3, "timestamp": "2026-10-25T18:00:00.000Z"}}}`))
	props, err := testClient(t, api.URL).GetProps(ctx, "nba", &PropsParams{Books: []string{"bet365", "fanduel"}, State: "ks"})
	if err != nil {
		t.Fatal(err)
	}
	if m := props.Meta; m.RetiredBooks["bet365"] != "GET /api/v2/bet365/nba" || *m.FanduelMarketIds.Mapped != 812 || *m.FanduelMarketIds.Unmapped != 3 {
		t.Errorf("props meta %+v", m)
	}

	api = newFakeAPI(t, ok(`{"success": true, "data": {"eventId": "e", "book": "pinnacle", "dataPoints": 0, "history": [],
		"dataQuality": {"allPricesSuppressed": true, "note": "every snapshot of this market was withheld", "suspectPricesExcluded": true}}}`))
	hist, err := testClient(t, api.URL).GetOddsHistory(ctx, OddsHistoryParams{EventID: "e", Book: "pinnacle", Market: "h2h", Side: "home"})
	if err != nil {
		t.Fatal(err)
	}
	if q := hist.Data.DataQuality; q == nil || !*q.AllPricesSuppressed || !*q.SuspectPricesExcluded || q.MainLineOnly != nil ||
		*q.Note != "every snapshot of this market was withheld" {
		t.Errorf("history data quality %+v", q)
	}
}
