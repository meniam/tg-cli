package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"tg-cli/internal/stem"
	"tg-cli/internal/textnorm"
)

// stemMatchIDs returns the message ids whose stemmed text matches the query.
func stemMatchIDs(t *testing.T, db *sql.DB, query string) []int {
	t.Helper()
	rows, err := db.Query(`SELECT m.id FROM messages_stem_fts JOIN messages m ON m.rowid=messages_stem_fts.rowid WHERE messages_stem_fts MATCH ? ORDER BY m.id`, stem.Query(query, false))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// databaseAtVersion creates a database file migrated only up to version, with
// the plain driver, as an older binary would have left it.
func databaseAtVersion(t *testing.T, version int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "store.db")
	migrations, err := listMigrations()
	if err != nil {
		t.Fatal(err)
	}
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	plain.SetMaxOpenConns(1)
	if _, err := plain.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations {
		if m.version > version {
			break
		}
		body, err := migrationFiles.ReadFile("migrations/" + m.name)
		if err != nil {
			t.Fatal(err)
		}
		schemaExec(t, plain, string(body))
	}
	schemaExec(t, plain, fmt.Sprintf("PRAGMA user_version=%d", version))
	return path
}

func TestMigration0011FromVersion10(t *testing.T) {
	path := databaseAtVersion(t, 10)
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	texts := map[int]string{
		1: "Доставка заказа задерживается",
		2: "Оплатил доставку вчера",
		3: "Счёт 4421 выставлен",
		4: "",
		5: "Running late, order 15 delivered",
	}
	for id, text := range texts {
		schemaExec(t, plain, `INSERT INTO messages(chat_id,id,date,text,raw,search_text,first_seen_at,last_seen_at) VALUES(1,?,?,?,'{}',?,1,1)`, id, id, text, textnorm.Normalize(text))
	}
	if err := plain.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if version, err := db.Version(); err != nil || version != 11 {
		t.Fatalf("version = %d, %v, want 11", version, err)
	}
	for id, text := range texts {
		var got string
		if err := db.read.QueryRow(`SELECT stem_text FROM messages WHERE chat_id=1 AND id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if want := stem.Text(text); got != want {
			t.Errorf("stem_text of message %d = %q, want %q", id, got, want)
		}
	}
	// Forms of the word that never appear in the text still find it.
	if got := stemMatchIDs(t, db.read, "доставки"); !equalInts(got, []int{1, 2}) {
		t.Errorf(`stem match "доставки" = %v, want [1 2]`, got)
	}
	if got := stemMatchIDs(t, db.read, "order"); !equalInts(got, []int{5}) {
		t.Errorf(`stem match "order" = %v, want [5]`, got)
	}
	if got := stemMatchIDs(t, db.read, "4421"); !equalInts(got, []int{3}) {
		t.Errorf(`stem match "4421" = %v, want [3]`, got)
	}
	var integrity string
	if err := db.read.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Errorf("integrity_check = %q, %v", integrity, err)
	}
	checkFTSIntegrity(t, db.write)
}

func TestStemIndexFollowsInsertEditAndDelete(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := db.UpsertMessages(ctx, []Message{
		{ChatID: 1, ID: 1, Date: 1, Text: "Доставка завтра", Raw: raw(`{}`)},
		{ChatID: 1, ID: 2, Date: 2, Text: "Счёт оплачен", Raw: raw(`{}`)},
	}); err != nil {
		t.Fatal(err)
	}
	if got := stemMatchIDs(t, db.read, "доставку"); !equalInts(got, []int{1}) {
		t.Fatalf("after insert: %v, want [1]", got)
	}
	checkFTSIntegrity(t, db.write)

	// Editing the text moves the message from one stem to another.
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 1, Date: 1, Text: "Оплата завтра", Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if got := stemMatchIDs(t, db.read, "доставки"); len(got) != 0 {
		t.Errorf("after edit the old stem still matches: %v", got)
	}
	if got := stemMatchIDs(t, db.read, "оплаты"); !equalInts(got, []int{1}) {
		t.Errorf("after edit: %v, want [1]", got)
	}
	var stored string
	if err := db.read.QueryRow(`SELECT stem_text FROM messages WHERE chat_id=1 AND id=1`).Scan(&stored); err != nil || stored != "оплат завтр" {
		t.Errorf("stem_text after edit = %q, %v", stored, err)
	}
	checkFTSIntegrity(t, db.write)

	// Updates that leave search_text alone do not touch the stem index.
	if _, err := db.MarkMessagesDeleted(ctx, 1, []int{2}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SetMessagePinned(ctx, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	checkFTSIntegrity(t, db.write)

	schemaExec(t, db.write, `DELETE FROM messages WHERE chat_id=1`)
	if got := stemMatchIDs(t, db.read, "оплаты"); len(got) != 0 {
		t.Errorf("after delete: %v, want none", got)
	}
	checkFTSIntegrity(t, db.write)
}

func TestStemIndexKeepsEmptyTextRowsConsistent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	// Media-only messages have empty search_text: they are indexed as empty
	// documents and must survive the edit and delete round trip.
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 1, Date: 1, Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	checkFTSIntegrity(t, db.write)
	if err := db.UpsertMessages(ctx, []Message{{ChatID: 1, ID: 1, Date: 1, Text: "Доставка", Raw: raw(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	if got := stemMatchIDs(t, db.read, "доставки"); !equalInts(got, []int{1}) {
		t.Errorf("after text appeared: %v, want [1]", got)
	}
	checkFTSIntegrity(t, db.write)
	schemaExec(t, db.write, `DELETE FROM messages`)
	checkFTSIntegrity(t, db.write)
}
