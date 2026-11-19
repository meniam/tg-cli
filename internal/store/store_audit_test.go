package store

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestAudit08_OpenRestoresPrivateModeOfExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = first.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode of reopened database = %o, want 600", got)
	}
}

// audit08NonStrictTables lists ordinary tables that migrations created without
// STRICT. Each entry needs a reason; the test fails when the exception goes stale.
var audit08NonStrictTables = map[string]string{
	"idempotency": "migrations/0004_idempotency.sql closes the definition without STRICT",
}

func TestAudit08_EveryOrdinaryTableIsStrict(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.read.Query(`SELECT name,"strict" FROM pragma_table_list WHERE schema='main' AND type='table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var name string
		var strict int
		if err = rows.Scan(&name, &strict); err != nil {
			t.Fatal(err)
		}
		seen[name] = true
		reason, excepted := audit08NonStrictTables[name]
		switch {
		case strict != 1 && !excepted:
			t.Errorf("table %s is not STRICT and has no recorded exception", name)
		case strict == 1 && excepted:
			t.Errorf("table %s is STRICT now; drop its exception (%s)", name, reason)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, want := range []string{"meta", "messages", "idempotency", "transcriptions", "owner_snapshots"} {
		if !seen[want] {
			t.Errorf("table %s missing from pragma_table_list, got %v", want, names)
		}
	}
	for name := range audit08NonStrictTables {
		if !seen[name] {
			t.Errorf("exception for unknown table %s", name)
		}
	}
}
