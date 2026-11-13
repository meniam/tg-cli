package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"tg-cli/internal/textnorm"
)

func stringPtr(v string) *string   { return &v }
func intPtr(v int64) *int64        { return &v }
func boolPtr(v bool) *bool         { return &v }
func raw(v string) json.RawMessage { return json.RawMessage(v) }
func setClock(db *DB, unix int64)  { db.now = func() time.Time { return time.Unix(unix, 0) } }

func TestUpsertChatsAndPeople(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	chat := Chat{ID: -100000000001, Type: "channel", Title: "Ёлка", Username: stringPtr("forest"), About: stringPtr("Сосна"), Members: intPtr(0), Forum: boolPtr(false), Unread: intPtr(3), Settings: raw(`{"theme":"green"}`), Raw: raw(`{"chat":1}`)}
	person := Person{ID: 42, Username: stringPtr("alena"), FirstName: stringPtr("Алёна"), LastName: stringPtr("Сосна"), Phone: stringPtr("+7 (999) 123-45-67"), Bio: stringPtr("Привет"), IsBot: boolPtr(false), IsContact: boolPtr(true), Raw: raw(`{"user":42}`)}
	if err := db.UpsertChats(ctx, []Chat{chat}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{person}); err != nil {
		t.Fatal(err)
	}
	gotChat, err := db.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotPerson, err := db.GetPerson(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotChat.FirstSeenAt != 100 || gotChat.LastSeenAt != 100 || gotPerson.FirstSeenAt != 100 || gotPerson.LastSeenAt != 100 {
		t.Fatal("initial timestamps incorrect")
	}
	if gotChat.SearchText != textnorm.Normalize("Ёлка forest Сосна") || gotPerson.SearchText != textnorm.Normalize("Алёна Сосна alena 79991234567 Привет") {
		t.Fatalf("search texts: chat=%q person=%q", gotChat.SearchText, gotPerson.SearchText)
	}
	if ftsCount(t, db.read, "chats_fts", "ЕЛКА") != 1 || ftsCount(t, db.read, "people_fts", "АЛЕНА") != 1 {
		t.Fatal("Cyrillic FTS query missed rows")
	}
	setClock(db, 200)
	if err := db.UpsertChats(ctx, []Chat{{ID: chat.ID, About: stringPtr(""), Unread: intPtr(0), Forum: boolPtr(true)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{{ID: person.ID, Bio: stringPtr(""), IsContact: boolPtr(false)}}); err != nil {
		t.Fatal(err)
	}
	gotChat, err = db.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotPerson, err = db.GetPerson(ctx, person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotChat.About == nil || *gotChat.About != "Сосна" || gotChat.Unread == nil || *gotChat.Unread != 0 || gotChat.Forum == nil || !*gotChat.Forum {
		t.Fatalf("chat merge failed: %+v", gotChat)
	}
	if string(gotChat.Raw) != `{"chat":1}` || string(gotChat.Settings) != `{"theme":"green"}` {
		t.Fatalf("chat JSON was erased: raw=%s settings=%s", gotChat.Raw, gotChat.Settings)
	}
	if gotPerson.Bio == nil || *gotPerson.Bio != "Привет" || gotPerson.IsContact == nil || *gotPerson.IsContact {
		t.Fatalf("person merge failed: %+v", gotPerson)
	}
	if string(gotPerson.Raw) != `{"user":42}` {
		t.Fatalf("person raw was erased: %s", gotPerson.Raw)
	}
	if gotChat.FirstSeenAt != 100 || gotChat.LastSeenAt != 200 || gotPerson.FirstSeenAt != 100 || gotPerson.LastSeenAt != 200 {
		t.Fatal("re-upsert timestamps incorrect")
	}
	if ftsCount(t, db.read, "chats_fts", "сосна") != 1 || ftsCount(t, db.read, "people_fts", "привет") != 1 {
		t.Fatal("merge erased searchable text")
	}
	setClock(db, 300)
	if err := db.UpsertChats(ctx, []Chat{{ID: chat.ID, About: stringPtr("Берёза")}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{{ID: person.ID, Bio: stringPtr("Река")}}); err != nil {
		t.Fatal(err)
	}
	if ftsCount(t, db.read, "chats_fts", "СОСНА") != 0 || ftsCount(t, db.read, "chats_fts", "БЕРЕЗА") != 1 {
		t.Fatal("chat FTS not updated")
	}
	if ftsCount(t, db.read, "people_fts", "привет") != 0 || ftsCount(t, db.read, "people_fts", "река") != 1 {
		t.Fatal("people FTS not updated")
	}
	if schemaCount(t, db.read, `SELECT count(*) FROM chats WHERE id=?`, chat.ID) != 1 || schemaCount(t, db.read, `SELECT count(*) FROM people WHERE id=?`, person.ID) != 1 {
		t.Fatal("re-upsert duplicated row")
	}
	checkFTSIntegrity(t, db.write)
}

func TestMessageVersionsDeletionAndDuplicates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	m := Message{ChatID: 1, ID: 7, Date: 50, Text: "Ёлка", Raw: raw(`{"v":1}`)}
	if err := db.UpsertMessages(ctx, []Message{m}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetMessage(ctx, 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.FirstSeenAt != 100 || got.LastSeenAt != 100 || got.DeletedAt != nil {
		t.Fatalf("initial message: %+v", got)
	}
	setClock(db, 200)
	m.Pinned = boolPtr(true)
	m.Raw = raw(`{"v":2}`)
	if err := db.UpsertMessages(ctx, []Message{m}); err != nil {
		t.Fatal(err)
	}
	versions, err := db.MessageVersions(ctx, 1, 7)
	if err != nil || len(versions) != 0 {
		t.Fatalf("unchanged text versions=%v err=%v", versions, err)
	}
	got, err = db.GetMessage(ctx, 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if got.FirstSeenAt != 100 || got.LastSeenAt != 200 || got.Pinned == nil || !*got.Pinned {
		t.Fatalf("metadata update failed: %+v", got)
	}
	setClock(db, 300)
	m.Text = "Берёза"
	m.EditedAt = intPtr(250)
	m.Raw = raw(`{"v":3}`)
	if err := db.UpsertMessages(ctx, []Message{m}); err != nil {
		t.Fatal(err)
	}
	setClock(db, 400)
	m.Text = "Река"
	m.EditedAt = intPtr(350)
	m.Raw = raw(`{"v":4}`)
	if err := db.UpsertMessages(ctx, []Message{m}); err != nil {
		t.Fatal(err)
	}
	versions, err = db.MessageVersions(ctx, 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Version != 1 || versions[0].Text != "Ёлка" || string(versions[0].Raw) != `{"v":2}` || versions[0].EditedAt != nil || versions[0].SavedAt != 300 || versions[1].Version != 2 || versions[1].Text != "Берёза" || string(versions[1].Raw) != `{"v":3}` || versions[1].EditedAt == nil || *versions[1].EditedAt != 250 || versions[1].SavedAt != 400 {
		t.Fatalf("versions=%+v", versions)
	}
	if ftsCount(t, db.read, "messages_fts", "ЁЛКА") != 0 || ftsCount(t, db.read, "messages_fts", "река") != 1 {
		t.Fatal("message FTS not updated")
	}
	setClock(db, 500)
	count, err := db.MarkMessagesDeleted(ctx, 1, []int{7, 7, 99})
	if err != nil || count != 1 {
		t.Fatalf("first delete count=%d err=%v", count, err)
	}
	count, err = db.MarkMessagesDeleted(ctx, 1, []int{7})
	if err != nil || count != 0 {
		t.Fatalf("second delete count=%d err=%v", count, err)
	}
	got, err = db.GetMessage(ctx, 1, 7)
	if err != nil || got.DeletedAt == nil || *got.DeletedAt != 500 {
		t.Fatalf("deleted message=%+v err=%v", got, err)
	}
	if ftsCount(t, db.read, "messages_fts", "река") != 1 {
		t.Fatal("deleted message left FTS")
	}
	if versions, err = db.MessageVersions(ctx, 1, 7); err != nil || len(versions) != 2 {
		t.Fatalf("deletion changed versions: %v, %v", versions, err)
	}
	setClock(db, 600)
	m.Pinned = boolPtr(false)
	if err := db.UpsertMessages(ctx, []Message{m}); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetMessage(ctx, 1, 7)
	if err != nil || got.DeletedAt != nil || got.FirstSeenAt != 100 || got.LastSeenAt != 600 || got.Pinned == nil || *got.Pinned {
		t.Fatalf("resurrected message=%+v err=%v", got, err)
	}
	if versions, err = db.MessageVersions(ctx, 1, 7); err != nil || len(versions) != 2 {
		t.Fatalf("resurrection changed versions: %v, %v", versions, err)
	}
	batch := []Message{{ChatID: 1, ID: 8, Date: 1, Text: "discarded", Raw: raw(`{"v":1}`)}, {ChatID: 2, ID: 8, Date: 1, Text: "other chat", Raw: raw(`{"v":2}`)}, {ChatID: 1, ID: 8, Date: 2, Text: "winner", Raw: raw(`{"v":3}`)}}
	if err := db.UpsertMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	winner, err := db.GetMessage(ctx, 1, 8)
	if err != nil || winner.Text != "winner" {
		t.Fatalf("duplicate winner=%+v err=%v", winner, err)
	}
	if schemaCount(t, db.read, `SELECT count(*) FROM messages WHERE id=8`) != 2 {
		t.Fatal("batch across chats lost a message")
	}
	if versions, err := db.MessageVersions(ctx, 1, 8); err != nil || len(versions) != 0 {
		t.Fatalf("duplicate created versions=%v err=%v", versions, err)
	}
	if err := db.UpsertMessages(ctx, []Message{
		{ChatID: 1, ID: 9, Date: 1, Text: "discarded", Raw: raw(`{invalid`)},
		{ChatID: 1, ID: 9, Date: 2, Text: "valid winner", Raw: raw(`{"v":9}`)},
	}); err != nil {
		t.Fatalf("superseded duplicate was validated: %v", err)
	}
	if winner, err := db.GetMessage(ctx, 1, 9); err != nil || winner.Text != "valid winner" {
		t.Fatalf("last duplicate did not win: %+v, %v", winner, err)
	}
	checkFTSIntegrity(t, db.write)
}

func TestInvalidRawAndCancelledContextAreAtomic(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 1, Date: 1, Text: "valid", Raw: raw(`{"ok":true}`)}, {ChatID: 1, ID: 2, Date: 1, Text: "invalid", Raw: raw(`{bad`)}})
	if err == nil {
		t.Fatal("invalid raw accepted")
	}
	if schemaCount(t, db.read, `SELECT count(*) FROM messages`) != 0 {
		t.Fatal("invalid batch was partially stored")
	}
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "valid"}, {ID: 2, Type: "invalid", Title: "bad"}}); err == nil {
		t.Fatal("invalid chat type was accepted")
	}
	if schemaCount(t, db.read, `SELECT count(*) FROM chats`) != 0 {
		t.Fatal("failed chat transaction was partially stored")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, fn := range []struct {
		name string
		run  func() error
	}{
		{"chats", func() error { return db.UpsertChats(canceled, []Chat{{ID: 1, Type: "group", Title: "x"}}) }},
		{"people", func() error { return db.UpsertPeople(canceled, []Person{{ID: 1}}) }},
		{"messages", func() error {
			return db.UpsertMessages(canceled, []Message{{ChatID: 1, ID: 1, Date: 1, Raw: raw(`{}`)}})
		}},
		{"delete", func() error { _, err := db.MarkMessagesDeleted(canceled, 1, []int{1}); return err }},
	} {
		t.Run(fn.name, func(t *testing.T) {
			if err := fn.run(); !errors.Is(err, context.Canceled) {
				t.Errorf("error=%v, want canceled", err)
			}
		})
	}
	for _, table := range []string{"chats", "people", "messages"} {
		if n := schemaCount(t, db.read, "SELECT count(*) FROM "+table); n != 0 {
			t.Errorf("%s has %d rows after cancellation", table, n)
		}
	}
	for _, tc := range []struct {
		name string
		get  func() error
	}{
		{"chat", func() error { _, err := db.GetChat(ctx, 1); return err }},
		{"person", func() error { _, err := db.GetPerson(ctx, 1); return err }},
		{"message", func() error { _, err := db.GetMessage(ctx, 1, 1); return err }},
	} {
		t.Run("not_found_"+tc.name, func(t *testing.T) {
			if err := tc.get(); !errors.Is(err, ErrNotFound) {
				t.Errorf("error=%v, want ErrNotFound", err)
			}
		})
	}
}

func TestBulkMessages(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	batch := make([]Message, 5000)
	for i := range batch {
		batch[i] = Message{ChatID: int64(i%5 + 1), ID: i + 1, Date: int64(i + 1), Text: fmt.Sprintf("Bulk Ёлка %d", i), Raw: raw(`{"kind":"synthetic"}`)}
	}
	start := time.Now()
	if err := db.UpsertMessages(ctx, batch); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("5000 messages took %s, want under 5s", elapsed)
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM messages`); n != 5000 {
		t.Errorf("rows=%d, want 5000", n)
	}
	if n := ftsCount(t, db.read, "messages_fts", "ЕЛКА"); n != 5000 {
		t.Errorf("FTS matches=%d, want 5000", n)
	}
	checkFTSIntegrity(t, db.write)
}

func TestNotesSurviveStoreAPI(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	chat := Chat{ID: 1, Type: "group", Title: "Chat"}
	person := Person{ID: 2, FirstName: stringPtr("Person")}
	message := Message{ChatID: 1, ID: 3, Date: 1, Text: "Message", Raw: raw(`{}`)}
	if err := db.UpsertChats(ctx, []Chat{chat}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{person}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{message}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id               int
		kind             string
		chatID, targetID any
	}{{11, "chat", int64(1), nil}, {12, "person", nil, int64(2)}, {13, "message", int64(1), int64(3)}} {
		schemaExec(t, db.write, `INSERT INTO notes(id,kind,chat_id,target_id,body,author,search_text,created_at,updated_at) VALUES(?,?,?,?,?,'owner',?,1,1)`, tc.id, tc.kind, tc.chatID, tc.targetID, "Keep Ёлка", textnorm.Normalize("Keep Ёлка"))
		schemaExec(t, db.write, `INSERT INTO note_tags(note_id,tag) VALUES(?,'protected')`, tc.id)
	}
	assert := func() {
		t.Helper()
		if n := schemaCount(t, db.read, `SELECT count(*) FROM notes WHERE id IN (11,12,13) AND body='Keep Ёлка'`); n != 3 {
			t.Errorf("notes=%d", n)
		}
		if n := schemaCount(t, db.read, `SELECT count(*) FROM note_tags WHERE tag='protected'`); n != 3 {
			t.Errorf("tags=%d", n)
		}
		if n := ftsCount(t, db.read, "notes_fts", "ЕЛКА"); n != 3 {
			t.Errorf("notes FTS=%d", n)
		}
		checkFTSIntegrity(t, db.write)
	}
	assert()
	setClock(db, 200)
	chat.Title = "Updated"
	person.FirstName = stringPtr("Updated")
	message.Text = "Updated"
	message.Raw = raw(`{"v":2}`)
	if err := db.UpsertChats(ctx, []Chat{chat}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertPeople(ctx, []Person{person}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{message}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkMessagesDeleted(ctx, 1, []int{3}); err != nil {
		t.Fatal(err)
	}
	assert()
}

func TestConcurrentMessageUpserts(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 1; i <= 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			results <- db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: id, Date: 1, Text: "parallel", Raw: raw(`{}`)}})
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("parallel upsert: %v", err)
		}
	}
	if n := schemaCount(t, db.read, `SELECT count(*) FROM messages`); n != 2 {
		t.Errorf("parallel messages=%d, want 2", n)
	}
}
