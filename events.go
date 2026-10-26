package owls

// Server events. Any name works with [Stream.On]; these are the fixed ones.
const (
	EventOddsUpdate           = "odds-update"
	EventEsportsUpdate        = "esports-update"
	EventPinnacleRealtime     = "pinnacle-realtime"
	EventPinnacleRealtimeSync = "pinnacle-realtime-sync"
	EventPS3838Realtime       = "ps3838-realtime"
	EventServerHeartbeat      = "server-heartbeat"
	EventServerNotice         = "server-notice"
	EventProphetXUpdate       = "prophetx-update"
	EventOneXBetUpdate        = "1xbet-update"
	// EventScoresUpdate is live scores for one sport, payload ScoresUpdate: a full
	// live board about every 30 seconds and a delta within a few seconds of a
	// change. Opt in with Subscription.Scores.
	EventScoresUpdate = "scores-update"
	// EventPlaysUpdate is play-by-play for one sport (nba, nhl, nfl, ncaaf),
	// payload PlaysUpdate: within a few seconds of a new play, each game with the
	// plays new or amended since the previous message. There is no full message:
	// start from GetPlays and merge plays by ID. Opt in with Subscription.Plays.
	EventPlaysUpdate = "plays-update"
	// EventFanaticsMarketsBooksV2Update is the Fanatics Markets live order books,
	// payload V2Update: books-* entries, whole or patch (merge by symbol; null means
	// that book is gone), RemovedIds, and about once a minute the league's full list
	// of books entries. Opt in with "books": true next to the sports of the
	// fanaticsmarkets v2 subscription, for example
	// map[string]map[string]any{"fanaticsmarkets": {"nfl": "*", "books": true}}.
	// The events alone stay on V2EventName("fanaticsmarkets").
	EventFanaticsMarketsBooksV2Update = "fanaticsmarkets-books-v2-update"
	// EventSubscribed acknowledges subscribe and update-subscription with the
	// subscription in force.
	EventSubscribed = "subscribed"
	// EventError is the server's application-level error, payload WSError: a
	// props subscription the plan does not include (code TIER_REQUIRED), a
	// revoked key, a reduced connection limit, or an account that no longer
	// allows the connection (sent just before the server disconnects it). It is an
	// ordinary event, not a connection failure (see EventConnectError for a
	// refused connection).
	EventError = "error"
)

// Connection events, produced by the SDK and delivered through [Stream.On] in
// order with the server's events.
const (
	// EventConnect: a connection is open and the stored subscriptions were sent.
	// Payload {"sid": "..."}.
	EventConnect = "connect"
	// EventDisconnect: the connection closed. Payload {"reason": "..."}.
	EventDisconnect = "disconnect"
	// EventConnectError: the server refused a connection. Payload is the
	// server's {"message": "...", "data": {"code", "retryable", ...}}.
	EventConnectError = "connect_error"
)

// Per-book and per-game event families the server derives its names from.
var (
	// esportsRealtimeGames have a <game>-realtime event (Subscription.EsportsRealtime).
	esportsRealtimeGames = []string{"cs2", "valorant", "lol", "dota2"}
	// propsStreamBooks have a props stream: pinnacle's is player-props-update,
	// the others <book>-props-update (Stream.SubscribeProps).
	propsStreamBooks = []string{"pinnacle", "bet365", "fanduel", "draftkings", "betmgm", "caesars"}
	// v2StreamBooks are the v2 books with a WebSocket event (V2EventName).
	v2StreamBooks = []string{
		"draftkings", "fanduel", "bet365", "mybookie", "kalshi", "polymarket", "thunderpick",
		"pinnacle", "versus", "betonline", "lowvig", "betrivers", "fanaticsmarkets", "betus",
		"bet105", "4casters", "tippmixpro", "ballybet", "propbuilder",
	}
)

// knownEvents is every server event name the SDK knows of.
func knownEvents() map[string]bool {
	known := map[string]bool{}
	for _, n := range []string{
		EventOddsUpdate, EventEsportsUpdate, EventPinnacleRealtime, EventPinnacleRealtimeSync,
		EventPS3838Realtime, EventServerHeartbeat, EventServerNotice, EventProphetXUpdate,
		EventOneXBetUpdate, EventScoresUpdate, EventPlaysUpdate, EventFanaticsMarketsBooksV2Update,
		EventSubscribed, EventError,
	} {
		known[n] = true
	}
	for _, g := range esportsRealtimeGames {
		known[g+"-realtime"] = true
	}
	for _, b := range propsStreamBooks {
		if b == "pinnacle" {
			known["player-props-update"] = true
		} else {
			known[b+"-props-update"] = true
		}
	}
	for _, b := range v2StreamBooks {
		known[V2EventName(b)] = true
	}
	return known
}
