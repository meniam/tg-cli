package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/store"
)

// newDatabase creates a real database at path with one row, so a moved file can
// be read back and compared. The row lives in the journal: closing the last
// connection folds the journal into the main file and deletes it, so the files
// are copied while the staging database is still open and the staging database
// is closed afterwards. path and path-wal stay on the disk with no open handle,
// the way a daemon that was killed leaves them, and the move has to fold the
// journal into the main file.
func newDatabase(t *testing.T, path string) {
	t.Helper()
	staging := filepath.Join(t.TempDir(), "staging.db")
	db, err := store.Open(staging)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetAccountState(context.Background(), stateKey, json.RawMessage(`{"value":1}`)); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(staging + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the test needs a journal with data: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal"} {
		raw, err := os.ReadFile(staging + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+suffix, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatalf("the copied fixture lost its journal: %v", err)
	}
}

// audit04CheckpointFixture folds the journal of a fixture database into its main file,
// for the tests that copy the main file alone.
func audit04CheckpointFixture(t *testing.T, path string) {
	t.Helper()
	busy, err := store.Checkpoint(path)
	if err != nil || busy {
		t.Fatalf("checkpoint of the fixture: busy=%v err=%v", busy, err)
	}
}

// audit04JournalSize returns the size of path-wal, zero when it does not exist.
func audit04JournalSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path + "-wal")
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

// audit04WantMoveStep fails unless err is a *MoveError that stopped at step.
func audit04WantMoveStep(t *testing.T, err error, step string) {
	t.Helper()
	var moveErr *MoveError
	if !errors.As(err, &moveErr) {
		t.Fatalf("want a *MoveError, got %v", err)
	}
	if moveErr.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", moveErr.ExitCode())
	}
	if moveErr.Step != step {
		t.Errorf("step = %q, want %q (error: %v)", moveErr.Step, step, err)
	}
}

const stateKey = "move.test"

// moveFixture returns the paths of a profile whose database lives in the old
// layout, plus that file.
func moveFixture(t *testing.T) (Paths, string) {
	t.Helper()
	p := guardFixture(t)
	newDatabase(t, p.LegacyDatabase)
	if _, err := os.Stat(p.Database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the new database exists before the move: %v", err)
	}
	return p, p.LegacyDatabase
}

// readState reads the row of the fixture database from a file.
func readState(t *testing.T, path string) string {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	raw, found, err := db.GetAccountState(context.Background(), stateKey)
	if err != nil || !found {
		t.Fatalf("the moved row is missing: found=%v err=%v", found, err)
	}
	return string(raw)
}

// holdWriteLock opens a write transaction on the database, which keeps the
// checkpoint from finishing. The returned function rolls the transaction back.
func holdWriteLock(t *testing.T, path string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(500)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO account_state(key,value,updated_at) VALUES('move.held','{}',0)`); err != nil {
		tx.Rollback()
		db.Close()
		t.Fatal(err)
	}
	return func() {
		_ = tx.Rollback()
		_ = db.Close()
	}
}

// checkGone fails when path exists.
func checkGone(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s must be gone: %v", what, err)
	}
}

// TestMoveLegacyDatabaseCopiesAndCommits covers steps 1 to 7 for a database with
// data in its journal: the copy is verified, the rename happens, the old file and
// its journals are gone and the data, which the main file did not hold, is
// readable in the new place.
func TestMoveLegacyDatabaseCopiesAndCommits(t *testing.T) {
	p, old := moveFixture(t)
	before, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	if audit04JournalSize(t, old) == 0 {
		t.Fatal("the fixture must hand the move a journal with data")
	}
	moved, err := MoveLegacyDatabase(p)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if !moved {
		t.Fatal("the move reports nothing done")
	}
	checkGone(t, old, "the old database")
	checkGone(t, old+"-wal", "the old journal")
	checkGone(t, old+"-shm", "the old shared memory file")
	info, err := os.Stat(p.Database)
	if err != nil {
		t.Fatal(err)
	}
	// The journal was folded into the main file, which can only grow.
	if info.Size() < before.Size() {
		t.Errorf("the new database is %d bytes, the old main file was %d", info.Size(), before.Size())
	}
	checkGone(t, p.Database+"-wal", "the journal of the new database")
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the new database: mode %o, want 0600", info.Mode().Perm())
	}
	if raw := readState(t, p.Database); raw != `{"value":1}` {
		t.Errorf("the moved row = %s", raw)
	}
	for _, dir := range []string{filepath.Dir(p.DatabaseDir), p.DatabaseDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s: mode %o, want 0700", dir, got)
		}
	}
	temps, err := filepath.Glob(filepath.Join(p.DatabaseDir, ".*.move-*"))
	if err != nil || len(temps) != 0 {
		t.Errorf("temporary copies left: %v %v", temps, err)
	}
}

// TestMoveLegacyDatabaseSecondCallDoesNothing keeps a repeated call from touching
// a database that has already moved.
func TestMoveLegacyDatabaseSecondCallDoesNothing(t *testing.T) {
	p, _ := moveFixture(t)
	if _, err := MoveLegacyDatabase(p); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p.Database)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := MoveLegacyDatabase(p)
	if err != nil {
		t.Fatalf("second move: %v", err)
	}
	if moved {
		t.Error("the second move reports work")
	}
	again, err := os.Stat(p.Database)
	if err != nil {
		t.Fatal(err)
	}
	if !again.ModTime().Equal(info.ModTime()) || again.Size() != info.Size() {
		t.Error("the second move changed the database")
	}
}

// TestMoveLegacyDatabaseKeepsTheOldFileOnFailure covers a failure before the
// copy: the database directory is a regular file, so the first step cannot read
// it. The old database stays as it was and the move reports code 1 at the step
// that inspects the database directory.
func TestMoveLegacyDatabaseKeepsTheOldFileOnFailure(t *testing.T) {
	p, old := moveFixture(t)
	original, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	// A regular file on the path of the directory makes every step impossible.
	if err := os.RemoveAll(p.DatabaseDir); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p.DatabaseDir, "not a directory")
	_, err = MoveLegacyDatabase(p)
	audit04WantMoveStep(t, err, "inspect the database directory")
	current, readErr := os.ReadFile(old)
	if readErr != nil || string(current) != string(original) {
		t.Fatalf("the old database changed: %v", readErr)
	}
}

// TestMoveLegacyDatabaseRefusesADamagedSource covers a verification failure of the
// real path: the source itself fails the integrity check of the engine. The copy
// is removed, the old database stays byte for byte and the move reports code 1.
func TestMoveLegacyDatabaseRefusesADamagedSource(t *testing.T) {
	p, old := moveFixture(t)
	// The pages are damaged in the main file, so the journal goes into it first:
	// a checkpoint in the move would overwrite the damage with the journal pages.
	audit04CheckpointFixture(t, old)
	info, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(old, os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	damage := make([]byte, 4096)
	for i := range damage {
		damage[i] = 0xff
	}
	if _, err := f.WriteAt(damage, (info.Size()/4096/2)*4096); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	_, err = MoveLegacyDatabase(p)
	audit04WantMoveStep(t, err, "the integrity check of the copy")
	after, readErr := os.ReadFile(old)
	if readErr != nil || !bytes.Equal(after, before) {
		t.Fatalf("the old database changed: %v", readErr)
	}
	checkGone(t, p.Database, "the new database")
	temps, err := filepath.Glob(filepath.Join(p.DatabaseDir, ".*.move-*"))
	if err != nil || len(temps) != 0 {
		t.Errorf("temporary copies left: %v %v", temps, err)
	}
}

// TestVerifyCopyRejectsADamagedCopy checks the guards that stand between a copy
// and the removal of the old database: a copy of the same size but other bytes,
// a truncated copy and a copy with a journal next to it all fail and name the
// check. The old database is only read here, never removed.
func TestVerifyCopyRejectsADamagedCopy(t *testing.T) {
	p, old := moveFixture(t)
	before, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(t.TempDir(), "copy.db")
	for _, tc := range []struct {
		name    string
		write   func()
		want    string
		cleanup func()
	}{
		{name: "same size, other bytes", want: "checksum", write: func() {
			damaged := append([]byte(nil), raw...)
			damaged[len(damaged)/2] ^= 0xff
			writeFile(t, temp, string(damaged))
		}},
		{name: "truncated", want: "size", write: func() {
			writeFile(t, temp, string(raw[:len(raw)/2]))
		}},
		{name: "journal next to the copy", want: "journal", write: func() {
			writeFile(t, temp, string(raw))
			writeFile(t, temp+"-wal", "pending")
		}, cleanup: func() {
			removeFile(temp + "-wal")
			removeFile(temp + "-shm")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.write()
			if tc.cleanup != nil {
				defer tc.cleanup()
			}
			err := verifyCopy(p, temp, before)
			var moveErr *MoveError
			if !errors.As(err, &moveErr) || moveErr.ExitCode() != 1 || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, err := os.Stat(old); err != nil {
				t.Fatalf("the old database was touched: %v", err)
			}
		})
	}
}

// TestMoveLegacyDatabaseRemovesLeftoverCopies covers step 1: leftovers of an
// earlier interrupted run are deleted and the move runs again.
func TestMoveLegacyDatabaseRemovesLeftoverCopies(t *testing.T) {
	p, old := moveFixture(t)
	leftover := filepath.Join(p.DatabaseDir, "."+filepath.Base(p.Database)+".move-999.tmp")
	for _, path := range []string{leftover, leftover + "-wal", leftover + "-shm"} {
		writeFile(t, path, databaseContents)
	}
	if _, err := MoveLegacyDatabase(p); err != nil {
		t.Fatalf("move: %v", err)
	}
	for _, path := range []string{leftover, leftover + "-wal", leftover + "-shm"} {
		checkGone(t, path, "the leftover copy")
	}
	checkGone(t, old, "the old database")
	if raw := readState(t, p.Database); raw != `{"value":1}` {
		t.Errorf("the moved row = %s", raw)
	}
}

// TestMoveLegacyDatabaseFinishesAnInterruptedMove covers the accident row between
// the rename and the removal: identical files, empty journal, the old one goes and
// nothing is copied again.
func TestMoveLegacyDatabaseFinishesAnInterruptedMove(t *testing.T) {
	p, old := moveFixture(t)
	// The interrupted run had folded the journal already: the main file alone
	// holds the row, and the journal files that are left are empty.
	audit04CheckpointFixture(t, old)
	raw, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p.Database, string(raw))
	for _, suffix := range []string{"-wal", "-shm"} {
		writeFile(t, old+suffix, "")
	}
	moved, err := MoveLegacyDatabase(p)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if !moved {
		t.Error("the finished move reports nothing done")
	}
	checkGone(t, old, "the old database")
	checkGone(t, old+"-wal", "the empty old journal")
	checkGone(t, old+"-shm", "the empty old shared memory file")
	if raw := readState(t, p.Database); raw != `{"value":1}` {
		t.Errorf("the moved row = %s", raw)
	}
}

// TestMoveLegacyDatabaseKeepsADatabaseInUse covers step 1.12: a writer holds the
// old database, the checkpoint cannot drain it, the move stops with code 1 and the
// old file stays as it was.
func TestMoveLegacyDatabaseKeepsADatabaseInUse(t *testing.T) {
	p, old := moveFixture(t)
	// A checkpoint that fails on a busy database may still move pages, so the
	// journal is folded first and "stays as it was" can be compared byte for byte.
	audit04CheckpointFixture(t, old)
	before, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	release := holdWriteLock(t, old)
	defer release()
	_, err = moveLegacyDatabase(p, 50*time.Millisecond)
	audit04WantMoveStep(t, err, "checkpoint the database")
	if !strings.Contains(err.Error(), "in use") {
		t.Errorf("the error must say the database is in use: %v", err)
	}
	current, readErr := os.ReadFile(old)
	if readErr != nil || string(current) != string(before) {
		t.Fatalf("the old database changed: %v", readErr)
	}
	checkGone(t, p.Database, "the new database")
}
