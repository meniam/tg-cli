package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestCatchupMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var migrations []string
	for _, e := range entries {
		if e.Name() < "0014" {
			migrations = append(migrations, e.Name())
		}
	}
	buildDatabaseAt(t, path, migrations, 13)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schemaExec(t, raw, `INSERT INTO chats(id,type,title,search_text,first_seen_at,last_seen_at) VALUES(42,'user','kept','kept',1,1)`)
	schemaExec(t, raw, `INSERT INTO sync_state(chat_id,oldest_id,newest_id,complete) VALUES(42,2,8,1)`)
	raw.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var title string
	var from, to sql.NullInt64
	var complete bool
	if err := db.read.QueryRow(`SELECT title,history_complete,history_gap_from,history_gap_to FROM chats_state WHERE chat_id=42`).Scan(&title, &complete, &from, &to); err != nil || title != "kept" || !complete || from.Valid || to.Valid {
		t.Fatalf("view: %q %v %v %v %v", title, complete, from, to, err)
	}
}

func TestCatchupPagesAndGap(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(10, "old")}, nil, true); err != nil {
		t.Fatal(err)
	}
	before, _, _ := db.SyncState(ctx, 42)
	if err := db.SaveCatchupPage(ctx, 42, []Message{historyMessage(40, "new")}, []Person{{ID: 3, FirstName: stringPtr("A")}}); err != nil {
		t.Fatal(err)
	}
	after, _, _ := db.SyncState(ctx, 42)
	if *after.OldestID != *before.OldestID || *after.NewestID != *before.NewestID || after.Complete != before.Complete || *after.FirstMessageDate != *before.FirstMessageDate {
		t.Fatalf("progress changed: %+v", after)
	}
	if n := schemaCount(t, db.read, `SELECT COUNT(*) FROM messages WHERE chat_id=42 AND id=40`); n != 1 {
		t.Fatal(n)
	}
	if n := schemaCount(t, db.read, `SELECT COUNT(*) FROM people WHERE id=3`); n != 1 {
		t.Fatal(n)
	}
	if err := db.MarkCatchupGap(ctx, 42, 40, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkCatchupGap(ctx, 42, 50, 10); err != nil {
		t.Fatal(err)
	}
	s, _, _ := db.SyncState(ctx, 42)
	if *s.GapFrom != 10 || *s.GapTo != 50 {
		t.Fatalf("merged gap: %+v", s)
	}
	s, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(45, "page")}, nil, false)
	if err != nil || s.GapTo == nil || *s.NewestID != 10 {
		t.Fatalf("ceiling: %+v %v", s, err)
	}
	s, err = db.SaveHistoryPage(ctx, 42, []Message{historyMessage(30, "page")}, nil, false, GapPass{Start: 50, Top: 50, Narrow: true})
	if err != nil || *s.GapTo != 30 || *s.NewestID != 10 {
		t.Fatalf("narrow: %+v %v", s, err)
	}
	s, err = db.SaveHistoryPage(ctx, 42, []Message{historyMessage(10, "old")}, nil, false, GapPass{Start: 30, Top: 50})
	if err != nil || s.GapTo != nil || s.GapFrom != nil || *s.NewestID != 50 || !s.Complete || *s.FirstMessageDate != 10 {
		t.Fatalf("closed: %+v %v", s, err)
	}
	if err := db.SaveCatchupPage(ctx, 43, []Message{{ChatID: 43, ID: 3, Date: 3, Raw: raw(`{}`)}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, found, err := db.SyncState(ctx, 43); err != nil || found {
		t.Fatalf("unexpected row: %v %v", found, err)
	}
	if err := db.MarkCatchupGap(ctx, 43, 3, 1); err != nil {
		t.Fatal(err)
	}
	s, found, err := db.SyncState(ctx, 43)
	if err != nil || !found || *s.GapFrom != 0 || *s.GapTo != 3 || s.OldestID != nil || s.NewestID != nil {
		t.Fatalf("new row: %+v %v", s, err)
	}
}

func TestLastAliveAt(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, found, err := db.LastAliveAt(ctx); err != nil || found {
		t.Fatalf("absent: %v %v", found, err)
	}
	if err := db.SetLastAliveAt(ctx, 123); err != nil {
		t.Fatal(err)
	}
	if n, found, err := db.LastAliveAt(ctx); err != nil || !found || n != 123 {
		t.Fatalf("value: %d %v %v", n, found, err)
	}
}

func TestCatchupPageRollback(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.MarkCatchupGap(ctx, 42, 50, 10); err != nil {
		t.Fatal(err)
	}
	_, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(20, "new")}, []Person{{ID: 3, Raw: raw(`invalid`)}}, false, GapPass{Start: 50, Top: 50, Narrow: true})
	if err == nil {
		t.Fatal("invalid person accepted")
	}
	s, _, err := db.SyncState(ctx, 42)
	if err != nil || *s.GapTo != 50 {
		t.Fatalf("gap changed: %+v %v", s, err)
	}
	if n := schemaCount(t, db.read, `SELECT COUNT(*) FROM messages WHERE chat_id=42 AND id=20`); n != 0 {
		t.Fatalf("message persisted: %d", n)
	}
}

func TestCatchupPageAtomic(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	schemaExec(t, db.write, `CREATE TRIGGER fail_person BEFORE INSERT ON people BEGIN SELECT RAISE(ABORT, 'failure'); END`)
	err := db.SaveCatchupPage(ctx, 42, []Message{historyMessage(9, "x")}, []Person{{ID: 3, FirstName: stringPtr("A")}})
	if err == nil {
		t.Fatal("expected write failure")
	}
	if n := schemaCount(t, db.read, `SELECT COUNT(*) FROM messages WHERE chat_id=42`); n != 0 {
		t.Fatalf("partial page saved: %d", n)
	}
}

func TestCatchupGapFromTop(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.MarkCatchupGap(ctx, 42, 30, 1); err != nil {
		t.Fatal(err)
	}
	s, err := db.SaveHistoryPage(ctx, 42, nil, nil, true, GapPass{Start: 0, Top: 60})
	if err != nil || s.GapTo != nil || s.GapFrom != nil || s.NewestID == nil || *s.NewestID != 60 || s.Complete {
		t.Fatalf("closed from top: %+v %v", s, err)
	}
}
