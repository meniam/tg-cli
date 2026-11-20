package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

func TestRawMigrationFromEight(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "store.db")
	files := append(append([]string{}, migrationsBeforeEight...), "0008_chats_state.sql")
	buildDatabaseAt(t, path, files, 8)
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	folder := rpc.Folder{ID: 1, Kind: "filter", Title: "folder", Pinned: []rpc.FolderPeer{{ID: 1}}, Include: []rpc.FolderPeer{}, Exclude: []rpc.FolderPeer{}}
	folderJSON, err := json.Marshal(folder)
	if err != nil {
		t.Fatal(err)
	}
	schemaExec(t, old, `INSERT INTO chats(id,type,title,raw,first_seen_at,last_seen_at) VALUES(1,'group','old','{"_":"chat","id":1}',1,1)`)
	schemaExec(t, old, `INSERT INTO people(id,first_name,is_contact,raw,first_seen_at,last_seen_at) VALUES(1,'A',1,'{"deleted":true}',1,1),(2,'B',1,'{"deleted":false}',1,1),(3,'C',0,'{"_":"user"}',1,1)`)
	schemaExec(t, old, `INSERT INTO folders(id,title,raw,first_seen_at,last_seen_at) VALUES(1,'folder',?,1,1)`, string(folderJSON))
	schemaExec(t, old, `INSERT INTO owner_snapshots(kind,synced_at) VALUES('folders',1)`)
	schemaExec(t, old, `INSERT INTO messages(chat_id,id,date,text,raw,first_seen_at,last_seen_at) VALUES(1,1,1,'text','{}',1,1)`)
	schemaExec(t, old, `INSERT INTO message_versions VALUES(1,1,1,'old',NULL,'{"v":1}',1),(1,1,2,'bad',NULL,'invalid',2)`)
	schemaExec(t, old, `INSERT INTO chat_versions(chat_id,version,type,title,observed_at) VALUES(1,1,'group','earlier',1)`)
	schemaExec(t, old, `INSERT INTO dialog_state(chat_id,data,synced_at) VALUES(1,'{"archived":true,"unread":3}',1)`)
	tables := []string{"meta", "chats", "chat_versions", "people", "messages", "message_versions", "folders", "folder_chats", "account_state", "sync_state", "notes", "note_tags", "events", "idempotency", "dialog_state", "account_sessions", "blocked_peers", "notify_defaults", "owner_snapshots", "transcriptions"}
	counts := map[string]int{}
	for _, table := range tables {
		counts[table] = schemaCount(t, old, "SELECT count(*) FROM "+table)
	}
	var beforeState string
	if err = old.QueryRow(`SELECT json_array(chat_id,type,title,username,created_at,muted,mute_until,archived,pinned,unread,unread_mentions,unread_mark,state_synced_at,messages,my_messages,first_message_id,first_message_date,first_message_text,last_message_id,last_message_date,last_message_text,history_complete) FROM chats_state WHERE chat_id=1`).Scan(&beforeState); err != nil {
		t.Fatal(err)
	}
	if err = old.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range tables {
		if got := schemaCount(t, db.read, "SELECT count(*) FROM "+table); got != counts[table] {
			t.Errorf("%s rows=%d, want %d", table, got, counts[table])
		}
	}
	for _, tc := range []struct {
		id      int
		deleted bool
		raw     string
	}{{1, true, ""}, {2, false, ""}, {3, false, `{"_":"user"}`}} {
		p, e := db.GetPerson(ctx, int64(tc.id))
		if e != nil || p.Deleted == nil || *p.Deleted != tc.deleted || string(p.Raw) != tc.raw {
			t.Errorf("person %d = %+v, %v", tc.id, p, e)
		}
	}
	people, _, err := db.ListContacts(ctx, 100, 0, ChatFilter{})
	if err != nil || len(people) != 2 || people[0].Deleted == nil || !*people[0].Deleted || people[1].Deleted == nil || *people[1].Deleted {
		t.Fatalf("contacts = %+v, %v", people, err)
	}
	// A partial update must not erase a known deleted flag.
	updatedName := "updated"
	if err := db.UpsertPeople(ctx, []Person{{ID: 1, FirstName: &updatedName}}); err != nil {
		t.Fatal(err)
	}
	updated, err := db.GetPerson(ctx, 1)
	if err != nil || updated.Deleted == nil || !*updated.Deleted {
		t.Fatalf("partial update lost deleted: %+v, %v", updated, err)
	}
	var data string
	var raw sql.NullString
	if err = db.read.QueryRow(`SELECT data,raw FROM folders WHERE id=1`).Scan(&data, &raw); err != nil || data != string(folderJSON) || raw.Valid {
		t.Fatalf("folder data=%s raw=%v err=%v", data, raw, err)
	}
	folder.Pinned[0].Title = "old"
	got, err := db.Folders(ctx, ChatFilter{})
	if err != nil || len(got.Folders) != 1 || !reflect.DeepEqual(got.Folders[0], folder) {
		t.Fatalf("folders = %+v, %v", got, err)
	}
	var afterState string
	if err = db.read.QueryRow(`SELECT json_array(chat_id,type,title,username,created_at,muted,mute_until,archived,pinned,unread,unread_mentions,unread_mark,state_synced_at,messages,my_messages,first_message_id,first_message_date,first_message_text,last_message_id,last_message_date,last_message_text,history_complete) FROM chats_state WHERE chat_id=1`).Scan(&afterState); err != nil || afterState != beforeState {
		t.Fatalf("chats_state before=%s after=%s err=%v", beforeState, afterState, err)
	}
	versions, err := db.MessageVersions(ctx, 1, 1)
	if err != nil || len(versions) != 2 || string(versions[0].Raw) != `{"v":1}` || versions[1].Raw != nil {
		t.Fatalf("message versions=%+v %v", versions, err)
	}
	schemaExec(t, db.write, `UPDATE chats SET raw='{"_":"new"}' WHERE id=1`)
	if n := schemaCount(t, db.read, `SELECT count(*) FROM chat_versions WHERE chat_id=1`); n != 1 {
		t.Fatalf("raw update created version: %d", n)
	}
	schemaExec(t, db.write, `UPDATE chats SET title='new',last_seen_at=2 WHERE id=1`)
	if err = db.read.QueryRow(`SELECT raw FROM chat_versions WHERE chat_id=1 AND version=2`).Scan(&data); err != nil || data != `{"_":"new"}` {
		t.Fatalf("version raw=%s err=%v", data, err)
	}
	version, err := db.GetChatVersion(ctx, 1, 2)
	if err != nil || string(version.Raw) != `{"_":"new"}` {
		t.Fatalf("stored chat version = %+v, %v", version, err)
	}
	changes, err := db.ChatVersions(ctx, 1, 10, ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(changes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response), `"_"`) || strings.Contains(string(response), `"raw"`) {
		t.Fatalf("raw leaked: %s", response)
	}
	var integrity string
	if err = db.read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity=%s err=%v", integrity, err)
	}
}
