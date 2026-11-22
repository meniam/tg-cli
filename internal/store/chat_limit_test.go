package store

import (
	"context"
	"testing"
)

func seedChatMessages(t *testing.T, db *DB, chatID int64, n int) {
	t.Helper()
	messages := make([]Message, 0, n)
	for id := 1; id <= n; id++ {
		messages = append(messages, Message{ChatID: chatID, ID: id, Date: int64(id), Text: "m", Raw: raw(`{}`)})
	}
	if _, err := db.SaveHistoryPage(context.Background(), chatID, messages, nil, false); err != nil {
		t.Fatal(err)
	}
}

func TestChatMessageLimitCRUD(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, found, err := db.ChatMessageLimit(ctx, 42); err != nil || found {
		t.Fatalf("absent: found=%v err=%v", found, err)
	}
	for _, bad := range []int{0, 99, MaxChatMessageLimit + 1} {
		if err := db.SetChatMessageLimit(ctx, 42, bad); err == nil {
			t.Errorf("accepted %d", bad)
		}
	}
	if err := db.SetChatMessageLimit(ctx, 43, 500); err != nil {
		t.Fatal(err)
	}
	if err := db.SetChatMessageLimit(ctx, 42, 100); err != nil {
		t.Fatal(err)
	}
	if limit, found, err := db.ChatMessageLimit(ctx, 42); err != nil || !found || limit != 100 {
		t.Fatalf("get: %d %v %v", limit, found, err)
	}
	list, err := db.ListChatMessageLimits(ctx)
	if err != nil || len(list) != 2 || list[0] != (ChatMessageLimitEntry{ChatID: 42, Limit: 100}) || list[1] != (ChatMessageLimitEntry{ChatID: 43, Limit: 500}) {
		t.Fatalf("list: %+v %v", list, err)
	}
	if removed, err := db.ClearChatMessageLimit(ctx, 42); err != nil || !removed {
		t.Fatalf("clear: %v %v", removed, err)
	}
	if removed, err := db.ClearChatMessageLimit(ctx, 42); err != nil || removed {
		t.Fatalf("clear twice: %v %v", removed, err)
	}
}

func TestTrimChatMessagesKeepsNinetyFivePercentOfTheNewest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedChatMessages(t, db, 42, 150)
	seedChatMessages(t, db, 77, 150)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.write.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Side rows of an old message (id 3) and of a kept one (id 140), plus a note.
	for _, id := range []int{3, 140} {
		exec(`INSERT INTO message_versions(chat_id,message_id,version,text,saved_at) VALUES(42,?,1,'v',1)`, id)
		exec(`INSERT INTO transcriptions(chat_id,message_id,transcription_id,text,pending,updated_at) VALUES(42,?,1,'t',0,1)`, id)
		exec(`INSERT INTO voice_queue(chat_id,message_id,kind,source,priority,state,enqueued_at) VALUES(42,?,'voice','sync',1,'done',1)`, id)
	}
	exec(`INSERT INTO voice_queue(chat_id,message_id,kind,source,priority,state,enqueued_at) VALUES(42,4,'voice','sync',1,'running',1)`)
	exec(`INSERT INTO notes(kind,chat_id,target_id,body,author,created_at,updated_at) VALUES('message',42,3,'note','owner',1,1)`)

	// Under the limit nothing happens.
	if n, err := db.TrimChatMessages(ctx, 42, 150); err != nil || n != 0 {
		t.Fatalf("at the limit: %d %v", n, err)
	}
	if _, err := db.TrimChatMessages(ctx, 42, 99); err == nil {
		t.Fatal("a limit below the floor was accepted")
	}
	removed, err := db.TrimChatMessages(ctx, 42, 100)
	if err != nil || removed != 55 {
		t.Fatalf("removed %d err=%v, want 55", removed, err)
	}
	if got := schemaCount(t, db.read, `SELECT count(*) FROM messages WHERE chat_id=42`); got != 95 {
		t.Fatalf("kept %d, want 95", got)
	}
	if got := schemaCount(t, db.read, `SELECT min(id) FROM messages WHERE chat_id=42`); got != 56 {
		t.Fatalf("oldest kept %d, want 56", got)
	}
	if got := schemaCount(t, db.read, `SELECT count(*) FROM messages WHERE chat_id=77`); got != 150 {
		t.Fatalf("another chat lost messages: %d", got)
	}
	for table, want := range map[string]int{"message_versions": 1, "transcriptions": 1} {
		if got := schemaCount(t, db.read, `SELECT count(*) FROM `+table+` WHERE chat_id=42`); got != want {
			t.Errorf("%s rows %d, want %d", table, got, want)
		}
	}
	// The finished job of the old message is gone, the running one stays.
	if got := schemaCount(t, db.read, `SELECT count(*) FROM voice_queue WHERE chat_id=42 AND message_id=3`); got != 0 {
		t.Errorf("old finished job stayed")
	}
	if got := schemaCount(t, db.read, `SELECT count(*) FROM voice_queue WHERE chat_id=42 AND message_id=4`); got != 1 {
		t.Errorf("running job was removed")
	}
	if got := schemaCount(t, db.read, `SELECT count(*) FROM notes WHERE chat_id=42`); got != 1 {
		t.Errorf("note was removed")
	}
	// The search index follows the delete.
	if got := schemaCount(t, db.read, `SELECT count(*) FROM messages_stem_fts`); got != 95+150 {
		t.Errorf("fts rows %d, want %d", got, 95+150)
	}
	// A second pass has nothing to do.
	if n, err := db.TrimChatMessages(ctx, 42, 100); err != nil || n != 0 {
		t.Fatalf("second pass: %d %v", n, err)
	}
}

func TestTrimChatMessagesWorksInSeveralBatches(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	seedChatMessages(t, db, 42, 5000)
	removed, err := db.TrimChatMessages(ctx, 42, 1000)
	if err != nil || removed != 5000-950 {
		t.Fatalf("removed %d err=%v", removed, err)
	}
	if got := schemaCount(t, db.read, `SELECT count(*) FROM messages WHERE chat_id=42`); got != 950 {
		t.Fatalf("kept %d, want 950", got)
	}
}
