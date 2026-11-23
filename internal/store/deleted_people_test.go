package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// The migration takes the deleted flag from the object Telegram sent and marks
// the stored messages of those people; a living or unknown person is left alone.
func TestDeletedPeopleMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var migrations []string
	for _, e := range entries {
		if e.Name() < "0017" {
			migrations = append(migrations, e.Name())
		}
	}
	buildDatabaseAt(t, path, migrations, 16)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schemaExec(t, raw, `INSERT INTO people(id,username,first_name,raw,search_text,first_seen_at,last_seen_at) VALUES
		(1,NULL,NULL,'{"messages.getDialogs":{"_":"user","deleted":true,"id":1}}','',1,1),
		(2,NULL,NULL,'{"_":"user","deleted":true,"id":2}','',1,1),
		(3,'alive','Ann','{"messages.getDialogs":{"_":"user","id":3}}','',1,1),
		(4,NULL,NULL,NULL,'',1,1),
		(5,'late','Late','{"users.getUsers":{"_":"user","deleted":false,"id":5}}','',1,1)`)
	const insert = `INSERT INTO messages(chat_id,id,date,sender_id,sender_name,raw,first_seen_at,last_seen_at) VALUES(-9,%d,1,%d,NULL,'{}',1,1)`
	for id := 1; id <= 5; id++ {
		schemaExec(t, raw, fmt.Sprintf(insert, id, id))
	}
	raw.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Person 3 got a name after migration 0015: the older message is named now.
	want := map[int]struct {
		deleted bool
		name    string
	}{1: {true, ""}, 2: {true, ""}, 3: {false, "Ann"}, 4: {false, ""}, 5: {false, "Late"}}
	for id, w := range want {
		var deleted bool
		var name sql.NullString
		if err := db.read.QueryRow(`SELECT sender_deleted, sender_name FROM messages WHERE chat_id=-9 AND id=?`, id).Scan(&deleted, &name); err != nil {
			t.Fatal(err)
		}
		if deleted != w.deleted || name.String != w.name {
			t.Errorf("message %d: deleted=%v name=%q, want deleted=%v name=%q", id, deleted, name.String, w.deleted, w.name)
		}
	}
}

// A person whose stored object says the account is deleted is marked as such by
// any path that saves people, with all their stored messages.
func TestUpsertPeopleReadsDeletedFromRaw(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := db.UpsertMessages(ctx, []Message{historyMessage(1, "old")}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.ExecContext(ctx, `UPDATE messages SET sender_id=77 WHERE chat_id=42`); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"_":"user","id":77}`, `{"messages.getDialogs":{"_":"user","deleted":true,"id":77}}`} {
		if err := db.UpsertPeople(ctx, []Person{{ID: 77, Raw: []byte(raw)}}); err != nil {
			t.Fatal(err)
		}
		deleted := schemaCount(t, db.read, `SELECT COUNT(*) FROM messages WHERE chat_id=42 AND sender_deleted=1`)
		person, err := db.GetPerson(ctx, 77)
		if err != nil {
			t.Fatal(err)
		}
		isDeleted := person.Deleted != nil && *person.Deleted
		if wantDeleted := raw != `{"_":"user","id":77}`; isDeleted != wantDeleted || (deleted == 1) != wantDeleted {
			t.Errorf("raw %s: person deleted=%v, marked messages=%d", raw, isDeleted, deleted)
		}
	}
}
