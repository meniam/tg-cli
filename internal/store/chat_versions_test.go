package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestChatVersionsObserveMeaningfulChanges(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	c := Chat{ID: 42, Type: "group", Title: "old"}
	if err := db.UpsertChats(ctx, []Chat{c}); err != nil {
		t.Fatal(err)
	}
	c.About = stringPtr("first")
	if err := db.UpsertChats(ctx, []Chat{c}); err != nil {
		t.Fatal(err)
	}
	c.Title = "new"
	if err := db.UpsertChats(ctx, []Chat{c}); err != nil {
		t.Fatal(err)
	}
	c.Members = intPtr(5)
	if err := db.UpsertChats(ctx, []Chat{c}); err != nil {
		t.Fatal(err)
	}
	c.Settings = json.RawMessage(`{"slow":1}`)
	if err := db.UpsertChats(ctx, []Chat{c}); err != nil {
		t.Fatal(err)
	}
	c.Settings = json.RawMessage(`{"slow":2}`)
	if err := db.UpsertChats(ctx, []Chat{c}); err != nil {
		t.Fatal(err)
	}
	got, err := db.ChatVersions(ctx, 42, 20, ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Versions) != 2 || got.Versions[0].Version != 2 || got.Versions[1].Title != "old" || string(got.Versions[0].Settings) != `{"slow":1}` {
		t.Fatalf("unexpected versions: %+v", got.Versions)
	}
	if _, err = db.ChatVersions(ctx, 42, 20, ChatFilter{Hide: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("closed chat: %v", err)
	}
}

func TestChatVersionsMigrationFromFive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	buildDatabaseAt(t, path, []string{"0001_init.sql", "0002_schema.sql", "0003_events.sql", "0004_idempotency.sql", "0005_folders_account_state.sql"}, 5)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = raw.Exec(`INSERT INTO chats(id,type,title,first_seen_at,last_seen_at) VALUES(42,'group','preserved',1,1)`); err != nil {
		t.Fatal(err)
	}
	if err = raw.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	chat, err := db.GetChat(context.Background(), 42)
	if err != nil || chat.Title != "preserved" {
		t.Fatalf("chat %+v err %v", chat, err)
	}
	result, err := db.ChatVersions(context.Background(), 42, 20, ChatFilter{})
	if err != nil || len(result.Versions) != 0 {
		t.Fatalf("versions %+v err %v", result, err)
	}
}
