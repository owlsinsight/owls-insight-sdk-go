package owls

import (
	"context"
	"encoding/json"
	"testing"
)

// The bout and the archive answer are trimmed from live answers (api-rest v2.566.0, 2026-11-10).
// The captured UFC answer and the score follow the documented shapes: no captured UFC rows exist yet.

func TestHistoryGamesUFCBoutFields(t *testing.T) {
	api := newFakeAPI(t, ok(`{"success":true,"data":{"games":[{"eventId":"mma:pedro tyson@petrino vitor-20240302","sport":"mma","homeTeam":"Vitor Petrino","awayTeam":"Tyson Pedro","gameDate":"2024-03-02T00:00:00.000Z","commenceTime":"2024-03-02T21:00:00.000Z","archivedAt":"2026-11-06T09:25:22.866Z","source":"third_party_archive","oddsSnapshots":72,"propsSnapshots":0,"playerStats":0,"card":"UFC Fight Night: Rozenstruik vs. Gaziev","weightClass":"Light Heavyweight","scheduledRounds":3,"status":"final","winner":"home","finishRound":3,"finishTimeSeconds":300}],"pagination":{"total":4478,"limit":1,"offset":0,"hasMore":true}}}`))
	c := testClient(t, api.URL)
	res, err := c.GetHistoryGames(context.Background(), &HistoryGamesParams{Sport: "mma", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if q := api.req(0).Query; q != "limit=1&sport=mma" {
		t.Errorf("query %s", q)
	}
	g := res.Data.Games[0]
	if *g.Source != "third_party_archive" || *g.Card != "UFC Fight Night: Rozenstruik vs. Gaziev" || *g.WeightClass != "Light Heavyweight" {
		t.Errorf("source/card/weight class: %v %v %v", *g.Source, *g.Card, *g.WeightClass)
	}
	if *g.ScheduledRounds != 3 || *g.Status != "final" || *g.Winner != "home" {
		t.Errorf("rounds/status/winner: %v %v %v", *g.ScheduledRounds, *g.Status, *g.Winner)
	}
	// The archive has no method of victory before June 2024: the key is absent, so the field is nil.
	if g.FinishMethod != nil || *g.FinishRound != 3 || *g.FinishTimeSeconds != 300 {
		t.Errorf("finish: %v %v %v", g.FinishMethod, *g.FinishRound, *g.FinishTimeSeconds)
	}
}

func TestHistoryGamesControlANonUFCGameHasNoBoutFields(t *testing.T) {
	api := newFakeAPI(t, ok(`{"success":true,"data":{"games":[{"eventId":"nba:Lakers@Celtics-20260201","sport":"nba","homeTeam":"Celtics","awayTeam":"Lakers","source":"live","oddsSnapshots":5000}],"pagination":{"total":1,"limit":1,"offset":0,"hasMore":false}}}`))
	res, err := testClient(t, api.URL).GetHistoryGames(context.Background(), &HistoryGamesParams{Sport: "nba", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	g := res.Data.Games[0]
	if g.Card != nil || g.WeightClass != nil || g.ScheduledRounds != nil || g.Status != nil || g.Winner != nil || g.FinishMethod != nil || g.FinishRound != nil || g.FinishTimeSeconds != nil {
		t.Errorf("a game that is not a bout carries bout fields: %+v", g)
	}
}

func TestHistoryOddsUFCFighterNames(t *testing.T) {
	for _, tc := range []struct{ name, source string }{{"archive answer", "third_party_archive"}, {"captured answer", "live"}} {
		t.Run(tc.name, func(t *testing.T) {
			api := newFakeAPI(t, ok(`{"success":true,"data":{"eventId":"mma:pedro tyson@petrino vitor-20240302","snapshots":[{"book":"bet365","market":"h2h","side":"away","price":300,"recordedAt":null,"recordedAtUtc":null,"phase":"close","bookRegion":null}],"count":1,"limit":50,"offset":0},"meta":{"source":"`+tc.source+`","awayTeam":"Tyson Pedro","homeTeam":"Vitor Petrino"}}`))
			res, err := testClient(t, api.URL).GetHistoryOdds(context.Background(), HistoryOddsParams{EventID: "mma:pedro tyson@petrino vitor-20240302"})
			if err != nil {
				t.Fatal(err)
			}
			if *res.Meta.Source != tc.source || *res.Meta.AwayTeam != "Tyson Pedro" || *res.Meta.HomeTeam != "Vitor Petrino" {
				t.Errorf("meta: %v %v %v", *res.Meta.Source, *res.Meta.AwayTeam, *res.Meta.HomeTeam)
			}
		})
	}
}

func TestRealtimeLiveScore(t *testing.T) {
	api := newFakeAPI(t, ok(`{"success":true,"data":[{"id":"1","sport_key":"soccer","home_team":"A","away_team":"B","bookmakers":[],"score":{"home":2,"away":1}},{"id":"2","sport_key":"soccer","home_team":"C","away_team":"D","bookmakers":[]}],"meta":{}}`))
	res, err := testClient(t, api.URL).GetRealtime(context.Background(), "soccer", "")
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Data[0].Score; s == nil || *s.Home != 2 || *s.Away != 1 {
		t.Errorf("score: %+v", s)
	}
	// CONTROL: an event without a score (before kickoff) has none.
	if res.Data[1].Score != nil {
		t.Errorf("score on an event without one: %+v", res.Data[1].Score)
	}
}

func TestBovadaSoccerSubscription(t *testing.T) {
	// Bovada soccer (API v2.566.0): one league, subscribed with "*" like the book's other sports.
	b, err := json.Marshal(BovadaV2BookSubscription{Soccer: ptrRaw(`"*"`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"soccer":"*"}` {
		t.Errorf("marshalled %s", b)
	}
}

func ptrRaw(s string) *json.RawMessage {
	r := json.RawMessage(s)
	return &r
}
