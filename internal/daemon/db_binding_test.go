package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bindingFixture returns two profiles that share one database root and differ in
// their data directory, which is the state Q1 protects against.
func bindingFixture(t *testing.T) (first, second Paths) {
	t.Helper()
	root := dbRoot(t)
	first, err := ResolvePaths(shortTempDir(t), root, "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	second, err = ResolvePaths(shortTempDir(t), root, "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if first.Database != second.Database || first.Dir == second.Dir {
		t.Fatalf("the fixture must share the database and differ in the data directory: %+v %+v", first, second)
	}
	for _, dir := range []string{first.DatabaseDir, first.Dir, second.Dir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return first, second
}

// TestDatabaseBinding covers Q1: the first data directory writes itself down next
// to the database, the same directory passes again and a second one is a
// configuration error that names both paths.
func TestDatabaseBinding(t *testing.T) {
	first, second := bindingFixture(t)
	if err := CheckDatabaseBinding(first); err != nil {
		t.Fatalf("a profile without a record must pass: %v", err)
	}
	if err := BindDatabase(first); err != nil {
		t.Fatalf("bind: %v", err)
	}
	info, err := os.Stat(DatabaseBindingFile(first))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the record: mode %o, want 0600", info.Mode().Perm())
	}
	recorded, err := os.ReadFile(DatabaseBindingFile(first))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(recorded)) != first.Dir {
		t.Errorf("the record holds %q, want %q", recorded, first.Dir)
	}
	if err := CheckDatabaseBinding(first); err != nil {
		t.Errorf("the same data directory must pass: %v", err)
	}
	err = CheckDatabaseBinding(second)
	var binding *BindingError
	if !errors.As(err, &binding) || binding.ExitCode() != 2 {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), first.Dir) || !strings.Contains(err.Error(), second.Dir) {
		t.Errorf("the message must name both data directories: %v", err)
	}
	// The record file is safe to remove on purpose: it is the act that adopts the
	// database. The database directory holds the history and the message must
	// never tell the reader to remove it.
	record := DatabaseBindingFile(first)
	if !strings.Contains(err.Error(), record) {
		t.Errorf("the message must name the record %s: %v", record, err)
	}
	// The reassurance is part of the contract, not only the absence of bad advice.
	if !strings.Contains(err.Error(), "is never deleted") {
		t.Errorf("the message must say the database is never deleted: %v", err)
	}
	for _, wrong := range []string{"remove " + first.DatabaseDir, "remove the directory", "delete " + first.DatabaseDir} {
		if strings.Contains(err.Error(), wrong) {
			t.Errorf("the message must never advise removing the database directory: %v", err)
		}
	}
	// Binding again never rewrites the record of the first data directory.
	if err := BindDatabase(second); err != nil {
		t.Fatalf("second bind: %v", err)
	}
	again, err := os.ReadFile(DatabaseBindingFile(first))
	if err != nil || string(again) != string(recorded) {
		t.Fatalf("the record changed: %q %v", again, err)
	}
}

// TestDatabaseBindingEmptyRecord treats an empty record as damage: it cannot name
// a data directory, and guessing one would mix two accounts.
func TestDatabaseBindingEmptyRecord(t *testing.T) {
	first, _ := bindingFixture(t)
	writeFile(t, DatabaseBindingFile(first), "\n")
	err := CheckDatabaseBinding(first)
	var binding *BindingError
	if err == nil || errors.As(err, &binding) {
		t.Fatalf("an empty record must be a plain error: %v", err)
	}
}

// TestDatabaseLock covers Q1 without a socket: the second holder of the database
// lock is refused and the first one keeps it until the release function runs. No
// data directory record exists here, so the lock is the only thing that stops the
// second start, which is the order Run uses.
func TestDatabaseLock(t *testing.T) {
	first, second := bindingFixture(t)
	release, err := LockDatabase(first)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	info, err := os.Stat(DatabaseLockFile(first))
	if err != nil {
		release()
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the lock file: mode %o, want 0600", info.Mode().Perm())
	}
	if _, err := LockDatabase(second); !errors.Is(err, ErrDatabaseInUse) {
		t.Errorf("the second data directory took the lock: %v", err)
	}
	release()
	again, err := LockDatabase(second)
	if err != nil {
		t.Fatalf("the lock stayed held: %v", err)
	}
	again()
}

// TestDatabaseLockCreatesTheDirectory lets a first start lock a database
// directory that does not exist yet.
func TestDatabaseLockCreatesTheDirectory(t *testing.T) {
	first, _ := bindingFixture(t)
	if err := os.RemoveAll(first.DatabaseDir); err != nil {
		t.Fatal(err)
	}
	release, err := LockDatabase(first)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer release()
	info, err := os.Stat(first.DatabaseDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("the directory: mode %o, want 0700", info.Mode().Perm())
	}
}

// TestRunDrainsTheLockOnFailure keeps a failed start from holding the database
// lock: the next start has to be able to take it.
func TestRunDrainsTheLockOnFailure(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty socket path stops Run after the database lock is taken.
	writeFile(t, filepath.Join(paths.Socket, "keep"), "x")
	if err := Run(context.Background(), Options{Profile: "test", Paths: paths, Telegram: newFake()}); err == nil || !strings.Contains(err.Error(), "remove stale socket") {
		t.Fatalf("Run error = %v", err)
	}
	release, err := LockDatabase(paths)
	if err != nil {
		t.Fatalf("Run kept the database lock: %v", err)
	}
	release()
}
