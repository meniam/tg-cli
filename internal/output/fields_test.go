package output

import (
	"bytes"
	"testing"
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
			}}),
			// username is optional and absent from the second chat: the schema
			// has it, this response does not.
			paths: []string{"chats.id", "chats.username", "chats.nope"},
			want:  `{"ok":true,"data":{"chats":[{"id":1,"username":"ann"}]}}` + "\n",
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
