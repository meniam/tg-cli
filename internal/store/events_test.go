package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventsDedupReplayFiltersAndPrune(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	base := time.Unix(1_700_000_000, 0).UTC()
	chat1, chat2 := int64(1), int64(2)
	events := []Event{
		{Type: "message.new", ChatID: &chat1, CreatedAt: base, Payload: json.RawMessage(`{"text":"one"}`), DedupKey: "one"},
		{Type: "reaction", ChatID: &chat2, CreatedAt: base.Add(time.Hour), Payload: json.RawMessage(`{"emoji":"+"}`), DedupKey: "two"},
		{Type: "message.edit", ChatID: &chat1, CreatedAt: base.Add(2 * time.Hour), Payload: json.RawMessage(`{"text":"edited"}`), DedupKey: "three"},
	}
	var ids []int64
	for _, event := range events {
		id, inserted, err := db.AddEvent(ctx, event)
		if err != nil || !inserted {
			t.Fatalf("add: id=%d inserted=%v err=%v", id, inserted, err)
		}
		ids = append(ids, id)
	}
	if !(ids[0] < ids[1] && ids[1] < ids[2]) {
		t.Fatalf("IDs not increasing: %v", ids)
	}
	id, inserted, err := db.AddEvent(ctx, events[0])
	if err != nil || inserted || id != ids[0] {
		t.Errorf("duplicate: id=%d inserted=%v err=%v", id, inserted, err)
	}
	last, err := db.LastEventID(ctx)
	if err != nil || last != ids[2] {
		t.Errorf("last=%d err=%v", last, err)
	}
	page, err := db.EventsAfter(ctx, ids[0], EventFilter{}, 10)
	if err != nil || len(page) != 2 || page[0].ID != ids[1] || page[1].ID != ids[2] {
		t.Errorf("after first: %+v err=%v", page, err)
	}
	filtered, err := db.EventsAfter(ctx, 0, EventFilter{Types: []string{"message.new", "message.edit"}, ChatIDs: []int64{chat1}}, 10)
	if err != nil || len(filtered) != 2 || filtered[0].ID != ids[0] || filtered[1].ID != ids[2] {
		t.Errorf("filtered: %+v err=%v", filtered, err)
	}
	deleted, err := db.PruneEvents(ctx, base.Add(30*time.Minute), 2)
	if err != nil || deleted != 1 {
		t.Errorf("age prune: deleted=%d err=%v", deleted, err)
	}
	deleted, err = db.PruneEvents(ctx, base, 1)
	if err != nil || deleted != 1 {
		t.Errorf("count prune: deleted=%d err=%v", deleted, err)
	}
	id, inserted, err = db.AddEvent(ctx, Event{Type: "message.new", CreatedAt: base.Add(3 * time.Hour), Payload: json.RawMessage(`{}`)})
	if err != nil || !inserted || id <= ids[2] {
		t.Errorf("ID reused after prune: id=%d err=%v", id, err)
	}
}

func TestAccountStateReadWrite(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if value, found, err := db.GetAccountState(ctx, "updates"); err != nil || found || value != nil {
		t.Errorf("missing: %s %v %v", value, found, err)
	}
	for _, value := range []json.RawMessage{json.RawMessage(`{"pts":1}`), json.RawMessage(`{"pts":2}`)} {
		if err := db.SetAccountState(ctx, "updates", value); err != nil {
			t.Fatal(err)
		}
	}
	value, found, err := db.GetAccountState(ctx, "updates")
	if err != nil || !found || string(value) != `{"pts":2}` {
		t.Errorf("updated: %s %v %v", value, found, err)
	}
	if err := db.SetAccountState(ctx, "updates", json.RawMessage(`bad`)); err == nil {
		t.Error("invalid JSON accepted")
	}
}

func TestEventsMigrationUpgradesVersionTwoDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	for _, version := range []int{1, 2} {
		body, err := migrationFiles.ReadFile("migrations/000" + string(rune('0'+version)) + "_" + map[int]string{1: "init", 2: "schema"}[version] + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		plain, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := plain.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := plain.Exec("PRAGMA user_version=" + string(rune('0'+version))); err != nil {
			t.Fatal(err)
		}
		_ = plain.Close()
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if version, err := db.Version(); err != nil || version != newestMigrationVersion(t) {
		t.Errorf("version=%d err=%v", version, err)
	}
	if _, err := db.write.Exec(`INSERT INTO events(type,created_at,payload) VALUES('bad',1,'not JSON')`); err == nil {
		t.Error("events table accepted invalid JSON")
	}
	id, inserted, err := db.AddEvent(context.Background(), Event{Type: "status", Payload: json.RawMessage(`{}`)})
	if err != nil || !inserted || id != 1 {
		t.Errorf("event after upgrade: id=%d inserted=%v err=%v", id, inserted, err)
	}
}
