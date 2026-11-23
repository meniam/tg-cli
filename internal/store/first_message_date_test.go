package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func requireFirstMessageDate(t *testing.T, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("first message date = %v, want %d", got, want)
	}
}

// TestFirstMessageDateSavedWithComplete proves the date is stored in the
// transaction that completes the history: empty until then, the earliest message
// date afterwards, and never replaced once set.
func TestFirstMessageDateSavedWithComplete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	state, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(50, "a"), historyMessage(40, "b")}, nil, false, GapPass{})
	if err != nil || state.Complete || state.FirstMessageDate != nil {
		t.Fatalf("first page: %+v err=%v", state, err)
	}
	state, err = db.SaveHistoryPage(ctx, 42, []Message{historyMessage(20, "c"), historyMessage(10, "d")}, nil, false, GapPass{})
	if err != nil || state.Complete || state.FirstMessageDate != nil {
		t.Fatalf("an incomplete chat must keep the date empty: %+v err=%v", state, err)
	}

	state, err = db.SaveHistoryPage(ctx, 42, nil, nil, true, GapPass{})
	if err != nil || !state.Complete {
		t.Fatalf("complete page: %+v err=%v", state, err)
	}
	requireFirstMessageDate(t, state.FirstMessageDate, 10)
	stored, found, err := db.SyncState(ctx, 42)
	if err != nil || !found {
		t.Fatalf("sync state: found=%v err=%v", found, err)
	}
	requireFirstMessageDate(t, stored.FirstMessageDate, 10)

	// A later write of an older message and another completion keep the saved date.
	state, err = db.SaveHistoryPage(ctx, 42, []Message{historyMessage(5, "e")}, nil, true, GapPass{})
	if err != nil {
		t.Fatal(err)
	}
	requireFirstMessageDate(t, state.FirstMessageDate, 10)
}

// TestFirstMessageDateOfCompletingPage covers a completing call that carries
// messages itself: its messages count, because the date is the minimum over what
// the transaction stored.
func TestFirstMessageDateOfCompletingPage(t *testing.T) {
	db := openTestDB(t)
	state, err := db.SaveHistoryPage(context.Background(), 42, []Message{historyMessage(9, "a"), historyMessage(3, "b")}, nil, true, GapPass{})
	if err != nil {
		t.Fatal(err)
	}
	requireFirstMessageDate(t, state.FirstMessageDate, 3)
}

// TestFirstMessageDateEmptyChat: a chat that completed without any stored
// message has nothing to date.
func TestFirstMessageDateEmptyChat(t *testing.T) {
	db := openTestDB(t)
	state, err := db.SaveHistoryPage(context.Background(), 42, nil, nil, true, GapPass{})
	if err != nil || !state.Complete || state.FirstMessageDate != nil {
		t.Fatalf("empty complete chat: %+v err=%v", state, err)
	}
}

// TestFirstMessageDateSurvivesLocalDelete proves the saved date does not follow
// a local deletion, while the first_message_date of the view still does.
func TestFirstMessageDateSurvivesLocalDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 42, Type: "group", Title: "Team"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(10, "a"), historyMessage(20, "b")}, nil, false, GapPass{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 42, nil, nil, true, GapPass{}); err != nil {
		t.Fatal(err)
	}
	if changed, err := db.MarkMessagesDeleted(ctx, 42, []int{10}); err != nil || changed != 1 {
		t.Fatalf("delete: changed=%d err=%v", changed, err)
	}

	stored, _, err := db.SyncState(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	requireFirstMessageDate(t, stored.FirstMessageDate, 10)
	rows, err := db.ChatsOverview(ctx, ChatFilter{}, 10, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("overview: %+v err=%v", rows, err)
	}
	if rows[0].FirstMessage == nil || rows[0].FirstMessage.Date != 20 {
		t.Fatalf("the first stored message must skip the deleted one: %+v", rows[0].FirstMessage)
	}
	requireFirstMessageDate(t, rows[0].HistoryFirstMessageDate, 10)
}

// TestFirstMessageDateInOverview covers the three states of a chat in the
// overview: complete with a date, not complete, and without a sync row.
func TestFirstMessageDateInOverview(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 42, Type: "group", Title: "Done"}, {ID: 43, Type: "group", Title: "Partial"}, {ID: 44, Type: "group", Title: "New"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(7, "a")}, nil, true, GapPass{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveHistoryPage(ctx, 43, []Message{{ChatID: 43, ID: 1, Date: 1, Text: "a", Raw: raw(`{}`)}}, nil, false, GapPass{}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ChatsOverview(ctx, ChatFilter{}, 10, "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("overview: %+v err=%v", rows, err)
	}
	byID := map[int64]ChatsOverviewRow{}
	for _, row := range rows {
		byID[row.ChatID] = row
	}
	requireFirstMessageDate(t, byID[42].HistoryFirstMessageDate, 7)
	if byID[43].HistoryFirstMessageDate != nil || byID[44].HistoryFirstMessageDate != nil {
		t.Fatalf("a chat that is not complete has no date: %+v %+v", byID[43], byID[44])
	}
}

// TestFirstMessageDateMigrationFromNine upgrades a version 9 database: a complete
// chat gets the earliest date of its messages, deleted ones included; an
// incomplete chat and a complete chat without messages stay empty; the rest of the
// view is unchanged.
func TestFirstMessageDateMigrationFromNine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	files := append(append([]string{}, migrationsBeforeEight...), "0008_chats_state.sql", "0009_raw.sql")
	buildDatabaseAt(t, path, files, 9)
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 3; id++ {
		schemaExec(t, old, `INSERT INTO chats(id,type,title,first_seen_at,last_seen_at) VALUES(?,'group','chat',1,1)`, id)
	}
	insert := func(chatID, id, date int, deletedAt any) {
		schemaExec(t, old, `INSERT INTO messages(chat_id,id,date,text,raw,search_text,first_seen_at,last_seen_at,deleted_at) VALUES(?,?,?,'t','{}','t',1,1,?)`, chatID, id, date, deletedAt)
	}
	insert(1, 1, 50, 99) // deleted first message still dates the chat
	insert(1, 2, 80, nil)
	insert(2, 1, 10, nil)
	schemaExec(t, old, `INSERT INTO sync_state(chat_id,complete) VALUES(1,1),(2,0),(3,1)`)
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade a version 9 database: %v", err)
	}
	defer db.Close()
	if newest := newestMigrationVersion(t); newest < 10 {
		t.Fatalf("newest migration = %d, want at least 10", newest)
	}
	if version, err := db.Version(); err != nil || version != newestMigrationVersion(t) {
		t.Fatalf("version = %d, %v", version, err)
	}
	ctx := context.Background()
	done, _, err := db.SyncState(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	requireFirstMessageDate(t, done.FirstMessageDate, 50)
	for _, id := range []int64{2, 3} {
		state, found, err := db.SyncState(ctx, id)
		if err != nil || !found || state.FirstMessageDate != nil {
			t.Fatalf("chat %d: %+v found=%v err=%v", id, state, found, err)
		}
	}
	rows, err := db.ChatsOverview(ctx, ChatFilter{}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ChatID != 1 {
			continue
		}
		requireFirstMessageDate(t, row.HistoryFirstMessageDate, 50)
		if row.FirstMessage == nil || row.FirstMessage.Date != 80 || row.Messages != 1 {
			t.Fatalf("the view must keep its first stored message: %+v", row)
		}
	}
	var integrity string
	if err := db.read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check = %q, %v", integrity, err)
	}
}
