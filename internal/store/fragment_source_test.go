package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fragmentSourceFixture(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{
		{ID: 1, Type: "supergroup", Title: "One"},
		{ID: 2, Type: "group", Title: "Two"},
	}); err != nil {
		t.Fatal(err)
	}
	topic, reply, service := int64(7), int64(3), "pin"
	if err := db.UpsertMessages(ctx, []Message{
		{ChatID: 2, ID: 9, Date: 50, Text: "Другой чат", Raw: raw(`{}`)},
		{ChatID: 1, ID: 4, Date: 20, Text: "Позже по дате", Raw: raw(`{}`)},
		{ChatID: 1, ID: 3, Date: 10, Text: "Привет", TopicID: &topic, Raw: raw(`{}`)},
		{ChatID: 1, ID: 2, Date: 10, Text: "Ответ", ReplyTo: &reply, Raw: raw(`{}`)},
		{ChatID: 1, ID: 5, Date: 11, Text: "Удалим", Raw: raw(`{}`)},
		{ChatID: 1, ID: 6, Date: 12, Text: "Служебное", Service: &service, Raw: raw(`{}`)},
		{ChatID: 1, ID: 7, Date: 13, Text: "", Raw: raw(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkMessagesDeleted(ctx, 1, []int{5}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMessagesForFragmentsSelectsAndOrders(t *testing.T) {
	db := fragmentSourceFixture(t)
	got, err := db.MessagesForFragments(context.Background(), ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	type key struct{ chat, id int64 }
	var order []key
	for _, m := range got {
		order = append(order, key{m.ChatID, m.ID})
	}
	// Deleted (5), service (6) and empty (7) messages are left out; the order is
	// (chat_id, date, id).
	want := []key{{1, 2}, {1, 3}, {1, 4}, {2, 9}}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if got[0].ReplyTo != 3 || got[0].TopicID != 0 {
		t.Errorf("message 2: reply %d topic %d, want 3 and 0", got[0].ReplyTo, got[0].TopicID)
	}
	if got[1].TopicID != 7 || got[1].ReplyTo != 0 {
		t.Errorf("message 3: reply %d topic %d, want 0 and 7", got[1].ReplyTo, got[1].TopicID)
	}
}

func TestMessagesForFragmentsCountsRunesNotBytes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "group", Title: "One"}}); err != nil {
		t.Fatal(err)
	}
	text := "Привет, мир"
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 1, Date: 1, Text: text, Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.MessagesForFragments(ctx, ChatFilter{})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if got[0].Chars != 11 || len(text) == 11 {
		t.Errorf("Chars = %d for %d bytes, want 11 characters", got[0].Chars, len(text))
	}
}

func TestMessagesForFragmentsAppliesTheAccessFilter(t *testing.T) {
	db := fragmentSourceFixture(t)
	ctx := context.Background()
	cases := []struct {
		name   string
		filter ChatFilter
		chats  []int64
	}{
		{"allow", ChatFilter{Allow: []int64{2}}, []int64{2}},
		{"block", ChatFilter{Block: []int64{2}}, []int64{1, 1, 1}},
		{"hide", ChatFilter{Hide: true}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := db.MessagesForFragments(ctx, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			var chats []int64
			for _, m := range got {
				chats = append(chats, m.ChatID)
			}
			if !reflect.DeepEqual(chats, tc.chats) {
				t.Errorf("chats = %v, want %v", chats, tc.chats)
			}
		})
	}
}

// BenchmarkMessagesForFragments reads 100 000 messages of 40 chats: the shape
// of a large profile. Run it with `go test ./internal/store -bench Fragments -benchtime 3x`.
func BenchmarkMessagesForFragments(b *testing.B) {
	db, err := Open(b.TempDir() + "/store.db")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	const chats, perChat = 40, 2500
	var rows []Chat
	for c := 1; c <= chats; c++ {
		rows = append(rows, Chat{ID: int64(c), Type: "group", Title: "Chat"})
	}
	if err := db.UpsertChats(ctx, rows); err != nil {
		b.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	text := strings.Repeat("слово ", 12)
	for c := 1; c <= chats; c++ {
		batch := make([]Message, 0, perChat)
		for i := 1; i <= perChat; i++ {
			batch = append(batch, Message{ChatID: int64(c), ID: i, Date: base + int64(i)*60, Text: text, Raw: raw(`{}`)})
		}
		if err := db.UpsertMessages(ctx, batch); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		got, err := db.MessagesForFragments(ctx, ChatFilter{})
		if err != nil || len(got) != chats*perChat {
			b.Fatalf("got %d messages, %v", len(got), err)
		}
	}
}
