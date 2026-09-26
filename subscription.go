package owls

import (
	"encoding/json"
)

// Subscription is what a [Stream] subscribes to. The stream keeps the last one
// it sent and sends it again, unchanged, on every connect.
//
// A nil field is not sent. A list or map that is empty but not nil is sent
// empty, which is how [Stream.UpdateSubscription] clears a filter:
// Subscription{EventIDs: []string{}} sends "eventIds": [] (no event filter) and
// Subscription{V2: map[string]map[string]any{}} sends "v2": {} (no v2 events).
type Subscription struct {
	// Sports for odds-update and the realtime events. Empty means every sport.
	Sports []string `json:"sports,omitempty"`
	// Books for odds-update and esports-update. Empty means every book; it does
	// not narrow the realtime events.
	Books []string `json:"books,omitempty"`
	// EventIDs limits odds-update to these events (OddsEvent.id).
	EventIDs []string `json:"eventIds,omitempty"`
	// Alternates includes alternate lines.
	Alternates *bool `json:"alternates,omitempty"`
	// ExcludeExchanges drops kalshi, polymarket and novig from Books.
	ExcludeExchanges *bool `json:"exclude_exchanges,omitempty"`
	// Esports is true for every esport, or a list such as []string{"cs2"}.
	Esports any `json:"esports,omitempty"`
	// EsportsRealtime opts in to <game>-realtime per esport, each with a
	// required league substring.
	EsportsRealtime map[string]EsportsRealtimeFilter `json:"esportsRealtime,omitempty"`
	// ProphetX is true for every prophetx-update, false for none, or a filter
	// such as map[string][]string{"sports": {"basketball"}, "kinds": {"prop"}}.
	ProphetX any `json:"prophetx,omitempty"`
	// V2 opts in to the v2 book events (see V2EventName): book, then sport,
	// then "*" or a list of league keys, for example
	// map[string]map[string]any{"fanduel": {"mlb": "*"}}.
	V2 map[string]map[string]any `json:"v2,omitempty"`
	// OneXBetSoccer opts in to 1xbet-update.
	OneXBetSoccer *bool `json:"oneXBetSoccer,omitempty"`
}

// MarshalJSON sends a list or map that is empty but not nil as [] or {}, so that
// an update can clear it; omitempty alone would drop it.
func (s Subscription) MarshalJSON() ([]byte, error) {
	type plain Subscription // no MarshalJSON: avoids recursion
	list := func(v []string) *[]string {
		if v == nil {
			return nil
		}
		return &v
	}
	w := struct {
		plain
		// These shadow the embedded fields of the same JSON name.
		Sports          *[]string                         `json:"sports,omitempty"`
		Books           *[]string                         `json:"books,omitempty"`
		EventIDs        *[]string                         `json:"eventIds,omitempty"`
		EsportsRealtime *map[string]EsportsRealtimeFilter `json:"esportsRealtime,omitempty"`
		V2              *map[string]map[string]any        `json:"v2,omitempty"`
	}{plain: plain(s), Sports: list(s.Sports), Books: list(s.Books), EventIDs: list(s.EventIDs)}
	if s.EsportsRealtime != nil {
		w.EsportsRealtime = &s.EsportsRealtime
	}
	if s.V2 != nil {
		w.V2 = &s.V2
	}
	return json.Marshal(w)
}

// EsportsRealtimeFilter is the league filter of one esport's realtime stream.
type EsportsRealtimeFilter struct {
	// League is a substring of the Pinnacle league name, for example "BLAST".
	League string `json:"league"`
}

// PropsFilter narrows a props subscription. Empty fields mean everything.
type PropsFilter struct {
	Sports     []string `json:"sports,omitempty"`
	Games      []string `json:"games,omitempty"`
	Categories []string `json:"categories,omitempty"`
}

// mergeSubscription shallow-merges patch into stored, as the server merges an
// update-subscription: each top-level field of patch replaces the stored one.
func mergeSubscription(stored, patch json.RawMessage) (json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	if len(stored) > 0 {
		if err := json.Unmarshal(stored, &m); err != nil {
			return nil, err
		}
	}
	var p map[string]json.RawMessage
	if err := json.Unmarshal(patch, &p); err != nil {
		return nil, err
	}
	for k, v := range p {
		m[k] = v
	}
	return json.Marshal(m)
}

// propsEvents returns the subscribe and unsubscribe events of a book's props
// stream ("" or "pinnacle" is the Pinnacle stream).
func propsEvents(book string) (subscribe, unsubscribe string) {
	if book == "" || book == "pinnacle" {
		return "subscribe-props", "unsubscribe-props"
	}
	return "subscribe-" + book + "-props", "unsubscribe-" + book + "-props"
}

// propsAckEvent is the acknowledgement of a props subscribe event:
// subscribe-props -> props-subscribed, subscribe-fanduel-props -> fanduel-props-subscribed.
func propsAckEvent(subscribe string) string {
	if subscribe == "subscribe-props" {
		return "props-subscribed"
	}
	book := subscribe[len("subscribe-") : len(subscribe)-len("-props")]
	return book + "-props-subscribed"
}
