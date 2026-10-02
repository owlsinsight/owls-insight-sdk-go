package owls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The signature check is held to the known-answer vector the API and every Owls
// Insight SDK share (testdata/fixtures/webhook-signature-vector.json, byte-identical
// to the API's copy, checked below when that copy is reachable), with the same
// rejections the API's own test makes. Every rejection has a CONTROL that passes
// with the one thing changed back.

type signatureVector struct {
	Secret             string `json:"secret"`
	PreviousSecret     string `json:"previous_secret"`
	Timestamp          int64  `json:"timestamp"`
	Payload            string `json:"payload"`
	Header             string `json:"header"`
	HeaderRotation     string `json:"header_rotation"`
	ExpectedV1         string `json:"expected_v1"`
	ExpectedV1Previous string `json:"expected_v1_previous"`
}

const vectorFile = "testdata/fixtures/webhook-signature-vector.json"

func loadVector(t *testing.T) signatureVector {
	t.Helper()
	b, err := os.ReadFile(vectorFile)
	if err != nil {
		t.Fatal(err)
	}
	var v signatureVector
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// at verifies with the vector's 300 s tolerance at unix second now.
func at(payload, header, secret string, now int64) bool {
	return VerifyWebhookSignatureAt([]byte(payload), header, secret, 300*time.Second, time.Unix(now, 0))
}

func TestWebhookVectorIsTheAPIs(t *testing.T) {
	candidates := []string{filepath.Join("..", "nba-odds-app", "tests", "fixtures", "webhook-signature-vector.json")}
	if env := os.Getenv("OWLS_API_REPO"); env != "" {
		candidates = append([]string{filepath.Join(env, "tests", "fixtures", "webhook-signature-vector.json")}, candidates...)
	}
	for _, p := range candidates {
		api, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		ours, err := os.ReadFile(vectorFile)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(api, ours) {
			t.Fatalf("%s differs from the API's %s: copy it", vectorFile, p)
		}
		return
	}
	t.Skipf("SKIPPED: the API's vector is not at %s. Set OWLS_API_REPO to the nba-odds-app checkout.", strings.Join(candidates, " or "))
}

func TestWebhookSignatureVector(t *testing.T) {
	v := loadVector(t)
	if got := ComputeWebhookSignature(v.Secret, v.Timestamp, []byte(v.Payload)); got != v.ExpectedV1 {
		t.Errorf("v1 %s, want %s", got, v.ExpectedV1)
	}
	if got := ComputeWebhookSignature(v.PreviousSecret, v.Timestamp, []byte(v.Payload)); got != v.ExpectedV1Previous {
		t.Errorf("previous v1 %s, want %s", got, v.ExpectedV1Previous)
	}
	if !at(v.Payload, v.Header, v.Secret, v.Timestamp) || !at(v.Payload, v.Header, v.Secret, v.Timestamp+299) {
		t.Error("the vector does not verify at its own timestamp")
	}
}

func TestWebhookSignatureRejections(t *testing.T) {
	v := loadVector(t)
	ts := v.Timestamp
	tsStr := "t=" + strconv.FormatInt(ts, 10)
	arabic := ""
	for _, c := range strconv.FormatInt(ts, 10) {
		arabic += string(rune(0x0660 + (c - '0')))
	}
	reserialized := func() string {
		var x any
		_ = json.Unmarshal([]byte(v.Payload), &x)
		b, _ := json.MarshalIndent(x, "", " ")
		return string(b)
	}()
	cases := []struct {
		name                    string
		payload, header, secret string
		now                     int64
		want                    bool
	}{
		{"replay: t too old", v.Payload, v.Header, v.Secret, ts + 301, false},
		{"replay: t in the future", v.Payload, v.Header, v.Secret, ts - 301, false},
		{"CONTROL: the edge of the tolerance", v.Payload, v.Header, v.Secret, ts + 300, true},
		{"CONTROL: the other edge", v.Payload, v.Header, v.Secret, ts - 300, true},
		{"t moved by an attacker", v.Payload, strings.Replace(v.Header, "t="+strconv.FormatInt(ts, 10), "t="+strconv.FormatInt(ts+100, 10), 1), v.Secret, ts + 100, false},
		{"CONTROL: the real t", v.Payload, v.Header, v.Secret, ts + 100, true},
		{"wrong secret", v.Payload, v.Header, v.Secret + "x", ts, false},
		{"the previous secret alone", v.Payload, v.Header, v.PreviousSecret, ts, false},
		{"one byte of the body changed", strings.Replace(v.Payload, "café", "cafe", 1), v.Header, v.Secret, ts, false},
		{"re-serialized JSON", reserialized, v.Header, v.Secret, ts, false},
		{"CONTROL: the raw body", v.Payload, v.Header, v.Secret, ts, true},
		{"empty header", v.Payload, "", v.Secret, ts, false},
		{"no t", v.Payload, "v1=" + v.ExpectedV1, v.Secret, ts, false},
		{"no v1", v.Payload, "t=" + strconv.FormatInt(ts, 10), v.Secret, ts, false},
		{"t not a number", v.Payload, "t=abc,v1=" + v.ExpectedV1, v.Secret, ts, false},
		{"v1 not hex", v.Payload, "t=" + strconv.FormatInt(ts, 10) + ",v1=zz", v.Secret, ts, false},
		{"t in other digits than ASCII", v.Payload, "t=" + arabic + ",v1=" + v.ExpectedV1, v.Secret, ts, false},
		{"CONTROL: whitespace, an unknown scheme and uppercase hex", v.Payload,
			"t=" + strconv.FormatInt(ts, 10) + ", v0=abc, v1=" + strings.ToUpper(v.ExpectedV1), v.Secret, ts, true},
		{"empty secret", v.Payload, v.Header, "", ts, false},
		{"rotation: the new secret", v.Payload, v.HeaderRotation, v.Secret, ts, true},
		{"rotation: the previous secret", v.Payload, v.HeaderRotation, v.PreviousSecret, ts, true},
		{"U+001F is not JavaScript whitespace", v.Payload, tsStr + "\u001f,v1=" + v.ExpectedV1, v.Secret, ts, false},
		{"U+0085 is not JavaScript whitespace", v.Payload, tsStr + "\u0085,v1=" + v.ExpectedV1, v.Secret, ts, false},
		{"CONTROL: U+FEFF is", v.Payload, "\ufefft=" + strconv.FormatInt(ts, 10) + ",v1=" + v.ExpectedV1, v.Secret, ts, true},
		{"CONTROL: U+3000 is", v.Payload, tsStr + "\u3000,v1=" + v.ExpectedV1, v.Secret, ts, true},
		{"the last valid t wins", v.Payload, "t=1," + tsStr + ",t=abc,v1=" + v.ExpectedV1, v.Secret, ts, true},
		{"CONTROL: so a later valid t replaces the right one", v.Payload, tsStr + ",t=1,v1=" + v.ExpectedV1, v.Secret, ts, false},
		{"a t of 13 digits is not a t", v.Payload, "t=1767225600000,v1=" + v.ExpectedV1, v.Secret, 1767225600000, false},
	}
	for _, c := range cases {
		if got := at(c.payload, c.header, c.secret, c.now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if VerifyWebhookSignatureAt([]byte(v.Payload), v.Header, v.Secret, -time.Second, time.Unix(ts, 0)) {
		t.Error("a negative tolerance verified")
	}
	if !VerifyWebhookSignatureAt([]byte(v.Payload), v.Header, v.Secret, 0, time.Unix(ts, 0)) {
		t.Error("CONTROL: a zero tolerance at the exact second failed")
	}
	got := ParseWebhookSignature(v.HeaderRotation)
	if !got.HasTimestamp || got.Timestamp != ts || !reflect.DeepEqual(got.V1, []string{v.ExpectedV1, v.ExpectedV1Previous}) {
		t.Errorf("parsed %+v", got)
	}
}

func TestWebhookSignatureDefaultTolerance(t *testing.T) {
	v := loadVector(t)
	if DefaultWebhookTolerance != 300*time.Second || WebhookSignatureHeader != "Owls-Signature" {
		t.Fatal("constants changed")
	}
	now := time.Now().Unix()
	for age, want := range map[int64]bool{290: true, 310: false} {
		header := "t=" + strconv.FormatInt(now-age, 10) + ",v1=" + ComputeWebhookSignature(v.Secret, now-age, []byte(v.Payload))
		if got := VerifyWebhookSignature([]byte(v.Payload), header, v.Secret); got != want {
			t.Errorf("a delivery %d s old: verified %v, want %v", age, got, want)
		}
	}
}

const lineMovedBody = `{"id":"evt_aaaaaaaaaaaaaaaaaaaaaaaaaa","type":"line.moved","api_version":"2026-10-01",
"created":"2026-10-02T17:00:01.000Z","occurred_at":"2026-10-02T17:00:00.000Z","expires_at":"2026-10-02T17:05:00.000Z",
"endpoint_id":"whk_aaaaaaaaaaaaaaaaaaaaaaaa","data":{"sport":"nba","league":"NBA",
"event_id":"nba:Boston Celtics@New York Knicks-20261002","source_event_id":"1600000000",
"home_team":"New York Knicks","away_team":"Boston Celtics","commence_time":"2026-10-02T23:30:00.000Z",
"book":"pinnacle","market":"spread","reason":"point",
"previous":{"home":{"price":-110,"point":-3.5},"away":{"price":-110,"point":3.5}},
"current":{"home":{"price":-108,"point":-4.5},"away":{"price":-112,"point":4.5}},
"max_probability_change_pp":0.48,"point_change":-1,"thresholds":{"price_step_pp":2,"point_step":1},"future_field":true}}`

func TestParseWebhookEvent(t *testing.T) {
	v := loadVector(t)
	ev, err := ParseWebhookEvent([]byte(lineMovedBody))
	if err != nil {
		t.Fatal(err)
	}
	lm, ok := ev.(*WebhookLineMovedEvent)
	if !ok {
		t.Fatalf("got %T", ev)
	}
	if *lm.Data.Market != "spread" || *lm.Data.Current.Home.Point != -4.5 || *lm.Data.Previous.Away.Price != -110 ||
		*lm.Data.Thresholds.PointStep != 1 || *lm.ID != "evt_aaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("decoded %+v", lm.Data)
	}
	if ev.EventID() != "evt_aaaaaaaaaaaaaaaaaaaaaaaaaa" || ev.EventType() != WebhookEventLineMoved {
		t.Errorf("EventID %q EventType %q", ev.EventID(), ev.EventType())
	}
	test, err := ParseWebhookEvent([]byte(v.Payload))
	if te, ok := test.(*WebhookTestEvent); err != nil || !ok || *te.Data.Message != "café ✓" {
		t.Errorf("the vector's body: %T %v", test, err)
	}
	unknown, err := ParseWebhookEvent([]byte(strings.Replace(lineMovedBody, `"line.moved"`, `"something.new"`, 1)))
	if p, ok := unknown.(*WebhookDeliveryPayload); err != nil || !ok || !bytes.Contains(*p.Data, []byte(`"sport":"nba"`)) {
		t.Errorf("an unknown type: %T %v", unknown, err)
	}
	for _, bad := range []string{`not json`, `{"id":"evt_x"}`, `[]`} {
		if got, err := ParseWebhookEvent([]byte(bad)); err == nil || got != nil {
			t.Errorf("%s: got %T, %v", bad, got, err)
		}
	}
}

// Every event type in the spec's webhooks map is a WebhookEvent* constant, and
// ParseWebhookEvent decodes it into the component the spec names.
func TestWebhookEventsMatchTheSpec(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("spec", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Webhooks map[string]struct {
			Post struct {
				RequestBody struct {
					Content map[string]struct {
						Schema struct {
							Ref string `json:"$ref"`
						} `json:"schema"`
					} `json:"content"`
				} `json:"requestBody"`
			} `json:"post"`
		} `json:"webhooks"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	consts := []string{WebhookEventLineMoved, WebhookEventEvFound, WebhookEventGameStarted, WebhookEventGameFinal, WebhookEventPropsGraded, WebhookEventTest}
	var types []string
	for typ, item := range spec.Webhooks {
		types = append(types, typ)
		ref := item.Post.RequestBody.Content["application/json"].Schema.Ref
		component := ref[strings.LastIndex(ref, "/")+1:]
		ev, err := ParseWebhookEvent([]byte(`{"type":"` + typ + `","data":{}}`))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if got := reflect.TypeOf(ev).Elem().Name(); got != goTypeName(component) {
			t.Errorf("%s decodes into %s, the spec names %s", typ, got, component)
		}
	}
	sort.Strings(types)
	sort.Strings(consts)
	if !reflect.DeepEqual(types, consts) {
		t.Errorf("spec webhooks %v, constants %v", types, consts)
	}
	// CONTROL: an event type the switch does not know falls back to the envelope.
	if ev, _ := ParseWebhookEvent([]byte(`{"type":"other.thing","data":{}}`)); reflect.TypeOf(ev).Elem().Name() != "WebhookDeliveryPayload" {
		t.Errorf("control: %T", ev)
	}
}

// inputKeyMismatches lists the hand-written webhook inputs whose JSON, fully set,
// does not send exactly the properties the spec gives that schema.
func inputKeyMismatches(schemas map[string]map[string]json.RawMessage, full map[string]any) []string {
	var out []string
	for name := range handWrittenInputs {
		if !strings.Contains(name, "Webhook") {
			continue
		}
		value, ok := full[name]
		if !ok {
			out = append(out, name+": no fully set value")
			continue
		}
		var sent map[string]json.RawMessage
		raw, _ := json.Marshal(value)
		if err := json.Unmarshal(raw, &sent); err != nil {
			out = append(out, name+": "+err.Error())
			continue
		}
		if got, want := keys(sent), keys(schemas[name]); !reflect.DeepEqual(got, want) {
			out = append(out, fmt.Sprintf("%s sends %v, the spec has %v", name, got, want))
		}
	}
	sort.Strings(out)
	return out
}

// The hand-written request bodies send exactly the properties the spec gives them.
func TestWebhookInputsMatchTheSpec(t *testing.T) {
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
	full := map[string]any{
		"CreateWebhookParams":          CreateWebhookParams{URL: "u", EventTypes: []string{"x"}, Description: "d", Filters: &WebhookFiltersInput{}},
		"UpdateWebhookParams":          UpdateWebhookParams{URL: "u", EventTypes: []string{"x"}, Description: Ptr("d"), Filters: &WebhookFiltersInput{}, Enabled: Ptr(true)},
		"RotateWebhookSecretParams":    RotateWebhookSecretParams{ExpirePreviousAfterHours: Ptr(1.0)},
		"WebhookFiltersInput":          WebhookFiltersInput{Sports: []string{}, LineMoved: &WebhookLineMovedFiltersInput{}, EvFound: &WebhookEvFoundFiltersInput{}},
		"WebhookLineMovedFiltersInput": WebhookLineMovedFiltersInput{PriceStepPp: 1, PointStep: 1, Markets: []string{"spread"}},
		"WebhookEvFoundFiltersInput":   WebhookEvFoundFiltersInput{MinEv: 1, Kinds: []string{"book"}, Venues: []string{}},
	}
	if bad := inputKeyMismatches(schemas, full); len(bad) > 0 {
		t.Fatalf("inputs:\n  %s", strings.Join(bad, "\n  "))
	}
	// CONTROL: a property the spec gains, and an input missing from the table, are reported.
	gained := map[string]map[string]json.RawMessage{}
	for k, v := range schemas {
		gained[k] = v
	}
	gained["RotateWebhookSecretParams"] = map[string]json.RawMessage{"expire_previous_after_hours": nil, "new_field": nil}
	partial := map[string]any{}
	for k, v := range full {
		if k != "WebhookEvFoundFiltersInput" {
			partial[k] = v
		}
	}
	if bad := inputKeyMismatches(gained, partial); len(bad) != 2 {
		t.Fatalf("control: %v", bad)
	}
}

func TestWebhookInputsRoundTrip(t *testing.T) {
	// A config loaded from JSON keeps every field (the tags match what MarshalJSON writes).
	in := CreateWebhookParams{
		URL: "https://hooks.example.com/x", EventTypes: []string{WebhookEventEvFound},
		Filters: &WebhookFiltersInput{
			Sports:    []string{},
			LineMoved: &WebhookLineMovedFiltersInput{PriceStepPp: 1, PointStep: 0.5, Markets: []string{"total"}},
			EvFound:   &WebhookEvFoundFiltersInput{MinEv: 5, Kinds: []string{"venue"}, Venues: []string{}},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out CreateWebhookParams
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(out)
	if string(b) != string(b2) || !reflect.DeepEqual(in, out) {
		t.Errorf("round trip:\n %s\n %s", b, b2)
	}
}

func keys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestWebhookInputsLeaveOutWhatIsNotSet(t *testing.T) {
	cases := []struct {
		name string
		v    any
		want string
	}{
		{"nil sports: nothing", WebhookFiltersInput{}, `{}`},
		{"empty sports: [] (every covered sport)", WebhookFiltersInput{Sports: []string{}}, `{"sports":[]}`},
		{"sections only when set", WebhookFiltersInput{LineMoved: &WebhookLineMovedFiltersInput{PointStep: 0.5}}, `{"line_moved":{"point_step":0.5}}`},
		{"ev.found zero fields left out", WebhookEvFoundFiltersInput{}, `{}`},
		{"empty venues: [] (every venue)", WebhookEvFoundFiltersInput{MinEv: 3, Venues: []string{}}, `{"min_ev":3,"venues":[]}`},
		{"update: only what is set", UpdateWebhookParams{Enabled: Ptr(false)}, `{"enabled":false}`},
		{"update: an emptied description", UpdateWebhookParams{Description: Ptr("")}, `{"description":""}`},
		{"rotate: the default", RotateWebhookSecretParams{}, `{}`},
		{"rotate: retire at once", RotateWebhookSecretParams{ExpirePreviousAfterHours: Ptr(0.0)}, `{"expire_previous_after_hours":0}`},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil || string(b) != c.want {
			t.Errorf("%s: %s (%v), want %s", c.name, b, err, c.want)
		}
	}
}

const endpointJSON = `{"id":"whk_a","url":"https://hooks.example.com/x","description":null,"event_types":["game.final"],
"filters":{"sports":[],"line_moved":{"price_step_pp":2,"point_step":1,"markets":["moneyline"]},"ev_found":{"min_ev":3,"kinds":["book"],"venues":[]}},
"api_version":"2026-10-01","status":"enabled","status_reason":null,"secret_hint":"abcd","previous_secret_expires_at":null,
"consecutive_failures":0,"failing_since":null,"last_success_at":null,"created_at":"2026-10-02T00:00:00.000Z","updated_at":"2026-10-02T00:00:00.000Z"`

func webhookAPI(r *http.Request) (int, http.Header, string) {
	p := r.URL.Path
	switch {
	case strings.HasSuffix(p, "/whk_nope"):
		return 404, nil, `{"success":false,"error":"Webhook endpoint not found","code":"not_found"}`
	case r.Method == http.MethodPost && p == "/api/v1/webhooks":
		return 201, nil, `{"success":true,"data":` + endpointJSON + `,"secret":"whsec_` + strings.Repeat("a", 43) + `"}}`
	case p == "/api/v1/webhooks":
		return 200, nil, `{"success":true,"data":[` + endpointJSON + `}],"meta":{"count":1,"limit":3}}`
	case strings.HasSuffix(p, "/test"):
		return 202, nil, `{"success":true,"data":{"id":"evt_x","type":"webhook.test","status":"pending"}}`
	case strings.HasSuffix(p, "/rotate-secret"):
		return 200, nil, `{"success":true,"data":{"id":"whk_a","secret":"whsec_b","secret_hint":"bbbb","previous_secret_expires_at":null}}`
	case strings.HasSuffix(p, "/deliveries"):
		return 200, nil, `{"success":true,"data":[{"id":"evt_x","type":"webhook.test","status":"delivered","attempts":1,
"payload":{"id":"evt_x","type":"webhook.test","data":{"message":"hi"}}}],"has_more":false}`
	case r.Method == http.MethodDelete:
		return 200, nil, `{"success":true,"data":{"id":"whk_a","deleted":true}}`
	}
	return 200, nil, `{"success":true,"data":` + endpointJSON + `}}`
}

func TestWebhookManagementRequests(t *testing.T) {
	api := newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) { return webhookAPI(r) })
	c := testClient(t, api.URL)
	ctx := context.Background()

	created, err := c.CreateWebhook(ctx, CreateWebhookParams{
		URL: "https://hooks.example.com/x", EventTypes: []string{WebhookEventGameFinal},
		Filters: &WebhookFiltersInput{Sports: []string{"nba"}, LineMoved: &WebhookLineMovedFiltersInput{PriceStepPp: 1}},
	})
	if err != nil || !strings.HasPrefix(*created.Data.Secret, "whsec_") || *created.Data.Filters.LineMoved.PriceStepPp != 2 {
		t.Fatalf("create: %+v %v", created, err)
	}
	list, err := c.ListWebhooks(ctx)
	if err != nil || *list.Meta.Limit != 3 || len(list.Data) != 1 || *list.Data[0].SecretHint != "abcd" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if got, err := c.GetWebhook(ctx, "whk_a"); err != nil || *got.Data.ID != "whk_a" || got.Data.Description != nil {
		t.Fatalf("get: %+v %v", got, err)
	}
	if _, err := c.UpdateWebhook(ctx, "whk_a", UpdateWebhookParams{Enabled: Ptr(false)}); err != nil {
		t.Fatal(err)
	}
	if d, err := c.DeleteWebhook(ctx, "whk_a"); err != nil || !*d.Data.Deleted {
		t.Fatalf("delete: %+v %v", d, err)
	}
	if q, err := c.TestWebhook(ctx, "whk_a"); err != nil || *q.Data.ID != "evt_x" || *q.Data.Status != "pending" {
		t.Fatalf("test: %+v %v", q, err)
	}
	if r, err := c.RotateWebhookSecret(ctx, "whk_a", &RotateWebhookSecretParams{ExpirePreviousAfterHours: Ptr(0.0)}); err != nil || *r.Data.Secret != "whsec_b" {
		t.Fatalf("rotate: %+v %v", r, err)
	}
	if _, err := c.RotateWebhookSecret(ctx, "whk_a", nil); err != nil {
		t.Fatal(err)
	}
	ds, err := c.ListWebhookDeliveries(ctx, "whk_a", &WebhookDeliveriesParams{Status: "delivered", Type: WebhookEventTest, Limit: 5, StartingAfter: "evt_w"})
	if err != nil || len(ds.Data) != 1 || *ds.Data[0].Attempts != 1 || *ds.HasMore {
		t.Fatalf("deliveries: %+v %v", ds, err)
	}
	if ev, err := ParseWebhookEvent(must(json.Marshal(ds.Data[0].Payload))); err != nil || *ev.(*WebhookTestEvent).Data.Message != "hi" {
		t.Errorf("a delivery's payload did not parse: %v", err)
	}
	if _, err := c.ListWebhookDeliveries(ctx, "whk_a", nil); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"POST /api/v1/webhooks ",
		"GET /api/v1/webhooks ",
		"GET /api/v1/webhooks/whk_a ",
		"PATCH /api/v1/webhooks/whk_a ",
		"DELETE /api/v1/webhooks/whk_a ",
		"POST /api/v1/webhooks/whk_a/test ",
		"POST /api/v1/webhooks/whk_a/rotate-secret ",
		"POST /api/v1/webhooks/whk_a/rotate-secret ",
		"GET /api/v1/webhooks/whk_a/deliveries limit=5&starting_after=evt_w&status=delivered&type=webhook.test",
		"GET /api/v1/webhooks/whk_a/deliveries ",
	}
	if api.count() != len(want) {
		t.Fatalf("%d requests, want %d", api.count(), len(want))
	}
	bodies := []string{
		`{"url":"https://hooks.example.com/x","event_types":["game.final"],"filters":{"line_moved":{"price_step_pp":1},"sports":["nba"]}}`,
		"", "", `{"enabled":false}`, "", "", `{"expire_previous_after_hours":0}`, `{}`, "", "",
	}
	for i, w := range want {
		r := api.req(i)
		if got := r.Method + " " + r.Path + " " + r.Query; got != w {
			t.Errorf("request %d: %q, want %q", i, got, w)
		}
		if r.Body != bodies[i] {
			t.Errorf("request %d body %q, want %q", i, r.Body, bodies[i])
		}
		if ct := r.Header.Get("Content-Type"); (r.Body != "") != (ct == "application/json") {
			t.Errorf("request %d: body %q with Content-Type %q", i, r.Body, ct)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("request %d: no key", i)
		}
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestWebhookIDIsOnePathSegment(t *testing.T) {
	var paths []string
	api := newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) {
		paths = append(paths, r.URL.EscapedPath())
		return 200, nil, `{"success":true,"data":` + endpointJSON + `}}`
	})
	c := testClient(t, api.URL)
	_, _ = c.GetWebhook(context.Background(), "whk_a/../x?y")
	_, _ = c.GetWebhook(context.Background(), "whk_a") // CONTROL
	if !reflect.DeepEqual(paths, []string{"/api/v1/webhooks/whk_a%2F..%2Fx%3Fy", "/api/v1/webhooks/whk_a"}) {
		t.Errorf("paths %v", paths)
	}
}

func TestWebhookErrors(t *testing.T) {
	ctx := context.Background()
	api := newFakeAPI(t, func(r *http.Request, _ int) (int, http.Header, string) { return webhookAPI(r) })
	if _, err := testClient(t, api.URL).GetWebhook(ctx, "whk_nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("404: %v", err)
	}
	problem := "game.final does not cover nfl (covered: mlb, nba, ncaab, nhl, soccer, tennis)"
	api = newFakeAPI(t, func(*http.Request, int) (int, http.Header, string) {
		return 400, nil, `{"success":false,"error":"Unsupported sport for an event type","code":"sport_not_covered","message":"` + problem + `","problems":["` + problem + `"]}`
	})
	_, err := testClient(t, api.URL).CreateWebhook(ctx, CreateWebhookParams{URL: "https://hooks.example.com/x", EventTypes: []string{WebhookEventGameFinal}})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 400 || ae.Code != "sport_not_covered" || ae.Message != problem || !bytes.Contains(ae.Details, []byte(`"problems"`)) {
		t.Errorf("400: %#v", err)
	}
}

func TestWebhookWritesAreNeverRetried(t *testing.T) {
	ctx := context.Background()
	busy := func(*http.Request, int) (int, http.Header, string) {
		return 503, nil, `{"success":false,"error":"Webhooks are temporarily unavailable","code":"unavailable"}`
	}
	retry := WithRetry(RetryPolicy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond})
	writes := map[string]func(*Client) error{
		"create": func(c *Client) error {
			_, err := c.CreateWebhook(ctx, CreateWebhookParams{URL: "https://hooks.example.com/x", EventTypes: []string{WebhookEventLineMoved}})
			return err
		},
		"update": func(c *Client) error {
			_, err := c.UpdateWebhook(ctx, "whk_a", UpdateWebhookParams{Enabled: Ptr(true)})
			return err
		},
		"delete": func(c *Client) error { _, err := c.DeleteWebhook(ctx, "whk_a"); return err },
		"test":   func(c *Client) error { _, err := c.TestWebhook(ctx, "whk_a"); return err },
		"rotate": func(c *Client) error { _, err := c.RotateWebhookSecret(ctx, "whk_a", nil); return err },
	}
	limited := func(*http.Request, int) (int, http.Header, string) {
		return 429, http.Header{"Retry-After": {"0"}}, `{"success":false,"error":"Too many test events","code":"rate_limited"}`
	}
	for name, call := range writes {
		api := newFakeAPI(t, busy)
		if err := call(testClient(t, api.URL, retry)); !errors.Is(err, ErrServiceBusy) || api.count() != 1 {
			t.Errorf("%s: %v after %d requests", name, err, api.count())
		}
		api = newFakeAPI(t, limited)
		var ae *APIError
		if err := call(testClient(t, api.URL, retry)); !errors.As(err, &ae) || ae.Code != "rate_limited" || api.count() != 1 {
			t.Errorf("%s on a 429: %v after %d requests", name, err, api.count())
		}
	}
	// CONTROL: a GET is retried under the same policy.
	api := newFakeAPI(t, busy)
	if _, err := testClient(t, api.URL, retry).ListWebhooks(ctx); !errors.Is(err, ErrServiceBusy) || api.count() != 4 {
		t.Errorf("control: %v after %d requests", err, api.count())
	}
}

func TestWebhookIDMustBeASegment(t *testing.T) {
	api := newFakeAPI(t, ok(`{"success":true,"data":{}}`))
	c := testClient(t, api.URL)
	ctx := context.Background()
	for _, id := range []string{"", ".", ".."} {
		calls := map[string]error{}
		_, calls["get"] = c.GetWebhook(ctx, id)
		_, calls["update"] = c.UpdateWebhook(ctx, id, UpdateWebhookParams{Enabled: Ptr(true)})
		_, calls["delete"] = c.DeleteWebhook(ctx, id)
		_, calls["test"] = c.TestWebhook(ctx, id)
		_, calls["rotate"] = c.RotateWebhookSecret(ctx, id, nil)
		_, calls["deliveries"] = c.ListWebhookDeliveries(ctx, id, nil)
		for name, err := range calls {
			if err == nil || !strings.Contains(err.Error(), "invalid webhook id") {
				t.Errorf("%s(%q): %v", name, id, err)
			}
		}
	}
	if api.count() != 0 {
		t.Fatalf("%d requests were sent", api.count())
	}
	if _, err := c.GetWebhook(ctx, "whk_a"); err != nil || api.count() != 1 { // CONTROL
		t.Fatalf("control: %v, %d requests", err, api.count())
	}
}

// The API's own bodies (testdata/fixtures/webhook-events.json, built with its payload
// builders) decode with every field known: unknown fields are an error here.
func TestWebhookModelsKnowEveryField(t *testing.T) {
	var fx struct {
		Events   []json.RawMessage `json:"events"`
		Endpoint json.RawMessage   `json:"endpoint"`
		Delivery json.RawMessage   `json:"delivery"`
	}
	if err := json.Unmarshal([]byte(fixture(t, "webhook-events.json")), &fx); err != nil {
		t.Fatal(err)
	}
	strict := func(raw json.RawMessage, into any) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		return d.Decode(into)
	}
	seen := map[string]bool{}
	for _, raw := range fx.Events {
		ev, err := ParseWebhookEvent(raw)
		if err != nil {
			t.Fatal(err)
		}
		seen[ev.EventType()] = true
		into := reflect.New(reflect.TypeOf(ev).Elem()).Interface()
		if err := strict(raw, into); err != nil {
			t.Errorf("%s: %v", ev.EventType(), err)
		}
		if ev.EventID() == "" {
			t.Errorf("%s: no EventID", ev.EventType())
		}
	}
	if len(seen) != 6 {
		t.Errorf("event types in the fixture: %v", seen)
	}
	if err := strict(fx.Endpoint, new(WebhookEndpoint)); err != nil {
		t.Errorf("endpoint: %v", err)
	}
	if err := strict(fx.Delivery, new(WebhookDelivery)); err != nil {
		t.Errorf("delivery: %v", err)
	}
	// CONTROL: a field the models do not know is an error.
	if err := strict(json.RawMessage(`{"id":"whk_a","new_field":1}`), new(WebhookEndpoint)); err == nil {
		t.Error("control: an unknown field was accepted")
	}
}
