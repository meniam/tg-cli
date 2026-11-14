package rpc

import (
	"testing"
	"time"
)

func TestScheduleTimeRounding(t *testing.T) {
	now := time.Date(2026, 11, 15, 9, 0, 0, 500000000, time.UTC)
	got, err := ScheduleTime("", "10s", now)
	if err != nil || !got.Equal(time.Date(2026, 11, 15, 9, 0, 11, 0, time.UTC)) {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err = ScheduleTime("", "9.9s", now); err == nil {
		t.Fatal("accepted less than 10 seconds")
	}
}

func TestScheduleTime(t *testing.T) {
	now := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		at, in string
		valid  bool
	}{
		{"", "", true}, {"2026-11-15T12:01:00+03:00", "", true}, {"", "90m", true}, {"2026-11-15T09:00:09Z", "", false}, {"2026-11-15T09:01:00", "", false}, {"", "9s", false}, {"2026-11-15T09:01:00Z", "1m", false}, {"", "no", false},
	} {
		_, err := ScheduleTime(tc.at, tc.in, now)
		if (err == nil) != tc.valid {
			t.Errorf("at=%q in=%q err=%v", tc.at, tc.in, err)
		}
	}
}
