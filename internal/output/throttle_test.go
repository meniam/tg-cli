package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestThrottleMetadataEnvelopeAndFormats(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		json  string
	}{
		{"success", SuccessDataWithThrottle(map[string]any{"id": 7}, 25), `{"ok":true,"data":{"id":7},"throttled_ms":25}` + "\n"},
		{"failure", FailureDataWithThrottle("flood_wait", "wait", 25), `{"ok":false,"error":{"code":"flood_wait","message":"wait"},"throttled_ms":25}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []Format{FormatJSON, FormatToon, FormatHuman} {
				var b bytes.Buffer
				if err := Write(&b, format, tc.value); err != nil {
					t.Fatal(err)
				}
				if format == FormatJSON {
					if b.String() != tc.json {
						t.Fatalf("JSON = %q, want %q", b.String(), tc.json)
					}
				} else if !strings.Contains(b.String(), "throttled_ms") || !strings.Contains(b.String(), "25") {
					t.Fatalf("%s does not show wait: %q", format, b.String())
				}
			}
		})
	}
	var b bytes.Buffer
	if err := Select(&b, FormatJSON, SuccessDataWithThrottle(map[string]any{"id": 7, "name": "Ann"}, 25), []string{"id"}); err != nil {
		t.Fatal(err)
	}
	if want := `{"ok":true,"data":{"id":7},"throttled_ms":25}` + "\n"; b.String() != want {
		t.Fatalf("--fields output = %q, want %q", b.String(), want)
	}
}

func TestThrottleMetadataJSONLFooter(t *testing.T) {
	items := []struct {
		ID int `json:"id"`
	}{{7}}
	for _, tc := range []struct {
		cursor string
		wait   int64
		want   string
	}{
		{"", 0, `{"id":7}` + "\n"},
		{"next", 0, `{"id":7}` + "\n" + `{"next_cursor":"next"}` + "\n"},
		{"", 25, `{"id":7}` + "\n" + `{"throttled_ms":25}` + "\n"},
		{"next", 25, `{"id":7}` + "\n" + `{"next_cursor":"next","throttled_ms":25}` + "\n"},
	} {
		var b bytes.Buffer
		if err := SelectLinesWithThrottle(&b, items, tc.cursor, []string{"id"}, tc.wait); err != nil {
			t.Fatal(err)
		}
		if b.String() != tc.want {
			t.Fatalf("cursor=%q wait=%d: got %q, want %q", tc.cursor, tc.wait, b.String(), tc.want)
		}
	}
}
