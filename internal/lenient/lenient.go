// Package lenient decodes API responses without failing on a value whose JSON
// type does not fit its Go field.
package lenient

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

var (
	rawMessageType  = reflect.TypeOf(json.RawMessage(nil))
	unmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
)

// Unmarshal decodes data into v (a non-nil pointer) like json.Unmarshal, except
// that a value whose JSON type does not fit its Go field is dropped, as if the
// field were absent: a pointer stays nil instead of pointing at a zero value, and
// the rest of the document still decodes. An array or map entry that does not
// fit is replaced by null, which keeps positions but is the zero value for a
// non-pointer element type. A document that is not JSON, or whose top level does
// not fit v, is still an error.
//
// The fast path is a plain json.Unmarshal; only a document that fails it with a
// type error is decoded a second time.
func Unmarshal(data []byte, v any) error {
	err := json.Unmarshal(data, v)
	var te *json.UnmarshalTypeError
	if err == nil || !errors.As(err, &te) {
		return err
	}
	var tree any
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if dec.Decode(&tree) != nil {
		return err
	}
	target := reflect.ValueOf(v)
	if target.Kind() != reflect.Pointer || target.IsNil() {
		return err
	}
	clean, fits := sanitize(tree, target.Type().Elem())
	if !fits {
		return err
	}
	b, merr := json.Marshal(clean)
	if merr != nil {
		return err
	}
	target.Elem().Set(reflect.Zero(target.Type().Elem()))
	return json.Unmarshal(b, v)
}

// sanitize returns node with every value that does not fit t removed (object
// fields) or nulled (array and map entries), and whether node itself fits t.
func sanitize(node any, t reflect.Type) (any, bool) {
	if node == nil {
		return nil, true // null fits anything
	}
	if t == rawMessageType || t.Kind() == reflect.Interface {
		return node, true
	}
	if t.Kind() == reflect.Pointer {
		return sanitize(node, t.Elem())
	}
	switch t.Kind() {
	case reflect.Bool:
		_, ok := node.(bool)
		return node, ok
	case reflect.String:
		_, ok := node.(string)
		return node, ok || implementsUnmarshaler(t)
	case reflect.Float32, reflect.Float64:
		// A number out of the type's range (1e400) does not fit either.
		n, ok := node.(json.Number)
		if ok {
			_, err := strconv.ParseFloat(string(n), t.Bits())
			ok = err == nil
		}
		return node, ok
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, ok := node.(json.Number)
		if ok {
			_, err := strconv.ParseInt(string(n), 10, t.Bits())
			ok = err == nil
		}
		return node, ok
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, ok := node.(json.Number)
		if ok {
			_, err := strconv.ParseUint(string(n), 10, t.Bits())
			ok = err == nil
		}
		return node, ok
	case reflect.Slice, reflect.Array:
		arr, ok := node.([]any)
		if !ok {
			return node, false
		}
		for i, e := range arr {
			if c, fits := sanitize(e, t.Elem()); fits {
				arr[i] = c
			} else {
				arr[i] = nil
			}
		}
		return arr, true
	case reflect.Map:
		obj, ok := node.(map[string]any)
		if !ok {
			return node, false
		}
		for k, e := range obj {
			if c, fits := sanitize(e, t.Elem()); fits {
				obj[k] = c
			} else {
				obj[k] = nil
			}
		}
		return obj, true
	case reflect.Struct:
		obj, ok := node.(map[string]any)
		if !ok {
			return node, false
		}
		fields := structFields(t)
		for k, e := range obj {
			ft, found := fields[k]
			if !found {
				ft, found = fields[strings.ToLower(k)]
			}
			if !found {
				continue // unknown keys are kept (a type may collect them)
			}
			if c, fits := sanitize(e, ft); fits {
				obj[k] = c
			} else {
				delete(obj, k)
			}
		}
		return obj, true
	}
	return node, true
}

func implementsUnmarshaler(t reflect.Type) bool {
	return t.Implements(unmarshalerType) || reflect.PointerTo(t).Implements(unmarshalerType)
}

var fieldCache sync.Map // reflect.Type -> map[string]reflect.Type

// structFields maps each JSON name of t's fields (and its lower-case form, for
// encoding/json's case-insensitive match) to the field's type. The result is
// cached per type: a large document visits the same struct types many times.
func structFields(t reflect.Type) map[string]reflect.Type {
	if m, ok := fieldCache.Load(t); ok {
		return m.(map[string]reflect.Type)
	}
	m := buildStructFields(t)
	fieldCache.Store(t, m)
	return m
}

func buildStructFields(t reflect.Type) map[string]reflect.Type {
	out := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = f.Type
		if _, taken := out[strings.ToLower(name)]; !taken {
			out[strings.ToLower(name)] = f.Type
		}
	}
	return out
}
