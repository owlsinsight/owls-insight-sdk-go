package owls

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// spec/openapi.json and spec/ws.json are vendored from owls-insight-sdk-js, which
// generates them. models_gen.go is generated from the vendored copy
// (`go generate ./...`, oapi-codegen pinned in generate.go).

func TestVendoredSpecMatchesSource(t *testing.T) {
	dirs := []string{filepath.Join("..", "owls-insight-sdk-js", "openapi")}
	if env := os.Getenv("OWLS_OPENAPI_DIR"); env != "" {
		if _, err := os.Stat(env); err != nil {
			t.Fatalf("OWLS_OPENAPI_DIR points at a missing directory: %s", env)
		}
		dirs = []string{env}
	}
	var dir string
	for _, d := range dirs {
		if _, err := os.Stat(filepath.Join(d, "openapi.json")); err == nil {
			dir = d
			break
		}
	}
	if dir == "" {
		t.Skipf("SKIPPED: no source spec found. Looked in: %s. Set OWLS_OPENAPI_DIR to owls-insight-sdk-js/openapi to run the drift check.",
			strings.Join(dirs, ", "))
	}
	t.Logf("source spec: %s", dir)
	for _, name := range []string{"openapi.json", "ws.json"} {
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		vendored, err := os.ReadFile(filepath.Join("spec", name))
		if err != nil {
			t.Fatal(err)
		}
		if !sameSpec(src, vendored) {
			t.Errorf("spec/%s differs from %s: copy it and run `go generate ./...`", name, filepath.Join(dir, name))
		}
	}
}

func sameSpec(a, b []byte) bool { return bytes.Equal(bytes.TrimSpace(a), bytes.TrimSpace(b)) }

func TestSpecDriftControl(t *testing.T) {
	// CONTROL: a one-byte change is detected.
	b, err := os.ReadFile(filepath.Join("spec", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(b, []byte(`"OddsEvent"`), []byte(`"OddsEvenT"`), 1)
	if !sameSpec(b, b) || sameSpec(b, changed) {
		t.Fatal("sameSpec cannot tell the vendored spec from a changed one")
	}
}

type openAPI struct {
	Paths map[string]map[string]struct {
		OperationID string `json:"operationId"`
		SDKMethod   string `json:"x-owls-sdk-method"`
		Responses   map[string]struct {
			Content map[string]struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"content"`
		} `json:"responses"`
		// A path that is one value of another path's parameter, with its own
		// response type (/api/v1/{sport}/props/betmgm of .../props/{book}).
		VariantOf *struct {
			Param string `json:"param"`
			Path  string `json:"path"`
			Value string `json:"value"`
		} `json:"x-owls-variant-of"`
		// Another SDK method served by the same path, with its own response type
		// (getEsportsRealtime on /api/v1/{sport}/realtime).
		Alternates []struct {
			SDKMethod string          `json:"x-owls-sdk-method"`
			Schema    json.RawMessage `json:"schema"`
		} `json:"x-owls-alternate-responses"`
	} `json:"paths"`
	Components struct {
		Schemas map[string]json.RawMessage `json:"schemas"`
	} `json:"components"`
}

func loadSpec(t *testing.T) *openAPI {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("spec", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var s openAPI
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return &s
}

// generatedTypes returns the type names declared in models_gen.go.
func generatedTypes(t *testing.T) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "models_gen.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for name, obj := range f.Scope.Objects {
		if obj.Kind.String() == "type" {
			out[name] = true
		}
	}
	return out
}

// goTypeName is the Go name oapi-codegen gives a schema. name-normalizer
// ToCamelCaseWithInitialisms leaves every current schema name alone except these;
// a new one shows up as a missing type in TestModelsCoverTheSpec.
func goTypeName(schema string) string {
	if n, ok := map[string]string{"RawJson": "RawJSON"}[schema]; ok {
		return n
	}
	return schema
}

func TestGeneratedDocsAreTidy(t *testing.T) {
	models, err := os.ReadFile("models_gen.go")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile(filepath.Join("spec", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	// CONTROL: the spec has both, so without internal/cmd/doctidy the models would too.
	if !bytes.Contains(spec, []byte("\u2014")) || !bytes.Contains(spec, []byte("{@link")) {
		t.Fatal("control: the spec has no em dash or {@link} left to tidy")
	}
	for _, bad := range []string{"\u2014", "{@link"} {
		if bytes.Contains(models, []byte(bad)) {
			t.Errorf("models_gen.go contains %q: run `go generate ./...`", bad)
		}
	}
}

// refusalCodes are the Refusal* constants of errors.go.
var refusalCodes = []string{
	RefusalMissingAPIKey, RefusalInvalidAPIKey, RefusalAPIKeyDeactivated, RefusalTierNoWS,
	RefusalSubscriptionInactive, RefusalPaymentOverdue, RefusalTrialExpired, RefusalTrialUnverifiable,
	RefusalSubscriptionCheckFailed, RefusalInternal, RefusalIPBlocked, RefusalConnectionLimit, RefusalClientClosed,
}

func enumDiff(schema json.RawMessage, have []string) (missing, extra []string, err error) {
	var s struct {
		Values []string `json:"x-extensible-enum"`
	}
	if err := json.Unmarshal(schema, &s); err != nil {
		return nil, nil, err
	}
	in := map[string]bool{}
	for _, v := range have {
		in[v] = true
	}
	spec := map[string]bool{}
	for _, v := range s.Values {
		spec[v] = true
		if !in[v] {
			missing = append(missing, v)
		}
	}
	for _, v := range have {
		if !spec[v] {
			extra = append(extra, v)
		}
	}
	return missing, extra, nil
}

func TestRefusalCodesMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	missing, extra, err := enumDiff(spec.Components.Schemas["ConnectErrorCode"], refusalCodes)
	if err != nil || len(missing)+len(extra) > 0 {
		t.Fatalf("ConnectErrorCode: no constant for %v, constants not in the spec %v (%v)", missing, extra, err)
	}
	// CONTROL: a code the spec gains is reported.
	if missing, _, _ := enumDiff(json.RawMessage(`{"x-extensible-enum": ["CONNECTION_LIMIT", "NEW_CODE"]}`), refusalCodes); len(missing) != 1 {
		t.Fatalf("control: %v", missing)
	}
}

// Request inputs the generator skips (oapi-codegen.yaml exclude-schemas).
var handWrittenInputs = map[string]string{
	"SubscribeOptions":      "Subscription",
	"PropsSubscribeOptions": "PropsFilter",
	"SgpBuildParams":        "SgpBuildParams",
	"SgpLeg":                "SgpLeg",
}

// modelDrift returns the spec schemas with no generated type (the hand-written
// inputs aside) and the generated types no spec schema names, such as a stand-in
// an overlay adds.
func modelDrift(schemas map[string]json.RawMessage, types map[string]bool) (missing, extra []string) {
	named := map[string]bool{}
	for name := range schemas {
		named[goTypeName(name)] = true
		if _, hand := handWrittenInputs[name]; hand {
			continue
		}
		if !types[goTypeName(name)] {
			missing = append(missing, name)
		}
	}
	for name := range types {
		if !named[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

func TestModelsCoverTheSpec(t *testing.T) {
	spec := loadSpec(t)
	types := generatedTypes(t)
	missing, extra := modelDrift(spec.Components.Schemas, types)
	if len(missing) > 0 {
		t.Fatalf("schemas with no generated type (run `go generate ./...`): %v", missing)
	}
	if len(extra) > 0 {
		t.Fatalf("generated types the spec does not define (remove them from spec/go-overlay.yaml): %v", extra)
	}
	if len(spec.Components.Schemas) < 200 {
		t.Fatalf("only %d schemas: not the real spec?", len(spec.Components.Schemas))
	}
	// CONTROL: the parser reads the real file.
	if !types["OddsEvent"] || types["NotAModel"] {
		t.Fatal("generatedTypes is broken")
	}
	// CONTROL: a schema without a type, and a type without a schema, are reported.
	m, x := modelDrift(map[string]json.RawMessage{"OddsEvent": nil, "RawJson": nil, "NewSchema": nil, "SgpLeg": nil},
		map[string]bool{"OddsEvent": true, "RawJSON": true, "StandIn": true})
	if !reflect.DeepEqual(m, []string{"NewSchema"}) || !reflect.DeepEqual(x, []string{"StandIn"}) {
		t.Fatalf("control: missing %v, extra %v", m, x)
	}
}

// responseRefs returns the component names a 200 schema names: one $ref, or
// each branch of a oneOf/anyOf.
func responseRefs(schema json.RawMessage) []string {
	var s struct {
		Ref   string            `json:"$ref"`
		OneOf []json.RawMessage `json:"oneOf"`
		AnyOf []json.RawMessage `json:"anyOf"`
	}
	if json.Unmarshal(schema, &s) != nil {
		return nil
	}
	if s.Ref != "" {
		return []string{s.Ref[strings.LastIndex(s.Ref, "/")+1:]}
	}
	var out []string
	for _, b := range append(s.OneOf, s.AnyOf...) {
		out = append(out, responseRefs(b)...)
	}
	return out
}

// returnTypeNames is the name of a method's result type and of each pointer
// field of it (the Scores, BookPropsResult and PropResults wrappers).
func returnTypeNames(m reflect.Method) map[string]bool {
	out := map[string]bool{}
	rt := m.Type.Out(0)
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	out[rt.Name()] = true
	if rt.Kind() == reflect.Struct && rt.PkgPath() == reflect.TypeOf(Client{}).PkgPath() {
		for i := 0; i < rt.NumField(); i++ {
			if ft := rt.Field(i).Type; ft.Kind() == reflect.Pointer {
				out[ft.Elem().Name()] = true
			}
		}
	}
	return out
}

// mergedAnyOf matches the description the spec generator gives a response it
// merged from several overloads (getPropResults): the SDK may return the branches
// instead of the lenient merge.
var mergedAnyOf = regexp.MustCompile(`^Any of: (\w+(?:, \w+)+)\.$`)

// mergedBranches returns the components a merged response component was built
// from, or nil when name is not one (or names a component the spec lacks).
func mergedBranches(spec *openAPI, name string) []string {
	var s struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(spec.Components.Schemas[name], &s) != nil {
		return nil
	}
	m := mergedAnyOf.FindStringSubmatch(s.Description)
	if m == nil {
		return nil
	}
	branches := strings.Split(m[1], ", ")
	for _, b := range branches {
		if _, ok := spec.Components.Schemas[b]; !ok {
			return nil
		}
	}
	return branches
}

func responseTypeMismatches(spec *openAPI) []string {
	var out []string
	ct := reflect.TypeOf(&Client{})
	check := func(where, name string, schema json.RawMessage) {
		m, ok := ct.MethodByName(upperFirst(name))
		if !ok {
			out = append(out, where+": no method "+upperFirst(name))
			return
		}
		got := returnTypeNames(m)
		for _, want := range responseRefs(schema) {
			if got[goTypeName(want)] {
				continue
			}
			// A merged response is matched by returning every shape it merges.
			branches := mergedBranches(spec, want)
			all := len(branches) > 0
			for _, b := range branches {
				all = all && got[goTypeName(b)]
			}
			if !all {
				out = append(out, where+": "+m.Name+" does not return "+goTypeName(want))
			}
		}
	}
	for path, ops := range spec.Paths {
		for verb, op := range ops {
			name := op.SDKMethod
			if name == "" {
				name = op.OperationID
			}
			check(verb+" "+path, name, op.Responses["200"].Content["application/json"].Schema)
			for _, alt := range op.Alternates {
				check(verb+" "+path+" (alternate)", alt.SDKMethod, alt.Schema)
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestResponseTypesMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	if len(spec.Paths) < 50 {
		t.Fatalf("only %d paths", len(spec.Paths))
	}
	if bad := responseTypeMismatches(spec); len(bad) > 0 {
		t.Fatalf("response types:\n  %s", strings.Join(bad, "\n  "))
	}
	// CONTROL: an operation whose 200 names another component is reported.
	var fake openAPI
	_ = json.Unmarshal([]byte(`{"paths": {"/x": {"get": {"x-owls-sdk-method": "getOdds",
		"responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/RealtimeResponse"}}}}}}}}}`), &fake)
	if bad := responseTypeMismatches(&fake); len(bad) != 1 {
		t.Fatalf("control: %v", bad)
	}
	// CONTROL: an alternate response is checked against its own method.
	fake = openAPI{}
	_ = json.Unmarshal([]byte(`{"paths": {"/x": {"get": {"x-owls-sdk-method": "getRealtime",
		"responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/RealtimeResponse"}}}}},
		"x-owls-alternate-responses": [{"x-owls-sdk-method": "getEsportsRealtime", "schema": {"$ref": "#/components/schemas/RealtimeResponse"}}]}}}}`), &fake)
	if bad := responseTypeMismatches(&fake); len(bad) != 1 || !strings.Contains(bad[0], "(alternate): GetEsportsRealtime") {
		t.Fatalf("control: %v", bad)
	}
	// CONTROL: a merged response passes only when the method returns every shape it merges.
	merged := func(desc string) []string {
		var f openAPI
		_ = json.Unmarshal([]byte(`{"paths": {"/x": {"get": {"x-owls-sdk-method": "getPropResults",
			"responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/GetPropResultsResponse"}}}}}}}},
			"components": {"schemas": {"PropResultsResponse": {}, "PropResultsDayResponse": {}, "OddsResponse": {},
				"GetPropResultsResponse": {"type": "object", "description": "`+desc+`"}}}}`), &f)
		return responseTypeMismatches(&f)
	}
	if bad := merged("Any of: PropResultsDayResponse, PropResultsResponse."); len(bad) != 0 {
		t.Fatalf("control: the branches were not accepted: %v", bad)
	}
	for _, desc := range []string{"Any of: PropResultsResponse, OddsResponse.", "Any of: PropResultsResponse, NotInTheSpec.", "A response."} {
		if bad := merged(desc); len(bad) != 1 {
			t.Fatalf("control %q: %v", desc, bad)
		}
	}
}

func TestGeneratedCodeUsesOnlyTheStandardLibrary(t *testing.T) {
	stdOnly := func(imports []string) []string {
		var bad []string
		for _, p := range imports {
			if first, _, _ := strings.Cut(p, "/"); strings.Contains(first, ".") {
				bad = append(bad, p)
			}
		}
		return bad
	}
	f, err := parser.ParseFile(token.NewFileSet(), "models_gen.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	var imports []string
	for _, i := range f.Imports {
		p, _ := strconv.Unquote(i.Path.Value)
		imports = append(imports, p)
	}
	if bad := stdOnly(imports); len(bad) > 0 {
		t.Fatalf("models_gen.go imports %v", bad)
	}
	// CONTROL: the union runtime the generator would otherwise pull in is caught.
	if bad := stdOnly([]string{"encoding/json", "github.com/oapi-codegen/runtime"}); len(bad) != 1 {
		t.Fatalf("control: %v", bad)
	}

	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	var requires []string
	inBlock := false
	for _, line := range strings.Split(string(mod), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "require (":
			inBlock = true
		case inBlock && line == ")":
			inBlock = false
		case inBlock && line != "":
			requires = append(requires, strings.Fields(line)[0])
		case strings.HasPrefix(line, "require "):
			requires = append(requires, strings.Fields(line)[1])
		}
	}
	if !reflect.DeepEqual(requires, []string{"github.com/coder/websocket"}) {
		t.Fatalf("go.mod requires %v, want only github.com/coder/websocket", requires)
	}
}

// readLimitShortfall reports when the stream's read limit is below the message
// size ws.json asks clients to accept.
func readLimitShortfall(wsSpec []byte, limit int64) (string, error) {
	var w struct {
		Transport struct {
			RecommendedMaxMessageBytes int64 `json:"recommendedMaxMessageBytes"`
		} `json:"transport"`
	}
	if err := json.Unmarshal(wsSpec, &w); err != nil {
		return "", err
	}
	if w.Transport.RecommendedMaxMessageBytes <= 0 {
		return "ws.json has no transport.recommendedMaxMessageBytes", nil
	}
	if limit < w.Transport.RecommendedMaxMessageBytes {
		return "read limit " + strconv.FormatInt(limit, 10) + " is below the recommended " +
			strconv.FormatInt(w.Transport.RecommendedMaxMessageBytes, 10), nil
	}
	return "", nil
}

func TestReadLimitCoversTheSpec(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("spec", "ws.json"))
	if err != nil {
		t.Fatal(err)
	}
	if msg, err := readLimitShortfall(b, defaultReadLimit); err != nil || msg != "" {
		t.Fatalf("%s (%v)", msg, err)
	}
	// CONTROL: a larger recommendation, or none, is reported.
	for _, ws := range []string{`{"transport": {"recommendedMaxMessageBytes": 134217728}}`, `{"transport": {}}`} {
		if msg, err := readLimitShortfall([]byte(ws), defaultReadLimit); err != nil || msg == "" {
			t.Fatalf("control %s: %q (%v)", ws, msg, err)
		}
	}
}

const bookPropsPath = "/api/v1/{sport}/props/{book}"

// bookPropsMismatches checks GetBookProps against the spec's per-book variants of
// bookPropsPath: each variant book must come back in a field of the variant's
// type, any other book in a field of the base path's type, and BookPropsResult may
// have no field for a book the spec no longer lists (a retired variant).
func bookPropsMismatches(t *testing.T, spec *openAPI) []string {
	t.Helper()
	var out []string
	base := responseRefs(spec.Paths[bookPropsPath]["get"].Responses["200"].Content["application/json"].Schema)
	if len(base) != 1 {
		return []string{bookPropsPath + ": no single 200 response"}
	}
	want := map[string]string{"": goTypeName(base[0])} // book -> Go type; "" is any other book
	for path, ops := range spec.Paths {
		for verb, op := range ops {
			v := op.VariantOf
			if v == nil {
				continue
			}
			if v.Path != bookPropsPath || v.Param != "book" || op.SDKMethod != "getBookProps" {
				out = append(out, verb+" "+path+": a variant of "+v.Path+" with no per-book check here")
				continue
			}
			refs := responseRefs(op.Responses["200"].Content["application/json"].Schema)
			if len(refs) != 1 {
				out = append(out, path+": no single 200 response")
				continue
			}
			want[v.Value] = goTypeName(refs[0])
		}
	}

	api := newFakeAPI(t, ok(`{}`))
	c := testClient(t, api.URL)
	used := map[string]bool{}
	for book, typ := range want {
		name := book
		if name == "" {
			name = "some-other-book"
		}
		res, err := c.GetBookProps(context.Background(), "nba", name, nil)
		if err != nil {
			out = append(out, name+": "+err.Error())
			continue
		}
		var set []string
		rv := reflect.ValueOf(res).Elem()
		for i := 0; i < rv.NumField(); i++ {
			if f := rv.Field(i); f.Kind() == reflect.Pointer && !f.IsNil() {
				set = append(set, f.Type().Elem().Name())
			}
		}
		if len(set) != 1 || set[0] != typ {
			out = append(out, "GetBookProps("+name+") sets "+strings.Join(set, ",")+", want "+typ)
		}
		used[typ] = true
	}
	rt := reflect.TypeOf(BookPropsResult{})
	for i := 0; i < rt.NumField(); i++ {
		if ft := rt.Field(i).Type; ft.Kind() == reflect.Pointer && !used[ft.Elem().Name()] {
			out = append(out, "BookPropsResult."+rt.Field(i).Name+": no book in the spec answers with "+ft.Elem().Name())
		}
	}
	sort.Strings(out)
	return out
}

func TestBookPropsVariantsMatchTheSpec(t *testing.T) {
	spec := loadSpec(t)
	if bad := bookPropsMismatches(t, spec); len(bad) > 0 {
		t.Fatalf("GetBookProps:\n  %s", strings.Join(bad, "\n  "))
	}
	variants := 0
	for _, ops := range spec.Paths {
		if op, ok := ops["get"]; ok && op.VariantOf != nil {
			variants++
		}
	}
	if variants == 0 {
		t.Fatal("control: the spec has no x-owls-variant-of path, so nothing was checked")
	}
	withVariants := func(extra string) *openAPI {
		var f openAPI
		b := `{"paths": {
			"/api/v1/{sport}/props/{book}": {"get": {"x-owls-sdk-method": "getBookProps",
				"responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/PropsResponse"}}}}}}}` + extra + `}}`
		if err := json.Unmarshal([]byte(b), &f); err != nil {
			t.Fatal(err)
		}
		return &f
	}
	variant := func(book, schema string) string {
		return `, "/api/v1/{sport}/props/` + book + `": {"get": {"x-owls-sdk-method": "getBookProps",
			"x-owls-variant-of": {"param": "book", "path": "/api/v1/{sport}/props/{book}", "value": "` + book + `"},
			"responses": {"200": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/` + schema + `"}}}}}}}`
	}
	// CONTROL: the betmgm variant alone passes.
	if bad := bookPropsMismatches(t, withVariants(variant("betmgm", "BetMGMPropsResponse"))); len(bad) != 0 {
		t.Fatalf("control: %v", bad)
	}
	// CONTROL: a variant the method does not handle (the retired bet365 one) is reported.
	if bad := bookPropsMismatches(t, withVariants(variant("betmgm", "BetMGMPropsResponse")+variant("bet365", "Bet365PropsResponse"))); len(bad) != 1 ||
		!strings.Contains(bad[0], "GetBookProps(bet365)") {
		t.Fatalf("control: %v", bad)
	}
	// CONTROL: a result field no variant in the spec needs is reported.
	if bad := bookPropsMismatches(t, withVariants("")); len(bad) != 1 || !strings.Contains(strings.Join(bad, " "), "BookPropsResult.BetMGM") {
		t.Fatalf("control: %v", bad)
	}
}
