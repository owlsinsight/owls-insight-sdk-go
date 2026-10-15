# Changelog

All notable changes to this module are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the module uses
[semantic versioning](https://semver.org/).

## [0.8.0] - 2026-10-15

### Added

- Prop Builder on the v2 endpoints, book `propbuilder`, through the methods every v2 book
  uses: `GetV2(ctx, "propbuilder", sport, "")`, `GetV2Markets` and `GetV2Leagues`, for
  `nfl`, `ncaaf`, `nba`, `wnba`, `nhl` and `mlb` (each one league). It is a pregame
  player-props board (Over/Under lines, "N+" ladders, Player Doubles, player vs player)
  with game markets. Several sportsbooks embed this board, and its prices were identical
  on every site we compared. Odds are decimal. `Data` is keyed by entry id, family first,
  so `GetV2Markets(ctx, "propbuilder", "nfl", "", "ou-*,ss-*")` returns every Over/Under
  and N+ board of the sport. The game markets are sold as parlay legs only, and a leg's
  price is not a parlay price.
- `propbuilder-v2-update` (`V2EventName("propbuilder")`), opted in with the `Subscription.V2`
  book `propbuilder`, for example `{"propbuilder": {"nfl": "*"}}`: the entries that
  changed, each one whole, and `RemovedIds`. It decodes into `V2Update`, and the generated
  `V2Subscription` has a `Propbuilder` field.
- `Subscription.Realtime` (`RealtimeFeeds`): `&RealtimeFeeds{PS3838: Ptr(false)}` stops
  `ps3838-realtime` for the connection and keeps `pinnacle-realtime`. Nil, the default,
  gets both. Only a literal false is honoured. `UpdateSubscription` replaces the whole
  object (`&RealtimeFeeds{}` there turns `ps3838-realtime` back on), and the stream sends
  it again on every reconnect with the rest of the stored subscription.
- `Subscription.Scores` and `EventScoresUpdate`: opt in to `scores-update` with `true` or
  a list of sport keys, and decode it with `Decode[ScoresUpdate]`. The model came with
  0.7.0; the subscription field did not.
- `RealtimeOddsPayload.Feed` and `PinnacleRealtimeSyncPayload.Feed` (`RealtimeFrameFeed`:
  `UpstreamAgeSeconds`, `Resubscribing`, `Stalled`) on every `pinnacle-realtime` and
  `pinnacle-realtime-sync` frame, and `RealtimeFeedState.Stalled` on `GetRealtime`:
  true when the sport has a game in play and Pinnacle has been silent for longer than its
  normal quiet spells, so prices can be out of date while frames keep arriving. It does
  not change `freshness.stale`.

### Changed

- `spec/` is the TypeScript SDK's 0.78.0 spec and the models are regenerated from it. The
  props `category` enum lists all 85 categories `/api/v1/{sport}/props` serves (football
  periods, kickers, `longest_completion`, `assisted_tackles`, ...); `category` stays a
  plain string in Go, so nothing changes at runtime. `ArchivedGame.OddsSnapshots` and
  `PropsSnapshots` are documented as null when not counted (they were already pointers),
  and the generated `ClientSubscription`, the `subscribed` acknowledgement, has `Realtime`.
- README: Prop Builder, `GetV2Markets`, the realtime opt-out, the scores opt-in and the
  frame feed state.

## [0.7.0] - 2026-10-13

### Added

- `GetV2Markets(ctx, book, sport, league, marketIds)`: `GetV2` with the `marketIds`
  filter, comma-separated market ids and prefixes ending in `*`. DraftKings stores
  its US-sport player props (nba, nfl, nhl, mlb, ncaab, ncaaf) one entry per
  subcategory, `props-<subcategoryId>`, and leaves them out unless asked for:
  `GetV2Markets(ctx, "draftkings", "nfl", "", "props-*")`.
- The spec is the JS SDK's 0.75.0: the `scores-update` WebSocket event, `AlternateLine.last_seen`,
  `Market.suspended` on pulled Pinnacle main markets, the v2 `meta.marketIds`,
  `marketsFilteredOut`, `defaultExcluded` and `marketAges`, and the leagues
  `marketCountExcludedByDefault`.

### Changed

- Closing odds: `total.line` can be null when a live-derived total is withheld, and a
  withheld spread arrives with every field null. Both were already pointers.

## [0.6.0] - 2026-10-07

### Added

- UFC/MMA on the FanDuel, DraftKings, Kalshi, Polymarket and Bovada v2 endpoints.
  `GetV2`, `GetV2Leagues` and `Subscription.V2` take the book and sport as strings, so
  no method changed: `GetV2(ctx, "fanduel", "mma", "ufc")`, the same for `draftkings`
  and `polymarket`; `GetV2(ctx, "kalshi", "mma", key)` with a lowercase series ticker
  (the UFC's, such as `kxufcfight`, and other promotions', such as `kxmmafight`); and
  `GetV2(ctx, "bovada", "mma", "")`, one league with every card.
  `GetV2Leagues(ctx, book, "mma")` lists the leagues.
- `fanduel-v2-update` and `draftkings-v2-update` carry MMA, for example
  `Subscription{V2: map[string]map[string]any{"fanduel": {"mma": []string{"ufc"}}}}`.
  `kalshi-v2-update` and `polymarket-v2-update` do not, so `mma` is REST only on
  Kalshi and Polymarket.
- The generated v2 subscription models follow the API description:
  `FanDuelV2BookSubscription` and `DraftKingsV2BookSubscription` have an `Mma` field,
  and `DraftKingsV2BookSubscription` also has `Mlb`, `Nba`, `Ncaab`, `Ncaaf`, `Nfl` and
  `Nhl`, as the API accepts them (pass `"*"`). For those six sports
  `draftkings-v2-update` pushes only the live slice (market id `live`), unless it is
  too large to send; their main catalog is REST only.

### Changed

- `spec/` is the TypeScript SDK's current spec (0.74.0) and the models are regenerated
  from it. Besides the subscription fields above, only doc comments changed: the two
  subscription types (and `V2Subscription.Draftkings` and `V2Subscription.Fanduel`).
  The spec also brings the API description's other updates: the webhook management
  paths (so the spec test now checks the webhook methods' return types against them),
  the scores, stats and splits descriptions, and on the WebSocket side the
  `draftkings-v2-update` and `ps3838-realtime` descriptions, the access lines of the
  props events, the esports realtime events and the Pinnacle and PS3838 realtime
  events, and `bet365-props-update`, which the API no longer documents (use
  `bet365-v2-update`).
- README: `mma` is REST only on Kalshi and Polymarket.

## [0.5.0] - 2026-10-02

### Added

- Customer webhooks (beta; MVP and Hall of Fame), the methods named after the
  TypeScript SDK's: `CreateWebhook`, `ListWebhooks`, `GetWebhook`, `UpdateWebhook`,
  `DeleteWebhook`, `TestWebhook`, `RotateWebhookSecret` and `ListWebhookDeliveries`
  for `/api/v1/webhooks`. The calls that change something are never retried, whatever
  `RetryPolicy` says (a retried create could register the endpoint twice). The signing
  secret is returned by `CreateWebhook` and `RotateWebhookSecret` only. A 400 or 409 is
  an `*APIError` with the API's `Code` (such as `sport_not_covered`, `https_required`,
  `event_type_unavailable` or `endpoint_limit`) and the body in `Details`. An empty id,
  `.` or `..` is refused before anything is sent, since a URL would resolve it to
  another route.
- Request inputs, written by hand with value fields: `CreateWebhookParams`,
  `UpdateWebhookParams` (only the fields you set are sent), `RotateWebhookSecretParams`,
  `WebhookFiltersInput` with `WebhookLineMovedFiltersInput` and
  `WebhookEvFoundFiltersInput` (a nil list is not sent; a non-nil empty `Sports` or
  `Venues` sends `[]`, which means all; their JSON tags let a config loaded from JSON
  keep every field), and `WebhookDeliveriesParams`.
- `VerifyWebhookSignature(payload, header, secret)` and `VerifyWebhookSignatureAt`
  (tolerance and clock given) check a delivery's `Owls-Signature` header against the raw
  body: HMAC-SHA256 of `t`, a period and the body, keyed with the whole secret; any v1
  may match (two are sent during a secret rotation), each compared in constant time,
  and a `t` more than `DefaultWebhookTolerance` (5 minutes) from the clock is refused.
  The header is read exactly as the API reads it. Also `ComputeWebhookSignature`,
  `ParseWebhookSignature` (`WebhookSignature`), `WebhookSignatureHeader` and the
  `WebhookEvent*` event type constants. It passes the known-answer vector the API and
  every Owls Insight SDK share.
- `ParseWebhookEvent(body)` decodes a verified delivery into a `WebhookEvent` (with
  `EventID()` to dedupe on and `EventType()`) holding the model of its type
  (`*WebhookLineMovedEvent`, `*WebhookEvFoundEvent`, `*WebhookGameStartedEvent`,
  `*WebhookGameFinalEvent`, `*WebhookPropsGradedEvent`, `*WebhookTestEvent`), or a
  `*WebhookDeliveryPayload` for a type this version does not know.
- Models generated from the API description: the endpoint (`WebhookEndpoint`,
  `WebhookEndpointWithSecret`, `WebhookFilters`), the responses, the deliveries
  (`WebhookDelivery`, `WebhookDeliveryPayload`) and the delivery bodies with their data
  (`WebhookLineMovedData`, `WebhookEvFoundData`, `WebhookGameData`,
  `WebhookPropsGradedData`, `WebhookTestData` and their parts). The tests decode the
  API's own bodies for every event type with unknown fields disallowed.

### Changed

- `spec/` is the TypeScript SDK's current spec (0.73.0) and the models are regenerated
  from it. Besides the webhook models nothing in the generated code changed. The Go
  overlay also removes the spec's `webhooks` operations (models only, as for the
  paths).
- The spec drift test checks an operation's only 2xx response when it has no 200 (a
  create answers 201), so `CreateWebhook` and `TestWebhook` are covered once the
  webhook paths are in the spec.

## [0.4.0] - 2026-10-02

### Added

- `EVParams.Venues`: `GetEV` sends `venues=true` when it is set, and the API then also
  lists Kalshi and Polymarket, their trading fee included in the EV. `EVParams.Book`
  takes `kalshi` or `polymarket` with it. Sportsbooks and Novig are listed either way.
- Optional fields of the EV answer (`GET /api/v1/{sport}/ev`), generated from the API
  description:
  - `EVOpportunity`: `Kind` (`book` or `venue`; Novig, Kalshi and Polymarket are
    venues), `FairUnadjusted` (the fair before the 1-point margin), `Roi` (`EvPct / 100`,
    not rounded), `Fee` (a venue's fee per contract in dollars, 0 for a sportsbook),
    `Tradeable` (clears the markets page trade bar: 2% EV for a sportsbook; 4%, a bid, a
    spread of 4 cents or less and $50 at the ask for a venue), `Reason` (why not),
    `Route` (`yes` or `no:<CODE>` on Kalshi and Polymarket), `Size` (dollars at the
    ask), `Link` (the venue's page) and `QuoteAgeMs` (the price's age in milliseconds
    when the answer was served, by its own timestamp). Each is nil when the API sends
    null or leaves it out.
  - `EVEvent`: `CanonicalEventID` (the stable `{sport}:{away}@{home}-{date}` id the
    history endpoints take) and `Fair` (new type `EVFair`: `Method` `pinnacle` or
    `consensus`, `Anchor` `realtime` or `odds`, `Sources`, `Reason`, and `Home`, `Away`
    and `Draw`, the fair before the margin).
  - `EVResponse.Meta.Board` (new type `EVBoard`): `BuiltAt`, `AgeMs`, `Stale`, `Venues`
    and `VenueQuotes`, for the board the answer was read from.

### Changed

- The EV doc comments say how each value is computed (`FairProbability` is the
  conservative fair, the fair minus 1 percentage point, and `FairPrice`, `EvPct`,
  `EdgePp` and `KellyFraction` are computed at it), what `BooksInConsensus` counts, and
  the freshness rules. No existing field changed name or type.
- The models are regenerated from the current API description (`spec/openapi.json`
  and `spec/ws.json`). Besides the EV models above, nothing in the generated code
  changed.

## [0.3.0] - 2026-10-01

### Added

- `GetDaySchedule` for `GET /api/v1/schedule` (every paying tier): every game of a
  calendar day in a time zone, across NFL, college football, MLB, NBA, WNBA, NHL,
  tennis, soccer and UFC. Each game has its start time, status, score and clock, both
  sides, Kalshi win prices (and the draw price on soccer), the `eventId` that `GetOdds`
  gives it when the game was found there (else nil), and its start time checked against
  other sources. `DayScheduleParams` takes `Date` (YYYY-MM-DD in `TZ`, default today
  there), `TZ` (an IANA zone, default America/New_York), `Days` (1 to 10, default 1)
  and `Sports` (default every sport); the zero value sends no query. `Meta.Status`
  (`ok`, `partial`, `stale` or `no-data`) says whether the answer is complete and
  fresh. New types: `DayScheduleParams`, `DayScheduleResponse`, `DayScheduleGame`,
  `DayScheduleCompetitor`, `DayScheduleMeta`, `DayScheduleStartCheck`,
  `DayScheduleWindow` and `DayScheduleBuildWindow`, with `DayScheduleStatus` and
  `DayScheduleStartCheckResult` as string aliases. `GetSchedule(ctx, sport)`, the
  per-sport list of upcoming games, is unchanged.
- The generated v2 subscription models follow the API description:
  `Bet365V2BookSubscription` has an `Nfl` field and `BetOnlineV2BookSubscription` an
  `Ncaaf` field. Nothing else needed to change for them: `Subscription.V2`, `GetV2` and
  `GetV2Leagues` take the book and sport as strings.

### Changed

- The models are regenerated from the current API description (`spec/openapi.json`
  and `spec/ws.json`), which brings the day schedule types and the two subscription
  fields above.

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
