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
	// ExcludeExchanges leaves the prediction-market exchanges (kalshi, polymarket
	// and novig) out of the board, keeping only sportsbooks. It is sent as
	// exclude_exchanges=true only when set, and Meta.ExcludeExchanges echoes it
	// (Meta.ExchangeBooks lists the exchanges the board can carry). If it removes
	// every book named in Books, the API answers 400.
	ExcludeExchanges bool
	// Alternates includes alternate lines.
	Alternates bool
	League     string
	// State is a two-letter FanDuel state code (for example "co") whose marketIds
	// to serve on the FanDuel outcomes. FanDuel issues a different marketId in every
	// state while the selectionId is the same everywhere, so a bet-slip link is
	// built with the state's marketId on that state's FanDuel site. FanDuel deepLink
	// fields are left out of a state response (they point at the New Jersey site),
	// and an outcome with no id for the state has its marketId omitted.
	// Meta.FanduelMarketIds reports what was mapped; Available false means the sport
	// has no ids for that state and every FanDuel marketId is omitted (the other
	// books are served as usual). A value that is not two letters is named in
	// meta.ignored_params. Default (empty): New Jersey, the feed's own ids. REST
	// only: odds-update carries the New Jersey ids.
	State string
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
	setStr(q, "state", p.State)
	return q
}

// EVParams filters GetEV.
type EVParams struct {
	// MinEV is the least EV% to include (0 means every positive EV).
	MinEV float64
	// Book restricts the result to one sportsbook key, or to kalshi or polymarket
	// when Venues is set.
	Book string
	// Venues also lists Kalshi and Polymarket, their trading fee included in the EV
	// (sportsbooks and Novig are always listed). It is sent as venues=true only when
	// set. The first request for a sport starts their prices, which appear within
	// about 15 seconds and stay on while you keep asking.
	Venues bool
}

// EVLinesParams filters GetEVLines.
type EVLinesParams struct {
	// Markets is "spreads", "totals" or both, sent as one comma list under
	// market. Nil or empty asks for both. Any other value is refused before a
	// request (see GetEVLines).
	Markets []string
	// MinEV is the least EV% to include (0 means every positive EV; the API caps
	// it at meta.max_ev).
	MinEV float64
	// Book restricts the result to one sportsbook key.
	Book string
}

// PropsEVParams filters GetPropsEV.
type PropsEVParams struct {
	// MinEV is the least EV% of an opportunity (0 means every positive EV; at
	// most meta.max_ev).
	MinEV float64
	// Book restricts the opportunities to one sportsbook key.
	Book string
	// Player is a partial match on the player name, as on GetProps.
	Player string
	// Category is one prop category (the GetProps category names and aliases).
	Category string
	// All also lists the props with no positive-EV price, each with its fair
	// line. It is sent as all=true only when set.
	All bool
}

// DayScheduleParams selects GetDaySchedule. Every field is optional: the zero
// value asks for today's games of every sport in America/New_York.
type DayScheduleParams struct {
	// Date is the first calendar day, YYYY-MM-DD in TZ. Default: today in TZ.
	Date string
	// TZ is the IANA time zone that defines the day, such as America/Los_Angeles.
	// Default: America/New_York. An unknown zone is a 400.
	TZ string
	// Days is the number of days from Date, 1 to 10. Default: 1.
	Days int
	// Sports narrows the schedule to these sports: nfl, ncaaf, mlb, nba, wnba,
	// nhl, tennis, soccer, ufc. Default: all. An unknown sport is a 400 that lists
	// the supported ones.
	Sports []string
}

// PropsParams filters GetProps and GetBookProps (Books is read by GetProps only).
type PropsParams struct {
	GameID   string
	Player   string
	Category string
	Books    []string
	// State is a two-letter FanDuel state code (for example "ks") whose marketIds
	// to serve on the FanDuel props and alternate-line rungs, on GetProps and
	// GetBookProps(ctx, sport, "fanduel", ...). FanDuel issues a different marketId
	// in every state while the selectionId is the same everywhere, so a bet-slip
	// link built with another state's marketId fails. A market with no id for the
	// state has its marketId omitted rather than served as New Jersey's;
	// Meta.FanduelMarketIds (GetProps) or Meta.MarketIds (GetBookProps for fanduel)
	// reports what was mapped. A state the API does not serve is a 404, a code that
	// is not two letters a 400. Default (empty): New Jersey, the feed's own ids. REST
	// only: the props WebSocket events carry the New Jersey ids.
	State string
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
	setStr(q, "state", p.State)
	return q
}

// PlaysParams narrows GetPlays for one game. Without an event id GetPlays refuses
// a Since or Limit.
type PlaysParams struct {
	// Since returns the plays whose Seq is greater than this, plus every play
	// amended in the last 2 minutes whatever its Seq: pass the LastSeq of the
	// previous response (Since: res.Game.LastSeq). Nil sends nothing (every play);
	// 0 is sent. The API takes a non-negative integer and answers 400 to anything
	// else.
	Since *float64
	// Limit returns only the last Limit plays (1 to 5000), after Since. 0 sends
	// nothing.
	Limit int
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
	// EventID is the book's event id from GetSgpEvents, as a string. SgpEvent.EventID
	// is raw JSON: a number on FanDuel (send its digits) and a JSON string on every
	// other book, such as BetMGM NFL's "6:43680" (send it unquoted).
	EventID string `json:"eventId"`
	// Book is "fanduel", "draftkings", "hardrock", "betrivers", "ballybet" or
	// "betmgm". Any other value is a 400 with code SGP_BOOK_UNSUPPORTED.
	Book string `json:"book"`
	// Legs holds 2 to 12 legs.
	Legs []SgpLeg `json:"legs"`
	// Fair adds our fair-price estimate as Fair on a priced 200 (SgpFairBlock).
	// MVP and Hall of Fame plans: on the Rookie plan the build is a 403 with Code
	// SGP_TIER_REQUIRED, refused before anything is priced and not counted against
	// any limit (below Rookie the builder itself is a 403 FORBIDDEN); a 503
	// SGP_FEATURE_DISABLED means the fair price is not switched on yet.
	Fair bool `json:"fair,omitempty"`
}

// SgpLeg is one leg of a same game parlay. Type is one of:
//
//   - "moneyline": Selection, the team. With a Period, the period's 3-way
//     result: a team or "Tie".
//   - "spread": Selection and Point.
//   - "total": Selection "over" or "under", and Point.
//   - "team_total": one team's own full-game total (Hard Rock NHL): Team,
//     Selection "over" or "under", and Point, a line Hard Rock lists for that team.
//   - "player_prop": Player, Category, Side and Line.
//
// Period is "P1", "P2" or "P3" on an NHL moneyline, spread or total leg, and
// prices that period instead of the full game. Only Hard Rock prices period and
// team_total legs. Every other book refuses them rather than pricing the full
// game: BuildSgp answers a 422 (ReasonCode "unresolved", the leg's reason says
// why), and CompareSgp and BatchSgp report that book with status "unsupported".
type SgpLeg struct {
	Type      string   `json:"type"`
	Team      string   `json:"team,omitempty"`
	Selection string   `json:"selection,omitempty"`
	Point     *float64 `json:"point,omitempty"`
	Period    string   `json:"period,omitempty"`
	Player    string   `json:"player,omitempty"`
	Category  string   `json:"category,omitempty"`
	Side      string   `json:"side,omitempty"`
	Line      *float64 `json:"line,omitempty"`
}

// SgpCompareParams is the body of CompareSgp.
type SgpCompareParams struct {
	// EventID is OUR event id for the game, as GetOdds gives it:
	// {sport}:{Away}@{Home}-{YYYYMMDD}, with -G2 for the second game of a
	// doubleheader. Not a book's id from GetSgpEvents.
	EventID string `json:"eventId"`
	// Legs holds the same legs BuildSgp takes, 1 to 12, sent to every book.
	Legs []SgpLeg `json:"legs"`
	// Books are the books to ask ("fanduel", "draftkings", "hardrock",
	// "betrivers", "ballybet", "betmgm"). Empty means every book.
	Books []string `json:"books,omitempty"`
	// DeadlineMs is how long to wait for the books, 1000 to 15000 ms. 0 sends
	// nothing: the server's default is 6000. Keep the client's timeout above it.
	DeadlineMs int `json:"deadlineMs,omitempty"`
	// FanDuel is "cold" (the server's default: FanDuel prices in a fresh browser
	// session and is given at least 12000 ms) or "skip" (FanDuel is left out,
	// status "skipped").
	FanDuel string `json:"fanduel,omitempty"`
	// Fair adds our fair-price estimate as Fair, built from every book that
	// priced (SgpFairBlock).
	Fair bool `json:"fair,omitempty"`
	// Fresh never uses a cached quote: every book is priced now.
	Fresh bool `json:"fresh,omitempty"`
}

// SgpComboLegInput is one leg of a Kalshi combo, named by its own market in
// SgpComboCompareParams.Legs.
type SgpComboLegInput struct {
	// MarketTicker is the leg's Kalshi market ticker, for example
	// "KXNHLGAME-26OCT28PHIWSH-WSH" (lower case is accepted).
	MarketTicker string `json:"marketTicker"`
	// Side is the side of that market the combo takes: "yes" or "no".
	Side string `json:"side"`
}

// SgpComboCompareParams is the body of CompareSgpCombo. Set ComboTicker OR Legs,
// never both (the API answers a 400 COMBO_INVALID otherwise).
type SgpComboCompareParams struct {
	// Venue is the prediction market the combo is on: "kalshi", the only one.
	// Empty sends "kalshi".
	Venue string `json:"venue"`
	// ComboTicker is a Kalshi combo market's ticker (a multivariate market, for
	// example "KXMVECROSSCATEGORY-S2026ZJDATBFMVOSS4HNQ-QRB4CF6A6"): its legs are
	// read from Kalshi. Lower case is accepted.
	ComboTicker string `json:"comboTicker,omitempty"`
	// Legs names the combo's legs one by one: 2 to 12, each a different market.
	Legs []SgpComboLegInput `json:"legs,omitempty"`
	// Books, DeadlineMs, FanDuel, Fair and Fresh are SgpCompareParams': the books
	// to ask (empty: every book), how long to wait for them (1000 to 15000 ms; 0
	// sends nothing, the server's default is 6000), "cold" or "skip" for FanDuel,
	// our fair-price estimate as Compare.Fair, and never a cached quote.
	Books      []string `json:"books,omitempty"`
	DeadlineMs int      `json:"deadlineMs,omitempty"`
	FanDuel    string   `json:"fanduel,omitempty"`
	Fair       bool     `json:"fair,omitempty"`
	Fresh      bool     `json:"fresh,omitempty"`
}

// SgpBatchParlay is one parlay of a BatchSgp call.
type SgpBatchParlay struct {
	// Ref is your label for the parlay (up to 100 characters), returned with its
	// result.
	Ref string `json:"ref,omitempty"`
	// EventID is OUR event id for the game, as GetOdds gives it (as on CompareSgp).
	EventID string `json:"eventId"`
	// Legs holds the legs BuildSgp takes, 1 to 12.
	Legs []SgpLeg `json:"legs"`
}

// SgpBatchParams is the body of BatchSgp.
type SgpBatchParams struct {
	// Mode is "book" (every parlay on the one Book) or "compare" (every parlay on
	// every book, or on Books, as CompareSgp prices one).
	Mode string `json:"mode"`
	// Book is the one book every parlay is priced on; required in mode "book".
	Book string `json:"book,omitempty"`
	// Books are the books to ask in mode "compare". Empty means every book.
	Books []string `json:"books,omitempty"`
	// Parlays holds at most 10 parlays in mode "book", 4 in mode "compare". A
	// parlay repeated in one batch (same EventID, same legs in the same order) is
	// priced once and reported for each copy.
	Parlays []SgpBatchParlay `json:"parlays"`
	// DeadlineMs is how long each parlay waits for its books, 1000 to 15000 ms.
	// 0 sends nothing: the server's default is 6000.
	DeadlineMs int `json:"deadlineMs,omitempty"`
	// FanDuel is "cold" (the server's default) or "skip", as on CompareSgp.
	FanDuel string `json:"fanduel,omitempty"`
	// Fair adds our fair-price estimate to each parlay's result (SgpFairBlock).
	Fair bool `json:"fair,omitempty"`
	// Fresh never uses a cached quote.
	Fresh bool `json:"fresh,omitempty"`
}

// SgpHistoryParams selects GetSgpHistory. Name the game one way, never both: by
// OUR event id (EventID), or by a book and that book's own event id (Book and
// BookEventID). The API checks the fields and answers a 400 (Message says which),
// for example for the game named both ways or not at all, a window over 7 days, a
// Limit outside 1 to 500 or a malformed LegsHash.
type SgpHistoryParams struct {
	// EventID is OUR event id for the game, as GetOdds gives it
	// ({sport}:{Away}@{Home}-{YYYYMMDD}).
	EventID string
	// Book, with BookEventID, names the game by one book's own event id
	// ("fanduel", "draftkings", "hardrock", "betrivers", "ballybet", "betmgm"):
	// that book's quotes for it.
	Book string
	// BookEventID is the book's own event id, as GetSgpEvents lists it and BuildSgp
	// takes it (SgpBuildParams.EventID): FanDuel's numeric id as its digits, every
	// other book's id unquoted. SgpEvent.EventID and SgpBuildMeta.EventID are raw
	// JSON: take a string id with json.Unmarshal into a string and FanDuel's number
	// as string(raw), or reuse the EventID you sent to BuildSgp. string(raw) of a
	// string id keeps its quotes, and a number decoded into any prints as 3.5989831e+07:
	// either matches no quote, so the history comes back empty rather than refused.
	BookEventID string
	// LegsHash narrows the history to one parlay: the 16 hex characters of
	// Meta.LegsHash on a priced BuildSgp answer (or on CompareSgp and BatchSgp).
	LegsHash string
	// From is the ISO 8601 start of the window (inclusive). Empty: 24 hours
	// before To.
	From string
	// To is the ISO 8601 end of the window (exclusive). Empty: now (or 24 hours
	// after From, if that is earlier). The window is at most 7 days.
	To string
	// Limit is at most this many quotes, 1 to 500. 0 sends nothing: the server's
	// default is 100.
	Limit int
}

func (p SgpHistoryParams) query() url.Values {
	q := url.Values{}
	setStr(q, "eventId", p.EventID)
	setStr(q, "book", p.Book)
	setStr(q, "bookEventId", p.BookEventID)
	setStr(q, "legsHash", p.LegsHash)
	setStr(q, "from", p.From)
	setStr(q, "to", p.To)
	setInt(q, "limit", p.Limit)
	return q
}
