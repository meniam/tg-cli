package store

import (
	"context"
	"testing"
	"time"
)

func historyMessage(id int, text string) Message {
	return Message{ChatID: 42, ID: id, Date: int64(id), Text: text, Raw: raw(`{}`)}
}

func requireSyncProgress(t *testing.T, db *DB, oldest, newest int, complete bool, syncedAt int64) {
	t.Helper()
	state, found, err := db.SyncState(context.Background(), 42)
	if err != nil || !found {
		t.Fatalf("sync state: found=%v err=%v", found, err)
	}
	if (oldest == 0) != (state.OldestID == nil) || (newest == 0) != (state.NewestID == nil) {
		t.Fatalf("sync IDs: %+v", state)
	}
	if oldest != 0 && *state.OldestID != oldest || newest != 0 && *state.NewestID != newest || state.Complete != complete || state.SyncedAt == nil || *state.SyncedAt != syncedAt {
		t.Fatalf("sync state = %+v, want oldest=%d newest=%d complete=%v synced=%d", state, oldest, newest, complete, syncedAt)
	}
}

func TestSyncStateAbsentAndReset(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		reset bool
	}{
		{name: "before any page"},
		{name: "after reset", reset: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.reset {
				if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(7, "a")}, nil, false, GapPass{}); err != nil {
					t.Fatal(err)
				}
				if err := db.ResetSyncState(ctx, 42); err != nil {
					t.Fatal(err)
				}
			}
			if state, found, err := db.SyncState(ctx, 42); err != nil || found || state != (SyncProgress{}) {
				t.Fatalf("absent sync state: %+v found=%v err=%v", state, found, err)
			}
		})
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM messages WHERE chat_id=42`); n != 1 {
		t.Fatalf("reset removed messages: %d", n)
	}
}

func TestSaveEmptyHistoryPage(t *testing.T) {
	db := openTestDB(t)
	setClock(db, 123)
	state, err := db.SaveHistoryPage(context.Background(), 42, nil, nil, true, GapPass{})
	if err != nil || state.OldestID != nil || state.NewestID != nil || !state.Complete || state.SyncedAt == nil || *state.SyncedAt != 123 {
		t.Fatalf("empty history: state=%+v err=%v", state, err)
	}
	requireSyncProgress(t, db, 0, 0, true, 123)
}

func TestSaveHistoryPageProgress(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name                  string
		now                   int64
		messages              []Message
		complete              bool
		oldest, newest, count int
		wantComplete          bool
	}{
		{"first page", 100, []Message{historyMessage(10, "ten"), historyMessage(9, "nine")}, false, 9, 10, 2, false},
		{"older page", 200, []Message{historyMessage(8, "eight"), historyMessage(7, "seven")}, false, 7, 10, 4, false},
		{"newer overlap", 300, []Message{historyMessage(12, "twelve"), historyMessage(10, "ten")}, false, 7, 12, 5, false},
		{"same page", 400, []Message{historyMessage(8, "eight"), historyMessage(7, "seven")}, false, 7, 12, 5, false},
		{"raw history ended", 500, nil, true, 7, 12, 5, true},
		{"complete stays set", 600, []Message{historyMessage(11, "eleven")}, false, 7, 12, 6, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setClock(db, tc.now)
			state, err := db.SaveHistoryPage(ctx, 42, tc.messages, nil, tc.complete, GapPass{})
			if err != nil {
				t.Fatal(err)
			}
			if state.OldestID == nil || *state.OldestID != tc.oldest || state.NewestID == nil || *state.NewestID != tc.newest || state.Complete != tc.wantComplete || state.SyncedAt == nil || *state.SyncedAt != tc.now {
				t.Fatalf("returned progress: %+v", state)
			}
			requireSyncProgress(t, db, tc.oldest, tc.newest, tc.wantComplete, tc.now)
			if n := schemaCount(t, db.read, `SELECT count(*) FROM messages WHERE chat_id=42`); n != tc.count {
				t.Fatalf("messages=%d, want %d", n, tc.count)
			}
			if n := schemaCount(t, db.read, `SELECT count(*) FROM message_versions WHERE chat_id=42`); n != 0 {
				t.Fatalf("repeated page created %d versions", n)
			}
		})
	}
}

func TestSaveHistoryPageTextVersionAndNotes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	first := historyMessage(7, "first")
	if _, err := db.SaveHistoryPage(ctx, 42, []Message{first}, []Person{{ID: 3, FirstName: stringPtr("Owner")}}, false, GapPass{}); err != nil {
		t.Fatal(err)
	}
	schemaExec(t, db.write, `INSERT INTO notes(id,kind,chat_id,target_id,body,author,created_at,updated_at) VALUES(1,'message',42,7,'keep','owner',1,1)`)
	setClock(db, 200)
	changed := historyMessage(7, "changed")
	if _, err := db.SaveHistoryPage(ctx, 42, []Message{changed}, []Person{{ID: 3, FirstName: stringPtr("Updated")}}, false, GapPass{}); err != nil {
		t.Fatal(err)
	}
	versions, err := db.MessageVersions(ctx, 42, 7)
	if err != nil || len(versions) != 1 || versions[0].Text != "first" || versions[0].SavedAt != 200 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if got, err := db.GetMessage(ctx, 42, 7); err != nil || got.Text != "changed" {
		t.Fatalf("message=%+v err=%v", got, err)
	}
	if person, err := db.GetPerson(ctx, 3); err != nil || person.FirstName == nil || *person.FirstName != "Updated" {
		t.Fatalf("person=%+v err=%v", person, err)
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM notes WHERE id=1 AND body='keep'`); n != 1 {
		t.Fatalf("note changed: %d", n)
	}
}

func TestSaveHistoryPageRollback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, *DB)
		page    []Message
		people  []Person
	}{
		{"invalid raw", nil, []Message{historyMessage(6, "ok"), {ChatID: 42, ID: 5, Date: 5, Text: "bad", Raw: raw(`{bad`)}}, nil},
		{"message write fails", func(t *testing.T, db *DB) {
			schemaExec(t, db.write, `CREATE TRIGGER fail_message BEFORE INSERT ON messages WHEN NEW.id=5 BEGIN SELECT RAISE(ABORT,'blocked'); END`)
		}, []Message{historyMessage(6, "ok"), historyMessage(5, "blocked")}, nil},
		{"person write fails", func(t *testing.T, db *DB) {
			schemaExec(t, db.write, `CREATE TRIGGER fail_person BEFORE INSERT ON people WHEN NEW.id=4 BEGIN SELECT RAISE(ABORT,'blocked'); END`)
		}, []Message{historyMessage(6, "ok")}, []Person{{ID: 4}}},
		{"state write fails", func(t *testing.T, db *DB) {
			schemaExec(t, db.write, `CREATE TRIGGER fail_state BEFORE UPDATE ON sync_state WHEN NEW.chat_id=42 BEGIN SELECT RAISE(ABORT,'blocked'); END`)
		}, []Message{historyMessage(6, "ok")}, []Person{{ID: 4}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			setClock(db, 100)
			if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(7, "existing")}, nil, false, GapPass{}); err != nil {
				t.Fatal(err)
			}
			if tc.prepare != nil {
				tc.prepare(t, db)
			}
			setClock(db, 200)
			if _, err := db.SaveHistoryPage(ctx, 42, tc.page, tc.people, false, GapPass{}); err == nil {
				t.Fatal("invalid page succeeded")
			}
			requireSyncProgress(t, db, 7, 7, false, 100)
			if n := schemaCount(t, db.read, `SELECT count(*) FROM messages WHERE chat_id=42`); n != 1 {
				t.Fatalf("failed page left %d messages", n)
			}
			if n := schemaCount(t, db.read, `SELECT count(*) FROM people`); n != 0 {
				t.Fatalf("failed page left %d people", n)
			}
		})
	}
}

func TestSyncStateReadsDuringWrite(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(7, "old")}, nil, false, GapPass{}); err != nil {
		t.Fatal(err)
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE sync_state SET oldest_id=6 WHERE chat_id=42`); err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	state, found, err := db.SyncState(readCtx, 42)
	if err != nil || !found || state.OldestID == nil || *state.OldestID != 7 {
		t.Fatalf("read during write: state=%+v found=%v err=%v", state, found, err)
	}
}

func TestMarkHistoryLimitReachedCompletesWithoutFirstMessageDate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 200)
	if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(9, "a"), historyMessage(8, "b")}, nil, false, GapPass{}); err != nil {
		t.Fatal(err)
	}
	setClock(db, 300)
	state, err := db.MarkHistoryLimitReached(ctx, 42)
	if err != nil || !state.Complete || state.FirstMessageDate != nil || state.OldestID == nil || *state.OldestID != 8 || state.SyncedAt == nil || *state.SyncedAt != 300 {
		t.Fatalf("state: %+v err=%v", state, err)
	}
	if _, err := db.MarkHistoryLimitReached(ctx, 0); err == nil {
		t.Fatal("zero chat id accepted")
	}
	// A chat without a row gets one.
	other, err := db.MarkHistoryLimitReached(ctx, 43)
	if err != nil || !other.Complete || other.OldestID != nil {
		t.Fatalf("new row: %+v err=%v", other, err)
	}
}

func TestMarkHistoryLimitReachedTakesBoundsFromSavedMessagesForANewRow(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// Messages that live events saved: no sync_state row yet.
	if err := db.UpsertMessages(ctx, []Message{historyMessage(7, "a"), historyMessage(9, "b")}); err != nil {
		t.Fatal(err)
	}
	state, err := db.MarkHistoryLimitReached(ctx, 42)
	if err != nil || !state.Complete || state.OldestID == nil || *state.OldestID != 7 || state.NewestID == nil || *state.NewestID != 9 || state.FirstMessageDate != nil {
		t.Fatalf("state: %+v err=%v", state, err)
	}
}
