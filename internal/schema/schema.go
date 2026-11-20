// Package schema turns the Go type of a command result into the list of field
// paths that `tg schema` prints and `--fields` selects.
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
	// Untrusted marks text supplied by Telegram or search results from it.
	Untrusted bool `json:"untrusted,omitempty"`
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
	w := &walker{
		onPath:        map[reflect.Type]bool{},
		taken:         map[string]candidate{},
		byDeclaration: map[declaration]Field{},
	}
	w.structFields(t, "", false, 0)
	return w.result()
}

// declaration identifies the struct field a path comes from. Two embedded
// structs can declare fields that produce the same Field value, so the winner
// of a path is decided by the declaration and not by the value.
type declaration struct {
	owner reflect.Type
	index int
}

// candidate is the declaration that stands for a path, with the depth of the
// struct that declares it: a field of an embedded struct is one level deeper.
type candidate struct {
	field declaration
	depth int
	// ambiguous is set when two declarations of one path sit at the same depth.
	// encoding/json drops such a field from the output, and so does the schema.
	ambiguous bool
}

// entry is a field of the walk with the declaration it came from.
type entry struct {
	field  Field
	origin declaration
}

type walker struct {
	out []entry
	// onPath holds the struct types the walk is inside: a type that contains
	// itself is listed once and then stops, so the walk always ends.
	onPath map[reflect.Type]bool
	// taken records the field of every path, so a duplicate is resolved the
	// way encoding/json resolves it rather than listed twice.
	taken map[string]candidate
	// byDeclaration keeps the Field of every declaration, so the winner of a
	// path can be printed even when it was walked before the loser.
	byDeclaration map[declaration]Field
}

// result returns the fields that survive the duplicate resolution: one field per
// path, in the order the walk first saw the path.
func (w *walker) result() []Field {
	out := make([]Field, 0, len(w.out))
	emitted := make(map[string]bool, len(w.out))
	for _, e := range w.out {
		if emitted[e.field.Path] {
			continue
		}
		emitted[e.field.Path] = true
		c, ok := w.taken[e.field.Path]
		if !ok || c.ambiguous {
			continue // two fields of one depth cancel each other, like encoding/json
		}
		if c.field == e.origin {
			out = append(out, e.field)
			continue
		}
		// A shallower field won this path after the deeper one was walked.
		out = append(out, w.byDeclaration[c.field])
	}
	return out
}

// take records the declaration of a path: a field declared closer to the root
// wins over one promoted from an embedded struct, and two declarations of one
// depth cancel each other, which is what encoding/json does.
func (w *walker) take(decl declaration, f Field, depth int) {
	known, seen := w.taken[f.Path]
	switch {
	case !seen:
		w.taken[f.Path] = candidate{field: decl, depth: depth}
	case depth < known.depth:
		w.taken[f.Path] = candidate{field: decl, depth: depth}
	case depth == known.depth && known.field != decl:
		w.taken[f.Path] = candidate{depth: depth, ambiguous: true}
	}
}

// structFields appends the fields of t. prefix is the path of t itself, empty at
// the root; optional says t may be absent as a whole, which makes all of its
// fields absent too.
//
// The walk stops when a struct type appears twice on one path: a type that
// contains itself is described once, not for ever.
func (w *walker) structFields(t reflect.Type, prefix string, optional bool, depth int) {
	if w.onPath[t] {
		return
	}
	w.onPath[t] = true
	defer delete(w.onPath, t)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		decl := declaration{owner: t, index: i}
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
				w.structFields(et, prefix, embeddedOptional, depth+1)
				continue
			}
		}
		if name == "" {
			name = f.Name // an untagged field keeps its Go name in the JSON
		}
		w.value(f.Type, join(prefix, name), optional || omit, decl, depth, f.Tag.Get("untrusted") == "true")
	}
}

// value appends the field at path for a value of type t. depth is the depth of
// the struct that declares it, which decides a duplicate path.
func (w *walker) value(t reflect.Type, path string, optional bool, decl declaration, depth int, untrusted bool) {
	for t.Kind() == reflect.Pointer {
		optional = true
		t = t.Elem()
	}
	if t == rawType {
		w.add(Field{Path: path, Type: TypeJSON, Optional: optional, Untrusted: untrusted}, decl, depth)
		return
	}
	if name := kindName(t.Kind()); name != "" {
		w.add(Field{Path: path, Type: name, Optional: optional, Untrusted: untrusted && name == TypeString}, decl, depth)
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		if t == timeType {
			w.add(Field{Path: path, Type: TypeTime, Optional: optional}, decl, depth)
			return
		}
		w.add(Field{Path: path, Type: TypeObject, Optional: optional}, decl, depth)
		w.structFields(t, path, optional, depth)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			// []byte is one base64 string in JSON.
			w.add(Field{Path: path, Type: TypeString, Optional: optional}, decl, depth)
			return
		}
		element := elementName(t.Elem())
		// An array of strings carries the mark of its tag: each item is text.
		w.add(Field{Path: path, Type: TypeArray, Element: element, Optional: optional, Untrusted: untrusted && element == TypeString}, decl, depth)
		w.element(t.Elem(), path, optional, decl, depth)
	case reflect.Map:
		// The keys of a map come from the data, so its members have no path.
		w.add(Field{Path: path, Type: TypeObject, Optional: optional}, decl, depth)
	default:
		w.add(Field{Path: path, Type: TypeAny, Optional: optional}, decl, depth)
	}
}

// add records a field of the walk and resolves a duplicate path.
func (w *walker) add(f Field, decl declaration, depth int) {
	w.out = append(w.out, entry{field: f, origin: decl})
	w.byDeclaration[decl] = f
	w.take(decl, f, depth)
}

// element lists the fields of the elements of an array at path. The elements sit
// one level deeper than the array, which is what makes their fields deeper.
func (w *walker) element(t reflect.Type, path string, optional bool, decl declaration, depth int) {
	for t.Kind() == reflect.Pointer {
		optional = true
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if t != timeType {
			w.structFields(t, path, optional, depth+1)
		}
	case reflect.Slice, reflect.Array:
		w.element(t.Elem(), path, optional, decl, depth+1)
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
		next, found := fieldByPath(t, part)
		if !found {
			return nil, false
		}
		t = next
	}
	return t, true
}

// fieldByPath finds the type of one level of a path in a struct. A field of an
// embedded struct counts as a field of the outer one, the way Fields lists it:
// both walk the same JSON shape, so a path the schema prints is one FieldType
// can follow.
func fieldByPath(t reflect.Type, name string) (reflect.Type, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		jsonName, _, named := jsonTag(f)
		if jsonName == "-" {
			continue
		}
		if f.Anonymous && !named {
			et := f.Type
			for et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct && et != timeType && et != rawType {
				if found, ok := fieldByPath(et, name); ok {
					return found, true
				}
				continue
			}
		}
		if jsonName == "" {
			jsonName = f.Name
		}
		if jsonName == name {
			return f.Type, true
		}
	}
	return nil, false
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
