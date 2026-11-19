package output

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"tg-cli/internal/schema"
)

// TestSelectKeepsOnlyTheChosenPaths is the contract of --fields from the
// decision: the paths are applied to data of an object and to every element of a
// list, a path the result does not have is skipped in silence, and the error
// envelope is not touched. A wrong answer here silently gives an agent less data
// than it asked for, or data it did not ask for.
func TestSelectKeepsOnlyTheChosenPaths(t *testing.T) {
	cases := []struct {
		name     string
		envelope any
		paths    []string
		want     string
	}{
		{
			name:     "an object keeps the named fields",
			envelope: SuccessData(map[string]any{"id": 7, "title": "Team", "members": 5}),
			paths:    []string{"id", "title"},
			want:     `{"ok":true,"data":{"id":7,"title":"Team"}}` + "\n",
		},
		{
			name: "a list is narrowed element by element",
			envelope: SuccessData(map[string]any{"chats": []map[string]any{
				{"id": 1, "title": "Ann", "type": "user"},
				{"id": -2, "title": "Family", "type": "group"},
			}}),
			paths: []string{"chats.id"},
			want:  `{"ok":true,"data":{"chats":[{"id":1},{"id":-2}]}}` + "\n",
		},
		{
			name: "a nested path keeps the levels above it",
			envelope: SuccessData(map[string]any{"messages": []map[string]any{
				{"id": 1, "sender": map[string]any{"id": 5, "name": "Ann"}},
				{"id": 2},
			}}),
			paths: []string{"messages.sender.name"},
			want:  `{"ok":true,"data":{"messages":[{"sender":{"name":"Ann"}},{}]}}` + "\n",
		},
		{
			name: "a path the result does not have adds nothing",
			envelope: SuccessData(map[string]any{"chats": []map[string]any{
				{"id": 1, "username": "ann"},
				{"id": 2},
			}}),
			// username is optional and absent from the second chat: the schema
			// has it, this response does not.
			paths: []string{"chats.id", "chats.username", "chats.nope"},
			want:  `{"ok":true,"data":{"chats":[{"id":1,"username":"ann"},{"id":2}]}}` + "\n",
		},
		{
			name:     "the error envelope is not filtered",
			envelope: FailureData("not_found", "no such chat"),
			paths:    []string{"id"},
			want:     `{"ok":false,"error":{"code":"not_found","message":"no such chat"}}` + "\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := Select(&b, FormatJSON, tc.envelope, tc.paths); err != nil {
				t.Fatal(err)
			}
			if got := b.String(); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestResultPathsUsesSchemaForListAndTopLevelFields(t *testing.T) {
	type item struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Name  string `json:"name,omitempty"`
	}
	type result struct {
		Chats      []item `json:"chats"`
		Title      string `json:"title,omitempty"`
		NextCursor string `json:"next_cursor,omitempty"`
	}
	paths := []string{"id", "title", "next_cursor", "chats.name", "absent"}
	want := []string{"chats.id", "title", "next_cursor", "chats.name", "absent"}
	got := ResultPaths(paths, "chats", schema.Fields(reflect.TypeOf(result{})))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestSelectKeepsANumberExact guards the value that a round trip through the
// filter could round: a chat id beyond 2^53 must come out as it went in.
func TestSelectKeepsANumberExact(t *testing.T) {
	var b bytes.Buffer
	envelope := SuccessData(map[string]any{"channel": int64(-1001234567890), "big": int64(9007199254740993)})
	if err := Select(&b, FormatJSON, envelope, []string{"big", "channel"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-1001234567890", "9007199254740993"} {
		if !bytes.Contains(b.Bytes(), []byte(want)) {
			t.Errorf("the filtered answer must contain %s, got %s", want, b.String())
		}
	}
}

func TestAudit06ItemPaths(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		item  string
		want  []string
	}{
		{"no item path keeps the paths", []string{"a", "b.c"}, "", []string{"a", "b.c"}},
		{"the item path names the whole item", []string{"chats"}, "chats", []string{""}},
		{"a prefixed path loses the prefix", []string{"chats.id"}, "chats", []string{"id"}},
		{"a bare path stays as it is", []string{"id", "next_cursor"}, "chats", []string{"id", "next_cursor"}},
		{"a longer name is not a prefix", []string{"chatsx.id"}, "chats", []string{"chatsx.id"}},
		{"mixed paths", []string{"chats.id", "title", "chats.sender.name"}, "chats", []string{"id", "title", "sender.name"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ItemPaths(tc.paths, tc.item); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ItemPaths(%q, %q) = %q, want %q", tc.paths, tc.item, got, tc.want)
			}
		})
	}
}

// TestAudit06JSONLWholeItem: --fields chats on a list result names the whole
// element, so a JSONL line must keep every field of it.
func TestAudit06JSONLWholeItem(t *testing.T) {
	type chat struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
	}
	items := []chat{{ID: 1, Title: "Ann"}, {ID: 2, Title: "Bob"}}
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"the whole item", ItemPaths([]string{"chats"}, "chats"), `{"id":1,"title":"Ann"}` + "\n" + `{"id":2,"title":"Bob"}` + "\n"},
		{"the whole item next to a field", ItemPaths([]string{"chats", "chats.id"}, "chats"), `{"id":1,"title":"Ann"}` + "\n" + `{"id":2,"title":"Bob"}` + "\n"},
		{"one field", ItemPaths([]string{"chats.id"}, "chats"), `{"id":1}` + "\n" + `{"id":2}` + "\n"},
		{"no paths", nil, `{"id":1,"title":"Ann"}` + "\n" + `{"id":2,"title":"Bob"}` + "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := SelectLines(&b, items, "", tc.paths); err != nil {
				t.Fatal(err)
			}
			if got := b.String(); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestAudit06ParseFields(t *testing.T) {
	good := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a, b,", []string{"a", "b"}},
		{" a.b , c ", []string{"a.b", "c"}},
		{",,a,,", []string{"a"}},
		{"snake_case,kebab-case,x1", []string{"snake_case", "kebab-case", "x1"}},
		{"имя", []string{"имя"}},
	}
	for _, tc := range good {
		got, err := ParseFields(tc.in)
		if err != nil {
			t.Errorf("ParseFields(%q) failed: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseFields(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	bad := []struct{ in, want string }{
		{"a..b", "empty level"},
		{".a", "empty level"},
		{"a.", "empty level"},
		{"a b", "not part of a field name"},
		{"a/b", "not part of a field name"},
		{"a[0]", "not part of a field name"},
		{"ok,a*", "not part of a field name"},
	}
	for _, tc := range bad {
		got, err := ParseFields(tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseFields(%q) = %q, %v; want an error with %q", tc.in, got, err, tc.want)
		}
	}
}

func TestAudit06FilterData(t *testing.T) {
	value := map[string]any{"id": 1, "title": "Ann", "tags": []string{"x", "y"}}
	cases := []struct {
		name  string
		paths []string
		want  string
	}{
		{"no paths returns the value", nil, `{"id":1,"tags":["x","y"],"title":"Ann"}`},
		{"empty path keeps the whole value", []string{""}, `{"id":1,"tags":["x","y"],"title":"Ann"}`},
		{"one field", []string{"id"}, `{"id":1}`},
		{"a list field is kept whole", []string{"tags"}, `{"tags":["x","y"]}`},
		{"an unknown path selects nothing", []string{"nope"}, `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FilterData(value, tc.paths)
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			if err := encode(&b, got); err != nil {
				t.Fatal(err)
			}
			// map keys marshal sorted, so the field order is stable.
			if s := strings.TrimSpace(b.String()); s != tc.want {
				t.Errorf("got %s, want %s", s, tc.want)
			}
		})
	}
}
