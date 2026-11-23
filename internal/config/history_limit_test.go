package config

import (
	"testing"
	"time"
)

func TestParseHistoryLimitSince(t *testing.T) {
	got, err := ParseHistoryLimitSince("2022-02-02")
	if err != nil || !got.Equal(time.Date(2022, 2, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("got %v, %v", got, err)
	}
	// An empty value is the way to turn the date limit off.
	if got, err := ParseHistoryLimitSince(""); err != nil || !got.IsZero() {
		t.Fatalf("empty: %v, %v", got, err)
	}
	if _, err := ParseSettingString("history_limit.since", ""); err != nil {
		t.Fatalf("empty setting: %v", err)
	}
	for _, bad := range []string{"02-02-2022", "2022-02-30", "2022/02/02", " 2022-02-02", "2022-02-02T00:00:00Z"} {
		if _, err := ParseHistoryLimitSince(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestHistoryLimitDefaultsValidate(t *testing.T) {
	if err := ValidateSettings(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseHistoryLimitSince(DefaultHistoryLimitSince); err != nil {
		t.Fatal(err)
	}
	if err := CheckHistoryLimitMessages(DefaultHistoryLimitMessages); err != nil {
		t.Fatal(err)
	}
}
