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

// GetBookProps returns one book's player props for a sport. p.Books is not sent.
// The bet365 props were retired: that book answers 410 Gone, an [*APIError]
// (Bet365 game lines are on GetV2).
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

// GetSgpEvents lists the events a book can price parlays for (book defaults to
// fanduel on the server).
func (c *Client) GetSgpEvents(ctx context.Context, sport, book string) (*SgpEventsResponse, error) {
	q := url.Values{}
	setStr(q, "book", book)
	return getInto[SgpEventsResponse](ctx, c, "/api/v1/"+esc(sport)+"/sgp/events", q)
}

// BuildSgp prices a same game parlay. It is never retried: every attempt is a
// real pricing request.
//
// A 422 whose body has success false and a legs array is RETURNED, not an error:
// branch on ReasonCode ("unresolved" means a leg did not resolve and legs[i].reason
// says why; any other code means the book refused the slip). Every other 422 is an
// [*APIError]. A 503 with Code [SgpNoPriceCode] means no usable price came back;
// its RetryAfter is the server's, or 2 s when the header is missing.
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
