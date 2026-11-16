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
	chat := Chat{ID: 9, Type: "channel", Title: "Restricted", CreatedAt: &created, Scam: true, Fake: true, Restricted: true, FlagsKnown: true, RestrictionReason: reason}
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
	// A source that did not read the flags must not clear them: a list, a resolve
	// or an event carries no channel object of its own.
	if err := db.UpsertChats(ctx, []Chat{{ID: 9, Type: "channel", Title: "Restricted"}}); err != nil {
		t.Fatal(err)
	}
	kept, err := db.GetChat(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !kept.Scam || !kept.Fake || !kept.Restricted {
		t.Errorf("a record without flags cleared them: %+v", kept)
	}
	if kept.CreatedAt == nil || *kept.CreatedAt != created {
		t.Errorf("the creation date must survive a read without it: %+v", kept.CreatedAt)
	}
	// A record with known flags does update them, and that is how a flag Telegram
	// no longer sets goes away.
	if err := db.UpsertChats(ctx, []Chat{{ID: 9, Type: "channel", Title: "Restricted", FlagsKnown: true}}); err != nil {
		t.Fatal(err)
	}
	cleared, err := db.GetChat(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Scam || cleared.Fake || cleared.Restricted {
		t.Errorf("known flags did not clear the old ones: %+v", cleared)
	}
	// The chat list and the resolve now carry the flags, so a save through the
	// daemon helper keeps them.
	if err := db.UpsertChats(ctx, []Chat{{ID: 9, Type: "channel", Title: "Restricted", Scam: true, FlagsKnown: true}}); err != nil {
		t.Fatal(err)
	}
	partial, err := db.GetChat(ctx, 9)
	if err != nil {
		t.Fatal(err)
	}
	if !partial.Scam || partial.Fake {
		t.Errorf("a partial known record: %+v", partial)
	}
}
