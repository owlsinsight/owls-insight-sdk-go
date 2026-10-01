package owls

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Drift between this SDK and the API manifest, the list of endpoints and events
// generated from the API server's source (docs/api-manifest.json in its
// repository).
//
// The manifest is $OWLS_API_MANIFEST when set (a missing file there is an error),
// otherwise the one in a sibling ../nba-odds-app checkout. When neither exists the checks
// against it are SKIPPED with a message, never passed. The extraction is always
// exercised against synthetic manifests (the CONTROL tests), so a parser that
// reads nothing cannot turn the real check into a silent pass.

type manifest struct {
	Endpoints []struct {
		ID           string `json:"id"`
		Method       string `json:"method"`
		PathTemplate string `json:"pathTemplate"`
		Status       string `json:"status"`
		Deprecated   any    `json:"deprecated"`
		SDK          *struct {
			TS string `json:"ts"`
		} `json:"sdk"`
	} `json:"endpoints"`
	WSEvents []struct {
		Name string `json:"name"`
	} `json:"wsEvents"`
	V2Books []struct {
		Book    string  `json:"book"`
		WSEvent *string `json:"wsEvent"`
	} `json:"v2Books"`
}

// endpointsWithoutSDKName maps the live endpoints the manifest names no TS method
// for to the Go method that serves them; "" means the SDK deliberately has none.
var endpointsWithoutSDKName = map[string]string{
	"v1.sport.injuries":     "GetInjuries",      // the manifest lacks its sdk name
	"v1.history.splits":     "GetHistorySplits", // the manifest lacks its sdk name
	"v1.schedule":           "GetDaySchedule",   // the manifest lacks its sdk name
	"v2.book.sport":         "GetV2",
	"v2.book.sport.leagues": "GetV2Leagues",
	"v1.docs.meta":          "",
	"v1.updates":            "",
}

func loadManifest(t *testing.T) (*manifest, bool) {
	t.Helper()
	candidates := []string{filepath.Join("..", "nba-odds-app", "docs", "api-manifest.json")}
	if env := os.Getenv("OWLS_API_MANIFEST"); env != "" {
		if _, err := os.Stat(env); err != nil {
			t.Fatalf("OWLS_API_MANIFEST points at a missing file: %s", env)
		}
		candidates = []string{env}
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m manifest
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		t.Logf("manifest: %s", p)
		return &m, true
	}
	t.Logf("SKIPPED: no API manifest found. Looked in: %s. Set OWLS_API_MANIFEST to nba-odds-app/docs/api-manifest.json to run the drift check.",
		strings.Join(candidates, ", "))
	return nil, false
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func clientHasMethod(name string) bool {
	_, ok := reflect.TypeOf(&Client{}).MethodByName(name)
	return ok
}

func deprecated(status string, flag any) bool {
	return status == "deprecated" || flag == true
}

// missingMethods lists the live endpoints the SDK has no method for.
func missingMethods(m *manifest, has func(string) bool) []string {
	var out []string
	for _, e := range m.Endpoints {
		if deprecated(e.Status, e.Deprecated) {
			continue
		}
		var method string
		if e.SDK != nil && e.SDK.TS != "" {
			method = upperFirst(e.SDK.TS)
		} else if mapped, ok := endpointsWithoutSDKName[e.ID]; ok {
			if mapped == "" {
				continue
			}
			method = mapped
		} else {
			out = append(out, e.ID+" (no sdk.ts name and no Go mapping)")
			continue
		}
		if !has(method) {
			out = append(out, e.ID+" -> "+method)
		}
	}
	sort.Strings(out)
	return out
}

// unknownEvents lists the manifest's WebSocket events the event layer does not know.
func unknownEvents(m *manifest, known map[string]bool) []string {
	var out []string
	for _, e := range m.WSEvents {
		if !known[e.Name] {
			out = append(out, e.Name)
		}
	}
	return out
}

// v2EventMismatches lists the v2 books whose documented event V2EventName does not produce.
func v2EventMismatches(m *manifest) []string {
	var out []string
	for _, b := range m.V2Books {
		if b.WSEvent != nil && V2EventName(b.Book) != *b.WSEvent {
			out = append(out, b.Book+": manifest "+*b.WSEvent+", SDK "+V2EventName(b.Book))
		}
	}
	return out
}

func TestManifestEndpointsHaveMethods(t *testing.T) {
	m, ok := loadManifest(t)
	if !ok {
		t.Skip("SKIPPED: no API manifest (see log)")
	}
	if len(m.Endpoints) < 40 {
		t.Fatalf("manifest has %d endpoints: not the real manifest?", len(m.Endpoints))
	}
	if missing := missingMethods(m, clientHasMethod); len(missing) > 0 {
		t.Fatalf("manifest endpoints without a Go method:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestManifestWSEventsAreKnown(t *testing.T) {
	m, ok := loadManifest(t)
	if !ok {
		t.Skip("SKIPPED: no API manifest (see log)")
	}
	if len(m.WSEvents) < 20 {
		t.Fatalf("manifest has %d wsEvents: not the real manifest?", len(m.WSEvents))
	}
	if unknown := unknownEvents(m, knownEvents()); len(unknown) > 0 {
		t.Fatalf("manifest WebSocket events the SDK does not know: %v", unknown)
	}
	if bad := v2EventMismatches(m); len(bad) > 0 {
		t.Fatalf("v2 event names: %v", bad)
	}
}

func TestManifestDriftControls(t *testing.T) {
	// CONTROL: the method lookup reads the real method set.
	if !clientHasMethod("GetOdds") || !clientHasMethod("IterHistoryOdds") || clientHasMethod("GetNotARealThing") {
		t.Fatal("method lookup is broken")
	}
	var synthetic manifest
	if err := json.Unmarshal([]byte(`{
		"endpoints": [
			{"id": "v1.sport.odds", "status": "live", "sdk": {"ts": "getOdds"}},
			{"id": "v1.fake", "status": "live", "sdk": {"ts": "getNotARealThing"}},
			{"id": "v1.old", "status": "deprecated", "sdk": {"ts": "getRetiredThing"}},
			{"id": "v1.unnamed", "status": "live"},
			{"id": "v2.book.sport", "status": "live"},
			{"id": "v1.docs.meta", "status": "live"}
		],
		"wsEvents": [{"name": "odds-update"}, {"name": "fanduel-v2-update"}, {"name": "not-a-real-event"}],
		"v2Books": [{"book": "fanduel", "wsEvent": "fanduel-v2-update"}, {"book": "newbook", "wsEvent": "newbook-realtime"},
			{"book": "hardrock", "wsEvent": null}]
	}`), &synthetic); err != nil {
		t.Fatal(err)
	}
	// CONTROL: a method the SDK lacks, and a live endpoint nobody mapped, are
	// reported; a deprecated one and the deliberately skipped ones are not.
	want := []string{"v1.fake -> GetNotARealThing", "v1.unnamed (no sdk.ts name and no Go mapping)"}
	if got := missingMethods(&synthetic, clientHasMethod); !reflect.DeepEqual(got, want) {
		t.Errorf("missingMethods = %q, want %q", got, want)
	}
	// CONTROL: an event the SDK does not know is reported.
	if got := unknownEvents(&synthetic, knownEvents()); !reflect.DeepEqual(got, []string{"not-a-real-event"}) {
		t.Errorf("unknownEvents = %q", got)
	}
	// CONTROL: a v2 book whose event breaks the naming rule is reported.
	if got := v2EventMismatches(&synthetic); len(got) != 1 || !strings.HasPrefix(got[0], "newbook") {
		t.Errorf("v2EventMismatches = %q", got)
	}
}
