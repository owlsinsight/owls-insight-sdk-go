package sio

import (
	"encoding/json"
	"net/url"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		frame string
		kind  Kind
		name  string
		data  string
	}{
		{`0{"sid":"abc","upgrades":[],"pingInterval":25000,"pingTimeout":120000,"maxPayload":1000000}`, KindOpen, "", ""},
		{`2`, KindPing, "", ""},
		{`1`, KindClose, "", ""},
		{`6`, KindOther, "", ""},
		{`40{"sid":"s1"}`, KindConnect, "", `{"sid":"s1"}`},
		{`44{"message":"Invalid API key","data":{"code":"INVALID_API_KEY","retryable":false}}`, KindConnectError, "", `{"message":"Invalid API key","data":{"code":"INVALID_API_KEY","retryable":false}}`},
		{`41`, KindDisconnect, "", `null`},
		{`42["odds-update",{"sports":{}}]`, KindEvent, "odds-update", `{"sports":{}}`},
		{`42["server-heartbeat"]`, KindEvent, "server-heartbeat", `null`},
		{`42["two",1,2]`, KindEvent, "two", `1`},
		{`42/,["slash",true]`, KindEvent, "slash", `true`},
		{`4217["with-ack-id","x"]`, KindEvent, "with-ack-id", `"x"`},
		// CONTROL: another namespace is not ours, and acks and binary are ignored.
		{`42/admin,["elsewhere",1]`, KindOther, "", ""},
		{`43["ack"]`, KindOther, "", ""},
		{`451-["bin",{"_placeholder":true,"num":0}]`, KindOther, "", ""},
	}
	for _, tc := range cases {
		p, err := Parse([]byte(tc.frame))
		if err != nil {
			t.Errorf("Parse(%s): %v", tc.frame, err)
			continue
		}
		if p.Kind != tc.kind || p.Name != tc.name || (tc.data != "" && string(p.Data) != tc.data) {
			t.Errorf("Parse(%s) = kind %d name %q data %s", tc.frame, p.Kind, p.Name, p.Data)
		}
	}
	p, _ := Parse([]byte(cases[0].frame))
	if p.Open != (Open{SID: "abc", PingInterval: 25000, PingTimeout: 120000, MaxPayload: 1000000}) {
		t.Errorf("open = %+v", p.Open)
	}
	for _, bad := range []string{``, `4`, `42`, `42[]`, `42[1]`, `42{"a":1}`, `0{`} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("Parse(%q) accepted a malformed frame", bad)
		}
	}
}

func TestFrames(t *testing.T) {
	f, err := EventFrame("subscribe", json.RawMessage(`{"sports":["nba"]}`))
	if err != nil || string(f) != `42["subscribe",{"sports":["nba"]}]` {
		t.Errorf("EventFrame = %s %v", f, err)
	}
	if f, _ := EventFrame("unsubscribe-props", nil); string(f) != `42["unsubscribe-props"]` {
		t.Errorf("EventFrame without argument = %s", f)
	}
	if _, err := EventFrame("x", json.RawMessage(`{bad`)); err == nil {
		t.Error("invalid JSON argument accepted")
	}
	f, _ = ConnectFrame(map[string]any{"sdk": "owls-insight-go/0.1.0"})
	if string(f) != `40{"sdk":"owls-insight-go/0.1.0"}` {
		t.Errorf("ConnectFrame = %s", f)
	}
	// An encoded event parses back to the same name and argument.
	f, _ = EventFrame("e", json.RawMessage(`[1,{"a":"b"}]`))
	if p, err := Parse(f); err != nil || p.Name != "e" || string(p.Data) != `[1,{"a":"b"}]` {
		t.Errorf("round trip: %+v %v", p, err)
	}
}

func TestSocketURL(t *testing.T) {
	u, err := SocketURL("https://api.example.test/", "k&y=1 +")
	if err != nil {
		t.Fatal(err)
	}
	pu, _ := url.Parse(u)
	q := pu.Query()
	if pu.Path != "/socket.io/" || q.Get("EIO") != "4" || q.Get("transport") != "websocket" || q.Get("apiKey") != "k&y=1 +" || len(q) != 3 {
		t.Errorf("SocketURL = %s", u)
	}
}
