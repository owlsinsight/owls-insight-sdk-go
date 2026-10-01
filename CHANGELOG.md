# Changelog

All notable changes to this module are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the module uses
[semantic versioning](https://semver.org/).

## [0.2.0] - 2026-10-01

### Added

- `GetHistorySplits` for `GET /api/v1/history/splits` (MVP and above): how a game's
  betting splits moved. Each row is one recorded reading of a book's handle and ticket
  percentages on the spread, total and moneyline, with the line or price at that moment,
  oldest first. Ask for one game by `EventID` (the `eventId` from `GetOdds`), or for a
  sport by `Sport` and `StartDate` over at most 7 days; filter by `Book` and page with
  `Limit` (at most 500) and `Offset`. Like the other history methods it counts against
  the client-side history gate and uses the opt-in 429/503 retry. New types:
  `HistorySplitsParams`, `HistorySplitsResponse` and `SplitsHistoryRow`.
- `SplitsBookEntry.AsOf`: when that book's splits figures were read. The books are read
  on different schedules, so two books on one game can show different times. Absent on
  responses from before 2026-09-27.
- 888sport, a new v2 book, works through `GetV2(ctx, "888sport", sport, league)` and
  `GetV2Leagues` (soccer and tennis need a league). It is REST only and sends no
  WebSocket event. No new method was needed: v2 book keys that start with a digit
  (`4casters`, `888sport`) pass as the URL has them. It is now covered by tests, and
  the README lists the REST-only v2 books.
- BetMGM on the splits board: `GetSplits` can return `betmgm` entries (since
  2026-09-29). BetMGM publishes ticket percentages only, so those entries have their
  bets fields and nil handle fields, never 0. Treat any splits field as possibly absent.

### Changed

- Splits doc comments follow the API since 2026-09-27: `SplitsGame.EventID` is the
  `eventId` that `GetOdds` gives the same game (before, it was an upstream game code
  that did not match /odds), and `AwayTeam` and `HomeTeam` are the /odds team names, so
  splits join to odds on `EventID`. `SplitsResponse.Meta.AsOf` is the oldest of the
  books' latest reads, `Meta.Books` lists the books on the board (it can be DraftKings
  alone), and `Meta.Source` is a fixed legacy value: read the book set from `Meta.Books`.
  `Meta.Status` and `Meta.PartialReason` now describe each book's latest read rather
  than one fetch of the whole board.

## [0.1.2] - 2026-09-26

### Changed

- The module path is now `github.com/owlsinsight/owls-insight-sdk-go`. Install with
  `go get github.com/owlsinsight/owls-insight-sdk-go@latest` and change the import from
  `github.com/Davidgsilva/owls-insight-sdk-go`. The code is otherwise identical to 0.1.0.
- The licence holder is Owls Insight LLC.

## [0.1.1] - 2026-09-26

### Deprecated

- This module path is deprecated. The SDK moved to
  `github.com/owlsinsight/owls-insight-sdk-go`; switch with
  `go get github.com/owlsinsight/owls-insight-sdk-go@latest` and update the import.
  The code is otherwise identical to 0.1.0.

## [0.1.0] - 2026-09-26

### Added

- `Client` with one method per documented REST endpoint, named after the TypeScript
  SDK's methods (`getOdds` is `GetOdds`), plus `GetV2` and `GetV2Leagues` for every v2
  book and `V2EventName`.
- `*APIError` with `errors.Is` sentinels for 401, 403, 404, 429 and 503; opt-in retries
  of 429 and 503 on GET honouring `Retry-After`; a client-side gate of 3 history
  requests in flight; `IterHistoryOdds` and `IterHistoryProps` pagers.
- `Stream`, a WebSocket client that re-sends the stored subscription on every connect,
  backs off when connections do not last, and retries retryable refused reconnects on
  their own schedule. A reconnect refused with an account code (`TIER_NO_WS`,
  `SUBSCRIPTION_INACTIVE`, `PAYMENT_OVERDUE`, `TRIAL_EXPIRED`, `TRIAL_UNVERIFIABLE`) is
  retried after 30 s and then every 60 s. Every other non-retryable refusal (a bad key,
  an unknown code, no code) ends `Run`, and so does a server disconnect, which the
  server sends when its periodic account check closes the connection.
- `RealtimeSportEvents`, which reads any sport's events from a `pinnacle-realtime` or
  `ps3838-realtime` frame.
- `GetBookProps` returns `BetMGM` for betmgm and `Props` for every other book; the
  retired bet365 props answer 410 Gone.
- `GetEsportsRealtime` for the esports real-time endpoints (league required, Pinnacle
  rows kept as `json.RawMessage`); `GetRealtime` refuses the esports keys.
- `GetMoneylineHistory`, `GetSpreadHistory` and `GetTotalsHistory`.
- `Decode[T]` for event payloads; `OnOddsUpdate` passes decode errors to the handler.
- `Refusal*` constants for `RefusalError.Code`.
- `EventError` and `WSError`: the server's application-level `error` event (a props
  subscription the plan does not include, a revoked key, a lowered connection limit),
  delivered like any other event.
- Models generated from the API's OpenAPI description with oapi-codegen v2.8.0, with
  Go initialisms in names (`EventID`). Every v2 WebSocket event decodes into
  `V2Update`, whose fields that differ between books (`Markets`, `Timestamp`,
  `Count`, `RemovedIds` and others) are `json.RawMessage`; `V2BookResponse.Data` is the
  book's JSON as one `json.RawMessage` (some books send `[]` or `null`); every other
  field whose type varies is `json.RawMessage` too.
