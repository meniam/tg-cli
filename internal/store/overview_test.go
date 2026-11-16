package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// buildDatabaseAt creates a database with the migrations up to version, so a test
// can prove that the next migration upgrades an older file in place.
func buildDatabaseAt(t *testing.T, path string, files []string, version int) {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, file := range files {
		body, err := migrationFiles.ReadFile("migrations/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = raw.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
	if _, err = raw.Exec("PRAGMA user_version=" + itoa(version)); err != nil {
		t.Fatal(err)
	}
}

// itoa renders a small number without a format package.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}

// migrationsBeforeEight are the migrations a version 7 database has.
var migrationsBeforeEight = []string{
	"0001_init.sql", "0002_schema.sql", "0003_events.sql", "0004_idempotency.sql",
	"0005_folders_account_state.sql", "0006_chat_versions.sql", "0007_transcriptions.sql",
}

// TestChatsStateMigrationFromSeven proves the migration only adds: a version 7
// database keeps every row of every table it already had, and the new columns and
// the index appear. This is the case of the live database of the owner.
func TestChatsStateMigrationFromSeven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	buildDatabaseAt(t, path, migrationsBeforeEight, 7)

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schemaExec(t, raw, `INSERT INTO chats(id,type,title,username,about,members,forum,verified,unread,settings,search_text,first_seen_at,last_seen_at)
		VALUES(1,'channel','Old channel','oldchan','about it',7,0,1,3,'{"slow_mode_seconds":10}','old channel',10,20)`)
	schemaExec(t, raw, `INSERT INTO chats(id,type,title,search_text,first_seen_at,last_seen_at) VALUES(2,'group','Kept group','kept group',11,21)`)
	schemaExec(t, raw, `INSERT INTO messages(chat_id,id,date,text,out,raw,search_text,first_seen_at,last_seen_at) VALUES(1,5,100,'first','0','{}','first',100,100)`)
	schemaExec(t, raw, `INSERT INTO messages(chat_id,id,date,text,out,raw,search_text,first_seen_at,last_seen_at) VALUES(1,6,200,'mine','1','{}','mine',200,200)`)
	schemaExec(t, raw, `INSERT INTO dialog_state(chat_id,data,synced_at) VALUES(1,'{"mute_until":2147483647,"archived":false}',300)`)
	schemaExec(t, raw, `INSERT INTO sync_state(chat_id,complete) VALUES(1,1)`)
	schemaExec(t, raw, `INSERT INTO notes(id,kind,chat_id,body,author,created_at,updated_at) VALUES(9,'chat',1,'note body','owner',1,1)`)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade a version 7 database: %v", err)
	}
	defer db.Close()
	if version, err := db.Version(); err != nil || version != newestMigrationVersion(t) {
		t.Fatalf("version = %d, %v, want %d", version, err, newestMigrationVersion(t))
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM chats`); count != 2 {
		t.Errorf("chats rows = %d, want 2", count)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM messages`); count != 2 {
		t.Errorf("messages rows = %d, want 2", count)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM notes WHERE body='note body'`); count != 1 {
		t.Errorf("notes rows = %d, want 1", count)
	}
	// The new NOT NULL columns take their defaults on the existing rows, and the
	// nullable ones stay empty: nothing is invented for older data.
	var scam, fake, restricted int
	var createdAt, fullSynced, restrictionReason sql.NullInt64
	var fullError sql.NullString
	err = db.read.QueryRow(`SELECT scam, fake, restricted, created_at, restriction_reason, full_synced_at, full_error FROM chats WHERE id=1`).
		Scan(&scam, &fake, &restricted, &createdAt, &restrictionReason, &fullSynced, &fullError)
	if err != nil {
		t.Fatal(err)
	}
	if scam != 0 || fake != 0 || restricted != 0 {
		t.Errorf("new flag defaults = %d/%d/%d, want 0/0/0", scam, fake, restricted)
	}
	if createdAt.Valid || restrictionReason.Valid || fullSynced.Valid || fullError.Valid {
		t.Errorf("new optional columns must stay empty: %+v %+v %+v %+v", createdAt, restrictionReason, fullSynced, fullError)
	}
	var title, about string
	if err := db.read.QueryRow(`SELECT title, about FROM chats WHERE id=1`).Scan(&title, &about); err != nil || title != "Old channel" || about != "about it" {
		t.Errorf("old columns changed: %q %q %v", title, about, err)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM pragma_index_list('messages') WHERE name='messages_chat_out_idx' AND partial=1`); count != 1 {
		t.Errorf("the partial index is missing: %d", count)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM pragma_index_list('messages') WHERE name='messages_chat_date_idx'`); count != 1 {
		t.Errorf("the old index is gone: %d", count)
	}
	var integrity string
	if err := db.read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Errorf("integrity_check = %q, %v", integrity, err)
	}
	// The view already answers for the upgraded rows.
	var messages, myMessages int64
	var muted bool
	if err := db.read.QueryRow(`SELECT messages, my_messages, muted FROM chats_state WHERE chat_id=1`).Scan(&messages, &myMessages, &muted); err != nil {
		t.Fatalf("read the view: %v", err)
	}
	if messages != 2 || myMessages != 1 || !muted {
		t.Errorf("view after upgrade: messages=%d my=%d muted=%v", messages, myMessages, muted)
	}
}

// TestChatsStateView covers the contract of the view: a chat without a saved
// dialog state, a chat muted forever, a mute in the past, an archive, deleted
// messages left out, own messages, the first and the last stored message and the
// history flag.
func TestChatsStateView(t *testing.T) {
	db := openTestDB(t)
	now := db.now().Unix()
	insertChat := func(id int64, title string) {
		schemaExec(t, db.write, `INSERT INTO chats(id,type,title,created_at,first_seen_at,last_seen_at) VALUES(?,?,?,1000,1,1)`, id, "channel", title)
	}
	insertChat(1, "no state")
	insertChat(2, "muted forever")
	insertChat(3, "mute in the past")
	insertChat(4, "archived and pinned")
	insertChat(5, "without messages")

	schemaExec(t, db.write, `INSERT INTO dialog_state(chat_id,data,synced_at) VALUES(2,'{"mute_until":2147483647,"unread":4}',?)`, now)
	schemaExec(t, db.write, `INSERT INTO dialog_state(chat_id,data,synced_at) VALUES(3,'{"mute_until":5,"unread_mark":true}',?)`, now)
	schemaExec(t, db.write, `INSERT INTO dialog_state(chat_id,data,synced_at) VALUES(4,'{"archived":true,"pinned":true,"unread_mentions":2}',?)`, now)

	insertMessage := func(chatID int64, id, date int, text string, out int, deletedAt any) {
		schemaExec(t, db.write, `INSERT INTO messages(chat_id,id,date,text,out,raw,search_text,first_seen_at,last_seen_at,deleted_at)
			VALUES(?,?,?,?,?,'{}',?,1,1,?)`, chatID, id, date, text, out, text, deletedAt)
	}
	insertMessage(1, 10, 1000, "first in", 0, nil)
	insertMessage(1, 11, 2000, "mine", 1, nil)
	insertMessage(1, 12, 3000, "last in", 0, nil)
	insertMessage(1, 13, 4000, "deleted", 1, 5000)
	insertMessage(2, 20, 1000, "only", 0, nil)
	// Messages of chat 3 are all deleted, so the chat has no stored message.
	insertMessage(3, 30, 1000, "gone", 0, 5000)
	schemaExec(t, db.write, `INSERT INTO sync_state(chat_id,complete) VALUES(1,1)`)
	schemaExec(t, db.write, `INSERT INTO sync_state(chat_id,complete) VALUES(2,0)`)

	type row struct {
		muted                        bool
		muteUntil, unread, mentions  *int64
		unreadMark, archived, pinned *bool
		messages, myMessages         int64
		firstID, firstDate           *int64
		lastID, lastDate             *int64
		firstText, lastText          *string
		stateSyncedAt                *int64
		historyComplete              *bool
	}
	read := func(chatID int64) row {
		t.Helper()
		var r row
		err := db.read.QueryRow(`SELECT muted, mute_until, unread, unread_mentions, unread_mark, archived, pinned,
			messages, my_messages, first_message_id, first_message_date, first_message_text,
			last_message_id, last_message_date, last_message_text, state_synced_at, history_complete
			FROM chats_state WHERE chat_id=?`, chatID).
			Scan(&r.muted, &r.muteUntil, &r.unread, &r.mentions, &r.unreadMark, &r.archived, &r.pinned,
				&r.messages, &r.myMessages, &r.firstID, &r.firstDate, &r.firstText, &r.lastID, &r.lastDate, &r.lastText, &r.stateSyncedAt, &r.historyComplete)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	// A chat without a dialog state: the state fields are null, muted is false.
	noState := read(1)
	if noState.muted || noState.muteUntil != nil || noState.unread != nil || noState.archived != nil || noState.pinned != nil || noState.stateSyncedAt != nil {
		t.Errorf("chat without state: %+v", noState)
	}
	if noState.messages != 3 || noState.myMessages != 1 {
		t.Errorf("deleted messages must not count: messages=%d my=%d", noState.messages, noState.myMessages)
	}
	if noState.firstID == nil || *noState.firstID != 10 || noState.firstText == nil || *noState.firstText != "first in" {
		t.Errorf("first stored message: %+v", noState.firstID)
	}
	if noState.lastID == nil || *noState.lastID != 12 || noState.lastText == nil || *noState.lastText != "last in" {
		t.Errorf("last stored message: %+v", noState.lastID)
	}
	if noState.historyComplete == nil || !*noState.historyComplete {
		t.Errorf("history_complete = %+v", noState.historyComplete)
	}

	// Muted forever and unread count from the saved dialog state.
	forever := read(2)
	if !forever.muted || forever.muteUntil == nil || *forever.muteUntil != 2147483647 {
		t.Errorf("muted forever: muted=%v mute_until=%+v", forever.muted, forever.muteUntil)
	}
	if forever.unread == nil || *forever.unread != 4 {
		t.Errorf("unread = %+v", forever.unread)
	}
	if forever.historyComplete == nil || *forever.historyComplete {
		t.Errorf("history_complete = %+v, want false", forever.historyComplete)
	}

	// A mute that ended in the past is not muted; unread_mark is a flag.
	past := read(3)
	if past.muted || past.muteUntil == nil || *past.muteUntil != 5 {
		t.Errorf("mute in the past: muted=%v mute_until=%+v", past.muted, past.muteUntil)
	}
	if past.unreadMark == nil || !*past.unreadMark {
		t.Errorf("unread_mark = %+v", past.unreadMark)
	}
	if past.messages != 0 || past.firstID != nil || past.lastID != nil {
		t.Errorf("a chat with only deleted messages: %+v", past)
	}
	if past.historyComplete != nil {
		t.Errorf("a chat without sync_state must have a null history_complete: %+v", past.historyComplete)
	}

	archived := read(4)
	if archived.archived == nil || !*archived.archived || archived.pinned == nil || !*archived.pinned {
		t.Errorf("archived and pinned: %+v", archived)
	}
	if archived.mentions == nil || *archived.mentions != 2 {
		t.Errorf("unread_mentions = %+v", archived.mentions)
	}

	empty := read(5)
	if empty.messages != 0 || empty.myMessages != 0 || empty.firstID != nil || empty.lastID != nil {
		t.Errorf("chat without messages: %+v", empty)
	}
}

// TestChatsOverview covers the reader over the view: the filter hides a chat
// before the limit applies, the type narrows the rows and the order puts the most
// recent chat first.
func TestChatsOverview(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for _, chat := range []struct {
		id    int64
		kind  string
		title string
	}{
		{1, "channel", "News"},
		{2, "group", "Family"},
		{3, "channel", "Archive"},
	} {
		schemaExec(t, db.write, `INSERT INTO chats(id,type,title,first_seen_at,last_seen_at) VALUES(?,?,?,1,1)`, chat.id, chat.kind, chat.title)
	}
	for _, m := range []struct {
		chatID int64
		id     int
		date   int
		text   string
		out    int
	}{
		{1, 10, 100, "old news", 0},
		{1, 11, 300, "newer news", 1},
		{2, 20, 200, "family", 0},
	} {
		schemaExec(t, db.write, `INSERT INTO messages(chat_id,id,date,text,out,raw,search_text,first_seen_at,last_seen_at) VALUES(?,?,?,?,?,'{}',?,1,1)`, m.chatID, m.id, m.date, m.text, m.out, m.text)
	}

	all, err := db.ChatsOverview(ctx, ChatFilter{}, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ChatID != 1 || all[1].ChatID != 2 || all[2].ChatID != 3 {
		t.Fatalf("order by the last message date: %+v", all)
	}
	if all[0].Messages != 2 || all[0].MyMessages != 1 {
		t.Errorf("counts of the first chat: %+v", all[0])
	}
	if all[0].LastMessage == nil || all[0].LastMessage.Text != "newer news" || all[0].FirstMessage == nil || all[0].FirstMessage.ID != 10 {
		t.Errorf("previews of the first chat: %+v %+v", all[0].FirstMessage, all[0].LastMessage)
	}
	if all[2].Messages != 0 || all[2].FirstMessage != nil || all[2].LastMessage != nil {
		t.Errorf("a chat without messages: %+v", all[2])
	}

	// The type narrows the rows.
	channels, err := db.ChatsOverview(ctx, ChatFilter{}, 50, "channel")
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 {
		t.Fatalf("channel rows = %d, want 2: %+v", len(channels), channels)
	}
	for _, row := range channels {
		if row.Type != "channel" {
			t.Errorf("a row of another type: %+v", row)
		}
	}

	// A hidden chat does not take a place: the limit is applied after the filter.
	hidden, err := db.ChatsOverview(ctx, ChatFilter{Block: []int64{1}}, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 1 || hidden[0].ChatID != 2 {
		t.Errorf("a blocked chat reached the answer: %+v", hidden)
	}
	allowed, err := db.ChatsOverview(ctx, ChatFilter{Allow: []int64{3}}, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(allowed) != 1 || allowed[0].ChatID != 3 {
		t.Errorf("the allow list: %+v", allowed)
	}
	none, err := db.ChatsOverview(ctx, ChatFilter{Hide: true}, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("a hidden filter returned %d rows", len(none))
	}

	// The limit is clamped, so a caller cannot ask for the whole table.
	clamped, err := db.ChatsOverview(ctx, ChatFilter{}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(clamped) != 1 {
		t.Errorf("limit 0 returned %d rows, want 1", len(clamped))
	}
	if _, err := db.ChatsOverview(ctx, ChatFilter{}, 10000, ""); err != nil {
		t.Fatalf("a too large limit: %v", err)
	}
}

// TestChatsOverviewEmpty keeps an empty database an empty list, not an error and
// not a nil slice.
func TestChatsOverviewEmpty(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.ChatsOverview(context.Background(), ChatFilter{}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("rows = %#v", rows)
	}
}

// TestChatsOverviewHiddenChat keeps a chat closed by the access lists out of the
// answer, which is the rule the command built on this reader must keep.
func TestChatsOverviewHiddenChat(t *testing.T) {
	db := openTestDB(t)
	schemaExec(t, db.write, `INSERT INTO chats(id,type,title,first_seen_at,last_seen_at) VALUES(7,'user','Ann',1,1)`)
	rows, err := db.ChatsOverview(context.Background(), ChatFilter{Allow: []int64{8}}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a chat outside the allow list: %+v", rows)
	}
}
