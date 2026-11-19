package output

import (
	"bytes"
	"strings"
	"testing"
)

func audit06Human(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	if err := Human(&b, v); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestAudit06_HumanExactText compares the whole human text, so a change of the
// table layout, the cut, the number format or the failure line shows up.
func TestAudit06_HumanExactText(t *testing.T) {
	type chat struct {
		ID       int     `json:"id"`
		Title    string  `json:"title"`
		Username *string `json:"username"`
	}
	ann := "ann"
	long := strings.Repeat("я", 70)
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"failure line", FailureData("not_found", "no such chat"), "error (not_found): no such chat\n"},
		{
			"a table of objects with one set of keys",
			SuccessData(struct {
				Chats []chat `json:"chats"`
			}{[]chat{{1, "Ann", &ann}, {22, "Family chat", nil}}}),
			"chats:\n" +
				"  id  title        username\n" +
				"  1   Ann          ann\n" +
				"  22  Family chat  -\n",
		},
		{
			"an object keeps its values aligned",
			SuccessData(struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
				Kind  string `json:"kind_of_chat"`
			}{7, "Team", "group"}),
			"id          : 7\n" + "title       : Team\n" + "kind_of_chat: group\n",
		},
		{
			"a long text is cut at 60 runes, not bytes",
			SuccessData(struct {
				Title string `json:"title"`
			}{long}),
			"title: " + strings.Repeat("я", 60) + "...\n",
		},
		{
			"a text of exactly 60 runes is kept and a line break becomes a space",
			SuccessData(struct {
				A string `json:"a"`
				B string `json:"b"`
			}{strings.Repeat("x", 60), "one\ntwo"}),
			"a: " + strings.Repeat("x", 60) + "\n" + "b: one two\n",
		},
		{
			"numbers: whole as is, fractions with four decimals",
			SuccessData(struct {
				Whole int64   `json:"whole"`
				Big   int64   `json:"big"`
				Half  float64 `json:"half"`
				Tiny  float64 `json:"tiny"`
				Neg   float64 `json:"neg"`
			}{5, 9007199254740993, 0.5, 0.123456, -1e-06}),
			"whole: 5\nbig  : 9007199254740993\nhalf : 0.5000\ntiny : 0.1235\nneg  : -0.0000\n",
		},
		{"an empty object", SuccessData(map[string]any{}), "(nothing)\n"},
		{
			"an empty list",
			SuccessData(struct {
				Items []int `json:"items"`
			}{[]int{}}),
			"items:\n  (nothing)\n",
		},
		{
			"a list of plain values is one per line",
			SuccessData(struct {
				Tags []string `json:"tags"`
			}{[]string{"a", "b"}}),
			"tags:\n  - a\n  - b\n",
		},
		{
			"objects with different keys are blocks, not a table",
			SuccessData(struct {
				Items []map[string]any `json:"items"`
			}{[]map[string]any{{"id": 1}, {"id": 2, "extra": "x"}}}),
			"items:\n  -\n    id: 1\n  -\n    extra: x\n    id   : 2\n",
		},
		{
			"a nested value in a table cell shows only its shape",
			SuccessData(struct {
				Items []map[string]any `json:"items"`
			}{[]map[string]any{{"id": 1, "sender": map[string]any{"id": 5}}}}),
			"items:\n  id  sender\n  1   {...}\n",
		},
		{"throttle is the last line", SuccessDataWithThrottle(map[string]any{"id": 7}, 25), "id: 7\nthrottled_ms: 25\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := audit06Human(t, tc.value); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestAudit06_ThrottleExactTextInHumanAndToon pins the whole text of the throttle
// line, not only that the words throttled_ms and 25 occur somewhere.
func TestAudit06_ThrottleExactTextInHumanAndToon(t *testing.T) {
	cases := []struct {
		name   string
		value  any
		format Format
		want   string
	}{
		{"toon success", SuccessDataWithThrottle(map[string]any{"id": 7}, 25), FormatToon, "ok: true\ndata:\n  id: 7\nthrottled_ms: 25\n"},
		{"toon failure", FailureDataWithThrottle("flood_wait", "wait", 25), FormatToon, "ok: false\nerror:\n  code: flood_wait\n  message: wait\nthrottled_ms: 25\n"},
		{"human success", SuccessDataWithThrottle(map[string]any{"id": 7}, 25), FormatHuman, "id: 7\nthrottled_ms: 25\n"},
		{"human failure", FailureDataWithThrottle("flood_wait", "wait", 25), FormatHuman, "error (flood_wait): wait\nthrottled_ms: 25\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := Write(&b, tc.format, tc.value); err != nil {
				t.Fatal(err)
			}
			if b.String() != tc.want {
				t.Errorf("got %q, want %q", b.String(), tc.want)
			}
		})
	}
}
