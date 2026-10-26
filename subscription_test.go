package owls

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSubscriptionMarshal(t *testing.T) {
	for _, tc := range []struct {
		name string
		sub  Subscription
		want string
	}{
		{"nil fields are not sent", Subscription{}, `{}`},
		{"CONTROL: set fields are sent", Subscription{Sports: []string{"nba"}, Alternates: Ptr(true), ProphetX: false},
			`{"sports":["nba"],"alternates":true,"prophetx":false}`},
		{"empty lists are sent empty", Subscription{Sports: []string{}, Books: []string{}, EventIDs: []string{}},
			`{"sports":[],"books":[],"eventIds":[]}`},
		{"empty maps are sent empty", Subscription{V2: map[string]map[string]any{}, EsportsRealtime: map[string]EsportsRealtimeFilter{}},
			`{"esportsRealtime":{},"v2":{}}`},
		{"every field", Subscription{
			Sports: []string{"mlb"}, Books: []string{"pinnacle"}, EventIDs: []string{"e1"}, Alternates: Ptr(false),
			ExcludeExchanges: Ptr(true), Esports: []string{"cs2"},
			EsportsRealtime: map[string]EsportsRealtimeFilter{"cs2": {League: "BLAST"}},
			ProphetX:        map[string][]string{"sports": {"basketball"}},
			V2:              map[string]map[string]any{"fanduel": {"mlb": "*"}, "propbuilder": {"nfl": "*"}}, OneXBetSoccer: Ptr(true),
			Scores: []string{"nfl"}, Plays: []string{"nfl", "nba"}, Realtime: &RealtimeFeeds{PS3838: Ptr(false)},
		}, `{"sports":["mlb"],"books":["pinnacle"],"eventIds":["e1"],"alternates":false,"exclude_exchanges":true,
			"esports":["cs2"],"esportsRealtime":{"cs2":{"league":"BLAST"}},"prophetx":{"sports":["basketball"]},
			"v2":{"fanduel":{"mlb":"*"},"propbuilder":{"nfl":"*"}},"oneXBetSoccer":true,"scores":["nfl"],
			"plays":["nfl","nba"],"realtime":{"ps3838":false}}`},
		{"the realtime opt-out is sent as a literal false", Subscription{Realtime: &RealtimeFeeds{PS3838: Ptr(false)}},
			`{"realtime":{"ps3838":false}}`},
		{"an empty realtime object is sent empty (both events again)", Subscription{Realtime: &RealtimeFeeds{}},
			`{"realtime":{}}`},
		{"scores: true for every sport", Subscription{Scores: true, Sports: []string{"nfl"}},
			`{"sports":["nfl"],"scores":true}`},
		{"plays: true for every play-by-play sport", Subscription{Plays: true}, `{"plays":true}`},
		{"the Fanatics Markets order books ride in the v2 map",
			Subscription{V2: map[string]map[string]any{"fanaticsmarkets": {"nfl": "*", "books": true}}},
			`{"v2":{"fanaticsmarkets":{"nfl":"*","books":true}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.sub)
			if err != nil {
				t.Fatal(err)
			}
			if !jsonEqual(t, got, json.RawMessage(tc.want)) {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
			// A pointer marshals the same way.
			if p, _ := json.Marshal(&tc.sub); string(p) != string(got) {
				t.Fatalf("pointer: %s", p)
			}
		})
	}
	// CONTROL: the same empty list through a plain omitempty field is dropped,
	// which is what MarshalJSON exists to prevent.
	type plain Subscription
	if b, _ := json.Marshal(plain(Subscription{EventIDs: []string{}})); string(b) != `{}` {
		t.Fatalf("control: omitempty sent %s", b)
	}
}

func TestMergeSubscriptionClearsWithEmpty(t *testing.T) {
	stored, _ := json.Marshal(Subscription{Sports: []string{"nba"}, EventIDs: []string{"e1"}})
	patch, _ := json.Marshal(Subscription{EventIDs: []string{}})
	merged, err := mergeSubscription(stored, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, merged, json.RawMessage(`{"sports":["nba"],"eventIds":[]}`)) {
		t.Fatalf("merged %s", merged)
	}
	// CONTROL: a nil field leaves the stored one alone.
	patch, _ = json.Marshal(Subscription{Books: []string{"x"}})
	if merged, _ = mergeSubscription(stored, patch); !jsonEqual(t, merged, json.RawMessage(`{"sports":["nba"],"eventIds":["e1"],"books":["x"]}`)) {
		t.Fatalf("merged %s", merged)
	}
}

func TestMergeSubscriptionReplacesRealtimeWhole(t *testing.T) {
	stored, _ := json.Marshal(Subscription{Sports: []string{"nba"}, Realtime: &RealtimeFeeds{PS3838: Ptr(false)}})
	// A patch without Realtime keeps the opt-out (it is re-sent on every reconnect).
	patch, _ := json.Marshal(Subscription{Books: []string{"pinnacle"}})
	merged, err := mergeSubscription(stored, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, merged, json.RawMessage(`{"sports":["nba"],"books":["pinnacle"],"realtime":{"ps3838":false}}`)) {
		t.Fatalf("merged %s", merged)
	}
	// An empty Realtime replaces the whole object, as the server does: both events again.
	patch, _ = json.Marshal(Subscription{Realtime: &RealtimeFeeds{}})
	if merged, _ = mergeSubscription(merged, patch); !jsonEqual(t, merged, json.RawMessage(`{"sports":["nba"],"books":["pinnacle"],"realtime":{}}`)) {
		t.Fatalf("merged %s", merged)
	}
}

func TestDecode(t *testing.T) {
	// A server-notice with a new code, an unknown field and a mistyped one.
	raw := json.RawMessage(`{"code": "SOMETHING_NEW", "message": "hello", "brandNew": {"x": 1},
		"details": {"closeCode": "not-a-number", "ageSeconds": 5}}`)
	n, err := Decode[ServerNotice](raw)
	if err != nil {
		t.Fatal(err)
	}
	if n.Code == nil || *n.Code != "SOMETHING_NEW" || n.Message == nil || *n.Message != "hello" ||
		n.Details == nil || n.Details.CloseCode != nil || n.Details.AgeSeconds == nil || *n.Details.AgeSeconds != 5 {
		t.Fatalf("decoded %+v", n)
	}
	// CONTROL: encoding/json alone fails on the same payload.
	var strict ServerNotice
	if json.Unmarshal(raw, &strict) == nil {
		t.Fatal("control: the payload is not mistyped for ServerNotice; pick another field")
	}
	for _, bad := range []string{`"a string"`, `[1]`, `{"code": `} {
		if _, err := Decode[ServerNotice](json.RawMessage(bad)); err == nil {
			t.Errorf("Decode(%s) succeeded", bad)
		}
	}
}

// subscriptionKeyMismatch reports when value, fully set, does not send exactly the
// properties the spec gives schema.
func subscriptionKeyMismatch(t *testing.T, schemas map[string]map[string]json.RawMessage, schema string, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatal(err)
	}
	if got, want := keys(sent), keys(schemas[schema]); !reflect.DeepEqual(got, want) {
		return fmt.Sprintf("%s sends %v, the spec's %s has %v", reflect.TypeOf(value).Name(), got, schema, want)
	}
	return ""
}

// The hand-written subscription inputs send exactly the properties the spec gives
// SubscribeOptions and PropsSubscribeOptions, so an option the API adds (plays, in
// 0.86.0) cannot go missing from Go.
func TestSubscriptionMatchesTheSpec(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("spec", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := map[string]map[string]json.RawMessage{}
	for name, s := range spec.Components.Schemas {
		schemas[name] = s.Properties
	}
	full := Subscription{
		Sports: []string{}, Books: []string{}, EventIDs: []string{}, Alternates: Ptr(true), ExcludeExchanges: Ptr(true),
		Esports: true, EsportsRealtime: map[string]EsportsRealtimeFilter{}, ProphetX: true, V2: map[string]map[string]any{},
		OneXBetSoccer: Ptr(true), Scores: true, Plays: true, Realtime: &RealtimeFeeds{},
	}
	props := PropsFilter{Sports: []string{"nba"}, Games: []string{"g"}, Categories: []string{"points"}}
	for _, bad := range []string{
		subscriptionKeyMismatch(t, schemas, "SubscribeOptions", full),
		subscriptionKeyMismatch(t, schemas, "PropsSubscribeOptions", props),
	} {
		if bad != "" {
			t.Error(bad)
		}
	}
	// CONTROL: a field left unset, and a property the spec gains, are reported.
	full.Plays = nil
	if subscriptionKeyMismatch(t, schemas, "SubscribeOptions", full) == "" {
		t.Error("control: a missing plays was not reported")
	}
	gained := map[string]map[string]json.RawMessage{"PropsSubscribeOptions": {"sports": nil, "games": nil, "categories": nil, "books": nil}}
	if subscriptionKeyMismatch(t, gained, "PropsSubscribeOptions", props) == "" {
		t.Error("control: a property the spec gained was not reported")
	}
}
