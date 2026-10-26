package owls

import (
	"context"
	"net/url"
)

// GetV2 returns a v2 book's raw markets for a sport: GET /api/v2/{book}/{sport}.
// Data holds each market exactly as the book published it, keyed by market id.
// Multi-league sports need a league key (see GetV2Leagues); leave league empty
// for the others. Every v2 book goes through this one method.
func (c *Client) GetV2(ctx context.Context, book, sport, league string) (*V2BookResponse, error) {
	return getInto[V2BookResponse](ctx, c, "/api/v2/"+esc(book)+"/"+esc(sport), leagueQuery(league))
}

// GetV2Markets is GetV2 with a marketIds filter: a comma-separated list of market
// ids and prefixes ending in *, for example "props-*", "main,props-*" or "*" for
// every entry. Only those entries are returned (and dated and hashed into the
// ETag); Meta.MarketIds echoes the filter and Meta.MarketsFilteredOut counts what
// it left out. It is also how the entries a book leaves out by default are asked
// for (on a default request Meta.DefaultExcluded says how many were left out and
// the marketIds value that includes them):
//   - DraftKings stores its US-sport player props one entry per subcategory,
//     "props-<subcategoryId>": GetV2Markets(ctx, "draftkings", "nfl", "", "props-*").
//   - Fanatics Markets keeps its live order books as "books-<eventId>-<group>"
//     entries: GetV2Markets(ctx, "fanaticsmarkets", "nfl", "", "books-*").
//   - Polymarket US ("polymarketus") serves an event's full market catalog as
//     "full-<eventSlug>": GetV2Markets(ctx, "polymarketus", "nfl", "nfl", "full-*").
//
// Prop Builder ("propbuilder") keys its entries family first, so
// GetV2Markets(ctx, "propbuilder", "nfl", "", "ou-*,ss-*") returns every Over/Under
// and N+ board of the sport. An empty marketIds behaves like GetV2.
// Books with their own routes (bet365, hardrock, mybookie, stake, thunderpick,
// underdog) ignore it and return their whole board.
func (c *Client) GetV2Markets(ctx context.Context, book, sport, league, marketIds string) (*V2BookResponse, error) {
	q := leagueQuery(league)
	setStr(q, "marketIds", marketIds)
	return getInto[V2BookResponse](ctx, c, "/api/v2/"+esc(book)+"/"+esc(sport), q)
}

// GetV2Leagues returns the league index of a v2 book and sport:
// GET /api/v2/{book}/{sport}/leagues.
func (c *Client) GetV2Leagues(ctx context.Context, book, sport string) (*V2LeaguesResponse, error) {
	return getInto[V2LeaguesResponse](ctx, c, "/api/v2/"+esc(book)+"/"+esc(sport)+"/leagues", url.Values(nil))
}

// V2EventName returns the WebSocket event a v2 book's deltas arrive on:
// "<book>-v2-update", except BetOnline, whose event predates the convention and
// is "betonline-realtime". Subscribe with Subscription.V2. Every one of them
// decodes into V2Update. Fanatics Markets also has a second event for its live
// order books, EventFanaticsMarketsBooksV2Update.
func V2EventName(book string) string {
	if book == "betonline" {
		return "betonline-realtime"
	}
	return book + "-v2-update"
}
