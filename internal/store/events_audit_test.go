package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestAudit07_EventsRefusals(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for name, event := range map[string]Event{
		"no type":      {Payload: json.RawMessage(`{}`)},
		"no payload":   {Type: "status"},
		"broken JSON":  {Type: "status", Payload: json.RawMessage(`{`)},
		"bare garbage": {Type: "status", Payload: json.RawMessage(`not json`)},
	} {
		if id, inserted, err := db.AddEvent(ctx, event); err == nil || inserted || id != 0 {
			t.Errorf("%s: id=%d inserted=%v err=%v", name, id, inserted, err)
		}
	}
	if _, err := db.EventsAfter(ctx, -1, EventFilter{}, 10); err == nil {
		t.Error("a negative cursor was accepted")
	}
	if _, err := db.PruneEvents(ctx, time.Now(), -1); err == nil {
		t.Error("a negative retention count was accepted")
	}
	if last, err := db.LastEventID(ctx); err != nil || last != 0 {
		t.Errorf("a refused event reached the journal: last=%d err=%v", last, err)
	}
}

func TestAudit07_EventsWithoutDedupKeyAreAllStored(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	first, ok1, err1 := db.AddEvent(ctx, Event{Type: "status", Payload: json.RawMessage(`{}`)})
	second, ok2, err2 := db.AddEvent(ctx, Event{Type: "status", Payload: json.RawMessage(`{}`)})
	if err1 != nil || err2 != nil || !ok1 || !ok2 || first == second {
		t.Fatalf("two events without a key: %d %v %v / %d %v %v", first, ok1, err1, second, ok2, err2)
	}
}

func TestAudit07_EventsAfterLimits(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.write.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1005)
		INSERT INTO events(type,created_at,payload) SELECT 'status',1,'{}' FROM n`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		limit, want int
	}{{0, 100}, {-3, 100}, {7, 7}, {1000, 1000}, {5000, 1000}} {
		got, err := db.EventsAfter(ctx, 0, EventFilter{}, tc.limit)
		if err != nil || len(got) != tc.want {
			t.Errorf("limit %d returned %d events, err=%v, want %d", tc.limit, len(got), err, tc.want)
		}
	}
	// The cursor keeps its meaning at the end of the journal.
	last, err := db.LastEventID(ctx)
	if err != nil || last != 1005 {
		t.Fatalf("last=%d err=%v", last, err)
	}
	if got, err := db.EventsAfter(ctx, last, EventFilter{}, 10); err != nil || len(got) != 0 {
		t.Errorf("events after the last one: %d err=%v", len(got), err)
	}
}

// TestAudit07_EventHighWaterSurvivesPruning is what keeps a cursor from being
// reused: after every event is removed, the high water mark still names the
// last id that existed.
func TestAudit07_EventHighWaterSurvivesPruning(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if high, err := db.EventHighWater(ctx); err != nil || high != 0 {
		t.Fatalf("an empty journal: high=%d err=%v", high, err)
	}
	if first, err := db.FirstEventID(ctx); err != nil || first != 0 {
		t.Fatalf("an empty journal: first=%d err=%v", first, err)
	}
	now := time.Unix(1_700_000_000, 0)
	var last int64
	for range 3 {
		id, _, err := db.AddEvent(ctx, Event{Type: "status", CreatedAt: now, Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		last = id
	}
	if first, err := db.FirstEventID(ctx); err != nil || first != 1 {
		t.Errorf("first=%d err=%v, want 1", first, err)
	}
	if deleted, err := db.PruneEvents(ctx, now.Add(time.Hour), 0); err != nil || deleted != 3 {
		t.Fatalf("prune: deleted=%d err=%v", deleted, err)
	}
	if got, err := db.LastEventID(ctx); err != nil || got != 0 {
		t.Errorf("last event id of an empty journal = %d err=%v", got, err)
	}
	if first, err := db.FirstEventID(ctx); err != nil || first != 0 {
		t.Errorf("first event id of an empty journal = %d err=%v", first, err)
	}
	if high, err := db.EventHighWater(ctx); err != nil || high != last {
		t.Errorf("high water after pruning = %d err=%v, want %d", high, err, last)
	}
	// A second prune moves the mark forward, never back.
	for range 2 {
		id, _, err := db.AddEvent(ctx, Event{Type: "status", CreatedAt: now, Payload: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		last = id
	}
	if id := last; id <= 3 {
		t.Fatalf("event ids were reused after pruning: %d", id)
	}
	if deleted, err := db.PruneEvents(ctx, now.Add(time.Hour), 0); err != nil || deleted != 2 {
		t.Fatalf("second prune: deleted=%d err=%v", deleted, err)
	}
	if high, err := db.EventHighWater(ctx); err != nil || high != last {
		t.Errorf("high water after the second prune = %d err=%v, want %d", high, err, last)
	}
}
