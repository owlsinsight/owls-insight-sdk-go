package owls

// The models of GetDaySchedule are written by hand: spec/openapi.json does not
// describe /api/v1/schedule yet. Below is, verbatim, what `go generate ./...` (the
// pinned oapi-codegen v2.8.0, then doctidy) produces from the DaySchedule* schemas
// of the JS SDK's spec for this endpoint, the names Python uses too. Once the
// vendored spec carries those schemas, delete this file and nothing else;
// TestResponseTypesMatchTheSpec then checks GetDaySchedule against the generated
// types.

// DayScheduleBuildWindow The days the schedule is built for: three UTC days back to nine ahead.
type DayScheduleBuildWindow struct {
	From *string `json:"from,omitempty"`
	To   *string `json:"to,omitempty"`
}

// DayScheduleCompetitor One side of a game. Team sports list away then home; tennis and UFC list the
// first then the second competitor, with `side` null.
type DayScheduleCompetitor struct {
	Code *string `json:"code,omitempty"`
	Name *string `json:"name,omitempty"`

	// Score Null before the game has a score.
	Score *float64 `json:"score,omitempty"`

	// Sets Tennis only: games won in each set, in set order.
	Sets  []float64 `json:"sets,omitempty"`
	Short *string   `json:"short,omitempty"`
	Side  *string   `json:"side,omitempty"`

	// WinPrice Kalshi price to buy this side to win, 0 to 1, so the two sides can add to a
	// little over 1. Null once the market closes, and null when the market is too thin
	// to price.
	WinPrice *float64 `json:"winPrice,omitempty"`

	// Winner Set once the game is final; null before then, and on a level final score.
	Winner *bool `json:"winner,omitempty"`
}

// DayScheduleGame One game on the day schedule.
type DayScheduleGame struct {
	// Competition Tennis tournament or UFC event name; null elsewhere.
	Competition *string `json:"competition,omitempty"`

	// Competitors Always two entries; see DayScheduleCompetitor for the order.
	Competitors []DayScheduleCompetitor `json:"competitors,omitempty"`

	// DrawPrice Soccer only: Kalshi price for the draw.
	DrawPrice *float64 `json:"drawPrice,omitempty"`

	// EventID The game's eventId on /api/v1/{sport}/odds when it was found there, else null.
	EventID *string `json:"eventId,omitempty"`

	// ID Stable for the life of the game, even when it is rescheduled.
	ID                *string `json:"id,omitempty"`
	KalshiEventTicker *string `json:"kalshiEventTicker,omitempty"`
	League            *string `json:"league,omitempty"`

	// LiveUpdatedAt When this game's live data (status, score, clock) was last read; null before it has any.
	LiveUpdatedAt *string `json:"liveUpdatedAt,omitempty"`

	// Round Tennis round, playoff "Game N", or UFC weight class; null when none.
	Round *string `json:"round,omitempty"`
	Sport *string `json:"sport,omitempty"`

	// StartCheck The game's start time compared with independent sources.
	StartCheck *DayScheduleStartCheck `json:"startCheck,omitempty"`

	// StartTime Scheduled start, ISO 8601 UTC.
	StartTime *string `json:"startTime,omitempty"`

	// Status A game's state on the day schedule. Open: a new value must not break a reader.
	// - `unconfirmed`: the scheduled start passed long ago and no live data confirms the
	//   game started or ended, or live data for a game in progress stopped updating for
	//   more than 10 minutes.
	// - `time_tbd`: the start time is not set yet.
	Status *DayScheduleStatus `json:"status,omitempty"`

	// StatusDetail Clock or result detail: "Q2 8:12", "Top 5th", "45'", "Final/OT", "KO/TKO R1 3:58".
	StatusDetail *string `json:"statusDetail,omitempty"`
	UpdatedAt    *string `json:"updatedAt,omitempty"`

	// Volume Kalshi volume across the game's winner markets, in contracts. Games starting together are ordered by it.
	Volume *float64 `json:"volume,omitempty"`
}

// DayScheduleMeta Freshness of a day-schedule answer. `status`, first match wins:
//   - `no-data`: nothing built for the range.
//   - `stale`: built more than 3 minutes ago, the schedule not refreshed for 5
//     minutes, or live scores more than 90 seconds old while a game has started and
//     not finished.
//   - `partial`: the range reaches outside `buildWindow`, a day in range was not
//     built, or the list behind the build was cut short.
//   - `ok`.
type DayScheduleMeta struct {
	// AgeSeconds Seconds since `cacheTimestamp`.
	AgeSeconds  *float64                `json:"ageSeconds,omitempty"`
	BuildWindow *DayScheduleBuildWindow `json:"buildWindow,omitempty"`

	// CacheTimestamp Build time of the least recently built day in range; null when none is known.
	CacheTimestamp *string `json:"cacheTimestamp,omitempty"`

	// LiveAgeSeconds Seconds since live data (scores, clocks) was last read in full.
	LiveAgeSeconds *float64 `json:"liveAgeSeconds,omitempty"`

	// Source Where the schedule comes from, currently "kalshi".
	Source    *string `json:"source,omitempty"`
	Status    *string `json:"status,omitempty"`
	Timestamp *string `json:"timestamp,omitempty"`

	// Window A UTC instant range `[start, end)`.
	Window *DayScheduleWindow `json:"window,omitempty"`
}

// DayScheduleResponse Response from GET /api/v1/schedule. `games` are in start order (larger volume first
// among games starting together).
type DayScheduleResponse struct {
	Count *float64 `json:"count,omitempty"`

	// Date The day asked for (or today in `tz`), YYYY-MM-DD.
	Date  *string           `json:"date,omitempty"`
	Days  *float64          `json:"days,omitempty"`
	Games []DayScheduleGame `json:"games,omitempty"`

	// Meta Freshness of a day-schedule answer. `status`, first match wins:
	// - `no-data`: nothing built for the range.
	// - `stale`: built more than 3 minutes ago, the schedule not refreshed for 5
	//   minutes, or live scores more than 90 seconds old while a game has started and
	//   not finished.
	// - `partial`: the range reaches outside `buildWindow`, a day in range was not
	//   built, or the list behind the build was cut short.
	// - `ok`.
	Meta *DayScheduleMeta `json:"meta,omitempty"`

	// Sports The sports asked for (all nine by default).
	Sports  []string `json:"sports,omitempty"`
	Success *bool    `json:"success,omitempty"`
	Tz      *string  `json:"tz,omitempty"`
}

// DayScheduleStartCheck The game's start time compared with independent sources.
type DayScheduleStartCheck struct {
	// DiffersByMinutes The larger of the two gaps below, in minutes; null when neither differs.
	DiffersByMinutes *float64 `json:"differsByMinutes,omitempty"`

	// Flashscore How an independent source's start time compares with the schedule's.
	Flashscore *DayScheduleStartCheckResult `json:"flashscore,omitempty"`

	// FlashscoreMinutes Minutes between this start and the scores feed's start, when they differ.
	FlashscoreMinutes *float64 `json:"flashscoreMinutes,omitempty"`

	// Sportsbooks How an independent source's start time compares with the schedule's.
	Sportsbooks *DayScheduleStartCheckResult `json:"sportsbooks,omitempty"`

	// SportsbooksMinutes Minutes between this start and the sportsbooks' start, when they differ.
	SportsbooksMinutes *float64 `json:"sportsbooksMinutes,omitempty"`
}

// DayScheduleStartCheckResult How an independent source's start time compares with the schedule's.
type DayScheduleStartCheckResult = string

// DayScheduleStatus A game's state on the day schedule. Open: a new value must not break a reader.
//   - `unconfirmed`: the scheduled start passed long ago and no live data confirms the
//     game started or ended, or live data for a game in progress stopped updating for
//     more than 10 minutes.
//   - `time_tbd`: the start time is not set yet.
type DayScheduleStatus = string

// DayScheduleWindow A UTC instant range `[start, end)`.
type DayScheduleWindow struct {
	End   *string `json:"end,omitempty"`
	Start *string `json:"start,omitempty"`
}
