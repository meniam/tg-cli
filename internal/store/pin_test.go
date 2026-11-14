package store

import (
	"context"
	"errors"
	"testing"
)

func TestSetMessagePinnedPreservesSavedData(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	setClock(db, 100)
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 42, ID: 7, Date: 50, Text: "forest question", Raw: raw(`{"media":"poll"}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 42, ID: 7, Date: 50, Text: "forest question edited", Raw: raw(`{"media":"poll"}`)}}); err != nil {
		t.Fatal(err)
	}
	if n, err := db.MarkMessagesDeleted(ctx, 42, []int{7}); err != nil || n != 1 {
		t.Fatalf("mark deleted: %d %v", n, err)
	}
	before, err := db.GetMessage(ctx, 42, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, pinned := range []bool{true, true, false} {
		updated, err := db.SetMessagePinned(ctx, 42, 7, pinned)
		if err != nil || !updated {
			t.Fatalf("set pinned=%v: updated=%v err=%v", pinned, updated, err)
		}
		got, err := db.GetMessage(ctx, 42, 7)
		if err != nil || got.Pinned == nil || *got.Pinned != pinned {
			t.Fatalf("pinned=%v: message=%+v err=%v", pinned, got, err)
		}
		if got.DeletedAt == nil || *got.DeletedAt != *before.DeletedAt || got.Text != before.Text || got.Date != before.Date || got.LastSeenAt != before.LastSeenAt || string(got.Raw) != string(before.Raw) {
			t.Fatalf("pin update changed other data: before=%+v after=%+v", before, got)
		}
		versions, err := db.MessageVersions(ctx, 42, 7)
		if err != nil || len(versions) != 1 || versions[0].Text != "forest question" {
			t.Fatalf("pin update changed versions: %+v %v", versions, err)
		}
	}
	if got := schemaCount(t, db.read, `SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'forest'`); got != 1 {
		t.Fatalf("FTS rows=%d", got)
	}
	checkFTSIntegrity(t, db.write)
	for _, key := range [][2]int64{{42, 8}, {43, 7}} {
		updated, err := db.SetMessagePinned(ctx, key[0], int(key[1]), true)
		if err != nil || updated {
			t.Fatalf("missing %v: updated=%v err=%v", key, updated, err)
		}
	}
	if got := schemaCount(t, db.read, `SELECT count(*) FROM messages`); got != 1 {
		t.Fatalf("message rows=%d", got)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.SetMessagePinned(cancelled, 42, 7, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
