# owls-insight-sdk-go

The Go client for the [Owls Insight](https://owlsinsight.com) sports odds API: REST
endpoints, history pagers, and a WebSocket stream that keeps your subscription across
reconnects.

```
go get github.com/owlsinsight/owls-insight-sdk-go@latest
```

Requires Go 1.23 or later. One dependency: `github.com/coder/websocket`.

Use it from server-side programs only. An API key is a credential: do not ship it in
code that runs on your users' machines.

## REST

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	owls "github.com/owlsinsight/owls-insight-sdk-go"
)

func main() {
	c, err := owls.NewClient(os.Getenv("OWLS_INSIGHT_API_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()

	odds, err := c.GetOdds(ctx, "nba", &owls.OddsParams{Books: []string{"pinnacle", "fanduel"}})
	if err != nil {
		log.Fatal(err)
	}
	for book, events := range odds.Data {
		fmt.Println(book, len(events), "events")
	}
}
```

Every method takes a `context.Context` first. The method names are the TypeScript SDK's
names with the first letter upper-cased (`getOdds` is `GetOdds`); the table at the end
lists them all. Calls with many filters take a params struct; a nil pointer sends none.

The esports keys (`cs2`, `lol`, `valorant`, `dota2`) have their own real-time method,
`GetEsportsRealtime(ctx, game, league)`: the league is required, and each item of `Data`
is a Pinnacle matchup exactly as Pinnacle sent it (`json.RawMessage`). `GetRealtime`
refuses those keys.

`GetDaySchedule` returns every game of a calendar day across sports (NFL, college
football, MLB, NBA, WNBA, NHL, tennis, soccer and UFC): start time, status, score and
clock, both sides and Kalshi win prices. The zero `DayScheduleParams` asks for today in
America/New_York; set `Date` (YYYY-MM-DD), `TZ` (an IANA zone), `Days` (1 to 10) and
`Sports` to change that. `EventID` on a game is its `eventId` on `GetOdds`, or nil when
the game was not found there, and `Meta.Status` (`ok`, `partial`, `stale` or `no-data`)
says whether the answer is complete and fresh. `GetSchedule(ctx, sport)` is the older
per-sport list of upcoming games, unchanged.

FanDuel issues a different marketId in every state while the selectionId is the same
everywhere, so a bet-slip link built with New Jersey's marketId fails in another state.
Set `State` (a two-letter code such as `"co"`) on `OddsParams` (`GetOdds` and the
per-market routes) or `PropsParams` (`GetProps`, `GetBookProps` for `fanduel`) to get
that state's marketIds on the FanDuel prices. An outcome with no id for the state has its
marketId omitted, and the odds routes leave the FanDuel `deepLink` out.
`Meta.FanduelMarketIds` reports what was mapped (on the odds routes, `Available` false
means no ids for that sport and state). The WebSocket events carry the New Jersey ids.

### Fair line and EV

MVP and Hall of Fame plans read the fair line, anchored on Pinnacle's de-vigged price, and
the prices that beat it:

```go
fair, err := c.GetFair(ctx, "nfl") // every upcoming game's fair moneyline, spread and total
for _, g := range fair.Data {
	if g.Spread != nil && g.Spread.Home != nil { // nil when there is no current fair
		fmt.Println(*g.AwayTeam, "@", *g.HomeTeam, *g.Spread.Home.Point, *g.Spread.Home.American)
	}
}

ml, err := c.GetEV(ctx, "nfl", &owls.EVParams{MinEV: 2})                                   // moneyline
lines, err := c.GetEVLines(ctx, "nfl", &owls.EVLinesParams{Markets: []string{"spreads"}}) // spreads, totals or both
props, err := c.GetPropsEV(ctx, "nba", &owls.PropsEVParams{MinEV: 2, Player: "Tatum"})    // player props
```

`GetEVLines` scores spreads and totals at Pinnacle's main number, against books that hang
exactly that number, and each opportunity carries its own `Point`. Its `Markets` takes
`spreads` and `totals` only (nil means both); anything else is refused before a request,
because the route would answer with the moneyline board. `GetPropsEV` compares
a price only at the same player, category and line; `All: true` also lists the props with
no positive-EV price, each with its fair line. Spreads, totals and props are supported for
nba, ncaab, nfl, ncaaf, nhl, mlb, wnba and soccer.

### Play-by-play

`GetPlays(ctx, sport, eventID, params)` returns every play of an NBA, NHL, NFL, NCAAF or
soccer game in play (or ended within the last 6 hours), oldest first, by the event id
`GetScores` gives the game (for soccer, the match incidents: goals, own goals, penalties,
cards and substitutions); with an empty event id it lists the games that have play-by-play
(`res.Games`), and a `Since` or `Limit` is refused. Before tip-off, and for any other id,
it is a 404.

```go
p := &owls.PlaysParams{}
res, err := c.GetPlays(ctx, "nfl", eventID, p)
for _, play := range res.Game.Plays {
	plays[*play.ID] = play // merge by id: a play can be amended after it is first published
}
p.Since = res.Game.LastSeq // next call: the new plays, plus any amended in the last 2 minutes
```

`Play.Detail` is the sport's own fields as raw JSON: decode it with
`owls.Decode[owls.FootballPlayDetail]`, `HockeyPlayDetail`, `BasketballPlayDetail` or
`SoccerPlayDetail`. The
same plays are pushed on `plays-update` (see WebSocket).

### Options

| Option | Default | |
|---|---|---|
| `WithBaseURL(url)` | `https://api.owlsinsight.com` | The WebSocket URL follows it unless `WithWSURL` is set. |
| `WithWSURL(url)` | the base URL | |
| `WithHTTPClient(hc)` | `http.DefaultClient` | Leave its `Timeout` at zero. |
| `WithTimeout(d)` | 30 s | Per attempt, through the request context. 0 disables it. `BatchSgp` and `CompareSgpCombo` raise a shorter value to 45 s. |
| `WithRetry(p)` | no retries | Retries 429 and 503 on GET, see below. |
| `WithHistoryConcurrency(n)` | 3 | History requests in flight at once. 0 disables the gate. |

### Errors

A non-2xx response is an `*owls.APIError` with `Status`, `Code`, `Message`, `RetryAfter`
and `Details` (the body's `details`, or the whole JSON body). Match the status with
`errors.Is`:

```go
_, err := c.GetRealtime(ctx, "nba", "")
var apiErr *owls.APIError
switch {
case errors.Is(err, owls.ErrUnauthorized): // 401: bad or missing key
case errors.Is(err, owls.ErrForbidden):    // 403: the plan does not include it (Code FORBIDDEN, or SGP_TIER_REQUIRED for the SGP tools), or owls.DataKeyForbiddenCode
case errors.Is(err, owls.ErrNotFound):     // 404 (Code COMBO_NOT_FOUND from CompareSgpCombo, else NOT_FOUND)
case errors.Is(err, owls.ErrRateLimited) && errors.As(err, &apiErr):
	time.Sleep(apiErr.RetryAfter) // 429; Code is HISTORY_CONCURRENCY or SGP_RATE_LIMIT when the API says so (never wait out SGP_COMPARE_TOO_MANY_BOOKS or SGP_BATCH_TOO_LARGE)
case errors.Is(err, owls.ErrServiceBusy):  // 503
}
```

A data-only key (Sign in with Owls) is refused on the webhook API and the betslip parse
with `Code == owls.DataKeyForbiddenCode`; use a key from your dashboard there.

`Message` is the body's `message`, else its `error`, else the HTTP status text. A
timeout or network failure is returned wrapped, so `errors.Is(err,
context.DeadlineExceeded)` works.

### Retries

Nothing is retried unless you ask:

```go
c, _ := owls.NewClient(key, owls.WithRetry(owls.RetryPolicy{MaxRetries: 3}))
```

Only 429 and 503 on GET requests are retried. The client waits for `Retry-After`
(seconds or an HTTP date) when the server sends one, and gives up instead when it is
longer than `MaxRetryAfter` (120 s). Without the header it backs off from `BaseDelay`
(2 s), doubling up to `MaxDelay` (30 s), with full jitter. A 429 is never retried once
the monthly quota is used up (`X-RateLimit-Remaining-Month: 0`), and a POST, PATCH or
DELETE is never retried.

### History

`/api/v1/history/*` and `/api/odds/history` calls pass through a client-side gate of 3
requests in flight (the MVP plan's per-key allowance), so a burst waits in your process
instead of being refused by the API.

To export a game's history, page through it rather than fanning out requests:

```go
for snap, err := range c.IterHistoryOdds(ctx, owls.HistoryOddsParams{EventID: id, Book: "pinnacle"}, nil) {
	if err != nil {
		return err
	}
	rows = append(rows, snap)
}
```

`IterHistoryOdds` and `IterHistoryProps` read 1000 rows per request (at most 5000),
stop after a short page, and retry a 429 or 503 up to 3 times per page by default
(`PageOptions`).

`GetHistorySplits` returns how a game's betting splits moved: each reading of a book's
handle and ticket percentages, with the line or price at that moment, oldest first.
Ask for one game by `EventID` (the `eventId` from `GetOdds`), or for a sport by `Sport`
and `StartDate` over at most 7 days, and page with `Limit` (at most 500) and `Offset`.
A market missing from a row was not quoted at that read, and a percentage missing from
a market is nil, not 0: BetMGM (`betmgm`) publishes ticket percentages only, on the live
board (`GetSplits`) and in the history alike.

### v2 books

Every v2 book goes through two methods. `Data` is the book's own JSON, exactly as it
was published, as a `*json.RawMessage`: usually an object keyed by market or event id,
but some books send `[]` or `null` when they have nothing.

```go
leagues, err := c.GetV2Leagues(ctx, "draftkings", "soccer")
board, err := c.GetV2(ctx, "draftkings", "soccer", "england-premier-league")
var markets map[string]json.RawMessage
if board.Data != nil {
	_ = json.Unmarshal(*board.Data, &markets) // stays nil when the book sent [] or null
}
for marketID, raw := range markets {
	// decode raw into your own type for this book
}
```

A new v2 book needs no SDK release; pass its key as the URL has it, including one that
starts with a digit (`4casters`, `888sport`). `owls.V2EventName(book)` gives its
WebSocket event (`<book>-v2-update`, except BetOnline's `betonline-realtime`). Some
books are REST only and send no event: 888sport, bookmaker, bovada, hardrock, novig,
polymarketus, stake, thescore and underdog. `mma` is REST only on kalshi and polymarket, whose events
carry their other sports. Every v2 event decodes into `owls.V2Update`. Books differ
in what they send, so `Markets` (a list of `{marketId, hash, kind, data}` or a count),
`Timestamp` (epoch milliseconds or an ISO string), `Events`, `Data`, `Raw` and the
fields only some books send (`Count`, `TotalCount`, `Leagues`, `Heartbeat`,
`MatchupIds`, `RemovedIds`, `RemovedMatchupIds`, `RemovedGhids`) stay `json.RawMessage`.

`GetV2Markets(ctx, book, sport, league, marketIds)` returns only the entries you name:
a comma-separated list of market ids and prefixes ending in `*` (at most 50), or `*` for
every entry. Every v2 book except bet365, hardrock, mybookie, stake, thunderpick and
underdog reads it. It is also how you ask for the entries a book leaves out by default:
DraftKings `props-*` (its US-sport player props), Fanatics Markets `books-*` (its live
order books) and Polymarket US `full-*` (each event's full market catalog). On a default
request `Meta.DefaultExcluded` says how many entries were left out and the `marketIds`
value that includes them; with a filter, `Meta.MarketIds` echoes it and
`Meta.MarketsFilteredOut` counts what it left out. DraftKings, Fanatics Markets and
Polymarket US also send `Meta.MarketAges`, the seconds since each returned entry was
last confirmed.

```go
books, err := c.GetV2Markets(ctx, "fanaticsmarkets", "nfl", "", "books-*")
full, err := c.GetV2Markets(ctx, "polymarketus", "nfl", "nfl", "full-*")
```

**Polymarket US** (book `polymarketus`) is the CFTC-regulated US exchange, not global
Polymarket: its own events, ids and prices, probabilities in 0..1 sent as strings. It is
REST only, and `league` is required for every sport (`GetV2Leagues`; season-long futures
sit under `<league>-futures`, such as `nfl-futures`).

**Fanatics Markets order books** arrive on their own event,
`owls.EventFanaticsMarketsBooksV2Update` (`fanaticsmarkets-books-v2-update`, decoded into
`owls.V2Update`), when the subscription asks for them next to its sports:
`V2: map[string]map[string]any{"fanaticsmarkets": {"nfl": "*", "books": true}}`. Each
market is a `books-*` entry, whole or a patch (merge by symbol; null means that book is
gone), with `RemovedIds`. `fanaticsmarkets-v2-update` keeps carrying the events alone.

**Prop Builder** (book `propbuilder`; sports `nfl`, `ncaaf`, `nba`, `wnba`, `nhl` and
`mlb`, each one league, so pass an empty league) is a pregame player-props board:
Over/Under lines, "N+" ladders, Player Doubles, player vs player, and game markets.
Several sportsbooks embed this board, and its prices were identical on every site we
compared. Odds are decimal, and a market whose `oddsOverride` is above 1 is priced at
`oddsOverride` instead of `odds`. A game leaves the board when it starts. `Data` is keyed
by entry id, family first:

| Entry id | Holds |
|---|---|
| `game-{gameId}` | The game: `home`, `away`, `startTime`, its tab map (`categories`) and `entries`, every entry id of the game |
| `ou-{gameId}-{stat}-{hash}` | Over/Under lines for one statistic: `condition` 3 is over and 1 under, `value` is the line |
| `ss-{gameId}-{stat}-{hash}` | The "N+" ladder for one statistic: `value` 2 means 2 or more |
| `gfm-{gameId}-{stat}-{hash}` | One game-market group (`to win`, `handicap win`, `total points`) |
| `ppd-{gameId}` | Player Doubles: two legs at one price |
| `h2h-{gameId}` | Player vs player |

Read the statistic from an entry's `statistic`, not from its id. `gameId` is the board's
own game id, not a `GetOdds` event id. More families may be added, so skip one you do not
handle. The game markets are sold as parlay legs only, and a leg's price is not a parlay
price.

```go
// Every Over/Under and N+ board of the sport.
nfl, err := c.GetV2Markets(ctx, "propbuilder", "nfl", "", "ou-*,ss-*")
// One game; the "-" before "*" keeps game 2816110 out.
game, err := c.GetV2Markets(ctx, "propbuilder", "nfl", "",
	"game-281611,ou-281611-*,ss-281611-*,gfm-281611-*,ppd-281611,h2h-281611")
```

`propbuilder-v2-update` pushes the entries that changed within seconds, each one whole
(replace yours by `marketId`), and `RemovedIds` for the entries that left the board.
Load the board with `GetV2` on every `connect` event, the first and every reconnect, then
apply the updates. Updates can arrive while the board loads: hold them, and apply them on
top of the board once it is in. (Handlers run one at a time, so a `GetV2` call inside the
`EventConnect` handler holds them for you, up to the stream's 64-event queue.) Subscribe with
`V2: map[string]map[string]any{"propbuilder": {"nfl": "*"}}`.

### Same game parlays

```go
res, err := c.BuildSgp(ctx, "mlb", owls.SgpBuildParams{
	EventID: "35989831",
	Book:    "fanduel",
	Legs: []owls.SgpLeg{
		{Type: "moneyline", Selection: "New York Yankees"},
		{Type: "player_prop", Player: "Aaron Judge", Category: "hits", Side: "over", Line: owls.Ptr(1.5)},
	},
})
```

The API prices parlays on fanduel, draftkings, hardrock, betrivers, ballybet and betmgm
(`GetSgpEvents(ctx, sport, book)` lists each book's events). `EventID` is a string for
every book: FanDuel's id is a number in `GetSgpEvents`, every other book's a string.

A 422 that reports unresolved legs or a refused slip is returned, not an error: check
`res.Success` and `res.ReasonCode`. A 503 with `Code == owls.SgpNoPriceCode` means no
usable price came back; wait `RetryAfter` (2 s when the server sends no header).

On NHL, Hard Rock also prices `team_total` legs (`{Type: "team_total", Team: "Boston
Bruins", Selection: "over", Point: owls.Ptr(2.5)}`) and period legs: `Period` `"P1"`,
`"P2"` or `"P3"` on a moneyline, spread or total leg prices that period. A period
moneyline is the period's 3-way result, so its `Selection` is a team or `"Tie"`. Every
other book refuses these legs: `BuildSgp` answers a 422 (`ReasonCode` `unresolved`), and
`CompareSgp` and `BatchSgp` report that book as `unsupported`.

MVP and Hall of Fame plans also get:

- `Fair: true` on `SgpBuildParams`: a priced 200 carries `res.Fair` (`SgpFairBlock`), our
  estimate of the parlay's price from de-vigged leg probabilities and the book's own
  correlation, beside the book's `Sgp`, never in place of it. `Fair.Price` is nil with
  `Fair.Why` when it cannot be made.
- `CompareSgp(ctx, sport, owls.SgpCompareParams{EventID, Legs, ...})`: the same legs on
  every book at once, from OUR event id (as `GetOdds` gives it), each book's own answer in
  `Books` with a `Status` (`priced`, `refused`, `unresolved`, `busy`, `no-price`, `timeout`,
  `event-not-listed`, `ambiguous-event`, `unsupported`, `disabled`, `skipped`; more can be
  added) and `Best`. A book that does not price is not an error. A book that priced the
  same legs in the last 15 seconds may answer from that quote (`Cached`), unless `Fresh`.
- `BatchSgp(ctx, sport, owls.SgpBatchParams{Mode: "book", Book: "draftkings", Parlays: ...})`:
  up to 10 parlays on one book, or with `Mode: "compare"` up to 4 on every book. Results
  come back in request order with your `Ref`; one with `Status` `not-started` was not
  priced: send it again. A batch can take close to 40 seconds, so it waits at least 45 s
  whatever a shorter `WithTimeout` says.
- `CompareSgpCombo(ctx, owls.SgpComboCompareParams{ComboTicker: "KXMVE..."})` (or `Legs`, 2
  to 12 Kalshi markets with a `Side`): a Kalshi combo priced as the same game parlay it is.
  Each leg's translation is in `Combo.Legs[].Translated` (an `SgpLeg`), the game's OUR event
  id in `EventID`, and `Compare` is exactly what `CompareSgp` returns for those legs. A 422
  refusal is returned, not an error: `Success` false and `Code` `COMBO_NOT_A_COMBO`,
  `COMBO_LEGS_UNSUPPORTED` (`Combo.Legs[].Why`), `COMBO_MULTI_GAME` (`Games`) or
  `COMBO_EVENT_NOT_FOUND`. It reads Kalshi before pricing, so it waits at least 45 s whatever
  a shorter `WithTimeout` says.
- `GetSgpHistory(ctx, sport, owls.SgpHistoryParams{EventID: ...})` (or `Book` and
  `BookEventID`): the prices YOUR account was quoted for one game, newest first, from the
  calls above. Every priced answer carries `Meta.LegsHash`; pass it as `LegsHash` to
  see one parlay's quotes. `From` / `To` (ISO 8601) set the window, at most 7 days, default
  the last 24 hours (`From` alone: the 24 hours from it, up to now); `Limit` is 1 to 500, default 100. Only priced answers are kept, never a
  cached quote, from the day recording was switched on, for 90 days.

On a lower plan those are a 403 with `Code` `SGP_TIER_REQUIRED` (below Rookie a build is a
403 `FORBIDDEN` whatever its options); a 503 `SGP_FEATURE_DISABLED` means the feature is
not switched on yet. A 429 `SGP_COMPARE_TOO_MANY_BOOKS` or `SGP_BATCH_TOO_LARGE` needs more
builds than the key's limit allows at all: waiting never helps, ask for fewer books or
parlays. The pricing calls are never retried: every attempt is a real pricing request.
`GetSgpHistory` is a read: `WithRetry` retries its 429s and 503s, including
`SGP_HISTORY_UNAVAILABLE` (the history could not be read just then, `RetryAfter` 5 s).

### Webhooks

Webhooks are in beta. MVP and Hall of Fame plans can register HTTPS endpoints that
receive signed POSTs (MVP 3 endpoints, Hall of Fame 10): `line.moved` (Pinnacle's main
moneyline, spread or total for a game that has not started moved by at least your
step), `ev.found` (a price on the EV board became tradeable at or above your `min_ev`),
`game.started`, `game.final` (never for a postponed or cancelled game) and
`props.graded`. Each type covers its own sports; a sport an event type does not cover
is refused with a 400 (`sport_not_covered`), and so is an event type that is not open
yet (`event_type_unavailable`).

```go
created, err := c.CreateWebhook(ctx, owls.CreateWebhookParams{
	URL:        "https://example.com/owls",
	EventTypes: []string{owls.WebhookEventLineMoved, owls.WebhookEventEvFound},
	Filters: &owls.WebhookFiltersInput{
		Sports:    []string{"nba", "nhl"},
		LineMoved: &owls.WebhookLineMovedFiltersInput{PriceStepPp: 2, PointStep: 1},
		EvFound:   &owls.WebhookEvFoundFiltersInput{MinEv: 3},
	},
})
if err != nil {
	log.Fatal(err)
}
secret := *created.Data.Secret // returned ONCE: store it
id := *created.Data.ID

_, _ = c.TestWebhook(ctx, id) // a signed webhook.test event
_, _ = c.ListWebhookDeliveries(ctx, id, &owls.WebhookDeliveriesParams{Limit: 10})
_, _ = c.UpdateWebhook(ctx, id, owls.UpdateWebhookParams{Enabled: owls.Ptr(false)})
rotated, _ := c.RotateWebhookSecret(ctx, id, &owls.RotateWebhookSecretParams{ExpirePreviousAfterHours: owls.Ptr(24.0)})
saveSecret(id, *rotated.Data.Secret) // also returned ONCE; the old secret keeps signing for 24 hours
_, _ = c.DeleteWebhook(ctx, id)
```

The calls that change something are never retried. If one times out, list your
endpoints before creating again; rotate the secret of an endpoint whose secret you
never saw. Verify every delivery against the raw body, then dedupe on the event id
(delivery is at least once and unordered):

```go
http.HandleFunc("/owls", func(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil || !owls.VerifyWebhookSignature(body, r.Header.Get(owls.WebhookSignatureHeader), secret) {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	ev, err := owls.ParseWebhookEvent(body)
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if alreadyHandled(ev.EventID()) {
		w.WriteHeader(http.StatusOK)
		return
	}
	switch ev := ev.(type) {
	case *owls.WebhookLineMovedEvent:
		log.Println(*ev.Data.Market, *ev.Data.Reason)
	case *owls.WebhookEvFoundEvent:
		log.Println(*ev.Data.Signal.Venue, *ev.Data.Signal.EvPercent)
	}
	w.WriteHeader(http.StatusOK) // any 2xx within 10 s acknowledges it
})
```

Answer with any 2xx within 10 seconds. 410 Gone disables the endpoint. Anything else,
a redirect included, is a failure: `line.moved` and `ev.found` are retried after about
5, 20 and 60 seconds, the other types up to 8 times over about a day, and nothing is
sent after the event's `expires_at`.

`Owls-Signature` is `t=<unix seconds>,v1=<hex HMAC-SHA256(secret, t + "." + raw body)>`,
keyed with the whole secret (`whsec_` included). During a secret rotation it carries a
v1 for each live secret, and either one verifies. `VerifyWebhookSignature` rejects a `t`
more than 5 minutes from your clock; `VerifyWebhookSignatureAt` takes the tolerance
and the clock. `ParseWebhookEvent` returns an `owls.WebhookEvent` (`EventID`,
`EventType`) holding the model of the event's type, or a `*owls.WebhookDeliveryPayload`
(with `Data` left raw) for a type this version does not know.

### Models

The response types are generated from the API's OpenAPI description
(`spec/openapi.json`), with Go initialisms in field names (`EventID`, `ID`). Every field
is optional: scalars are pointers (a missing spread is not a 0 spread), slices and maps
are nil when absent, and enums are plain strings, so a new value never breaks decoding.
Unknown fields are ignored. A value whose JSON type does not match its field is left
unset instead of failing the call. Union fields, fields whose type varies (a number or
a string, such as `Outcome.SelectionID`) and raw book data are `json.RawMessage`.
`owls.Ptr(v)` builds a pointer for the optional inputs.

`owls.Decode[T](raw)` decodes any payload the same way, for example a WebSocket event:
`n, err := owls.Decode[owls.ServerNotice](raw)`.

A `pinnacle-realtime` or `ps3838-realtime` frame is keyed by sport. `RealtimeOddsPayload`
has a field for each common sport; `owls.RealtimeSportEvents(raw, sport)` reads the
events of any sport from the raw frame, including one added to the API later.
`pinnacle-realtime` and `pinnacle-realtime-sync` frames can carry `Feed`
(`UpstreamAgeSeconds`, `Resubscribing`, `Stalled`, nil when absent), the same as `Meta.Feed` on
`GetRealtime`: `Stalled` is true when the sport has a game in play and Pinnacle has been
silent for longer than its normal quiet spells, so prices can be out of date while
frames keep arriving.

## WebSocket

```go
s := c.Stream(owls.WithSubscription(owls.Subscription{
	Sports: []string{"nba"},
	Books:  []string{"pinnacle"},
}))
s.OnOddsUpdate(func(u *owls.OddsUpdatePayload, err error) {
	// frames are partial after the first snapshot: merge by event id
})
s.On(owls.EventServerNotice, func(raw json.RawMessage) {
	log.Printf("server notice: %s", raw)
})
err := s.Run(ctx) // blocks until ctx ends, the key is refused or the server disconnects
```

WebSocket access needs the Hall of Fame plan, or a paid plan with the WebSocket add-on.

**Subscriptions survive reconnects.** The stream stores the last subscription it sent
and your props subscriptions, and sends them on every connect, in the handshake and as
messages. If the server does not acknowledge them within 5 s it sends them once more.

- `Subscribe(ctx, sub)` replaces the subscription; fields you leave out are cleared.
- `UpdateSubscription(ctx, patch)` merges the fields you set into it. A nil field is left
  alone; an empty one clears it: `Subscription{EventIDs: []string{}}` removes the event
  filter.
- `SubscribeProps(ctx, book, filter)` and `UnsubscribeProps(ctx, book)` manage the props
  streams (`""` for Pinnacle's `player-props-update`).

These return `owls.ErrNotConnected` while the stream is not connected; use
`WithSubscription` for the first connect. The context only bounds the wait for a send
already in progress. Once a call has stored its change it writes it before returning
(within 10 s), so calls reach the server in the order they return.

Three `Subscription` fields are opt-ins rather than filters:

- `Realtime: &owls.RealtimeFeeds{PS3838: owls.Ptr(false)}` stops `ps3838-realtime` for the
  connection and keeps `pinnacle-realtime` (default: both). Only a literal false counts.
  `UpdateSubscription` replaces the whole object, so `Realtime: &owls.RealtimeFeeds{}`
  there turns `ps3838-realtime` back on. The `subscribed` acknowledgement carries the
  same type in `ClientSubscription.Realtime`.
- `Scores: true`, or a list of sport keys such as `[]string{"nfl"}`, opts in to
  `scores-update` (`owls.EventScoresUpdate`, payload `owls.ScoresUpdate`): a full live
  board per sport about every 30 seconds and a delta within a few seconds of a change.
- `Plays: true`, or a list of `nba`, `nhl`, `nfl`, `ncaaf` and `soccer`, opts in to `plays-update`
  (`owls.EventPlaysUpdate`, payload `owls.PlaysUpdate`): one message per sport within a
  few seconds of a new play, each game with the plays new or amended since the previous
  message. There is no full message: start from `GetPlays` and merge plays by `ID`.

**Events.** `On(name, fn)` receives each event's payload as `json.RawMessage`, in order,
on one goroutine; `owls.Decode[T]` turns it into a model type. Up to 64 events queue for
your handlers while the stream keeps answering the server's pings; after that it stops
reading, and the server drops a connection that falls too far behind. Keep handlers
quick: when the context ends, queued events are dropped and `Run` returns once the
handler that is running has returned. The stream also delivers `connect`, `disconnect`
and `connect_error` (the server's refusal, with `data.code`).

The server's own `error` event (`owls.EventError`, payload `owls.WSError` with `Code`
and `Message`) is delivered like any other event and does not close the connection.
The server sends it when it refuses a props subscription your plan does not include
(`TIER_REQUIRED`), when your key is revoked, when your connection limit is lowered, and
just before it disconnects a connection your account no longer allows:

```go
s.On(owls.EventError, func(raw json.RawMessage) {
	log.Printf("server error: %s", raw)
	if e, err := owls.Decode[owls.WSError](raw); err == nil && e.Code != nil && *e.Code == "TIER_REQUIRED" {
		// the plan does not include the props stream you subscribed to
	}
})
```

**Reconnects.** After the first successful connect, `Run` reconnects by itself:

- A dropped connection is retried after about 1 s. Each drop of a connection that had
  lasted less than 60 s doubles the delay, up to 30 s; a connection that lasts 60 s
  resets it.
- A refused reconnect the server marks retryable (for example `CONNECTION_LIMIT` while
  an old connection still holds your slot) is retried on its own schedule: 5, 15, 30,
  then 60 s for `CONNECTION_LIMIT`, 2, 5, 15, 30, then 60 s for the others.
- A reconnect refused for the account (`TIER_NO_WS`, `SUBSCRIPTION_INACTIVE`,
  `PAYMENT_OVERDUE`, `TRIAL_EXPIRED`, `TRIAL_UNVERIFIABLE`) is retried slowly, after
  30 s and then every 60 s. That covers a reconnect after a network drop or a server
  restart: it goes through once the invoice is paid or the plan upgraded.
- Every refusal retry is jittered, never sooner than the server's `retryAfterMs`, and
  at most 4 refusal retries of any kind are made in a minute. Each refusal is also
  delivered as a `connect_error` event, so you can see why the stream is waiting.
- Every other refusal the server marks not retryable ends `Run` with an
  `*owls.RefusalError`: a bad key (`MISSING_API_KEY`, `INVALID_API_KEY`,
  `API_KEY_DEACTIVATED`), a code this SDK does not know, or no code at all (an older
  server, where a bad key looks the same). Waiting never fixes a bad key, and every
  refusal counts toward the server's per-IP block.
- A disconnect sent by the server ends `Run` with `owls.ErrServerDisconnect`, and the
  stream does not reconnect after it. The server checks every connection's account
  every 5 minutes; when the plan, payment or trial no longer allows it, it sends an
  `error` event with the reason and then this disconnect. Call `Run` again once the
  account is fixed.

An expired trial deactivates the key, so the retry after `TRIAL_EXPIRED` meets
`API_KEY_DEACTIVATED` and `Run` returns. A cancelled subscription deactivates the key
too: until the account subscribes again, a reconnect meets `API_KEY_DEACTIVATED`. In
both cases, call `Run` again once the account is active.

If the very first attempt fails, `Run` returns the error at once (an
`*owls.RefusalError`, an `*owls.DialError` or a network error) so a wrong key or URL
fails fast. `RefusalError.Code` is one of the `owls.Refusal*` constants, for example
`owls.RefusalConnectionLimit`. The stream remembers the delay it owes before its next
attempt, so calling `Run` again on the same stream waits it out (the refusal schedule,
the server's `retryAfterMs`, a `Retry-After`, or the dropped-connection backoff): a loop
around `Run` cannot connect faster than the stream would on its own. Network errors
never contain your API key.

**Connection limits.** The API allows a small number of WebSocket connection attempts
per IP address per minute (every connect, reconnect and refused attempt counts) and
blocks the whole address, REST included, for 10 minutes when they are exceeded. Every
process behind one IP address shares that budget. Run one stream per API key, keep it
running, and do not restart it in a tight loop.

## Method names

| TypeScript | Go | Endpoint |
|---|---|---|
| `listEvents` | `ListEvents(ctx, sport, league)` | `GET /api/v1/{sport}/events` |
| `getOdds` | `GetOdds(ctx, sport, *OddsParams)` | `GET /api/v1/{sport}/odds` |
| `getMoneyline` | `GetMoneyline(ctx, sport, *OddsParams)` | `GET /api/v1/{sport}/moneyline` |
| `getSpreads` | `GetSpreads(ctx, sport, *OddsParams)` | `GET /api/v1/{sport}/spreads` |
| `getTotals` | `GetTotals(ctx, sport, *OddsParams)` | `GET /api/v1/{sport}/totals` |
| `getRealtime` | `GetRealtime(ctx, sport, league)` | `GET /api/v1/{sport}/realtime` |
| `getEsportsRealtime` | `GetEsportsRealtime(ctx, game, league)` | `GET /api/v1/{cs2,lol,valorant,dota2}/realtime` |
| `getPS3838Realtime` | `GetPS3838Realtime(ctx, sport, league)` | `GET /api/v1/{sport}/ps3838-realtime` |
| `getEV` | `GetEV(ctx, sport, *EVParams)` | `GET /api/v1/{sport}/ev` |
| `getEVLines` | `GetEVLines(ctx, sport, *EVLinesParams)` | `GET /api/v1/{sport}/ev?market=spreads,totals` |
| `getFair` | `GetFair(ctx, sport)` | `GET /api/v1/{sport}/fair` |
| `getOneXBetSoccer` | `GetOneXBetSoccer(ctx)` | `GET /api/v1/1xbet/soccer` |
| `getProphetxOdds` | `GetProphetxOdds(ctx, ProphetXOddsParams)` | `GET /api/v1/prophetx/odds` |
| `getSchedule` | `GetSchedule(ctx, sport)` | `GET /api/v1/{sport}/schedule` |
| `getDaySchedule` | `GetDaySchedule(ctx, DayScheduleParams)` | `GET /api/v1/schedule` |
| `getResults` | `GetResults(ctx, sport)` | `GET /api/v1/{sport}/results` |
| `getSplits` | `GetSplits(ctx, sport)` | `GET /api/v1/{sport}/splits` |
| `getScores` | `GetScores(ctx, sport)` | `GET /api/v1/scores/live` (sport `""`), `GET /api/v1/{sport}/scores/live` |
| `getPlays` | `GetPlays(ctx, sport, eventID, *PlaysParams)` | `GET /api/v1/{sport}/plays` |
| `getInjuries` | `GetInjuries(ctx, sport, *InjuriesParams)` | `GET /api/v1/{sport}/injuries` |
| `normalize` | `Normalize(ctx, name, sport)` | `GET /api/v1/normalize` |
| `normalizeBatch` | `NormalizeBatch(ctx, names, sport)` | `GET /api/v1/normalize/batch` |
| `getProps` | `GetProps(ctx, sport, *PropsParams)` | `GET /api/v1/{sport}/props` |
| `getBookProps` | `GetBookProps(ctx, sport, book, *PropsParams)` | `GET /api/v1/{sport}/props/{book}` |
| `getPropsEV` | `GetPropsEV(ctx, sport, *PropsEVParams)` | `GET /api/v1/{sport}/props/ev` |
| `getPropsHistory` | `GetPropsHistory(ctx, sport, PropsHistoryParams)` | `GET /api/v1/{sport}/props/history` |
| `getPropResults` | `GetPropResults(ctx, sport, PropResultsParams)` | `GET /api/v1/{sport}/props/results` |
| `getPropTrends` | `GetPropTrends(ctx, sport, PropTrendsParams)` | `GET /api/v1/{sport}/props/trends` |
| `getPropsStats` | `GetPropsStats(ctx)` | `GET /api/v1/props/stats` |
| `getBookPropsStats` | `GetBookPropsStats(ctx, book)` | `GET /api/v1/props/{book}/stats` |
| `getStats` | `GetStats(ctx, sport, date)` | `GET /api/v1/{sport}/stats` |
| `getMatchStats` | `GetMatchStats(ctx, sport, eventID)` | `GET /api/v1/{sport}/stats/match` |
| `getH2H` | `GetH2H(ctx, sport, eventID)` | `GET /api/v1/{sport}/stats/h2h` |
| `getPlayerAverages` | `GetPlayerAverages(ctx, sport, PlayerAveragesParams)` | `GET /api/v1/{sport}/stats/averages` |
| `getOddsHistory` | `GetOddsHistory(ctx, OddsHistoryParams)` | `GET /api/odds/history` |
| `getMoneylineHistory` | `GetMoneylineHistory(ctx, LineHistoryParams)` | `GET /api/odds/history`, market `h2h` |
| `getSpreadHistory` | `GetSpreadHistory(ctx, LineHistoryParams)` | `GET /api/odds/history`, market `spreads` |
| `getTotalsHistory` | `GetTotalsHistory(ctx, LineHistoryParams)` | `GET /api/odds/history`, market `totals` |
| `getHistoryCoverage` | `GetHistoryCoverage(ctx)` | `GET /api/v1/history/coverage` |
| `getHistoryGames` | `GetHistoryGames(ctx, *HistoryGamesParams)` | `GET /api/v1/history/games` |
| `getHistoryOdds` | `GetHistoryOdds(ctx, HistoryOddsParams)` | `GET /api/v1/history/odds` |
| `getHistoryProps` | `GetHistoryProps(ctx, HistoryPropsParams)` | `GET /api/v1/history/props` |
| `iterHistoryOdds` | `IterHistoryOdds(ctx, HistoryOddsParams, *PageOptions)` | `GET /api/v1/history/odds`, paged |
| `iterHistoryProps` | `IterHistoryProps(ctx, HistoryPropsParams, *PageOptions)` | `GET /api/v1/history/props`, paged |
| `getHistoryStats` | `GetHistoryStats(ctx, *HistoryStatsParams)` | `GET /api/v1/history/stats` |
| `getHistoryTennisStats` | `GetHistoryTennisStats(ctx, eventID)` | `GET /api/v1/history/tennis-stats` |
| `getGameStatsDetail` | `GetGameStatsDetail(ctx, eventID)` | `GET /api/v1/history/game-stats-detail` |
| `getClosingOdds` | `GetClosingOdds(ctx, *ClosingOddsParams)` | `GET /api/v1/history/closing-odds` |
| `getHistoricalPlayerProps` | `GetHistoricalPlayerProps(ctx, *HistoricalPlayerPropsParams)` | `GET /api/v1/history/player-props` |
| `getPublicBetting` | `GetPublicBetting(ctx, *PublicBettingParams)` | `GET /api/v1/history/public-betting` |
| `getHistorySplits` | `GetHistorySplits(ctx, HistorySplitsParams)` | `GET /api/v1/history/splits` |
| `getCS2Matches` | `GetCS2Matches(ctx, *CS2MatchesParams)` | `GET /api/v1/history/cs2/matches` |
| `getCS2Match` | `GetCS2Match(ctx, matchID)` | `GET /api/v1/history/cs2/matches/{matchId}` |
| `getCS2Players` | `GetCS2Players(ctx, CS2PlayersParams)` | `GET /api/v1/history/cs2/players` |
| `getHardRockLeagues` | `GetHardRockLeagues(ctx, state, sport)` | `GET /api/v2/hardrock/{state}/{sport}/leagues` |
| `getHardRockEvents` | `GetHardRockEvents(ctx, state, sport, league)` | `GET /api/v2/hardrock/{state}/{sport}` |
| `getHardRockLadder` | `GetHardRockLadder(ctx)` | `GET /api/v2/hardrock/ladder` |
| `getStakeBets` | `GetStakeBets(ctx)` | `GET /api/v2/stake/bets` |
| `get<Book>V2` | `GetV2(ctx, book, sport, league)`, `GetV2Markets(ctx, book, sport, league, marketIds)` | `GET /api/v2/{book}/{sport}` |
| `get<Book>V2Leagues` | `GetV2Leagues(ctx, book, sport)` | `GET /api/v2/{book}/{sport}/leagues` |
| `getSgpEvents` | `GetSgpEvents(ctx, sport, book)` | `GET /api/v1/{sport}/sgp/events` |
| `buildSgp` | `BuildSgp(ctx, sport, SgpBuildParams)` | `POST /api/v1/{sport}/sgp/build` |
| `compareSgp` | `CompareSgp(ctx, sport, SgpCompareParams)` | `POST /api/v1/{sport}/sgp/compare` |
| `compareSgpCombo` | `CompareSgpCombo(ctx, SgpComboCompareParams)` | `POST /api/v1/sgp/combo-compare` |
| `batchSgp` | `BatchSgp(ctx, sport, SgpBatchParams)` | `POST /api/v1/{sport}/sgp/batch` |
| `getSgpHistory` | `GetSgpHistory(ctx, sport, SgpHistoryParams)` | `GET /api/v1/{sport}/sgp/history` |
| `createWebhook` | `CreateWebhook(ctx, CreateWebhookParams)` | `POST /api/v1/webhooks` |
| `listWebhooks` | `ListWebhooks(ctx)` | `GET /api/v1/webhooks` |
| `getWebhook` | `GetWebhook(ctx, id)` | `GET /api/v1/webhooks/{id}` |
| `updateWebhook` | `UpdateWebhook(ctx, id, UpdateWebhookParams)` | `PATCH /api/v1/webhooks/{id}` |
| `deleteWebhook` | `DeleteWebhook(ctx, id)` | `DELETE /api/v1/webhooks/{id}` |
| `testWebhook` | `TestWebhook(ctx, id)` | `POST /api/v1/webhooks/{id}/test` |
| `rotateWebhookSecret` | `RotateWebhookSecret(ctx, id, *RotateWebhookSecretParams)` | `POST /api/v1/webhooks/{id}/rotate-secret` |
| `listWebhookDeliveries` | `ListWebhookDeliveries(ctx, id, *WebhookDeliveriesParams)` | `GET /api/v1/webhooks/{id}/deliveries` |
| `verifyWebhookSignature` | `VerifyWebhookSignature(payload, header, secret)` | checks a delivery's `Owls-Signature` |

Four methods answer with one of several shapes and return a small result struct with
exactly one field set: `GetScores` (`All` or `Sport`), `GetBookProps` (`Props`, or
`BetMGM` for that book), `GetPropResults` (`Game`, or `Day` when you pass only a
date) and `GetPlays` (`Game`, or `Games` for an empty event id). The bet365 props were retired: `GetBookProps` for `bet365` returns an
`*owls.APIError` with status 410. Bet365 game lines are on `GetV2`.

## Development

```
make conformance       # npm ci in conformance/: the WebSocket test server's socket.io
go test ./...          # unit tests, WebSocket conformance tests, drift checks
go test -race ./...
make test              # make conformance, then go test ./... with the WebSocket tests required
go generate ./...      # regenerate models_gen.go from spec/openapi.json
```

The WebSocket tests start `conformance/server.mjs`, a local server on the same
socket.io version (4.8.1, engine.io 6.6.4, ws 8.17.1) and transport settings as the
API, so they need Node.js. The server loads socket.io from `$OWLS_API_REPO/node_modules`
when that is set, else from `conformance/node_modules` (pinned by
`conformance/package-lock.json`; `make conformance` or `npm ci` in `conformance/`
installs it), else from a sibling `../nba-odds-app` checkout. Without node or socket.io
the WebSocket tests are skipped with a message, except under CI (`CI` set) or with
`OWLS_REQUIRE_WS_SERVER_TESTS=1`, where they fail.

The drift tests compare the SDK with the API manifest (`OWLS_API_MANIFEST`, else
`../nba-odds-app/docs/api-manifest.json`) and the vendored spec with its source
(`OWLS_OPENAPI_DIR`, else `../owls-insight-sdk-js/openapi`), and are skipped with a
message when those are missing.

The live tests call the production API and are off unless `OWLS_INSIGHT_LIVE=1` and
`OWLS_INSIGHT_API_KEY` are set. Run them at most once a minute.

## License

MIT
