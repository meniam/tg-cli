package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// fullSyncFixture stores four chats: a group, a supergroup, a channel and a direct
// chat, which the job never covers.
func fullSyncFixture(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []Chat{
		{ID: 1, Type: "group", Title: "Family"},
		{ID: 2, Type: "supergroup", Title: "Team"},
		{ID: 3, Type: "channel", Title: "News"},
		{ID: 4, Type: "user", Title: "Ann"},
	}); err != nil {
		t.Fatal(err)
	}
}

// TestChatsForFullSync covers the selection of the job: only groups and channels,
// only the ones the filter allows, and without refresh only the chats that were
// never read completely.
func TestChatsForFullSync(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	fullSyncFixture(t, db)

	all, err := db.ChatsForFullSync(ctx, ChatFilter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != 1 || all[1].ID != 2 || all[2].ID != 3 {
		t.Fatalf("chats = %+v", all)
	}

	// A chat read completely leaves the list; another chat closed by the access
	// lists is never returned.
	synced := db.now().Unix()
	if err := db.SaveChatFull(ctx, 2, ChatFull{SyncedAt: &synced}); err != nil {
		t.Fatal(err)
	}
	remaining, err := db.ChatsForFullSync(ctx, ChatFilter{Block: []int64{3}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != 1 {
		t.Fatalf("remaining = %+v", remaining)
	}
	// With refresh every allowed group and channel comes back, read or not.
	refreshed, err := db.ChatsForFullSync(ctx, ChatFilter{Block: []int64{3}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed) != 2 || refreshed[0].ID != 1 || refreshed[1].ID != 2 {
		t.Fatalf("refreshed = %+v", refreshed)
	}
	// A hidden policy returns nothing at all.
	hidden, err := db.ChatsForFullSync(ctx, ChatFilter{Hide: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 0 {
		t.Fatalf("a hidden filter returned %+v", hidden)
	}
}

// TestSaveChatFull covers the two outcomes: a successful read fills the fields,
// clears the error and adds no version; a failed one records the error, keeps the
// chat on the list, and a successful retry clears the error again.
func TestSaveChatFull(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	fullSyncFixture(t, db)

	failure := "CHANNEL_PRIVATE"
	if err := db.SaveChatFull(ctx, 3, ChatFull{Error: &failure}); err != nil {
		t.Fatal(err)
	}
	chat, err := db.GetChat(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if chat.FullError == nil || *chat.FullError != failure || chat.FullSyncedAt != nil {
		t.Fatalf("a failed read: %+v", chat)
	}
	// The failed chat stays on the list without refresh, so a later start retries
	// it; with refresh it is there as well.
	pending, err := db.ChatsForFullSync(ctx, ChatFilter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 3 {
		t.Fatalf("a failed read dropped the chat: %+v", pending)
	}

	about := "About the news"
	members := int64(42)
	settings := json.RawMessage(`{"slow_mode_seconds":30}`)
	synced := db.now().Unix()
	if err := db.SaveChatFull(ctx, 3, ChatFull{About: &about, Members: &members, Settings: settings, SyncedAt: &synced}); err != nil {
		t.Fatal(err)
	}
	chat, err = db.GetChat(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if chat.FullError != nil || chat.FullSyncedAt == nil || *chat.FullSyncedAt != synced {
		t.Fatalf("a successful read: %+v", chat)
	}
	if chat.About == nil || *chat.About != about || chat.Members == nil || *chat.Members != members || string(chat.Settings) != string(settings) {
		t.Fatalf("the fields of the read: %+v", chat)
	}
	// The first value of an empty column is not a change the version trigger
	// records, and writing the same values again adds nothing either: the job
	// stores what it read, it does not invent history.
	if count := schemaCount(t, db.read, `SELECT count(*) FROM chat_versions WHERE chat_id=3`); count != 0 {
		t.Errorf("versions after the first full read = %d", count)
	}
	if err := db.SaveChatFull(ctx, 3, ChatFull{About: &about, Members: &members, Settings: settings, SyncedAt: &synced}); err != nil {
		t.Fatal(err)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM chat_versions WHERE chat_id=3`); count != 0 {
		t.Errorf("a repeated write added a version: %d", count)
	}
	// A new description of an already known one is a change and is versioned.
	changed := "About the news, updated"
	if err := db.SaveChatFull(ctx, 3, ChatFull{About: &changed, SyncedAt: &synced}); err != nil {
		t.Fatal(err)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM chat_versions WHERE chat_id=3`); count != 1 {
		t.Errorf("a changed description added %d versions", count)
	}
	// A read chat leaves the list without refresh.
	after, err := db.ChatsForFullSync(ctx, ChatFilter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("after the read: %+v", after)
	}
	// An unknown chat is not silently accepted.
	if err := db.SaveChatFull(ctx, 99, ChatFull{SyncedAt: &synced}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown chat: %v", err)
	}
}

// TestFullSyncProgress counts the three groups of the job.
func TestFullSyncProgress(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	fullSyncFixture(t, db)
	synced := db.now().Unix()
	failure := "CHANNEL_PRIVATE"
	if err := db.SaveChatFull(ctx, 1, ChatFull{SyncedAt: &synced}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveChatFull(ctx, 2, ChatFull{Error: &failure}); err != nil {
		t.Fatal(err)
	}
	got, err := db.FullSyncProgress(ctx, ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || got.Done != 1 || got.Failed != 1 {
		t.Fatalf("progress = %+v", got)
	}
	blocked, err := db.FullSyncProgress(ctx, ChatFilter{Block: []int64{3}})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Total != 2 {
		t.Fatalf("progress with a filter = %+v", blocked)
	}
}
