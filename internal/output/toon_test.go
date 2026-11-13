package output

import (
	"bytes"
	"encoding/json"
	"testing"

	toon "github.com/toon-format/toon-go"
)

// TestToonDecodesBackToTheSameData is the point of the format: whatever a
// command prints as TOON must decode to the data JSON prints for it, with one
// difference the decision names: a field an element of a list does not have
// comes back as null, so the elements share one set of columns.
//
// Nothing else checks this, and a mistake here silently changes what an agent
// reads: a value read in the wrong place, a number rounded, a title lost.
func TestToonDecodesBackToTheSameData(t *testing.T) {
	type sender struct {
		ID   int64  `json:"id"`
		Name string `json:"name,omitempty"`
	}
	type message struct {
		ID     int     `json:"id"`
		ChatID int64   `json:"chat_id"`
		Text   string  `json:"text"`
		Sender *sender `json:"sender,omitempty"`
	}
	cases := map[string]any{
		// A table: every element has the same keys.
		"a list of uniform objects": SuccessData(map[string]any{"chats": []map[string]any{
			{"id": 1, "type": "user", "title": "Ann"},
			{"id": -2, "type": "group", "title": "Family"},
		}}),
		// The case alignment exists for: the optional field makes the elements
		// different, so without it TOON writes every element in full.
		"a list with a field missing from some items": SuccessData(map[string]any{"messages": []message{
			{ID: 1, ChatID: 77, Text: "a", Sender: &sender{ID: 5, Name: "Ann"}},
			{ID: 2, ChatID: 77, Text: "b"},
		}}),
		"a single object": SuccessData(message{ID: 1, ChatID: 77, Text: "a"}),
		"a failure":       FailureData("not_found", "no such chat"),
		"empty lists":     SuccessData(map[string]any{"chats": []any{}, "notes": nil}),
	}
	for name, envelope := range cases {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			if err := Toon(&b, envelope); err != nil {
				t.Fatal(err)
			}
			back, err := toon.DecodeString(b.String())
			if err != nil {
				t.Fatalf("TOON does not decode: %v\n%s", err, b.String())
			}
			got := canonical(t, back)
			// The data must be what the JSON of the same envelope carries, with
			// the nulls the alignment adds. The JSON is the reference rather
			// than the Go value, so the two formats stay tied together.
			var wantJSON bytes.Buffer
			if err := encode(&wantJSON, envelope); err != nil {
				t.Fatal(err)
			}
			var wantValue any
			if err := json.Unmarshal(wantJSON.Bytes(), &wantValue); err != nil {
				t.Fatal(err)
			}
			want := canonical(t, withNulls(wantValue))
			if got != want {
				t.Errorf("TOON decodes to different data:\n got %s\nwant %s\nTOON:\n%s", got, want, b.String())
			}
		})
	}
}

// withNulls fills the fields that the alignment added, so the expected value has
// the same shape as what the encoder writes: an object that lacks a key another
// element of its list has gets that key as null.
func withNulls(v any) any {
	list, ok := v.([]any)
	if !ok {
		if object, ok := v.(map[string]any); ok {
			for key, value := range object {
				object[key] = withNulls(value)
			}
		}
		return v
	}
	keys := map[string]bool{}
	for _, item := range list {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for key := range object {
			keys[key] = true
		}
	}
	for _, item := range list {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for key := range keys {
			if _, has := object[key]; !has {
				object[key] = nil
			}
		}
		for key, value := range object {
			object[key] = withNulls(value)
		}
	}
	return v
}

// canonical renders a decoded JSON value as JSON with sorted keys, so two values
// are compared by their data and not by key order.
func canonical(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestToonAlignsElementsOfATable checks the alignment itself: the elements of a
// table share one set of keys, so the encoder can use its compact form.
func TestToonAlignsElementsOfATable(t *testing.T) {
	type item struct {
		ID   int    `json:"id"`
		Name string `json:"name,omitempty"`
	}
	var b bytes.Buffer
	err := Toon(&b, SuccessData(map[string]any{"items": []item{{1, "a"}, {2, ""}}}))
	if err != nil {
		t.Fatal(err)
	}
	// The header line names both columns once, and a missing value is null.
	for _, want := range []string{"items[2]{id,name}:", "1,a", "2,null"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("TOON must contain %q, got:\n%s", want, b.String())
		}
	}
}

// TestToonKeepsNumbersExact guards the values a wrong conversion would corrupt:
// a chat id, a whole number beyond 2^53 and a non-ASCII text. The encoder turns
// a float64 back into a number that no longer matches, so the conversion to the
// encoder's input has to keep an int64.
func TestToonKeepsNumbersExact(t *testing.T) {
	var b bytes.Buffer
	envelope := SuccessData(map[string]any{
		"channel": int64(-1001234567890),   // a channel id, as the Bot API writes it
		"big":     int64(9007199254740993), // 2^53 + 1: a float64 cannot hold it
		"title":   "Ёлка",
	})
	if err := Toon(&b, envelope); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-1001234567890", "9007199254740993", "Ёлка"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("TOON must contain %q, got:\n%s", want, b.String())
		}
	}
	for _, wrong := range []string{"9007199254740992", "90071992547409930"} {
		if bytes.Contains(b.Bytes(), []byte(wrong)) {
			t.Errorf("TOON changed a number to %q:\n%s", wrong, b.String())
		}
	}
}
