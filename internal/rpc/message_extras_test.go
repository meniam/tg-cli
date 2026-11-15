package rpc

import "testing"

func TestMessageExtrasPeriods(t *testing.T) {
	for _, tc := range []struct {
		input string
		value int
		valid bool
	}{
		{"off", 0, true}, {"1d", 86400, true}, {"1w", 604800, true}, {"1m", 2592000, true}, {"86400", 86400, true}, {"31536000", 31536000, true}, {"86399", 0, false}, {"31536001", 0, false}, {"invalid", 0, false},
	} {
		got, err := ParseAutoDeletePeriod(tc.input)
		if (err == nil) != tc.valid || (tc.valid && got != tc.value) {
			t.Errorf("period %q: %d %v", tc.input, got, err)
		}
	}
	for _, tc := range []struct {
		input string
		value int
		valid bool
	}{
		{"view-once", 2147483647, true}, {"1", 1, true}, {"60", 60, true}, {"0", 0, false}, {"61", 0, false}, {"bad", 0, false},
	} {
		got, err := ParseSelfDestruct(tc.input)
		if (err == nil) != tc.valid || (tc.valid && got != tc.value) {
			t.Errorf("self-destruct %q: %d %v", tc.input, got, err)
		}
	}
}
func TestStickerShortName(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		valid       bool
	}{{"Example_1", "Example_1", true}, {"https://t.me/addstickers/Example_1", "Example_1", true}, {"t.me/addstickers/Example_1", "Example_1", true}, {"https://evil.test/addstickers/X", "", false}, {"https://t.me/addstickers/X?y=1", "", false}, {"a/b", "", false}, {"", "", false}} {
		got, err := StickerShortName(tc.input)
		if (err == nil) != tc.valid || got != tc.want {
			t.Errorf("%q: %q %v", tc.input, got, err)
		}
	}
}
