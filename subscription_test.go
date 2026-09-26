package owls

import (
	"encoding/json"
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
			V2:              map[string]map[string]any{"fanduel": {"mlb": "*"}}, OneXBetSoccer: Ptr(true),
		}, `{"sports":["mlb"],"books":["pinnacle"],"eventIds":["e1"],"alternates":false,"exclude_exchanges":true,
			"esports":["cs2"],"esportsRealtime":{"cs2":{"league":"BLAST"}},"prophetx":{"sports":["basketball"]},
			"v2":{"fanduel":{"mlb":"*"}},"oneXBetSoccer":true}`},
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
