package rpc

import (
	"testing"
	"time"
)

func TestParseExportMonth(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, s := range []string{"2026-1", "2026-13", "2026-11", "2026-12", ""} {
		if _, _, err := ParseExportMonth(s, now); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	start, end, err := ParseExportMonth("2026-10", now)
	if err != nil || start.Format(time.RFC3339) != "2026-10-01T00:00:00Z" || !end.Equal(now) {
		t.Fatalf("%v %v %v", start, end, err)
	}
}
