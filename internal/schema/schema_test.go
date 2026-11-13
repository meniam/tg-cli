package schema

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"
)

// every holds one field of every shape a result type can use.
type every struct {
	Text    string  `json:"text"`
	Count   int     `json:"count"`
	Ratio   float64 `json:"ratio"`
	On      bool    `json:"on"`
	Skipped string  `json:"-"`
	// untagged fields keep their Go name in the JSON
	NoTag   string
	private string

	// A field can be absent when it is a pointer or when its tag says omitempty.
	Comment  string   `json:"comment,omitempty"`
	Parent   *int     `json:"parent"`
	Tags     []string `json:"tags"`
	Children []child  `json:"children"`
	Matrix   [][]int  `json:"matrix"`
	Blob     []byte   `json:"blob"`
	When     time.Time
	Raw      json.RawMessage `json:"raw"`
	Extra    map[string]int  `json:"extra"`
	Anything any             `json:"anything"`
	Node     *node           `json:"node,omitempty"`
	Names    map[string][]child
	Empty    struct{} `json:"empty"`
}

type child struct {
	ID   int    `json:"id"`
	Name string `json:"child_name"`
	Deep *deep  `json:"deep,omitempty"`
}

type deep struct {
	Value string `json:"value"`
}

// node contains itself, so the walk must stop.
type node struct {
	Name string `json:"name"`
	Next *node  `json:"next,omitempty"`
}

// embedded is promoted into the fields of the outer type.
type embedded struct {
	Promoted string `json:"promoted"`
	hidden   string
}

// namedEmbed keeps its own name in the JSON.
type namedEmbed struct {
	Value string `json:"value"`
}

// outer checks the embedded cases.
type outer struct {
	embedded
	*namedEmbed `json:"named"`
	Plain       embedded `json:"plain"`
}

func fieldMap(t *testing.T, typ reflect.Type) map[string]Field {
	t.Helper()
	fields := Fields(typ)
	if len(fields) == 0 {
		t.Fatalf("no fields for %v", typ)
	}
	out := map[string]Field{}
	for _, f := range fields {
		if _, dup := out[f.Path]; dup {
			t.Errorf("path %q appears twice", f.Path)
		}
		out[f.Path] = f
	}
	return out
}

// checkFields compares the walked paths with the expected ones, both ways: a
// path that is missing and a path nobody expected are both failures.
func checkFields(t *testing.T, typ reflect.Type, want map[string]Field) {
	t.Helper()
	got := fieldMap(t, typ)
	var paths []string
	for path := range got {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if _, ok := want[path]; !ok {
			t.Errorf("%v: unexpected path %q (%+v)", typ, path, got[path])
		}
	}
	for path, wantField := range want {
		gotField, ok := got[path]
		if !ok {
			t.Errorf("%v: missing path %q", typ, path)
			continue
		}
		if gotField != wantField {
			t.Errorf("%v: %q = %+v, want %+v", typ, path, gotField, wantField)
		}
	}
}

func TestFieldsCoverEveryShape(t *testing.T) {
	checkFields(t, reflect.TypeOf(every{}), map[string]Field{
		"text":  {Path: "text", Type: TypeString},
		"count": {Path: "count", Type: TypeInteger},
		"ratio": {Path: "ratio", Type: TypeNumber},
		"on":    {Path: "on", Type: TypeBoolean},
		"NoTag": {Path: "NoTag", Type: TypeString},
		"When":  {Path: "When", Type: TypeTime},
		"Names": {Path: "Names", Type: TypeObject},

		// omitempty and pointers make a field optional, then and there.
		"comment": {Path: "comment", Type: TypeString, Optional: true},
		"parent":  {Path: "parent", Type: TypeInteger, Optional: true},

		"tags":                {Path: "tags", Type: TypeArray, Element: TypeString},
		"children":            {Path: "children", Type: TypeArray, Element: TypeObject},
		"children.id":         {Path: "children.id", Type: TypeInteger},
		"children.child_name": {Path: "children.child_name", Type: TypeString},
		"children.deep":       {Path: "children.deep", Type: TypeObject, Optional: true},
		"children.deep.value": {Path: "children.deep.value", Type: TypeString, Optional: true},
		"matrix":              {Path: "matrix", Type: TypeArray, Element: TypeArray},
		"blob":                {Path: "blob", Type: TypeString},
		"raw":                 {Path: "raw", Type: TypeJSON},
		"extra":               {Path: "extra", Type: TypeObject},
		"anything":            {Path: "anything", Type: TypeAny},
		"empty":               {Path: "empty", Type: TypeObject},

		// A type that contains itself is listed once, then the walk stops.
		"node":      {Path: "node", Type: TypeObject, Optional: true},
		"node.name": {Path: "node.name", Type: TypeString, Optional: true},
		"node.next": {Path: "node.next", Type: TypeObject, Optional: true},
	})
}

func TestFieldsOfEmbeddedStructs(t *testing.T) {
	checkFields(t, reflect.TypeOf(outer{}), map[string]Field{
		// An embedded struct without a json name is promoted.
		"promoted": {Path: "promoted", Type: TypeString},
		// An embedded pointer with a json name stays one field, and its
		// members are optional with it.
		"named":       {Path: "named", Type: TypeObject, Optional: true},
		"named.value": {Path: "named.value", Type: TypeString, Optional: true},
		// An embedded struct given as a named field is a nested object.
		"plain":          {Path: "plain", Type: TypeObject},
		"plain.promoted": {Path: "plain.promoted", Type: TypeString},
	})
}

// TestFieldsMatchJSON checks the list against what encoding/json really prints
// for the same type, so the two views cannot drift apart.
func TestFieldsMatchJSON(t *testing.T) {
	value := every{
		Text: "t", Count: 1, Ratio: 0.5, On: true, NoTag: "untagged",
		Comment: "c", Parent: ptr(7), Tags: []string{"a"},
		Children: []child{{ID: 2, Name: "c", Deep: &deep{Value: "v"}}},
		Matrix:   [][]int{{1}}, Blob: []byte("bytes"),
		When: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Raw:  json.RawMessage(`{"k":1}`), Extra: map[string]int{"k": 1},
		Anything: "s", Node: &node{Name: "n", Next: &node{Name: "m"}},
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := Paths(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("the fixture printed no fields")
	}
	fields := Fields(reflect.TypeOf(value))
	// A path of the real JSON must be a path of the schema. The other way
	// round is allowed: a nil pointer or omitempty leaves a field out.
	for _, missing := range Missing(paths, fields) {
		t.Errorf("the JSON has %q, the schema does not: %s", missing, data)
	}
}

func ptr[T any](v T) *T { return &v }
