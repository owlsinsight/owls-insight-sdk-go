package owls

import (
	"net/url"
	"strconv"
	"strings"
)

// Query helpers: a zero value is not sent.

func setStr(q url.Values, key, v string) {
	if v != "" {
		q.Set(key, v)
	}
}

func setList(q url.Values, key string, v []string) {
	if len(v) > 0 {
		q.Set(key, strings.Join(v, ","))
	}
}

func setInt(q url.Values, key string, v int) {
	if v != 0 {
		q.Set(key, strconv.Itoa(v))
	}
}

func setInt64(q url.Values, key string, v int64) {
	if v != 0 {
		q.Set(key, strconv.FormatInt(v, 10))
	}
}

func setFloat(q url.Values, key string, v float64) {
	if v != 0 {
		q.Set(key, strconv.FormatFloat(v, 'f', -1, 64))
	}
}

func setTrue(q url.Values, key string, v bool) {
	if v {
		q.Set(key, "true")
	}
}

// OddsParams filters GetOdds, GetMoneyline, GetSpreads and GetTotals.
type OddsParams struct {
	// Books narrows the board to these books.
	Books []string
	// Market narrows the board to one market. Only GetOdds reads it; the
	// per-market routes report a differing value in meta.ignored_params.
	Market string
	// ExcludeExchanges drops kalshi, polymarket and novig.
	ExcludeExchanges bool
	// Alternates includes alternate lines.
	Alternates bool
	League     string
}

func (p *OddsParams) query() url.Values {
	q := url.Values{}
	if p == nil {
		return q
	}
	setList(q, "books", p.Books)
	setStr(q, "market", p.Market)
	setTrue(q, "exclude_exchanges", p.ExcludeExchanges)
	setTrue(q, "alternates", p.Alternates)
	setStr(q, "league", p.League)
	return q
}

// EVParams filters GetEV.
type EVParams struct {
	// MinEV is the least EV% to include (0 means every positive EV).
	MinEV float64
	// Book restricts the result to one sportsbook.
	Book string
}

// PropsParams filters GetProps and GetBookProps (Books is read by GetProps only).
type PropsParams struct {
	GameID   string
	Player   string
	Category string
	Books    []string
}

func (p *PropsParams) query(withBooks bool) url.Values {
	q := url.Values{}
	if p == nil {
		return q
	}
	setStr(q, "game_id", p.GameID)
	setStr(q, "player", p.Player)
	setStr(q, "category", p.Category)
	if withBooks {
		setList(q, "books", p.Books)
	}
	return q
}

// PropsHistoryParams selects the line history of one prop (GetPropsHistory).
type PropsHistoryParams struct {
	GameID   string // required
	Player   string // required
	Category string // required
	Book     string
	Hours    int
}

// PropResultsParams selects GetPropResults: one game (GameID) or, with only
// Date (YYYY-MM-DD), the graded games of a day.
type PropResultsParams struct {
	GameID        string
	Date          string
	Player        string
	Category      string
	ConfirmedOnly bool
}

// PropTrendsParams selects GetPropTrends.
type PropTrendsParams struct {
	Player   string // required
	Category string // required
	// Line is a threshold such as "0.5", or "closing" to grade each game against
	// the line Book had captured at its start. Without it no hit rate is computed.
	Line string
	// Book is required with Line "closing".
	Book string
	// Window is the number of games, 1-50 (default 10).
	Window        int
	ConfirmedOnly bool
}

// PlayerAveragesParams selects GetPlayerAverages.
type PlayerAveragesParams struct {
	PlayerName string // required
	Opponent   string
}

// OddsHistoryParams selects GetOddsHistory: the line movement of one book, market
// and side of a live or recent event.
type OddsHistoryParams struct {
	EventID string // required
	Book    string // required
	Market  string // required
	Side    string // required
	// StartTime is a Unix time in milliseconds to start the window at.
	StartTime int64
	// Hours looks back this many hours (1 to 168); ignored when StartTime is set.
	Hours int
}

// LineHistoryParams selects GetMoneylineHistory, GetSpreadHistory and
// GetTotalsHistory: OddsHistoryParams without the market, which each method sets.
type LineHistoryParams struct {
	EventID string // required
	Book    string // required
	Side    string // required: "home", "away" ("draw" for moneyline), or "over" or "under" for totals
	// StartTime is a Unix time in milliseconds to start the window at.
	StartTime int64
	// Hours looks back this many hours (1 to 168); ignored when StartTime is set.
	Hours int
}

func (p LineHistoryParams) withMarket(market string) OddsHistoryParams {
	return OddsHistoryParams{EventID: p.EventID, Book: p.Book, Market: market, Side: p.Side, StartTime: p.StartTime, Hours: p.Hours}
}

// HistoryGamesParams filters GetHistoryGames.
type HistoryGamesParams struct {
	Sport     string
	Season    string
	Team      string
	GameType  string
	StartDate string
	EndDate   string
	Limit     int
	Offset    int
}

// HistoryOddsParams selects the odds snapshots of one archived game
// (GetHistoryOdds, IterHistoryOdds; the pager sets Limit and Offset itself).
type HistoryOddsParams struct {
	EventID   string // required
	Book      string
	Market    string
	Side      string
	StartTime string
	EndTime   string
	Opening   bool
	Limit     int
	Offset    int
}

func (p *HistoryOddsParams) query() url.Values {
	q := url.Values{}
	q.Set("eventId", p.EventID)
	setStr(q, "book", p.Book)
	setStr(q, "market", p.Market)
	setStr(q, "side", p.Side)
	setStr(q, "startTime", p.StartTime)
	setStr(q, "endTime", p.EndTime)
	setTrue(q, "opening", p.Opening)
	return q
}

// HistoryPropsParams selects the prop snapshots of one archived game
// (GetHistoryProps, IterHistoryProps; the pager sets Limit and Offset itself).
type HistoryPropsParams struct {
	EventID    string // required
	PlayerName string
	PropType   string
	Book       string
	StartTime  string
	EndTime    string
	Opening    bool
	Limit      int
	Offset     int
}

func (p *HistoryPropsParams) query() url.Values {
	q := url.Values{}
	q.Set("eventId", p.EventID)
	setStr(q, "playerName", p.PlayerName)
	setStr(q, "propType", p.PropType)
	setStr(q, "book", p.Book)
	setStr(q, "startTime", p.StartTime)
	setStr(q, "endTime", p.EndTime)
	setTrue(q, "opening", p.Opening)
	return q
}

// HistoryStatsParams filters GetHistoryStats.
type HistoryStatsParams struct {
	EventID    string
	PlayerName string
	Sport      string
	Position   string
	StartDate  string
	EndDate    string
	Limit      int
	Offset     int
}

// ClosingOddsParams filters GetClosingOdds. Season does not work for soccer,
// tennis or cs2; narrow those with StartDate.
type ClosingOddsParams struct {
	EventID   string
	Sport     string
	Book      string
	Source    string
	StartDate string
	EndDate   string
	Season    string
	Limit     int
	Offset    int
}

// HistoricalPlayerPropsParams filters GetHistoricalPlayerProps.
type HistoricalPlayerPropsParams struct {
	EventID  string
	Sport    string
	Player   string
	PropType string
	Book     string
	// IsMain filters to main lines (true) or alternate lines (false); nil sends nothing.
	IsMain    *bool
	StartDate string
	EndDate   string
	Limit     int
	Offset    int
}

// PublicBettingParams filters GetPublicBetting.
type PublicBettingParams struct {
	EventID   string
	Sport     string
	StartDate string
	EndDate   string
	Limit     int
	Offset    int
}

// HistorySplitsParams selects GetHistorySplits: one game by EventID, or a sport's
// games by Sport and StartDate over a window of at most 7 days.
type HistorySplitsParams struct {
	// EventID is the game's eventId on GetOdds (the EventID of a SplitsGame).
	EventID string
	// Sport is required without EventID: nfl, ncaaf, mlb, nba, nhl, ncaab or wnba.
	Sport string
	// Book is a book key as on the live splits: dk, circa or betmgm.
	Book string
	// StartDate (YYYY-MM-DD, UTC, on the time each reading was recorded) is
	// required with Sport.
	StartDate string
	// EndDate (YYYY-MM-DD, UTC) is inclusive. With Sport the window is at most 7
	// days and defaults to 7 days from StartDate.
	EndDate string
	// Limit is the rows per page: default 100, at most 500.
	Limit  int
	Offset int
}

// CS2MatchesParams filters GetCS2Matches.
type CS2MatchesParams struct {
	Team      string
	Event     string
	StartDate string
	EndDate   string
	Stars     int
	LAN       bool
	Limit     int
	Offset    int
}

// CS2PlayersParams filters GetCS2Players; give at least PlayerName or Team.
type CS2PlayersParams struct {
	PlayerName string
	Team       string
	Event      string
	StartDate  string
	EndDate    string
	MapName    string
	MinRating  float64
	Limit      int
	Offset     int
}

// ProphetXOddsParams selects GetProphetxOdds.
type ProphetXOddsParams struct {
	// Sports is one or more ProphetX sport slugs (required).
	Sports []string
	// Kind is "game" or "prop".
	Kind string
}

// InjuriesParams filters GetInjuries.
type InjuriesParams struct {
	// Source limits the result to one source by role.
	Source string
	// Status keeps only these designations; it overrides the default view.
	Status []string
	// Team keeps only these teams (names or abbreviations).
	Team []string
	// IncludeActive includes players every source considers available.
	IncludeActive bool
}

// SgpBuildParams is the body of BuildSgp.
type SgpBuildParams struct {
	// EventID is the book's event id, as a string.
	EventID string `json:"eventId"`
	// Book is "fanduel", "draftkings" or "hardrock".
	Book string `json:"book"`
	// Legs holds 2 to 12 legs.
	Legs []SgpLeg `json:"legs"`
}

// SgpLeg is one leg of an SGP build. Type is "moneyline" (Selection), "spread"
// (Selection, Point), "total" (Selection "over" or "under", Point) or
// "player_prop" (Player, Category, Side, Line).
type SgpLeg struct {
	Type      string   `json:"type"`
	Selection string   `json:"selection,omitempty"`
	Point     *float64 `json:"point,omitempty"`
	Player    string   `json:"player,omitempty"`
	Category  string   `json:"category,omitempty"`
	Side      string   `json:"side,omitempty"`
	Line      *float64 `json:"line,omitempty"`
}
