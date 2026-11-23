package store

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
)

// The migration names the authors of stored messages from the people table and
// leaves alone everything it cannot name.
func TestSenderNamesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var migrations []string
	for _, e := range entries {
		if e.Name() < "0015" {
			migrations = append(migrations, e.Name())
		}
	}
	buildDatabaseAt(t, path, migrations, 14)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	schemaExec(t, raw, `INSERT INTO people(id,username,first_name,last_name,search_text,first_seen_at,last_seen_at) VALUES
		(1,'full','Ann','Lee','',1,1), (2,'only_user',NULL,NULL,'',1,1), (3,'nameless','  ',NULL,'',1,1), (4,NULL,NULL,NULL,'',1,1)`)
	const insert = `INSERT INTO messages(chat_id,id,date,sender_id,sender_name,raw,first_seen_at,last_seen_at) VALUES(-9,%s,1,%s,%s,'{}',1,1)`
	for _, row := range []struct{ id, sender, name string }{
		{"1", "1", "NULL"},        // named from first and last name
		{"2", "2", "NULL"},        // named by username
		{"3", "3", "''"},          // empty name, username of a blank-name person
		{"4", "4", "NULL"},        // person without any name: stays unnamed
		{"5", "99", "NULL"},       // person not in the table: stays unnamed
		{"6", "1", "'Kept Name'"}, // an existing name is never replaced
		{"7", "NULL", "NULL"},     // no author at all
		{"8", "-100", "NULL"},     // a channel or a group is not looked up
	} {
		schemaExec(t, raw, fmt.Sprintf(insert, row.id, row.sender, row.name))
	}
	raw.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	want := map[int]string{1: "Ann Lee", 2: "only_user", 3: "nameless", 4: "", 5: "", 6: "Kept Name", 7: "", 8: ""}
	for id, name := range want {
		var got sql.NullString
		if err := db.read.QueryRow(`SELECT sender_name FROM messages WHERE chat_id=-9 AND id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got.String != name {
			t.Errorf("message %d: sender_name = %q, want %q", id, got.String, name)
		}
	}
}
