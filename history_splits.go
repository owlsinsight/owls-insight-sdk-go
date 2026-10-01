package owls

// The models of GetHistorySplits are written by hand: spec/openapi.json does not
// describe /api/v1/history/splits yet. They follow the generated conventions
// (every field optional, scalars as pointers, fields in JSON-name order). Once the
// vendored spec carries these schemas and `go generate ./...` produces them, delete
// this file; TestResponseTypesMatchTheSpec then checks GetHistorySplits against
// the generated types.

// SplitsHistoryRow One recorded reading of one book's betting splits for a game. It has
// the shape of a SplitsBookEntry on the live board (GetSplits), with RecordedAt
// where the live board has AsOf, plus the game's EventID and Sport.
//
// A market absent from a row was not quoted at that read, and a percentage absent
// from a market is not zero: BetMGM publishes ticket percentages only, so its rows
// carry the *BetsPct fields and no *HandlePct fields.
type SplitsHistoryRow struct {
	// Book Book key as on the live board: 'dk' (DraftKings), 'circa' (Circa Sports) or
	// 'betmgm' (BetMGM, tickets only).
	Book *string `json:"book,omitempty"`

	// EventID Our event id: the OddsEvent.eventId of the game on `/api/v1/{sport}/odds`
	// and the SplitsGame.event_id on `/api/v1/{sport}/splits`.
	EventID   *string `json:"event_id,omitempty"`
	Moneyline *struct {
		AwayBetsPct   *float64 `json:"away_bets_pct,omitempty"`
		AwayHandlePct *float64 `json:"away_handle_pct,omitempty"`
		AwayPrice     *float64 `json:"away_price,omitempty"`
		HomeBetsPct   *float64 `json:"home_bets_pct,omitempty"`
		HomeHandlePct *float64 `json:"home_handle_pct,omitempty"`
		HomePrice     *float64 `json:"home_price,omitempty"`
	} `json:"moneyline,omitempty"`

	// RecordedAt ISO 8601 time this reading was recorded. A row is written when the
	// book's figures (percentages, line or price) change, and again after 24 hours
	// without a change, so consecutive rows for a game and book can carry the same
	// figures.
	RecordedAt *string `json:"recorded_at,omitempty"`
	Sport      *string `json:"sport,omitempty"`
	Spread     *struct {
		AwayBetsPct   *float64 `json:"away_bets_pct,omitempty"`
		AwayHandlePct *float64 `json:"away_handle_pct,omitempty"`
		AwayLine      *float64 `json:"away_line,omitempty"`
		HomeBetsPct   *float64 `json:"home_bets_pct,omitempty"`
		HomeHandlePct *float64 `json:"home_handle_pct,omitempty"`
		HomeLine      *float64 `json:"home_line,omitempty"`
	} `json:"spread,omitempty"`

	// Title Display name: 'DraftKings', 'Circa Sports' or 'BetMGM'.
	Title *string `json:"title,omitempty"`
	Total *struct {
		Line           *float64 `json:"line,omitempty"`
		OverBetsPct    *float64 `json:"over_bets_pct,omitempty"`
		OverHandlePct  *float64 `json:"over_handle_pct,omitempty"`
		UnderBetsPct   *float64 `json:"under_bets_pct,omitempty"`
		UnderHandlePct *float64 `json:"under_handle_pct,omitempty"`
	} `json:"total,omitempty"`
}

// HistorySplitsResponse Response from GET /api/v1/history/splits.
type HistorySplitsResponse struct {
	Data *struct {
		// EventID The eventId the request named. Absent when it named none.
		EventID    *string `json:"eventId,omitempty"`
		Pagination *struct {
			HasMore *bool    `json:"hasMore,omitempty"`
			Limit   *float64 `json:"limit,omitempty"`
			Offset  *float64 `json:"offset,omitempty"`
			Total   *float64 `json:"total,omitempty"`
		} `json:"pagination,omitempty"`

		// Splits The readings, oldest first.
		Splits []SplitsHistoryRow `json:"splits,omitempty"`

		// Sport The sport the request named. Absent when it named none.
		Sport *string `json:"sport,omitempty"`

		// Window Only on a query by sport (no eventId): the UTC dates served, both
		// inclusive. End is the endDate sent, or 6 days after Start when none was.
		Window *struct {
			End   *string `json:"end,omitempty"`
			Start *string `json:"start,omitempty"`
		} `json:"window,omitempty"`
	} `json:"data,omitempty"`
	Success *bool `json:"success,omitempty"`
}
