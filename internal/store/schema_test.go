package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/textnorm"
)

func schemaExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("SQL failed: %s: %v", query, err)
	}
}

func schemaReject(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err == nil {
		t.Errorf("SQL unexpectedly accepted: %s", query)
	}
}

func schemaCount(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count failed: %s: %v", query, err)
	}
	return count
}

func ftsCount(t *testing.T, db *sql.DB, table, query string) int {
	t.Helper()
	return schemaCount(t, db, fmt.Sprintf("SELECT count(*) FROM %s WHERE %s MATCH ?", table, table), textnorm.Query(query, false))
}

func checkFTSIntegrity(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"chats_fts", "people_fts", "messages_fts", "notes_fts"} {
		schemaExec(t, db, fmt.Sprintf("INSERT INTO %s(%s, rank) VALUES('integrity-check', 1)", table, table))
	}
}

func TestSchemaMigration(t *testing.T) {
	for _, existingV1 := range []bool{false, true} {
		name := "empty"
		if existingV1 {
			name = "version_1"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "store.db")
			if existingV1 {
				file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					t.Fatal(err)
				}
				file.Close()
				plain, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				body, err := migrationFiles.ReadFile("migrations/0001_init.sql")
				if err != nil {
					t.Fatal(err)
				}
				schemaExec(t, plain, string(body))
				schemaExec(t, plain, `INSERT INTO meta(key,value) VALUES('v1-marker','preserved')`)
				schemaExec(t, plain, `PRAGMA user_version=1`)
				if err := plain.Close(); err != nil {
					t.Fatal(err)
				}
			}
			db, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			if version, err := db.Version(); err != nil || version != newestMigrationVersion(t) {
				t.Fatalf("version = %d, %v, want %d", version, err, newestMigrationVersion(t))
			}
			for _, table := range []string{"meta", "chats", "people", "messages", "message_versions", "folders", "folder_chats", "account_state", "sync_state", "notes", "note_tags", "events", "dialog_state", "account_sessions", "blocked_peers", "notify_defaults", "owner_snapshots"} {
				var strict int
				if err := db.read.QueryRow(`SELECT "strict" FROM pragma_table_list WHERE name=?`, table).Scan(&strict); err != nil || strict != 1 {
					t.Errorf("table %s strict = %d, %v", table, strict, err)
				}
			}
			for _, table := range []string{"folders", "folder_chats", "account_state", "sync_state"} {
				if count := schemaCount(t, db.read, "SELECT count(*) FROM "+table); count != 0 {
					t.Errorf("table %s has %d rows, want empty", table, count)
				}
			}
			if existingV1 && schemaCount(t, db.read, `SELECT count(*) FROM meta WHERE key='v1-marker' AND value='preserved'`) != 1 {
				t.Error("version 1 data was lost")
			}
			schemaExec(t, db.write, `INSERT INTO notes(id,kind,chat_id,body,author,created_at,updated_at) VALUES(1,'chat',77,'marker','owner',1,1)`)
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatalf("reopen reran migration or lost schema: %v", err)
			}
			defer reopened.Close()
			if version, err := reopened.Version(); err != nil || version != newestMigrationVersion(t) {
				t.Errorf("reopened version = %d, %v", version, err)
			}
			if count := schemaCount(t, reopened.read, `SELECT count(*) FROM notes WHERE id=1 AND body='marker'`); count != 1 {
				t.Errorf("reopened note count = %d", count)
			}
			var integrity string
			if err := reopened.read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
				t.Errorf("integrity_check = %q, %v", integrity, err)
			}
			if count := schemaCount(t, reopened.read, `SELECT count(*) FROM pragma_foreign_key_check`); count != 0 {
				t.Errorf("foreign_key_check returned %d rows", count)
			}
			checkFTSIntegrity(t, reopened.write)
		})
	}
}

func TestFTSSyncAndNormalization(t *testing.T) {
	db := openTestDB(t)
	search := textnorm.Normalize("Ёлка Привет")
	schemaExec(t, db.write, `INSERT INTO chats(id,type,title,search_text,first_seen_at,last_seen_at) VALUES(1,'group','Ёлка',?,1,1)`, search)
	schemaExec(t, db.write, `INSERT INTO people(id,first_name,search_text,first_seen_at,last_seen_at) VALUES(2,'Ёлка',?,1,1)`, search)
	schemaExec(t, db.write, `INSERT INTO messages(chat_id,id,date,raw,search_text,first_seen_at,last_seen_at) VALUES(1,3,1,'{}',?,1,1)`, search)
	schemaExec(t, db.write, `INSERT INTO notes(id,kind,chat_id,body,author,search_text,created_at,updated_at) VALUES(4,'chat',1,'Ёлка','owner',?,1,1)`, search)
	checkFTSIntegrity(t, db.write)
	for _, table := range []string{"chats_fts", "people_fts", "messages_fts", "notes_fts"} {
		if count := ftsCount(t, db.read, table, "ЕЛКА"); count != 1 {
			t.Errorf("%s Cyrillic match = %d, want 1", table, count)
		}
	}
	for _, table := range []string{"chats", "people", "messages", "notes"} {
		var definition string
		if err := db.read.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name=?`, table+"_au").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(definition, "AFTER UPDATE OF search_text ON "+table) {
			t.Errorf("%s update trigger is not limited to search_text: %s", table, definition)
		}
	}
	for _, query := range []string{
		`UPDATE chats SET last_seen_at=2, unread=1 WHERE id=1`,
		`UPDATE people SET last_seen_at=2, first_name='Renamed' WHERE id=2`,
		`UPDATE messages SET last_seen_at=2, pinned=1 WHERE chat_id=1 AND id=3`,
		`UPDATE notes SET updated_at=2, body='Changed' WHERE id=4`,
	} {
		schemaExec(t, db.write, query)
	}
	checkFTSIntegrity(t, db.write)
	for _, table := range []string{"chats_fts", "people_fts", "messages_fts", "notes_fts"} {
		if count := ftsCount(t, db.read, table, "ёлка"); count != 1 {
			t.Errorf("%s after non-search update: %d matches, want 1", table, count)
		}
	}
	updated := textnorm.Normalize("Берёза")
	for _, table := range []string{"chats", "people", "messages", "notes"} {
		schemaExec(t, db.write, "UPDATE "+table+" SET search_text=?", updated)
	}
	checkFTSIntegrity(t, db.write)
	for _, table := range []string{"chats_fts", "people_fts", "messages_fts", "notes_fts"} {
		if old, new := ftsCount(t, db.read, table, "ёлка"), ftsCount(t, db.read, table, "БЕРЕЗА"); old != 0 || new != 1 {
			t.Errorf("%s after update: old=%d new=%d", table, old, new)
		}
	}
	for _, table := range []string{"chats", "people", "messages", "notes"} {
		schemaExec(t, db.write, "DELETE FROM "+table)
	}
	checkFTSIntegrity(t, db.write)
	for _, table := range []string{"chats_fts", "people_fts", "messages_fts", "notes_fts"} {
		if count := ftsCount(t, db.read, table, "береза"); count != 0 {
			t.Errorf("%s after delete: %d matches", table, count)
		}
	}
}

func TestSchemaConstraints(t *testing.T) {
	db := openTestDB(t)
	valid := []struct{ name, query string }{
		{"chat type", `INSERT INTO chats(id,type,first_seen_at,last_seen_at,raw,settings) VALUES(1,'group',1,1,'{}','{}')`},
		{"person raw", `INSERT INTO people(id,raw,first_seen_at,last_seen_at) VALUES(1,'{}',1,1)`},
		{"message raw", `INSERT INTO messages(chat_id,id,date,raw,first_seen_at,last_seen_at) VALUES(1,1,1,'{}',1,1)`},
		{"folder raw", `INSERT INTO folders(id,title,raw,first_seen_at,last_seen_at) VALUES(1,'main','{}',1,1)`},
		{"account value", `INSERT INTO account_state(key,value,updated_at) VALUES('theme','{}',1)`},
		{"chat note", `INSERT INTO notes(id,kind,chat_id,body,author,created_at,updated_at) VALUES(1,'chat',1,'a','owner',1,1)`},
		{"person note", `INSERT INTO notes(id,kind,target_id,body,author,created_at,updated_at) VALUES(2,'person',1,'b','owner',1,1)`},
		{"message note", `INSERT INTO notes(id,kind,chat_id,target_id,body,author,created_at,updated_at) VALUES(3,'message',1,1,'c','owner',1,1)`},
		{"ai model", `INSERT INTO notes(id,kind,chat_id,body,author,model,created_at,updated_at) VALUES(4,'chat',1,'d','ai','model-x',1,1)`},
	}
	for _, tc := range valid {
		t.Run("accept_"+strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) { schemaExec(t, db.write, tc.query) })
	}
	rejected := []struct{ name, query string }{
		{"chat type", `INSERT INTO chats(id,type,first_seen_at,last_seen_at) VALUES(2,'unknown',1,1)`},
		{"chat raw JSON", `INSERT INTO chats(id,type,first_seen_at,last_seen_at,raw) VALUES(2,'group',1,1,'bad')`},
		{"chat settings JSON", `INSERT INTO chats(id,type,first_seen_at,last_seen_at,settings) VALUES(2,'group',1,1,'bad')`},
		{"person raw JSON", `INSERT INTO people(id,raw,first_seen_at,last_seen_at) VALUES(2,'bad',1,1)`},
		{"message raw JSON", `INSERT INTO messages(chat_id,id,date,raw,first_seen_at,last_seen_at) VALUES(1,2,1,'bad',1,1)`},
		{"folder raw JSON", `INSERT INTO folders(id,title,raw,first_seen_at,last_seen_at) VALUES(2,'main','bad',1,1)`},
		{"person missing first_seen_at", `INSERT INTO people(id,last_seen_at) VALUES(2,1)`},
		{"person missing last_seen_at", `INSERT INTO people(id,first_seen_at) VALUES(2,1)`},
		{"message missing first_seen_at", `INSERT INTO messages(chat_id,id,date,raw,last_seen_at) VALUES(1,2,1,'{}',1)`},
		{"message missing last_seen_at", `INSERT INTO messages(chat_id,id,date,raw,first_seen_at) VALUES(1,2,1,'{}',1)`},
		{"folder missing first_seen_at", `INSERT INTO folders(id,title,last_seen_at) VALUES(2,'main',1)`},
		{"folder missing last_seen_at", `INSERT INTO folders(id,title,first_seen_at) VALUES(2,'main',1)`},
		{"account value JSON", `INSERT INTO account_state(key,value,updated_at) VALUES('bad','bad',1)`},
		{"note kind", `INSERT INTO notes(id,kind,chat_id,body,author,created_at,updated_at) VALUES(5,'folder',1,'x','owner',1,1)`},
		{"note author", `INSERT INTO notes(id,kind,chat_id,body,author,created_at,updated_at) VALUES(5,'chat',1,'x','stranger',1,1)`},
		{"chat target missing", `INSERT INTO notes(id,kind,body,author,created_at,updated_at) VALUES(5,'chat','x','owner',1,1)`},
		{"chat target extra", `INSERT INTO notes(id,kind,chat_id,target_id,body,author,created_at,updated_at) VALUES(5,'chat',1,1,'x','owner',1,1)`},
		{"person target missing", `INSERT INTO notes(id,kind,body,author,created_at,updated_at) VALUES(5,'person','x','owner',1,1)`},
		{"person target extra", `INSERT INTO notes(id,kind,chat_id,target_id,body,author,created_at,updated_at) VALUES(5,'person',1,1,'x','owner',1,1)`},
		{"message chat missing", `INSERT INTO notes(id,kind,target_id,body,author,created_at,updated_at) VALUES(5,'message',1,'x','owner',1,1)`},
		{"message id missing", `INSERT INTO notes(id,kind,chat_id,body,author,created_at,updated_at) VALUES(5,'message',1,'x','owner',1,1)`},
		{"owner model", `INSERT INTO notes(id,kind,chat_id,body,author,model,created_at,updated_at) VALUES(5,'chat',1,'x','owner','model-x',1,1)`},
		{"STRICT text", `INSERT INTO notes(id,kind,chat_id,body,author,created_at,updated_at) VALUES(5,'chat',1,x'00','owner',1,1)`},
		{"duplicate message", `INSERT INTO messages(chat_id,id,date,raw,first_seen_at,last_seen_at) VALUES(1,1,2,'{}',1,1)`},
	}
	for _, tc := range rejected {
		t.Run("reject_"+strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) { schemaReject(t, db.write, tc.query) })
	}
}

func TestNotesSurviveTelegramChanges(t *testing.T) {
	db := openTestDB(t)
	schemaExec(t, db.write, `INSERT INTO chats(id,type,first_seen_at,last_seen_at) VALUES(1,'group',1,1)`)
	schemaExec(t, db.write, `INSERT INTO people(id,first_seen_at,last_seen_at) VALUES(2,1,1)`)
	schemaExec(t, db.write, `INSERT INTO messages(chat_id,id,date,raw,first_seen_at,last_seen_at) VALUES(1,3,1,'{}',1,1)`)
	for _, tc := range []struct {
		id, target int
		kind       string
		chat       any
	}{
		{11, 0, "chat", 1},
		{12, 2, "person", nil},
		{13, 3, "message", 1},
	} {
		var target any
		if tc.kind != "chat" {
			target = tc.target
		}
		schemaExec(t, db.write, `INSERT INTO notes(id,kind,chat_id,target_id,body,author,search_text,created_at,updated_at) VALUES(?,?,?,?,?,'owner',?,1,1)`, tc.id, tc.kind, tc.chat, target, "Keeps Ёлка", textnorm.Normalize("Keeps Ёлка"))
		schemaExec(t, db.write, `INSERT INTO note_tags(note_id,tag) VALUES(?,'important')`, tc.id)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM pragma_foreign_key_list('notes')`); count != 0 {
		t.Fatalf("notes has %d foreign keys to Telegram data", count)
	}
	for _, query := range []string{
		`DELETE FROM messages WHERE chat_id=1 AND id=3`,
		`DELETE FROM people WHERE id=2`,
		`DELETE FROM chats WHERE id=1`,
	} {
		schemaExec(t, db.write, query)
	}
	assertNotesUnchanged(t, db)
	schemaExec(t, db.write, `INSERT INTO chats(id,type,first_seen_at,last_seen_at) VALUES(1,'group',2,2)`)
	schemaExec(t, db.write, `INSERT INTO people(id,first_seen_at,last_seen_at) VALUES(2,2,2)`)
	schemaExec(t, db.write, `INSERT INTO messages(chat_id,id,date,raw,first_seen_at,last_seen_at) VALUES(1,3,2,'{}',2,2)`)
	for _, query := range []string{
		`INSERT INTO chats(id,type,first_seen_at,last_seen_at) VALUES(1,'group',3,3) ON CONFLICT(id) DO UPDATE SET last_seen_at=excluded.last_seen_at`,
		`INSERT INTO people(id,first_name,first_seen_at,last_seen_at) VALUES(2,'updated',3,3) ON CONFLICT(id) DO UPDATE SET first_name=excluded.first_name`,
		`INSERT INTO messages(chat_id,id,date,raw,first_seen_at,last_seen_at) VALUES(1,3,3,'{}',3,3) ON CONFLICT(chat_id,id) DO UPDATE SET date=excluded.date`,
	} {
		schemaExec(t, db.write, query)
	}
	assertNotesUnchanged(t, db)
	schemaExec(t, db.write, `DELETE FROM notes WHERE id=11`)
	if count := schemaCount(t, db.read, `SELECT count(*) FROM note_tags WHERE note_id=11`); count != 0 {
		t.Errorf("deleted note retained %d tags", count)
	}
	if count := ftsCount(t, db.read, "notes_fts", "елка"); count != 2 {
		t.Errorf("notes FTS after deletion = %d, want 2", count)
	}
	checkFTSIntegrity(t, db.write)
}

func assertNotesUnchanged(t *testing.T, db *DB) {
	t.Helper()
	if count := schemaCount(t, db.read, `SELECT count(*) FROM notes WHERE id IN (11,12,13) AND body='Keeps Ёлка'`); count != 3 {
		t.Errorf("notes retained = %d, want 3", count)
	}
	if count := schemaCount(t, db.read, `SELECT count(*) FROM note_tags WHERE note_id IN (11,12,13) AND tag='important'`); count != 3 {
		t.Errorf("tags retained = %d, want 3", count)
	}
	if count := ftsCount(t, db.read, "notes_fts", "ЕЛКА"); count != 3 {
		t.Errorf("notes FTS retained = %d, want 3", count)
	}
	checkFTSIntegrity(t, db.write)
}
