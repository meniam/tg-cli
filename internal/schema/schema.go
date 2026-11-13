// Package schema turns the Go type of a command result into the list of field
// paths that `tg-cli schema` prints and `--fields` selects.
//
// The list follows the `json` tags of the result types, so it is never written
// by hand: a field added to a result type appears in the schema by itself.
package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
	"unicode"
)

// Type names of Field.Type and Field.Element, in the terms an agent sees in the
// JSON. They say how a value is encoded, not which Go type holds it.
const (
	// TypeString is a JSON string.
	TypeString = "string"
	// TypeInteger is a JSON number without a fraction.
	TypeInteger = "integer"
	// TypeNumber is a JSON number, possibly with a fraction.
	TypeNumber = "number"
	// TypeBoolean is true or false.
	TypeBoolean = "boolean"
	// TypeObject is a JSON object whose members are listed as paths below it.
	TypeObject = "object"
	// TypeArray is a JSON array; Field.Element says what its elements hold.
	TypeArray = "array"
	// TypeTime is an RFC 3339 time in a JSON string.
	TypeTime = "time"
	// TypeJSON is a JSON value of any shape, whose members are unknown here.
	TypeJSON = "json"
	// TypeAny is a value of no fixed shape, whose members are unknown here.
	TypeAny = "any"
)

// Field is one value of a result, addressed by its path.
//
// A path is relative to data in the success envelope and, for a JSONL line, to
// the line itself. Dots separate the levels. A path never names an index:
// "chats.id" is the id of every chat of the list.
type Field struct {
	Path string `json:"path"`
	Type string `json:"type"`
	// Element is the type of the elements, set for TypeArray only.
	Element string `json:"element,omitempty"`
	// Optional is true when the field can be absent from a response: the Go
	// field is a pointer or its tag carries omitempty, or one of its parents
	// can be absent.
	Optional bool `json:"optional,omitempty"`
}

var (
	timeType = reflect.TypeOf(time.Time{})
	rawType  = reflect.TypeOf(json.RawMessage(nil))
)

// Fields lists every field of the JSON object made from t: the type of data in
// a success envelope, or the type of one JSONL line. It returns nil when t is
// not a struct: such a result has no fields to name.
//
// A map, a json.RawMessage and an interface value have no fixed members; each
// is listed once and not walked into.
func Fields(t reflect.Type) []Field {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || t == timeType {
		return nil
	}
	w := &walker{onPath: map[reflect.Type]bool{}}
	w.structFields(t, "", false)
	return w.out
}

type walker struct {
	out []Field
	// onPath holds the struct types the walk is inside: a type that contains
	// itself is listed once and then stops, so the walk always ends.
	onPath map[reflect.Type]bool
}

// structFields appends the fields of t. prefix is the path of t itself, empty at
// the root; optional says t may be absent as a whole, which makes all of its
// fields absent too.
//
// The walk stops when a struct type appears twice on one path: a type that
// contains itself is described once, not for ever.
func (w *walker) structFields(t reflect.Type, prefix string, optional bool) {
	if w.onPath[t] {
		return
	}
	w.onPath[t] = true
	defer delete(w.onPath, t)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		// An anonymous field is encoded even when its type is unexported:
		// encoding/json promotes or names it all the same.
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		name, omit, named := jsonTag(f)
		if name == "-" {
			continue
		}
		if f.Anonymous && !named {
			// Like encoding/json, an embedded struct without a json name is
			// promoted: its fields sit at the level of the outer one.
			et := f.Type
			embeddedOptional := optional || et.Kind() == reflect.Pointer
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && et != timeType && et != rawType {
				w.structFields(et, prefix, embeddedOptional)
				continue
			}
		}
		if name == "" {
			name = f.Name // an untagged field keeps its Go name in the JSON
		}
		w.value(f.Type, join(prefix, name), optional || omit)
	}
}

// value appends the field at path for a value of type t.
func (w *walker) value(t reflect.Type, path string, optional bool) {
	for t.Kind() == reflect.Pointer {
		optional = true
		t = t.Elem()
	}
	if t == rawType {
		w.out = append(w.out, Field{Path: path, Type: TypeJSON, Optional: optional})
		return
	}
	if name := kindName(t.Kind()); name != "" {
		w.out = append(w.out, Field{Path: path, Type: name, Optional: optional})
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		if t == timeType {
			w.out = append(w.out, Field{Path: path, Type: TypeTime, Optional: optional})
			return
		}
		w.out = append(w.out, Field{Path: path, Type: TypeObject, Optional: optional})
		w.structFields(t, path, optional)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			// []byte is one base64 string in JSON.
			w.out = append(w.out, Field{Path: path, Type: TypeString, Optional: optional})
			return
		}
		w.out = append(w.out, Field{Path: path, Type: TypeArray, Element: elementName(t.Elem()), Optional: optional})
		w.element(t.Elem(), path, optional)
	case reflect.Map:
		// The keys of a map come from the data, so its members have no path.
		w.out = append(w.out, Field{Path: path, Type: TypeObject, Optional: optional})
	default:
		w.out = append(w.out, Field{Path: path, Type: TypeAny, Optional: optional})
	}
}

// element lists the fields of the elements of an array at path.
func (w *walker) element(t reflect.Type, path string, optional bool) {
	for t.Kind() == reflect.Pointer {
		optional = true
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if t != timeType {
			w.structFields(t, path, optional)
		}
	case reflect.Slice, reflect.Array:
		w.element(t.Elem(), path, optional)
	}
}

// elementName is the type of the elements of an array.
func elementName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawType {
		return TypeJSON
	}
	if name := kindName(t.Kind()); name != "" {
		return name
	}
	switch t.Kind() {
	case reflect.Struct:
		if t == timeType {
			return TypeTime
		}
		return TypeObject
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return TypeString
		}
		return TypeArray
	case reflect.Map:
		return TypeObject
	}
	return TypeAny
}

// kindName is the type name of a kind that is always the same in JSON, empty
// for the kinds that need their own handling.
func kindName(k reflect.Kind) string {
	switch k {
	case reflect.Bool:
		return TypeBoolean
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return TypeInteger
	case reflect.Float32, reflect.Float64:
		return TypeNumber
	case reflect.String:
		return TypeString
	case reflect.Interface:
		return TypeAny
	}
	return ""
}

// jsonTag reads the name, the omitempty option and whether the tag names the
// field, which decides if an embedded struct is promoted or stays a field.
// Other options, like ",string", do not change the shape of the JSON.
func jsonTag(f reflect.StructField) (name string, omit, named bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "-", false, true
	}
	name, opts, _ := strings.Cut(tag, ",")
	if !validName(name) {
		name = ""
	}
	for _, opt := range strings.Split(opts, ",") {
		if opt == "omitempty" {
			omit = true
		}
	}
	return name, omit, name != ""
}

// FieldType finds the Go type at a path of a struct, the same notation Fields
// uses: dots between the levels, no indexes. The path may end on a list, which
// is how a list command finds the type of one JSONL line.
func FieldType(t reflect.Type, path string) (reflect.Type, bool) {
	if t == nil {
		return nil, false
	}
	for _, part := range strings.Split(path, ".") {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			t = t.Elem()
		}
		if t.Kind() != reflect.Struct {
			return nil, false
		}
		found := false
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, named := jsonTag(f)
			if !named {
				name = f.Name
			}
			if name == part {
				t, found = f.Type, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return t, true
}

// validName reports whether the tag names a field. Copied from encoding/json so
// that a tag it ignores is ignored here too.
func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}
	return true
}

// join appends a field name to a path.
func join(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}
