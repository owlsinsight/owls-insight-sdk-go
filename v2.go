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

// GetV2Leagues returns the league index of a v2 book and sport:
// GET /api/v2/{book}/{sport}/leagues.
func (c *Client) GetV2Leagues(ctx context.Context, book, sport string) (*V2LeaguesResponse, error) {
	return getInto[V2LeaguesResponse](ctx, c, "/api/v2/"+esc(book)+"/"+esc(sport)+"/leagues", url.Values(nil))
}

// V2EventName returns the WebSocket event a v2 book's deltas arrive on:
// "<book>-v2-update", except BetOnline, whose event predates the convention and
// is "betonline-realtime". Subscribe with Subscription.V2. Every one of them
// decodes into V2Update.
func V2EventName(book string) string {
	if book == "betonline" {
		return "betonline-realtime"
	}
	return book + "-v2-update"
}
