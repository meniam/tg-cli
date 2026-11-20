package store

import (
	"context"
	"testing"
)

func TestAudit08_SaveHistoryPageRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chatID int64
		page   []Message
	}{
		{"zero chat id", 0, nil},
		{"zero chat id with messages", 0, []Message{{ChatID: 0, ID: 6, Date: 6, Raw: raw(`{}`)}}},
		{"message of another chat", 42, []Message{historyMessage(6, "ok"), {ChatID: 43, ID: 5, Date: 5, Raw: raw(`{}`)}}},
		{"zero message id", 42, []Message{historyMessage(6, "ok"), {ChatID: 42, ID: 0, Date: 5, Raw: raw(`{}`)}}},
		{"negative message id", 42, []Message{historyMessage(6, "ok"), {ChatID: 42, ID: -1, Date: 5, Raw: raw(`{}`)}}},
		{"zero date", 42, []Message{historyMessage(6, "ok"), {ChatID: 42, ID: 5, Date: 0, Raw: raw(`{}`)}}},
		{"negative date", 42, []Message{historyMessage(6, "ok"), {ChatID: 42, ID: 5, Date: -1, Raw: raw(`{}`)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			ctx := context.Background()
			setClock(db, 100)
			if _, err := db.SaveHistoryPage(ctx, 42, []Message{historyMessage(7, "existing")}, nil, false); err != nil {
				t.Fatal(err)
			}
			setClock(db, 200)
			if _, err := db.SaveHistoryPage(ctx, tc.chatID, tc.page, nil, true); err == nil {
				t.Fatal("invalid page succeeded")
			}
			requireSyncProgress(t, db, 7, 7, false, 100)
			if n := schemaCount(t, db.read, `SELECT count(*) FROM messages`); n != 1 {
				t.Fatalf("invalid page left %d messages, want only the existing one", n)
			}
			if n := schemaCount(t, db.read, `SELECT count(*) FROM sync_state`); n != 1 {
				t.Fatalf("invalid page created sync state rows: %d", n)
			}
		})
	}
}
