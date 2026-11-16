package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestSaveChatFlags covers the new chat columns: the flags, the creation date and
// the restriction reason survive an upsert and a read, and saving the same values
// again adds no row to chat_versions. The version trigger stores what the owner
// sees as settings, not the flags.
func TestSaveChatFlags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	created := int64(1700000000)
	reason := json.RawMessage(`[{"platform":"ios","reason":"porno","text":"Not available"}]`)
	chat := Chat{ID: 9, Type: "channel", Title: "Restricted", CreatedAt: &created, Scam: true, Fake: true, Restricted: true, RestrictionReason: reason}
	if err := db.UpsertChats(ctx, []Chat{chat}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetChat(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if got.CreatedAt == nil || *got.CreatedAt != created || !got.Scam || !got.Fake || !got.Restricted {
		t.Fatalf("flags = %+v", got)
	}
	if string(got.RestrictionReason) != string(reason) {
		t.Errorf("restriction_reason = %s", got.RestrictionReason)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM chat_versions WHERE chat_id=9`); count != 0 {
		t.Errorf("the first save created %d versions", count)
	}
	// Saving the same values again changes neither the row nor the versions.
	if err := db.UpsertChats(ctx, []Chat{chat}); err != nil {
		t.Fatal(err)
	}
	again, err := db.GetChat(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if again.CreatedAt == nil || *again.CreatedAt != created || !again.Scam {
		t.Fatalf("flags after the second save = %+v", again)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM chat_versions WHERE chat_id=9`); count != 0 {
		t.Errorf("the repeated save created %d versions", count)
	}
	// A chat without the flags keeps them empty: nil means "Telegram did not say".
	plain := Chat{ID: 10, Type: "group", Title: "Plain"}
	if err := db.UpsertChats(ctx, []Chat{plain}); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetChat(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CreatedAt != nil || stored.Scam || stored.Fake || stored.Restricted || stored.RestrictionReason != nil {
		t.Errorf("a chat without flags stored %+v", stored)
	}
	// The flags are NOT NULL with a default of 0, so the latest read wins: a chat
	// Telegram no longer marks as scam loses the flag.
	if err := db.UpsertChats(ctx, []Chat{{ID: 9, Type: "channel", Title: "Restricted"}}); err != nil {
		t.Fatal(err)
	}
	cleared, err := db.GetChat(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Scam || cleared.Fake || cleared.Restricted {
		t.Errorf("the flags were not refreshed: %+v", cleared)
	}
	if cleared.CreatedAt == nil || *cleared.CreatedAt != created {
		t.Errorf("the creation date must survive a read without it: %+v", cleared.CreatedAt)
	}
}
