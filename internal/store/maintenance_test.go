package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckpointFoldsTheJournal writes through the database, then folds the
// write-ahead journal into the main file: the checkpoint of a move must not
// leave history behind in a journal file.
func TestCheckpointFoldsTheJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetAccountState(context.Background(), "checkpoint.test", []byte(`{"value":1}`)); err != nil {
		t.Fatal(err)
	}
	journal := path + "-wal"
	if info, err := os.Stat(journal); err == nil && info.Size() == 0 {
		t.Fatalf("the journal is empty before the checkpoint: the test proves nothing")
	}
	busy, err := Checkpoint(path)
	if err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	if busy {
		t.Fatalf("the checkpoint reports a busy journal")
	}
	if info, err := os.Stat(journal); err == nil && info.Size() != 0 {
		t.Fatalf("the journal still holds %d bytes", info.Size())
	}
	raw, found, err := db.GetAccountState(context.Background(), "checkpoint.test")
	if err != nil || !found || string(raw) != `{"value":1}` {
		t.Fatalf("the row did not survive the checkpoint: %q %v %v", raw, found, err)
	}
}

// TestCheckpointRefusesAMissingFile keeps a wrong path from looking like a
// finished checkpoint.
func TestCheckpointRefusesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Checkpoint(filepath.Join(dir, "missing.db")); err == nil {
		t.Fatal("a missing database must be an error")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.db")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the checkpoint created the database: %v", err)
	}
}

// TestIntegrityCheckReportsOk covers the healthy file and the damaged one: a
// copy that lost a page must not pass as a database.
func TestIntegrityCheckReportsOk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAccountState(context.Background(), "integrity.test", []byte(`{"value":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ok, detail, err := IntegrityCheck(path)
	if err != nil {
		t.Fatalf("integrity check: %v", err)
	}
	if !ok || detail != "ok" {
		t.Fatalf("a healthy database: ok=%v detail=%q", ok, detail)
	}
	// Damage one page in the middle of the file: the engine reads it and
	// reports the damage instead of the file size.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < 4096*4 {
		t.Fatalf("the database is too small to damage: %d", info.Size())
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	page := make([]byte, 4096)
	for i := range page {
		page[i] = 0xff
	}
	if _, err := f.WriteAt(page, 4096*2); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	ok, detail, err = IntegrityCheck(path)
	if err != nil {
		t.Fatalf("integrity check of a damaged file: %v", err)
	}
	if ok || detail == "" || detail == "ok" {
		t.Fatalf("a damaged database passed: ok=%v detail=%q", ok, detail)
	}
	if strings.Contains(detail, t.TempDir()) {
		t.Errorf("the detail must not depend on the temporary path: %q", detail)
	}
}
