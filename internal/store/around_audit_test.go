package store

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// TestAudit07_MessagesAroundWindowEdges covers the window shapes the middle of
// the history never reaches: only the anchor, one side, the edges of the
// history, a deleted neighbour and a negative size.
func TestAudit07_MessagesAroundWindowEdges(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for id := 1; id <= 7; id++ {
		if err := db.UpsertMessages(ctx, []Message{{ChatID: 42, ID: id, Date: int64(id), Text: "text", Raw: json.RawMessage(`{}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.MarkMessagesDeleted(ctx, 42, []int{4}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		id            int
		before, after int
		want          []int
	}{
		{"only the anchor", 5, 0, 0, []int{5}},
		{"one side before", 5, 1, 0, []int{3, 5}},
		{"one side after", 5, 0, 1, []int{5, 6}},
		{"the start of the history", 1, 2, 2, []int{1, 2, 3}},
		{"the end of the history", 7, 2, 2, []int{5, 6, 7}},
		{"a window wider than the history", 3, 50, 50, []int{1, 2, 3, 5, 6, 7}},
	} {
		got, err := db.MessagesAround(ctx, 42, tc.id, tc.before, tc.after)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var ids []int
		for _, m := range got {
			ids = append(ids, m.ID)
		}
		if !slices.Equal(ids, tc.want) {
			t.Errorf("%s: ids %v, want %v", tc.name, ids, tc.want)
		}
	}
	for _, tc := range []struct{ before, after int }{{-1, 0}, {0, -1}, {-1, -1}} {
		if got, err := db.MessagesAround(ctx, 42, 5, tc.before, tc.after); err == nil {
			t.Errorf("window %d/%d accepted: %+v", tc.before, tc.after, got)
		}
	}
}
