package owls

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

// CompareSgpCombo (POST /api/v1/sgp/combo-compare, API v2.523.0). The bodies are the
// API's (nba-odds-app routes/sgp-combo.ts, lib/sgp/combo/kalshi-legs.ts), with the
// real Kalshi combo of its tests/unit/fixtures/kalshi-combo-mve-mlb-miltb.json.

const sgpComboTicker = "KXMVECROSSCATEGORY-S2026ZJDATBFMVOSS4HNQ-QRB4CF6A6"

const sgpCombo200 = `{"success": true, "venue": "kalshi",
	"combo": {"ticker": "KXMVECROSSCATEGORY-S2026ZJDATBFMVOSS4HNQ-QRB4CF6A6", "legs": [
		{"marketTicker": "KXMLBHIT-26OCT282000MILTB-MILJCHOURIO11-2", "side": "yes", "title": "Jackson Chourio: 2+",
			"translated": {"type": "player_prop", "player": "Jackson Chourio", "category": "hits", "side": "over", "line": 1.5}, "why": null},
		{"marketTicker": "KXMLBHIT-26OCT282000MILTB-MILWCONTRERAS24-1", "side": "yes", "title": "William Contreras: 1+",
			"translated": {"type": "player_prop", "player": "William Contreras", "category": "hits", "side": "over", "line": 0.5}, "why": null},
		{"marketTicker": "KXMLBSPREAD-26OCT282000MILTB-MIL3", "side": "yes", "title": "Milwaukee wins by over 2.5 runs",
			"translated": {"type": "spread", "selection": "Milwaukee Brewers", "point": -2.5}, "why": null}]},
	"venuePrice": {"yesBid": null, "yesAsk": null, "status": "active"},
	"eventId": "mlb:Milwaukee Brewers@Tampa Bay Rays-20261028",
	"compare": {"success": true, "sport": "mlb", "eventId": "mlb:Milwaukee Brewers@Tampa Bay Rays-20261028",
		"legs": [{"type": "player_prop", "player": "Jackson Chourio", "category": "hits", "side": "over", "line": 1.5},
			{"type": "player_prop", "player": "William Contreras", "category": "hits", "side": "over", "line": 0.5},
			{"type": "spread", "selection": "Milwaukee Brewers", "point": -2.5}],
		"best": {"book": "draftkings", "american": 650, "decimal": 7.5},
		"books": [{"book": "fanduel", "label": "FanDuel", "status": "skipped", "reason": "fanduel: skip"},
			{"book": "draftkings", "label": "DraftKings", "status": "priced", "bookEventId": "33099001", "httpStatus": 200, "ms": 840,
				"result": {"success": true, "pricing": "draftkings-slip", "sgp": {"american": 650, "decimal": 7.5, "isSGM": true, "legs": 3},
					"legs": [], "meta": {"eventId": "33099001", "book": "draftkings", "legsHash": "0f1e2d3c4b5a6978"}}}],
		"fair": null,
		"meta": {"timestamp": "2026-10-28T18:00:01.000Z", "deadlineMs": 6000, "legsHash": "0f1e2d3c4b5a6978", "booksAsked": 2,
			"booksCached": 0, "booksQuoted": 1, "booksPriced": 1}}}`

func sgpComboLegs() []SgpComboLegInput {
	return []SgpComboLegInput{
		{MarketTicker: "KXNHLGAME-26OCT28PHIWSH-WSH", Side: "yes"},
		{MarketTicker: "KXNHLTOTAL-26OCT28PHIWSH-7", Side: "no"},
	}
}

func TestCompareSgpCombo(t *testing.T) {
	t.Run("request and the 200", func(t *testing.T) {
		api := newFakeAPI(t, ok(sgpCombo200))
		res, err := testClient(t, api.URL).CompareSgpCombo(context.Background(), SgpComboCompareParams{
			ComboTicker: sgpComboTicker, Books: []string{"draftkings"}, DeadlineMs: 8000, FanDuel: "skip", Fair: true, Fresh: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		r := api.req(0)
		want := `{"venue":"kalshi","comboTicker":"KXMVECROSSCATEGORY-S2026ZJDATBFMVOSS4HNQ-QRB4CF6A6","books":["draftkings"],` +
			`"deadlineMs":8000,"fanduel":"skip","fair":true,"fresh":true}`
		if r.Method != "POST" || r.Path != "/api/v1/sgp/combo-compare" || r.Body != want || r.Header.Get("Content-Type") != "application/json" ||
			r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("request %s %s %q body %s", r.Method, r.Path, r.Header.Get("Content-Type"), r.Body)
		}
		if !*res.Success || *res.Venue != "kalshi" || *res.EventID != "mlb:Milwaukee Brewers@Tampa Bay Rays-20261028" ||
			*res.Combo.Ticker != sgpComboTicker || len(res.Combo.Legs) != 3 || res.Code != nil {
			t.Errorf("response %+v", res)
		}
		leg := res.Combo.Legs[0]
		if *leg.Title != "Jackson Chourio: 2+" || leg.Why != nil || leg.Translated.Type != "player_prop" ||
			leg.Translated.Player != "Jackson Chourio" || *leg.Translated.Line != 1.5 || *res.Combo.Legs[2].Translated.Point != -2.5 {
			t.Errorf("leg %+v translated %+v", leg, leg.Translated)
		}
		if res.VenuePrice.YesBid != nil || res.VenuePrice.YesAsk != nil || *res.VenuePrice.Status != "active" {
			t.Errorf("venuePrice %+v", res.VenuePrice)
		}
		if *res.Compare.Best.Book != "draftkings" || *res.Compare.Books[1].Result.Sgp.American != 650 || *res.Compare.Meta.BooksPriced != 1 {
			t.Errorf("compare %+v", res.Compare)
		}
		// CONTROL: legs instead of a ticker, no options: the venue defaults and nothing else is sent.
		_, _ = testClient(t, api.URL).CompareSgpCombo(context.Background(), SgpComboCompareParams{Legs: sgpComboLegs()})
		if got := api.req(1).Body; got != `{"venue":"kalshi","legs":[{"marketTicker":"KXNHLGAME-26OCT28PHIWSH-WSH","side":"yes"},`+
			`{"marketTicker":"KXNHLTOTAL-26OCT28PHIWSH-7","side":"no"}]}` {
			t.Errorf("control: body %s", got)
		}
	})

	t.Run("a coded 422 is returned with success false", func(t *testing.T) {
		for _, tc := range []struct {
			name, body, code string
			check            func(*SgpComboCompareResponse) bool
		}{
			{"COMBO_LEGS_UNSUPPORTED", `{"success":false,"error":"Some legs have no sportsbook equivalent","code":"COMBO_LEGS_UNSUPPORTED",
				"message":"Every leg must translate to a same game parlay leg; legs[].why says why each one that did not.","venue":"kalshi",
				"combo":{"ticker":null,"legs":[{"marketTicker":"KXNHLGAME-26OCT28PHIWSH-WSH","side":"yes","title":null,"translated":null,"why":null},
					{"marketTicker":"KXNBA1HSPREAD-26OCT28CHADET-DET12","side":"yes","title":null,"translated":null,
					"why":"KXNBA1HSPREAD is not a market the sportsbooks price in a same game parlay"}]},"venuePrice":null}`,
				"COMBO_LEGS_UNSUPPORTED", func(r *SgpComboCompareResponse) bool {
					return r.Combo.Ticker == nil && r.Combo.Legs[0].Why == nil && r.Combo.Legs[0].Translated == nil &&
						strings.HasPrefix(*r.Combo.Legs[1].Why, "KXNBA1HSPREAD") && r.VenuePrice == nil && r.Compare == nil
				}},
			{"COMBO_MULTI_GAME", `{"success":false,"error":"The combo spans more than one game","code":"COMBO_MULTI_GAME","message":"x",
				"venue":"kalshi","combo":{"ticker":null,"legs":[
					{"marketTicker":"KXNHLGAME-26OCT28PHIWSH-WSH","side":"yes","title":null,"translated":null,"why":null},
					{"marketTicker":"KXNBATOTAL-26OCT28CHADET-197","side":"yes","title":null,"translated":null,"why":null}]},"venuePrice":null,
				"games":[{"sport":"nhl","teams":["Philadelphia Flyers","Washington Capitals"],"eventDateEt":"2026-10-28","legs":[0]},
					{"sport":"nba","teams":["Charlotte Hornets","Detroit Pistons"],"eventDateEt":"2026-10-28","legs":[1]}]}`,
				"COMBO_MULTI_GAME", func(r *SgpComboCompareResponse) bool {
					return len(r.Games) == 2 && *r.Games[1].Sport == "nba" && r.Games[1].Legs[0] == 1 &&
						r.Games[0].Teams[1] == "Washington Capitals" && *r.Games[0].EventDateEt == "2026-10-28"
				}},
			{"COMBO_EVENT_NOT_FOUND", `{"success":false,"error":"The game is not on our board","code":"COMBO_EVENT_NOT_FOUND","message":"x",
				"venue":"kalshi","combo":{"ticker":"` + sgpComboTicker + `","legs":[]},"venuePrice":{"yesBid":0.12,"yesAsk":0.15,"status":"active"}}`,
				"COMBO_EVENT_NOT_FOUND", func(r *SgpComboCompareResponse) bool { return *r.VenuePrice.YesAsk == 0.15 }},
			// The API sends this one without success: the SDK sets it to false.
			{"COMBO_NOT_A_COMBO", `{"error":"Not a combo the builder can read","code":"COMBO_NOT_A_COMBO",
				"message":"` + sgpComboTicker + ` does not list 2 to 12 legs, each a market and a yes or no side."}`,
				"COMBO_NOT_A_COMBO", func(r *SgpComboCompareResponse) bool {
					return r.Combo == nil && strings.Contains(*r.Message, "2 to 12 legs")
				}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) { return 422, nil, tc.body })
				res, err := testClient(t, api.URL).CompareSgpCombo(context.Background(), SgpComboCompareParams{ComboTicker: sgpComboTicker})
				if err != nil {
					t.Fatalf("err = %v, want the 422 returned", err)
				}
				if res.Success == nil || *res.Success || *res.Code != tc.code || *res.Error == "" || !tc.check(res) {
					t.Errorf("response %+v", res)
				}
			})
		}
		// CONTROL: a 422 that is not a coded refusal is an error.
		for _, body := range []string{`<html>Unprocessable</html>`, `{"detail":[{"msg":"field required"}]}`,
			`{"success":true,"error":"x","code":"COMBO_MULTI_GAME"}`, `{"code":"COMBO_MULTI_GAME"}`, `{"error":"x"}`,
			`{"code":5,"error":"x"}`} {
			api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) { return 422, nil, body })
			res, err := testClient(t, api.URL).CompareSgpCombo(context.Background(), SgpComboCompareParams{ComboTicker: sgpComboTicker})
			var ae *APIError
			if res != nil || !errors.As(err, &ae) || ae.Status != 422 {
				t.Errorf("control %s: res %+v err %v", body, res, err)
			}
		}
	})

	for _, tc := range []struct {
		name     string
		status   int
		header   http.Header
		body     string
		sentinel error
		code     string
		retry    time.Duration
		detail   string
	}{
		{"400 COMBO_INVALID", 400, nil,
			`{"error":"Invalid request","code":"COMBO_INVALID","message":"send either 'comboTicker' (a Kalshi combo market) or 'legs', not both"}`,
			nil, "COMBO_INVALID", 0, `"message":"send either`},
		{"404 COMBO_NOT_FOUND keeps its code", 404, nil,
			`{"error":"Combo not found","code":"COMBO_NOT_FOUND","message":"Kalshi has no market KXMVECROSSCATEGORY-NOPE."}`,
			ErrNotFound, "COMBO_NOT_FOUND", 0, `"code":"COMBO_NOT_FOUND"`},
		{"CONTROL: any other 404 stays NOT_FOUND", 404, nil, `{"error":"Not found","code":"SOMETHING_ELSE"}`,
			ErrNotFound, "NOT_FOUND", 0, `SOMETHING_ELSE`},
		{"503 COMBO_VENUE_UNAVAILABLE", 503, http.Header{"Retry-After": {"17"}},
			`{"error":"Kalshi could not be read","code":"COMBO_VENUE_UNAVAILABLE","message":"Retry after the Retry-After seconds."}`,
			ErrServiceBusy, "COMBO_VENUE_UNAVAILABLE", 17 * time.Second, `"code":"COMBO_VENUE_UNAVAILABLE"`},
		{"403 SGP_TIER_REQUIRED", 403, nil,
			`{"error":"Upgrade required","code":"SGP_TIER_REQUIRED","message":"Prediction-market combo pricing requires the MVP plan or higher.","currentTier":"rookie","requiredTier":"mvp"}`,
			ErrForbidden, "SGP_TIER_REQUIRED", 0, `"requiredTier":"mvp"`},
		{"503 SGP_FEATURE_DISABLED", 503, http.Header{"Retry-After": {"60"}},
			`{"error":"SGP feature temporarily unavailable","code":"SGP_FEATURE_DISABLED","message":"x","feature":"combos"}`,
			ErrServiceBusy, "SGP_FEATURE_DISABLED", 60 * time.Second, `"feature":"combos"`},
		{"429 SGP_RATE_LIMIT", 429, http.Header{"Retry-After": {"41"}},
			`{"error":"SGP build rate limit exceeded","code":"SGP_RATE_LIMIT","limit":10,"unitsNeeded":5,"resetAt":"2026-10-28T18:00:41.000Z"}`,
			ErrRateLimited, "SGP_RATE_LIMIT", 41 * time.Second, `"unitsNeeded":5`},
		{"429 SGP_COMPARE_TOO_MANY_BOOKS", 429, nil,
			`{"error":"Too many books for your SGP build limit","code":"SGP_COMPARE_TOO_MANY_BOOKS","limit":3,"unitsNeeded":6,"maxBooks":3}`,
			ErrRateLimited, "SGP_COMPARE_TOO_MANY_BOOKS", 60 * time.Second, `"maxBooks":3`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) { return tc.status, tc.header, tc.body })
			c := testClient(t, api.URL, WithRetry(RetryPolicy{MaxRetries: 3, BaseDelay: time.Millisecond}))
			res, err := c.CompareSgpCombo(context.Background(), SgpComboCompareParams{ComboTicker: sgpComboTicker})
			var ae *APIError
			if res != nil || !errors.As(err, &ae) || ae.Status != tc.status || ae.Code != tc.code || ae.RetryAfter != tc.retry ||
				!strings.Contains(string(ae.Details), tc.detail) || (tc.sentinel != nil && !errors.Is(err, tc.sentinel)) {
				t.Fatalf("res %+v err = %#v", res, err)
			}
			// Never retried, whatever the retry policy says.
			if api.count() != 1 {
				t.Fatalf("%d requests, want 1", api.count())
			}
		})
	}

	t.Run("each attempt waits at least the combo minimum, and keeps a longer timeout", func(t *testing.T) {
		defer func(d time.Duration) { sgpComboMinTimeout = d }(sgpComboMinTimeout)
		sgpComboMinTimeout = 400 * time.Millisecond
		slow := func(d time.Duration) *fakeAPI {
			return newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) {
				select {
				case <-time.After(d):
				case <-r.Context().Done():
				}
				return 200, nil, sgpCombo200
			})
		}
		params := SgpComboCompareParams{ComboTicker: sgpComboTicker}
		api := slow(150 * time.Millisecond)
		short := testClient(t, api.URL, WithTimeout(50*time.Millisecond))
		if _, err := short.CompareSgpCombo(context.Background(), params); err != nil {
			t.Fatalf("a 50ms client timeout aborted the call: %v", err)
		}
		// CONTROL: CompareSgp keeps the client's own timeout.
		if _, err := short.CompareSgp(context.Background(), "mlb", SgpCompareParams{EventID: "e"}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("control: CompareSgp err = %v, want context.DeadlineExceeded", err)
		}
		// The minimum is a floor, not unlimited.
		api = slow(1500 * time.Millisecond)
		start := time.Now()
		_, err := testClient(t, api.URL, WithTimeout(50*time.Millisecond)).CompareSgpCombo(context.Background(), params)
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) < 350*time.Millisecond || time.Since(start) > 1200*time.Millisecond {
			t.Fatalf("err %v after %v, want a timeout after about 400ms", err, time.Since(start))
		}
		// A longer client timeout is kept, and 0 still means none.
		api = slow(600 * time.Millisecond)
		for _, d := range []time.Duration{2 * time.Second, 0} {
			if _, err := testClient(t, api.URL, WithTimeout(d)).CompareSgpCombo(context.Background(), params); err != nil {
				t.Fatalf("WithTimeout(%v): %v", d, err)
			}
		}
	})

	t.Run("the combo codes are listed apart from the SGP codes", func(t *testing.T) {
		want := []string{"COMBO_INVALID", "COMBO_NOT_FOUND", "COMBO_NOT_A_COMBO", "COMBO_LEGS_UNSUPPORTED", "COMBO_MULTI_GAME",
			"COMBO_EVENT_NOT_FOUND", "COMBO_VENUE_UNAVAILABLE"}
		if len(sgpComboErrorCodes) != len(want) {
			t.Fatalf("%d combo codes, want %d", len(sgpComboErrorCodes), len(want))
		}
		for _, code := range want {
			if !sgpComboErrorCodes[code] || sgpErrorCodes[code] {
				t.Errorf("%s: combo %v sgp %v", code, sgpComboErrorCodes[code], sgpErrorCodes[code])
			}
		}
	})
}
