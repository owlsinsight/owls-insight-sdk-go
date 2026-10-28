package owls

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// esc escapes one path segment.
func esc(s string) string { return url.PathEscape(s) }

func getInto[T any](ctx context.Context, c *Client, path string, q url.Values) (*T, error) {
	out := new(T)
	if err := c.get(ctx, path, q, out); err != nil {
		return nil, err
	}
	return out, nil
}

func leagueQuery(league string) url.Values {
	q := url.Values{}
	setStr(q, "league", league)
	return q
}

// ── Events and odds ─────────────────────────────────────────

// ListEvents lists a sport's current events without odds. The event's eventId is
// the id the history endpoints take.
func (c *Client) ListEvents(ctx context.Context, sport, league string) (*EventsResponse, error) {
	return getInto[EventsResponse](ctx, c, "/api/v1/"+esc(sport)+"/events", leagueQuery(league))
}

// GetOdds returns the merged board for one sport. A parameter the endpoint does
// not read, or a market value it does not know, is reported in
// meta.ignored_params rather than rejected.
func (c *Client) GetOdds(ctx context.Context, sport string, p *OddsParams) (*OddsResponse, error) {
	return getInto[OddsResponse](ctx, c, "/api/v1/"+esc(sport)+"/odds", p.query())
}

// GetMoneyline returns the moneyline board for one sport.
func (c *Client) GetMoneyline(ctx context.Context, sport string, p *OddsParams) (*OddsResponse, error) {
	return getInto[OddsResponse](ctx, c, "/api/v1/"+esc(sport)+"/moneyline", p.query())
}

// GetSpreads returns the spreads board for one sport.
func (c *Client) GetSpreads(ctx context.Context, sport string, p *OddsParams) (*OddsResponse, error) {
	return getInto[OddsResponse](ctx, c, "/api/v1/"+esc(sport)+"/spreads", p.query())
}

// GetTotals returns the totals board for one sport.
func (c *Client) GetTotals(ctx context.Context, sport string, p *OddsParams) (*OddsResponse, error) {
	return getInto[OddsResponse](ctx, c, "/api/v1/"+esc(sport)+"/totals", p.query())
}

// GetRealtime returns Pinnacle real-time odds for one sport (MVP and above). The
// esports keys (cs2, lol, valorant, dota2) answer with another shape: use
// GetEsportsRealtime for them; GetRealtime refuses them without a request.
func (c *Client) GetRealtime(ctx context.Context, sport, league string) (*RealtimeResponse, error) {
	if slices.Contains(esportsRealtimeGames, sport) {
		return nil, fmt.Errorf("owls: GetRealtime(%q): the esports realtime endpoints answer in the Pinnacle wire format; use GetEsportsRealtime", sport)
	}
	return getInto[RealtimeResponse](ctx, c, "/api/v1/"+esc(sport)+"/realtime", leagueQuery(league))
}

// GetEsportsRealtime returns Pinnacle real-time odds for one esport (cs2, lol,
// valorant, dota2), MVP and above. league is required: a substring of the
// Pinnacle league name such as "BLAST" or "LCK" (the server never serves the
// whole feed). Each item of Data is one Pinnacle matchup exactly as Pinnacle
// sent it: no team-name normalization, prices in American odds.
func (c *Client) GetEsportsRealtime(ctx context.Context, game, league string) (*EsportsRealtimeResponse, error) {
	league = strings.TrimSpace(league)
	if league == "" {
		return nil, fmt.Errorf("owls: GetEsportsRealtime(%q): league is required, a substring of the Pinnacle league name such as \"BLAST\"", game)
	}
	return getInto[EsportsRealtimeResponse](ctx, c, "/api/v1/"+esc(game)+"/realtime", url.Values{"league": {league}})
}

// GetPS3838Realtime returns PS3838 real-time odds for one sport (MVP and above).
func (c *Client) GetPS3838Realtime(ctx context.Context, sport, league string) (*RealtimeResponse, error) {
	return getInto[RealtimeResponse](ctx, c, "/api/v1/"+esc(sport)+"/ps3838-realtime", leagueQuery(league))
}

// GetEV returns the positive-EV prices of a sport's upcoming games (MVP and above):
// moneyline prices that beat the fair, Pinnacle's de-vigged line, which the other
// books do not move (they only veto a side they disagree with). EV is computed at the
// fair minus 1 percentage point (FairProbability); the fair before that margin is
// FairUnadjusted on each opportunity and Fair on each event. Set Venues to also list
// Kalshi and Polymarket.
func (c *Client) GetEV(ctx context.Context, sport string, p *EVParams) (*EVResponse, error) {
	q := url.Values{}
	if p != nil {
		setFloat(q, "min_ev", p.MinEV)
		setStr(q, "book", p.Book)
		setTrue(q, "venues", p.Venues)
	}
	return getInto[EVResponse](ctx, c, "/api/v1/"+esc(sport)+"/ev", q)
}

// GetEVLines returns the positive-EV spreads and totals of a sport's upcoming games
// (MVP and above): GET /api/v1/{sport}/ev with market=spreads, totals or both (the
// default). The same math as GetEV, scored at Pinnacle's main number against the
// books that hang exactly that number; every opportunity carries its own Point.
// Supported for nba, ncaab, nfl, ncaaf, nhl, mlb, wnba and soccer; elsewhere
// Meta.Unsupported says why nothing is scored.
//
// The route answers with the moneyline board (GetEV's shape) unless every market it
// is sent is spreads or totals, so GetEVLines checks Markets before any request, as
// the route reads them: split on commas, trimmed and lower-cased, empty entries
// dropped. Anything other than spreads or totals ("h2h", "spread", "moneyline") is
// an error, as is a Markets with no entry left. A nil or empty Markets asks for
// both. An answer that still reports the moneyline market is an error too.
func (c *Client) GetEVLines(ctx context.Context, sport string, p *EVLinesParams) (*EVLinesResponse, error) {
	markets := []string{"spreads", "totals"}
	q := url.Values{}
	if p != nil {
		if len(p.Markets) > 0 {
			var err error
			if markets, err = evLineMarkets(p.Markets); err != nil {
				return nil, fmt.Errorf("owls: GetEVLines(%q): %w", sport, err)
			}
		}
		setFloat(q, "min_ev", p.MinEV)
		setStr(q, "book", p.Book)
	}
	q.Set("market", strings.Join(markets, ","))
	res, err := getInto[EVLinesResponse](ctx, c, "/api/v1/"+esc(sport)+"/ev", q)
	if err != nil {
		return nil, err
	}
	if res.Meta != nil && res.Meta.Market != nil && *res.Meta.Market == "h2h" {
		return nil, fmt.Errorf("owls: GetEVLines(%q): the API answered with the moneyline board (market=%s); use GetEV for it", sport, q.Get("market"))
	}
	return res, nil
}

// evLineMarkets normalizes GetEVLines' markets the way the route parses its market
// parameter (split on commas, trimmed, lower-cased, empty entries dropped,
// duplicates removed) and refuses anything the route would answer with the
// moneyline board instead.
func evLineMarkets(in []string) ([]string, error) {
	var out []string
	for _, m := range strings.Split(strings.Join(in, ","), ",") {
		m = strings.ToLower(strings.TrimSpace(m))
		switch {
		case m == "":
			continue
		case m != "spreads" && m != "totals":
			return nil, fmt.Errorf("market %q is not spreads or totals (the moneyline is GetEV)", m)
		case !slices.Contains(out, m):
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("markets %q name no market; leave Markets nil for spreads and totals", in)
	}
	return out, nil
}

// GetFair returns the Owls fair line of a sport (MVP and above): every upcoming
// game's fair moneyline, spread and total, each side as a probability and as
// American odds, anchored on Pinnacle (GET /api/v1/{sport}/fair). These are the
// fairs before the 1 percentage point margin GetEV scores against. A market is nil when it
// has no current fair (Meta.Boards says which board is old). Spreads and totals
// are priced at Pinnacle's main number for nba, ncaab, nfl, ncaaf, nhl, mlb, wnba
// and soccer (Meta.Markets); other sports carry the moneyline only.
func (c *Client) GetFair(ctx context.Context, sport string) (*FairResponse, error) {
	return getInto[FairResponse](ctx, c, "/api/v1/"+esc(sport)+"/fair", nil)
}

// GetPropsEV returns the fair line and the positive-EV prices of a sport's player
// props (MVP and above): GET /api/v1/{sport}/props/ev. For each upcoming prop
// Pinnacle prices both ways, the fair over and under, and every sportsbook price at
// the same player, category and line that beats it. Set All to also list the props
// with no positive-EV price. Supported for nba, ncaab, wnba, nfl, ncaaf, nhl, mlb
// and soccer.
func (c *Client) GetPropsEV(ctx context.Context, sport string, p *PropsEVParams) (*PropsEVResponse, error) {
	q := url.Values{}
	if p != nil {
		setFloat(q, "min_ev", p.MinEV)
		setStr(q, "book", p.Book)
		setStr(q, "player", p.Player)
		setStr(q, "category", p.Category)
		setTrue(q, "all", p.All)
	}
	return getInto[PropsEVResponse](ctx, c, "/api/v1/"+esc(sport)+"/props/ev", q)
}

// GetOneXBetSoccer returns the isolated 1xBet soccer feed, which is not part of GetOdds.
func (c *Client) GetOneXBetSoccer(ctx context.Context) (*OneXBetSoccerResponse, error) {
	return getInto[OneXBetSoccerResponse](ctx, c, "/api/v1/1xbet/soccer", nil)
}

// GetProphetxOdds returns a raw ProphetX exchange snapshot for one or more sports.
func (c *Client) GetProphetxOdds(ctx context.Context, p ProphetXOddsParams) (*ProphetXOddsResponse, error) {
	q := url.Values{}
	q.Set("sport", strings.Join(p.Sports, ","))
	setStr(q, "kind", p.Kind)
	return getInto[ProphetXOddsResponse](ctx, c, "/api/v1/prophetx/odds", q)
}

// GetSchedule returns today's upcoming games for a sport.
func (c *Client) GetSchedule(ctx context.Context, sport string) (*ScheduleResponse, error) {
	return getInto[ScheduleResponse](ctx, c, "/api/v1/"+esc(sport)+"/schedule", nil)
}

// GetDaySchedule returns every game of a calendar day in a time zone, across
// sports: start time, status, score and clock, both sides and Kalshi win prices.
// The zero DayScheduleParams asks for today in America/New_York, every sport;
// Days extends the range up to 10 days. Meta.Status says whether the answer is
// complete and fresh ('ok', 'partial', 'stale' or 'no-data').
func (c *Client) GetDaySchedule(ctx context.Context, p DayScheduleParams) (*DayScheduleResponse, error) {
	q := url.Values{}
	setStr(q, "date", p.Date)
	setStr(q, "tz", p.TZ)
	setInt(q, "days", p.Days)
	setList(q, "sport", p.Sports)
	return getInto[DayScheduleResponse](ctx, c, "/api/v1/schedule", q)
}

// GetResults returns today's completed games for a sport.
func (c *Client) GetResults(ctx context.Context, sport string) (*ResultsResponse, error) {
	return getInto[ResultsResponse](ctx, c, "/api/v1/"+esc(sport)+"/results", nil)
}

// GetSplits returns betting splits for a sport.
func (c *Client) GetSplits(ctx context.Context, sport string) (*SplitsResponse, error) {
	return getInto[SplitsResponse](ctx, c, "/api/v1/"+esc(sport)+"/splits", nil)
}

// Normalize maps a sportsbook team name to its canonical form.
func (c *Client) Normalize(ctx context.Context, name, sport string) (*NormalizeResponse, error) {
	return getInto[NormalizeResponse](ctx, c, "/api/v1/normalize", url.Values{"name": {name}, "sport": {sport}})
}

// NormalizeBatch normalizes up to 25 team names at once.
func (c *Client) NormalizeBatch(ctx context.Context, names []string, sport string) (*NormalizeBatchResponse, error) {
	q := url.Values{"names": {strings.Join(names, ",")}, "sport": {sport}}
	return getInto[NormalizeBatchResponse](ctx, c, "/api/v1/normalize/batch", q)
}

// Scores is the result of GetScores: All for every sport, Sport for one.
type Scores struct {
	// All is set when GetScores was called with an empty sport.
	All *ScoresResponse
	// Sport is set when GetScores was called with a sport.
	Sport *SportScoresResponse
}

// GetScores returns live scores: every sport when sport is empty
// (/api/v1/scores/live), else one sport (/api/v1/{sport}/scores/live).
func (c *Client) GetScores(ctx context.Context, sport string) (*Scores, error) {
	if sport == "" {
		all, err := getInto[ScoresResponse](ctx, c, "/api/v1/scores/live", nil)
		if err != nil {
			return nil, err
		}
		return &Scores{All: all}, nil
	}
	one, err := getInto[SportScoresResponse](ctx, c, "/api/v1/"+esc(sport)+"/scores/live", nil)
	if err != nil {
		return nil, err
	}
	return &Scores{Sport: one}, nil
}

// Plays is the result of GetPlays: Game for one game's play-by-play, Games for
// the list of games that have it.
type Plays struct {
	// Game is set when GetPlays was called with an event id.
	Game *PlaysResponse
	// Games is set when GetPlays was called with an empty event id.
	Games *PlaysGamesResponse
}

// GetPlays returns the play-by-play of one NBA, NHL, NFL, NCAAF or soccer game in
// play (or ended within the last 6 hours), oldest play first, by the event id
// GetScores gives the game (GET /api/v1/{sport}/plays?eventId=). For soccer the
// plays are the match incidents (goals, own goals, penalties, cards,
// substitutions) of every game on the live board, whatever its league. With an empty eventID it
// returns the games that have play-by-play; p must then be nil or set neither Since
// nor Limit (they apply to one game, so GetPlays refuses them without a request).
// A game with no play-by-play (before tip-off, or any other id) is a 404, an
// [*APIError].
//
// Poll with Since set to the LastSeq you have, at least every 2 minutes, to receive
// the new plays and every amended one, or opt in to plays-update with
// Subscription.Plays. Merge plays by ID: a play can be amended after it is first
// published. Play.Detail is the sport's own fields as raw JSON: decode it with
// Decode[FootballPlayDetail], Decode[HockeyPlayDetail], Decode[BasketballPlayDetail]
// or Decode[SoccerPlayDetail].
func (c *Client) GetPlays(ctx context.Context, sport, eventID string, p *PlaysParams) (*Plays, error) {
	path := "/api/v1/" + esc(sport) + "/plays"
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		if p != nil && (p.Since != nil || p.Limit != 0) {
			return nil, fmt.Errorf("owls: GetPlays(%q): Since and Limit apply to one game; pass its event id, or nil params to list the games", sport)
		}
		games, err := getInto[PlaysGamesResponse](ctx, c, path, nil)
		if err != nil {
			return nil, err
		}
		return &Plays{Games: games}, nil
	}
	q := url.Values{"eventId": {eventID}}
	if p != nil {
		if p.Since != nil {
			q.Set("since", strconv.FormatFloat(*p.Since, 'f', -1, 64))
		}
		setInt(q, "limit", p.Limit)
	}
	game, err := getInto[PlaysResponse](ctx, c, path, q)
	if err != nil {
		return nil, err
	}
	return &Plays{Game: game}, nil
}

// GetInjuries returns player injury designations for a sport (nfl, mlb), one
// record per source.
func (c *Client) GetInjuries(ctx context.Context, sport string, p *InjuriesParams) (*InjuriesResponse, error) {
	q := url.Values{}
	if p != nil {
		setStr(q, "source", p.Source)
		setList(q, "status", p.Status)
		setList(q, "team", p.Team)
		setTrue(q, "includeActive", p.IncludeActive)
	}
	return getInto[InjuriesResponse](ctx, c, "/api/v1/"+esc(sport)+"/injuries", q)
}

// ── Props ───────────────────────────────────────────────────

// GetProps returns player props for a sport across books.
func (c *Client) GetProps(ctx context.Context, sport string, p *PropsParams) (*PropsResponse, error) {
	return getInto[PropsResponse](ctx, c, "/api/v1/"+esc(sport)+"/props", p.query(true))
}

// BookPropsResult is the result of GetBookProps. BetMGM answers with its own
// shape; every other book with the common one.
type BookPropsResult struct {
	// Props is set for every book except betmgm.
	Props *PropsResponse
	// BetMGM is set for book "betmgm".
	BetMGM *BetMGMPropsResponse
}

// GetBookProps returns one book's player props for a sport. p.Books is not sent;
// p.State serves another state's FanDuel marketIds on book "fanduel" (see
// PropsParams.State). The bet365 props were retired: that book answers 410 Gone,
// an [*APIError] (Bet365 game lines are on GetV2).
func (c *Client) GetBookProps(ctx context.Context, sport, book string, p *PropsParams) (*BookPropsResult, error) {
	path := "/api/v1/" + esc(sport) + "/props/" + esc(book)
	q := p.query(false)
	if book == "betmgm" {
		r, err := getInto[BetMGMPropsResponse](ctx, c, path, q)
		if err != nil {
			return nil, err
		}
		return &BookPropsResult{BetMGM: r}, nil
	}
	r, err := getInto[PropsResponse](ctx, c, path, q)
	if err != nil {
		return nil, err
	}
	return &BookPropsResult{Props: r}, nil
}

// GetPropsHistory returns the line history of one player prop.
func (c *Client) GetPropsHistory(ctx context.Context, sport string, p PropsHistoryParams) (*PropsHistoryResponse, error) {
	q := url.Values{"game_id": {p.GameID}, "player": {p.Player}, "category": {p.Category}}
	setStr(q, "book", p.Book)
	setInt(q, "hours", p.Hours)
	return getInto[PropsHistoryResponse](ctx, c, "/api/v1/"+esc(sport)+"/props/history", q)
}

// GetPropsStats returns prop counts per sport and book.
func (c *Client) GetPropsStats(ctx context.Context) (*PropsStatsResponse, error) {
	return getInto[PropsStatsResponse](ctx, c, "/api/v1/props/stats", nil)
}

// GetBookPropsStats returns one book's prop counts.
func (c *Client) GetBookPropsStats(ctx context.Context, book string) (*BookPropsStatsResponse, error) {
	return getInto[BookPropsStatsResponse](ctx, c, "/api/v1/props/"+esc(book)+"/stats", nil)
}

// PropResults is the result of GetPropResults: Game for one game's graded
// board, Day for the list of a day's graded games.
type PropResults struct {
	// Game is set when the request named a GameID.
	Game *PropResultsResponse
	// Day is set when the request named only a Date.
	Day *PropResultsDayResponse
}

// GetPropResults returns graded player-prop outcomes for one completed game, or
// with only p.Date, the graded games of that day.
func (c *Client) GetPropResults(ctx context.Context, sport string, p PropResultsParams) (*PropResults, error) {
	q := url.Values{}
	setStr(q, "game_id", p.GameID)
	setStr(q, "date", p.Date)
	setStr(q, "player", p.Player)
	setStr(q, "category", p.Category)
	setTrue(q, "confirmed_only", p.ConfirmedOnly)
	path := "/api/v1/" + esc(sport) + "/props/results"
	if p.GameID == "" && p.Date != "" {
		day, err := getInto[PropResultsDayResponse](ctx, c, path, q)
		if err != nil {
			return nil, err
		}
		return &PropResults{Day: day}, nil
	}
	game, err := getInto[PropResultsResponse](ctx, c, path, q)
	if err != nil {
		return nil, err
	}
	return &PropResults{Game: game}, nil
}

// GetPropTrends returns one player's recent form in one prop category.
func (c *Client) GetPropTrends(ctx context.Context, sport string, p PropTrendsParams) (*PropTrendsResponse, error) {
	q := url.Values{"player": {p.Player}, "category": {p.Category}}
	setStr(q, "line", p.Line)
	setStr(q, "book", p.Book)
	setInt(q, "window", p.Window)
	setTrue(q, "confirmed_only", p.ConfirmedOnly)
	return getInto[PropTrendsResponse](ctx, c, "/api/v1/"+esc(sport)+"/props/trends", q)
}

// ── Stats ───────────────────────────────────────────────────

// GetStats returns box scores for a sport; date (YYYY-MM-DD) is optional.
func (c *Client) GetStats(ctx context.Context, sport, date string) (*StatsResponse, error) {
	q := url.Values{}
	setStr(q, "date", date)
	return getInto[StatsResponse](ctx, c, "/api/v1/"+esc(sport)+"/stats", q)
}

// GetMatchStats returns live match statistics for one event.
func (c *Client) GetMatchStats(ctx context.Context, sport, eventID string) (*MatchStatsResponse, error) {
	return getInto[MatchStatsResponse](ctx, c, "/api/v1/"+esc(sport)+"/stats/match", url.Values{"eventId": {eventID}})
}

// GetH2H returns head-to-head data for one event.
func (c *Client) GetH2H(ctx context.Context, sport, eventID string) (*H2HResponse, error) {
	return getInto[H2HResponse](ctx, c, "/api/v1/"+esc(sport)+"/stats/h2h", url.Values{"eventId": {eventID}})
}

// GetPlayerAverages returns a player's season and rolling averages.
func (c *Client) GetPlayerAverages(ctx context.Context, sport string, p PlayerAveragesParams) (*PlayerAveragesResponse, error) {
	q := url.Values{"playerName": {p.PlayerName}}
	setStr(q, "opponent", p.Opponent)
	return getInto[PlayerAveragesResponse](ctx, c, "/api/v1/"+esc(sport)+"/stats/averages", q)
}

// ── History ─────────────────────────────────────────────────

// GetOddsHistory returns the line movement of one book, market and side of a
// live or recent event.
func (c *Client) GetOddsHistory(ctx context.Context, p OddsHistoryParams) (*OddsHistoryResponse, error) {
	q := url.Values{"eventId": {p.EventID}, "book": {p.Book}, "market": {p.Market}, "side": {p.Side}}
	setInt64(q, "startTime", p.StartTime)
	setInt(q, "hours", p.Hours)
	return getInto[OddsHistoryResponse](ctx, c, "/api/odds/history", q)
}

// GetMoneylineHistory is GetOddsHistory for the h2h market.
func (c *Client) GetMoneylineHistory(ctx context.Context, p LineHistoryParams) (*OddsHistoryResponse, error) {
	return c.GetOddsHistory(ctx, p.withMarket("h2h"))
}

// GetSpreadHistory is GetOddsHistory for the spreads market.
func (c *Client) GetSpreadHistory(ctx context.Context, p LineHistoryParams) (*OddsHistoryResponse, error) {
	return c.GetOddsHistory(ctx, p.withMarket("spreads"))
}

// GetTotalsHistory is GetOddsHistory for the totals market (Side "over" or "under").
func (c *Client) GetTotalsHistory(ctx context.Context, p LineHistoryParams) (*OddsHistoryResponse, error) {
	return c.GetOddsHistory(ctx, p.withMarket("totals"))
}

// GetHistoryCoverage returns how far back each historical dataset goes, per sport.
func (c *Client) GetHistoryCoverage(ctx context.Context) (*HistoryCoverageResponse, error) {
	return getInto[HistoryCoverageResponse](ctx, c, "/api/v1/history/coverage", nil)
}

// GetHistoryGames lists archived games.
func (c *Client) GetHistoryGames(ctx context.Context, p *HistoryGamesParams) (*HistoryGamesResponse, error) {
	q := url.Values{}
	if p != nil {
		setStr(q, "sport", p.Sport)
		setStr(q, "season", p.Season)
		setStr(q, "team", p.Team)
		setStr(q, "gameType", p.GameType)
		setStr(q, "startDate", p.StartDate)
		setStr(q, "endDate", p.EndDate)
		setInt(q, "limit", p.Limit)
		setInt(q, "offset", p.Offset)
	}
	return getInto[HistoryGamesResponse](ctx, c, "/api/v1/history/games", q)
}

// GetHistoryOdds returns one page of an archived game's odds snapshots. To read
// them all, use IterHistoryOdds.
func (c *Client) GetHistoryOdds(ctx context.Context, p HistoryOddsParams) (*HistoryOddsResponse, error) {
	q := p.query()
	setInt(q, "limit", p.Limit)
	setInt(q, "offset", p.Offset)
	return getInto[HistoryOddsResponse](ctx, c, "/api/v1/history/odds", q)
}

// GetHistoryProps returns one page of an archived game's prop snapshots. To read
// them all, use IterHistoryProps.
func (c *Client) GetHistoryProps(ctx context.Context, p HistoryPropsParams) (*HistoryPropsResponse, error) {
	q := p.query()
	setInt(q, "limit", p.Limit)
	setInt(q, "offset", p.Offset)
	return getInto[HistoryPropsResponse](ctx, c, "/api/v1/history/props", q)
}

// GetHistoryStats returns archived player stats.
func (c *Client) GetHistoryStats(ctx context.Context, p *HistoryStatsParams) (*HistoryStatsResponse, error) {
	q := url.Values{}
	if p != nil {
		setStr(q, "eventId", p.EventID)
		setStr(q, "playerName", p.PlayerName)
		setStr(q, "sport", p.Sport)
		setStr(q, "position", p.Position)
		setStr(q, "startDate", p.StartDate)
		setStr(q, "endDate", p.EndDate)
		setInt(q, "limit", p.Limit)
		setInt(q, "offset", p.Offset)
	}
	return getInto[HistoryStatsResponse](ctx, c, "/api/v1/history/stats", q)
}

// GetHistoryTennisStats returns the archived match statistics of a tennis event.
func (c *Client) GetHistoryTennisStats(ctx context.Context, eventID string) (*TennisStatsResponse, error) {
	return getInto[TennisStatsResponse](ctx, c, "/api/v1/history/tennis-stats", url.Values{"eventId": {eventID}})
}

// GetGameStatsDetail returns team stats, lineups, head-to-head and incidents of an
// archived game.
func (c *Client) GetGameStatsDetail(ctx context.Context, eventID string) (*GameStatsDetailResponse, error) {
	return getInto[GameStatsDetailResponse](ctx, c, "/api/v1/history/game-stats-detail", url.Values{"eventId": {eventID}})
}

// GetClosingOdds returns historical closing lines, paginated.
func (c *Client) GetClosingOdds(ctx context.Context, p *ClosingOddsParams) (*ClosingOddsResponse, error) {
	q := url.Values{}
	if p != nil {
		setStr(q, "eventId", p.EventID)
		setStr(q, "sport", p.Sport)
		setStr(q, "book", p.Book)
		setStr(q, "source", p.Source)
		setStr(q, "startDate", p.StartDate)
		setStr(q, "endDate", p.EndDate)
		setStr(q, "season", p.Season)
		setInt(q, "limit", p.Limit)
		setInt(q, "offset", p.Offset)
	}
	return getInto[ClosingOddsResponse](ctx, c, "/api/v1/history/closing-odds", q)
}

// GetHistoricalPlayerProps returns historical closing player-prop lines, paginated.
func (c *Client) GetHistoricalPlayerProps(ctx context.Context, p *HistoricalPlayerPropsParams) (*HistoricalPlayerPropsResponse, error) {
	q := url.Values{}
	if p != nil {
		setStr(q, "eventId", p.EventID)
		setStr(q, "sport", p.Sport)
		setStr(q, "player", p.Player)
		setStr(q, "propType", p.PropType)
		setStr(q, "book", p.Book)
		if p.IsMain != nil {
			q.Set("isMain", strconv.FormatBool(*p.IsMain))
		}
		setStr(q, "startDate", p.StartDate)
		setStr(q, "endDate", p.EndDate)
		setInt(q, "limit", p.Limit)
		setInt(q, "offset", p.Offset)
	}
	return getInto[HistoricalPlayerPropsResponse](ctx, c, "/api/v1/history/player-props", q)
}

// GetPublicBetting returns historical public betting percentages, paginated.
func (c *Client) GetPublicBetting(ctx context.Context, p *PublicBettingParams) (*PublicBettingResponse, error) {
	q := url.Values{}
	if p != nil {
		setStr(q, "eventId", p.EventID)
		setStr(q, "sport", p.Sport)
		setStr(q, "startDate", p.StartDate)
		setStr(q, "endDate", p.EndDate)
		setInt(q, "limit", p.Limit)
		setInt(q, "offset", p.Offset)
	}
	return getInto[PublicBettingResponse](ctx, c, "/api/v1/history/public-betting", q)
}

// GetHistorySplits returns one page of the movement history of betting splits
// (MVP and above): each book's handle and ticket percentages on a game's spread,
// total and moneyline, with the line or price shown, recorded whenever they
// changed, oldest first. Ask for one game by EventID, or for a sport by Sport and
// StartDate; page with Limit and Offset until Pagination.HasMore is false.
func (c *Client) GetHistorySplits(ctx context.Context, p HistorySplitsParams) (*HistorySplitsResponse, error) {
	q := url.Values{}
	setStr(q, "eventId", p.EventID)
	setStr(q, "sport", p.Sport)
	setStr(q, "book", p.Book)
	setStr(q, "startDate", p.StartDate)
	setStr(q, "endDate", p.EndDate)
	setInt(q, "limit", p.Limit)
	setInt(q, "offset", p.Offset)
	return getInto[HistorySplitsResponse](ctx, c, "/api/v1/history/splits", q)
}

// GetCS2Matches searches archived CS2 matches.
func (c *Client) GetCS2Matches(ctx context.Context, p *CS2MatchesParams) (*CS2MatchesResponse, error) {
	q := url.Values{}
	if p != nil {
		setStr(q, "team", p.Team)
		setStr(q, "event", p.Event)
		setStr(q, "startDate", p.StartDate)
		setStr(q, "endDate", p.EndDate)
		setInt(q, "stars", p.Stars)
		setTrue(q, "lan", p.LAN)
		setInt(q, "limit", p.Limit)
		setInt(q, "offset", p.Offset)
	}
	return getInto[CS2MatchesResponse](ctx, c, "/api/v1/history/cs2/matches", q)
}

// GetCS2Match returns one CS2 match with map scores and player stats.
func (c *Client) GetCS2Match(ctx context.Context, matchID string) (*CS2MatchDetailResponse, error) {
	return getInto[CS2MatchDetailResponse](ctx, c, "/api/v1/history/cs2/matches/"+esc(matchID), nil)
}

// GetCS2Players searches CS2 player stats across matches.
func (c *Client) GetCS2Players(ctx context.Context, p CS2PlayersParams) (*CS2PlayersResponse, error) {
	q := url.Values{}
	setStr(q, "playerName", p.PlayerName)
	setStr(q, "team", p.Team)
	setStr(q, "event", p.Event)
	setStr(q, "startDate", p.StartDate)
	setStr(q, "endDate", p.EndDate)
	setStr(q, "mapName", p.MapName)
	setFloat(q, "minRating", p.MinRating)
	setInt(q, "limit", p.Limit)
	setInt(q, "offset", p.Offset)
	return getInto[CS2PlayersResponse](ctx, c, "/api/v1/history/cs2/players", q)
}

// ── Hard Rock and Stake raw endpoints ───────────────────────

// GetHardRockLeagues lists the leagues of a Hard Rock sport in a state.
func (c *Client) GetHardRockLeagues(ctx context.Context, state, sport string) (*HardRockLeaguesResponse, error) {
	return getInto[HardRockLeaguesResponse](ctx, c, "/api/v2/hardrock/"+esc(state)+"/"+esc(sport)+"/leagues", nil)
}

// GetHardRockEvents returns raw Hard Rock events with all their markets. Pass a
// league slug for the big sports; leave it empty for the small ones.
func (c *Client) GetHardRockEvents(ctx context.Context, state, sport, league string) (*HardRockEventsResponse, error) {
	return getInto[HardRockEventsResponse](ctx, c, "/api/v2/hardrock/"+esc(state)+"/"+esc(sport), leagueQuery(league))
}

// GetHardRockLadder returns the rootIdx to American odds ladder that decodes Hard
// Rock selections.
func (c *Client) GetHardRockLadder(ctx context.Context) (*HardRockLadderResponse, error) {
	return getInto[HardRockLadderResponse](ctx, c, "/api/v2/hardrock/ladder", nil)
}

// GetStakeBets returns a sample of Stake's public bet feed. It is a sample, not a
// ledger: see meta.sampling.
func (c *Client) GetStakeBets(ctx context.Context) (*StakeBetsResponse, error) {
	return getInto[StakeBetsResponse](ctx, c, "/api/v2/stake/bets", nil)
}

// ── Same Game Parlay ────────────────────────────────────────

// GetSgpEvents lists the events a book can price parlays for: fanduel (the
// server's default when book is empty), draftkings, hardrock, betrivers, ballybet
// or betmgm.
func (c *Client) GetSgpEvents(ctx context.Context, sport, book string) (*SgpEventsResponse, error) {
	q := url.Values{}
	setStr(q, "book", book)
	return getInto[SgpEventsResponse](ctx, c, "/api/v1/"+esc(sport)+"/sgp/events", q)
}

// BuildSgp prices a same game parlay on one book: "fanduel", "draftkings",
// "hardrock", "betrivers", "ballybet" or "betmgm". It is never retried: every
// attempt is a real pricing request.
//
// A 422 whose body has success false and a legs array is RETURNED, not an error:
// branch on ReasonCode ("unresolved" means a leg did not resolve and legs[i].reason
// says why; any other code means the book refused the slip). Every other 422 is an
// [*APIError]. A 503 with Code [SgpNoPriceCode] means no usable price came back;
// its RetryAfter is the server's, or 2 s when the header is missing.
//
// With p.Fair (MVP and Hall of Fame plans) a priced 200 also carries Fair, our
// estimate of the parlay's price from de-vigged leg probabilities and the book's
// own correlation (SgpFairBlock), beside the book's Sgp, never in place of it.
// Fair.Price is nil, with Fair.Why, when the estimate cannot be made. On the
// Rookie plan the call is a 403 with Code SGP_TIER_REQUIRED, refused before
// anything is priced (below Rookie the builder itself is a 403 FORBIDDEN); a 503
// with Code SGP_FEATURE_DISABLED means the fair price is not switched on yet. To
// price the same legs on every book at once, see CompareSgp.
func (c *Client) BuildSgp(ctx context.Context, sport string, p SgpBuildParams) (*SgpBuildResponse, error) {
	status, header, body, err := c.roundTrip(ctx, http.MethodPost, "/api/v1/"+esc(sport)+"/sgp/build", nil, p)
	if err != nil {
		return nil, err
	}
	switch {
	case status >= 200 && status < 300:
		out := new(SgpBuildResponse)
		if err := decodeBody(body, out); err != nil {
			return nil, err
		}
		return out, nil
	case status == http.StatusUnprocessableEntity:
		var envelope struct {
			Success *bool             `json:"success"`
			Legs    []json.RawMessage `json:"legs"`
		}
		if json.Unmarshal(body, &envelope) == nil && envelope.Success != nil && !*envelope.Success && envelope.Legs != nil {
			out := new(SgpBuildResponse)
			if err := decodeBody(body, out); err == nil {
				return out, nil
			}
		}
		return nil, newAPIError(status, header, body)
	case status == http.StatusServiceUnavailable:
		e := newAPIError(status, header, body)
		if e.Code == SgpNoPriceCode {
			if _, ok := parseRetryAfter(header, time.Now()); !ok {
				e.RetryAfter = sgpNoPriceRetryAfter
			}
		}
		return nil, e
	}
	return nil, newAPIError(status, header, body)
}

// sgpBatchMinTimeout is the shortest per-attempt timeout BatchSgp uses. The API
// starts a batch's last parlay up to about 20 s after the call began and gives it
// up to DeadlineMs (15 s at most) plus about 2.5 s more, so a batch can rightly
// take about 38 s: the 30 s default would abort it while the builds it reserved are
// still being priced. A variable only so the tests can shorten it.
var sgpBatchMinTimeout = 45 * time.Second

// CompareSgp prices one same game parlay on every SGP book at once (FanDuel,
// DraftKings, Hard Rock, BetRivers, Bally Bet and BetMGM), from OUR event id, and
// returns each book's own answer side by side with the best price (MVP and Hall of
// Fame).
//
// p.EventID is our id for the game, as GetOdds gives it
// ({sport}:{Away}@{Home}-{YYYYMMDD}, -G2 for the second game of a doubleheader),
// not a book's id from GetSgpEvents. The same legs go to every book; books price
// game legs at the lines they list (the main line, and on NHL the alternates Hard
// Rock, Bally Bet and, before the game, DraftKings list), so a spread or total at a
// number one book does not list comes back "unresolved" on that book. A team_total
// or period leg is "unsupported" on every book except Hard Rock on NHL.
//
// A 200 always carries every book asked, each with a Status: "priced" books have
// Result.Sgp (exactly the body BuildSgp returns for that book), "refused" and
// "unresolved" books have the 422 body in Result, and the others a Reason. Best is
// the highest decimal price among the priced books (nil when none priced). Branch
// on each book's Status; a book that did not price is not an error. New statuses
// can appear: treat one you do not know as no price.
//
// Before a game starts, a book that priced exactly these legs for this game in the
// last 15 seconds may be answered from that quote (Cached, Result.Meta.CacheAgeMs),
// which costs no build; p.Fresh prices every book now. Every other book asked
// counts one build against the key's per-minute SGP build limit; a book that is
// never asked for a price (not listed, a different game, not reached in time, a leg
// that does not resolve) is given back at once, while the minute it was counted in
// is still running. p.Fair adds our estimate (SgpFairBlock), built from every book
// that priced.
//
// The call can take up to p.DeadlineMs (default 6000; FanDuel, unless p.FanDuel is
// "skip", is given at least 12000) plus about a second: keep the client's timeout
// ([WithTimeout], default 30 s) above that.
//
// Errors are [*APIError] values with these codes: a 403 SGP_TIER_REQUIRED (the plan
// is below MVP); a 503 SGP_FEATURE_DISABLED (compare, or with p.Fair the fair
// price, is not switched on yet; RetryAfter 60 s); a 429 SGP_RATE_LIMIT (the books
// this call needs exceed what is left of the per-minute build limit: wait
// RetryAfter; Details carries unitsNeeded); a 429 SGP_COMPARE_TOO_MANY_BOOKS (the
// comparison needs more builds than the limit allows at all, sent without
// Retry-After, so RetryAfter is the 60 s default and waiting never helps: resend
// with at most Details' maxBooks books); a 503 SGP_RATE_LIMIT_UNAVAILABLE (2 s) or
// a code-less 503 (SERVICE_BUSY): retry later; a 400 for a malformed body (Message
// says which field).
//
// It is never retried: every attempt is a real pricing request.
func (c *Client) CompareSgp(ctx context.Context, sport string, p SgpCompareParams) (*SgpCompareResponse, error) {
	return sendInto[SgpCompareResponse](ctx, c, http.MethodPost, "/api/v1/"+esc(sport)+"/sgp/compare", p)
}

// sgpComboMinTimeout is the shortest per-attempt timeout CompareSgpCombo uses.
// Before pricing, the API may read the combo and then its legs from Kalshi (each
// read up to about 5 s), then prices them as CompareSgp does: up to DeadlineMs
// (15 s at most; FanDuel at least 12 s) plus a second or two. The 30 s default can
// run out first. A variable only so the tests can shorten it.
var sgpComboMinTimeout = 45 * time.Second

// CompareSgpCombo prices a Kalshi combo as the same game parlay it is, on every
// SGP book at once (MVP and Hall of Fame). It is not sport-scoped: the sport
// comes from the legs.
//
// Set p.ComboTicker (a Kalshi combo market: its legs are read from Kalshi) OR
// p.Legs (2 to 12 markets, each with a side), never both, plus any of
// CompareSgp's Books, DeadlineMs, FanDuel, Fair and Fresh. p.Venue is "kalshi"
// (empty sends "kalshi"). Each Kalshi leg is translated to the leg a sportsbook
// prices (Combo.Legs[].Translated), the game is matched to OUR event id
// (EventID), and the translated legs are priced exactly as CompareSgp prices
// them: Compare is its 200 body, with the same book statuses, quote cache, build
// counting and fair price. It never returns a Kalshi price for the combo:
// VenuePrice is the combo market's own quote on Kalshi, for context only (its
// prices are nil without a resting quote, and it is nil itself when Legs were
// sent). The priced quotes join your GetSgpHistory with Via "compare".
//
// A game winner YES is that team's moneyline, a spread YES the team at minus the
// line (NO: the other team at plus it), a total or team total YES the over (NO
// the under), and a player "N+" market YES the over N - 0.5 (NO the under); a
// line market must be greater-than at a half point. A NO on a game winner, other
// periods, soccer and non-sports markets are not translated.
//
// A 422 whose body carries a code and an error is RETURNED, not an error:
// Success is false and Code says why. COMBO_NOT_A_COMBO: the market does not list 2 to 12 legs.
// COMBO_LEGS_UNSUPPORTED: a leg has no sportsbook equivalent (Combo.Legs[].Why
// says why). COMBO_MULTI_GAME: the legs span games (Games groups them by index
// into Combo.Legs: send one game's legs). COMBO_EVENT_NOT_FOUND: the game is not
// on our pregame board, or cannot be told from another game of the pair that
// day. A code added later is returned the same way. The API sends
// COMBO_NOT_A_COMBO without success; the SDK sets it to false. A 422 that is not
// JSON, lacks a string code or error, or claims success is an [*APIError].
//
// Errors are [*APIError] values with these codes: a 400 COMBO_INVALID (a
// malformed body: Message says which field); a 404 COMBO_NOT_FOUND (Kalshi has no
// market p.ComboTicker; it matches [ErrNotFound]; with p.Legs, a leg market Kalshi
// does not have is a returned COMBO_LEGS_UNSUPPORTED instead); a 503 COMBO_VENUE_UNAVAILABLE (Kalshi
// could not be read just then: wait RetryAfter); a 403 SGP_TIER_REQUIRED (the plan
// is below MVP, refused before anything is read from Kalshi); a 503
// SGP_FEATURE_DISABLED (combos, compare or, with p.Fair, the fair price are not
// switched on yet; Details names the feature; RetryAfter 60 s); a 429 as on
// CompareSgp (SGP_RATE_LIMIT, SGP_COMPARE_TOO_MANY_BOOKS, or the plan's limits);
// a 503 SGP_RATE_LIMIT_UNAVAILABLE (2 s) or a code-less 503 (SERVICE_BUSY): retry
// later.
//
// Kalshi is read before the books are priced, so each attempt waits at least 45 s
// whatever a shorter client timeout ([WithTimeout]) says; a longer one, or none,
// is kept. A deadline on ctx still applies. It is never retried: every attempt is
// a real pricing request.
func (c *Client) CompareSgpCombo(ctx context.Context, p SgpComboCompareParams) (*SgpComboCompareResponse, error) {
	if p.Venue == "" {
		p.Venue = "kalshi"
	}
	timeout := c.timeout
	if timeout > 0 && timeout < sgpComboMinTimeout {
		timeout = sgpComboMinTimeout
	}
	status, header, body, err := c.roundTripWithin(ctx, timeout, http.MethodPost, "/api/v1/sgp/combo-compare", nil, p)
	if err != nil {
		return nil, err
	}
	switch {
	case status >= 200 && status < 300:
		out := new(SgpComboCompareResponse)
		if err := decodeBody(body, out); err != nil {
			return nil, err
		}
		return out, nil
	case status == http.StatusUnprocessableEntity:
		var envelope struct {
			Success *bool   `json:"success"`
			Code    *string `json:"code"`
			Error   *string `json:"error"`
		}
		if json.Unmarshal(body, &envelope) == nil && (envelope.Success == nil || !*envelope.Success) && envelope.Code != nil && envelope.Error != nil {
			out := new(SgpComboCompareResponse)
			if err := decodeBody(body, out); err == nil {
				refused := false
				out.Success = &refused
				return out, nil
			}
		}
	}
	return nil, newAPIError(status, header, body)
}

// BatchSgp prices several same game parlays in one call (MVP and Hall of Fame).
//
//   - Mode "book": each parlay on the one p.Book (at most 10 parlays). Each result
//     carries Book, BookStatus and, when the book answered, Result: exactly the
//     body BuildSgp returns for it.
//   - Mode "compare": each parlay on every book, or on p.Books (at most 4
//     parlays). Each result carries Best and Books: exactly what CompareSgp
//     returns for it.
//
// Every parlay names OUR event id for its game, as on CompareSgp. Parlays are
// priced one after the other, in request order (in mode "compare" the books price
// each parlay side by side), and results come back in that order with your Ref. A
// result's Status is "done", or "not-started" when that parlay's pricing would have
// begun more than about 20 seconds after the call began: it was not priced (except
// by any book answered from a cached quote) and carries no fair block, so send it
// again. BookStatus and Books[].Status take the values a CompareSgp book's Status
// takes. A repeated parlay (same EventID, same legs in the same order) is priced
// once and reported for each copy.
//
// Every quote not answered from a cached quote counts one build against the key's
// per-minute SGP build limit (one per parlay in mode "book", one per book and
// parlay in mode "compare"), reserved for the whole batch up front. A quote that
// never reaches the book (not listed, a different game, a leg that does not
// resolve, no time left, a parlay not started) is given back at once, while the
// minute it was counted in is still running.
//
// A batch can rightly take close to 40 seconds, so each attempt waits at least
// 45 s whatever a shorter client timeout ([WithTimeout]) says; a longer one, or
// none, is kept. A deadline on ctx still applies.
//
// Errors are [*APIError] values with these codes: a 403 SGP_TIER_REQUIRED (the plan
// is below MVP); a 503 SGP_FEATURE_DISABLED (batches, or with p.Fair the fair
// price, are not switched on yet; RetryAfter 60 s); a 429 SGP_RATE_LIMIT (the
// builds this batch needs exceed what is left of the per-minute limit: wait
// RetryAfter; Details carries unitsNeeded); a 429 SGP_BATCH_TOO_LARGE (the batch
// needs more builds than the limit allows at all, sent without Retry-After, so
// waiting never helps: send fewer parlays or books; Details carries unitsNeeded
// and limit). Mode "book" checks p.Book before pricing, as BuildSgp does: a 400
// SGP_BOOK_UNSUPPORTED (Details lists supportedBooks) or SGP_BOOK_SPORT_UNSUPPORTED
// (Details lists the sports that book prices), and a 503 SGP_BOOK_DISABLED when
// that book's pricing is paused (60 s). A 503 SGP_RATE_LIMIT_UNAVAILABLE (2 s) or a
// code-less 503 (SERVICE_BUSY): retry later. A 400 for a malformed body (Message
// says which field).
//
// It is never retried: every attempt is a real pricing request.
func (c *Client) BatchSgp(ctx context.Context, sport string, p SgpBatchParams) (*SgpBatchResponse, error) {
	timeout := c.timeout
	if timeout > 0 && timeout < sgpBatchMinTimeout {
		timeout = sgpBatchMinTimeout
	}
	return sendIntoWithin[SgpBatchResponse](ctx, c, timeout, http.MethodPost, "/api/v1/"+esc(sport)+"/sgp/batch", p)
}

// GetSgpHistory returns the same game parlay prices YOUR account was quoted for
// one game, newest first: the legs, the book's price and each leg's own price,
// from BuildSgp, CompareSgp, CompareSgpCombo and BatchSgp (MVP and Hall of Fame).
//
// Name the game by OUR event id (p.EventID, as GetOdds gives it), or by a book
// and that book's own event id (p.Book and p.BookEventID, as GetSgpEvents lists
// it), never both. p.LegsHash narrows it to one parlay: every priced answer
// carries it as Meta.LegsHash (on CompareSgp and BatchSgp also beside the
// results), and the same parlay sent the same way always has the same hash.
// p.From and p.To (ISO 8601) set the window, at most 7 days, by default the last
// 24 hours (p.From alone: the 24 hours from it, up to now, so set p.To too for a
// longer window); p.Limit is 1 to 500, default 100. Meta echoes the query as the API
// applied it.
//
// Only the calling account's own quotes are returned, and only priced answers are
// kept: a refusal, a no-price or a busy answer is not, and a quote served from
// the short quote cache is not a new quote. The history holds quotes from the day
// recording was switched on (nothing earlier is filled in), for 90 days. QuotedAt
// is when the book priced the parlay (Meta.PricedAt on the answer). Fair is our
// estimate only on a quote from BuildSgp with Fair set whose answer carried a fair
// price (Fair.Price not nil); otherwise it is nil (CompareSgp, CompareSgpCombo
// and BatchSgp record each book's quote without it, whatever their Fair). Via is
// "build", "compare" (CompareSgpCombo's quotes too) or "batch"; more can be added.
//
// Errors are [*APIError] values: a 403 SGP_TIER_REQUIRED (the plan is below
// MVP); a 503 SGP_FEATURE_DISABLED (the history is not switched on yet;
// RetryAfter 60 s); a 503 SGP_HISTORY_UNAVAILABLE (the history could not be read
// just then; RetryAfter 5 s); a 429 (the plan rate limit, or the concurrent
// request limit); a 400 for a parameter the API cannot read, for example the
// game named both ways or not at all, a window over 7 days, a Limit outside 1 to
// 500 or a malformed LegsHash (Message says which).
//
// It is a read, so with [WithRetry] its 429s and 503s are retried like any other
// GET.
func (c *Client) GetSgpHistory(ctx context.Context, sport string, p SgpHistoryParams) (*SgpHistoryResponse, error) {
	return getInto[SgpHistoryResponse](ctx, c, "/api/v1/"+esc(sport)+"/sgp/history", p.query())
}
