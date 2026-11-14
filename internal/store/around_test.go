package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestMessagesAroundOnlyCurrentChatAndNondeleted(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	for id := 1; id <= 7; id++ {
		if err := db.UpsertMessages(ctx, []Message{{ChatID: 42, ID: id, Date: int64(id), Text: "text", Raw: json.RawMessage(`{}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 77, ID: 5, Date: 5, Text: "other", Raw: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkMessagesDeleted(ctx, 42, []int{4}); err != nil {
		t.Fatal(err)
	}
	got, err := db.MessagesAround(ctx, 42, 5, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{2, 3, 5, 6, 7}
	if len(got) != len(want) {
		t.Fatalf("window: %+v", got)
	}
	for i, m := range got {
		if m.ID != want[i] || m.ChatID != 42 {
			t.Fatalf("window[%d]=%+v", i, m)
		}
	}
	if _, err := db.MessagesAround(ctx, 42, 4, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted anchor: %v", err)
	}
	if _, err := db.MessagesAround(ctx, 42, 99, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown anchor: %v", err)
	}
}
