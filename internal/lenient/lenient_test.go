package lenient

import (
	"encoding/json"
	"testing"
)

type outcome struct {
	Name  *string  `json:"name,omitempty"`
	Price *float64 `json:"price,omitempty"`
}

type doc struct {
	Success  *bool                     `json:"success,omitempty"`
	Outcomes []outcome                 `json:"outcomes,omitempty"`
	Nested   *struct{ Count *float64 } `json:"nested,omitempty"`
	ByBook   map[string]outcome        `json:"byBook,omitempty"`
	Raw      json.RawMessage           `json:"raw,omitempty"`
	Any      any                       `json:"any,omitempty"`
	Lines    []float64                 `json:"lines,omitempty"`
}

const mistyped = `{
  "success": true,
  "outcomes": [{"name": "A", "price": -110}, {"name": "B", "price": "n/a"}],
  "nested": {"Count": "three"},
  "byBook": {"x": {"name": "X", "price": {"american": -120}}},
  "raw": {"kept": ["as", "is"]},
  "any": [1, "two"],
  "lines": [1.5, "2.5"],
  "extra": 1
}`

func TestUnmarshalDropsMistypedValues(t *testing.T) {
	var d doc
	if err := Unmarshal([]byte(mistyped), &d); err != nil {
		t.Fatal(err)
	}
	if !*d.Success || *d.Outcomes[0].Price != -110 || *d.Outcomes[1].Name != "B" {
		t.Errorf("well-typed values lost: %+v", d)
	}
	if d.Outcomes[1].Price != nil || d.Nested == nil || d.Nested.Count != nil || d.ByBook["x"].Price != nil {
		t.Errorf("mistyped values must be left unset: %+v %+v %+v", d.Outcomes[1], d.Nested, d.ByBook["x"])
	}
	if string(d.Raw) != `{"kept":["as","is"]}` || d.Any == nil {
		t.Errorf("raw %s any %v", d.Raw, d.Any)
	}
	if len(d.Lines) != 2 || d.Lines[0] != 1.5 {
		t.Errorf("lines %v", d.Lines)
	}
}

func TestUnmarshalControl(t *testing.T) {
	// CONTROL: encoding/json alone fails, and leaves a mistyped pointer pointing at
	// a zero value, which is exactly what must not reach a caller as a price.
	var d doc
	err := json.Unmarshal([]byte(mistyped), &d)
	if err == nil {
		t.Fatal("control: encoding/json accepted the mistyped document")
	}
	if d.Outcomes[1].Price == nil || *d.Outcomes[1].Price != 0 {
		t.Fatalf("control: expected encoding/json to leave a pointer to 0, got %v", d.Outcomes[1].Price)
	}
}

func TestUnmarshalStillFails(t *testing.T) {
	for _, body := range []string{`[1]`, `"str"`, `{"success": tru`, ``} {
		var d doc
		if err := Unmarshal([]byte(body), &d); err == nil {
			t.Errorf("%q decoded without error", body)
		}
	}
	var d doc
	if err := Unmarshal([]byte(`{"success": null, "outcomes": null}`), &d); err != nil || d.Success != nil || d.Outcomes != nil {
		t.Errorf("nulls: %+v %v", d, err)
	}
}

func TestUnmarshalDropsOutOfRangeNumbers(t *testing.T) {
	type small struct {
		Price *float64 `json:"price,omitempty"`
		Count *int8    `json:"count,omitempty"`
		Size  *uint    `json:"size,omitempty"`
		Name  *string  `json:"name,omitempty"`
	}
	const body = `{"price": 1e400, "count": 300, "size": -1, "name": "kept"}`
	var d small
	if err := Unmarshal([]byte(body), &d); err != nil {
		t.Fatalf("one out-of-range number failed the whole document: %v", err)
	}
	if d.Price != nil || d.Count != nil || d.Size != nil || d.Name == nil || *d.Name != "kept" {
		t.Fatalf("decoded %+v", d)
	}
	// CONTROL: encoding/json alone rejects the document.
	var c small
	if json.Unmarshal([]byte(body), &c) == nil {
		t.Fatal("control: encoding/json accepted 1e400 into a float64")
	}
	// CONTROL: an in-range number of the same fields still decodes.
	var ok small
	if err := Unmarshal([]byte(`{"price": 1e300, "count": 12, "size": 3}`), &ok); err != nil || *ok.Price != 1e300 || *ok.Count != 12 || *ok.Size != 3 {
		t.Fatalf("in range: %+v %v", ok, err)
	}
}
