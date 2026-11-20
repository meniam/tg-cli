package schema

import (
	"encoding/json"
	"reflect"
	"testing"
)

// audit07Marked puts the untrusted tag on every shape a text can have.
type audit07Marked struct {
	Text    string          `json:"text" untrusted:"true"`
	Pointer *string         `json:"pointer" untrusted:"true"`
	Raw     json.RawMessage `json:"raw" untrusted:"true"`
	Count   int             `json:"count" untrusted:"true"`
	Plain   string          `json:"plain"`
	Names   []string        `json:"names" untrusted:"true"`
}

// TestAudit07_UntrustedTagByShape fixes which shapes keep the mark. The mark
// only means something for text, so an integer drops it.
func TestAudit07_UntrustedTagByShape(t *testing.T) {
	fields := fieldMap(t, reflect.TypeFor[audit07Marked]())
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"text", true},
		{"pointer", true},
		{"raw", true},
		{"count", false},
		{"plain", false},
	} {
		if got := fields[tc.path].Untrusted; got != tc.want {
			t.Errorf("%s untrusted=%v, want %v", tc.path, got, tc.want)
		}
	}
	if got := fields["names"]; got.Type != TypeArray || got.Element != TypeString {
		t.Fatalf("names is %s of %s, want array of string", got.Type, got.Element)
	}
	if !fields["names"].Untrusted {
		t.Fatal(`a []string with untrusted:"true" loses the mark in the schema`)
	}
}
