package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const databaseContents = "SQLite format 3\x00 and the rest of a database"

// guardFixture builds a profile with both directories created and empty.
func guardFixture(t *testing.T) Paths {
	t.Helper()
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	p, err := ResolvePaths(home, dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{p.Dir, p.DatabaseDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// writeFile creates a file with the given contents and mode 0600.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestFileNameGuardStates covers every row of the "States at start" table of the
// plan: the database is only old, only new, in both places or nowhere, and the
// old journal may hold data or be empty.
func TestFileNameGuardStates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		old, new     string // contents; "" leaves the file absent
		journal      string // contents of the old journal
		writeJournal bool
		wantConflict bool
		wantText     string
	}{
		{name: "neither"},
		{name: "only new", new: databaseContents},
		{name: "only old waits for the move", old: databaseContents},
		{name: "identical files finish an interrupted move", old: databaseContents, new: databaseContents},
		{name: "identical files with an empty old journal", old: databaseContents, new: databaseContents, writeJournal: true},
		{name: "different files", old: databaseContents, new: databaseContents + " newer", wantConflict: true, wantText: "both places"},
		{name: "identical files with data in the old journal", old: databaseContents, new: databaseContents, journal: "pending", writeJournal: true, wantConflict: true, wantText: "both places"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := guardFixture(t)
			if tc.old != "" {
				writeFile(t, p.LegacyDatabase, tc.old)
			}
			if tc.new != "" {
				writeFile(t, p.Database, tc.new)
			}
			if tc.writeJournal {
				writeFile(t, p.LegacyDatabase+"-wal", tc.journal)
			}
			_, err := CheckFileNames(p)
			var conflict *FileConflictError
			if tc.wantConflict {
				if !errors.As(err, &conflict) || conflict.ExitCode() != 2 || !strings.Contains(err.Error(), tc.wantText) {
					t.Fatalf("unexpected error: %v", err)
				}
				if !strings.Contains(err.Error(), p.LegacyDatabase) || !strings.Contains(err.Error(), p.Database) {
					t.Errorf("the message must name both paths: %v", err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// A check never creates a database: neither the configured file nor
			// the original name may appear because of it.
			for _, path := range []string{p.Database, p.LegacyDatabase, filepath.Join(p.Dir, "tg.db"), filepath.Join(p.DatabaseDir, "tg.db")} {
				if tc.old != "" || tc.new != "" {
					continue
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("check created %s: %v", path, err)
				}
			}
		})
	}
}

// TestFileNameGuardSeesBothDirectories covers "A changed name is checked in two
// directories": the original name next to the configuration file, the original
// name in the profile directory, and a database under an unknown name.
func TestFileNameGuardSeesBothDirectories(t *testing.T) {
	for _, tc := range []struct {
		name     string
		path     func(Paths) string
		contents string
		wantText string
	}{
		{name: "original name next to the configuration", path: func(p Paths) string {
			return filepath.Join(p.DatabaseDir, "store.db")
		}, contents: databaseContents, wantText: "store.db"},
		{name: "original name in the profile directory", path: func(p Paths) string {
			return filepath.Join(p.Dir, "store.db")
		}, contents: databaseContents, wantText: "store.db"},
		{name: "unknown name in the profile directory", path: func(p Paths) string {
			return filepath.Join(p.Dir, "renamed.db")
		}, contents: databaseContents, wantText: "renamed.db"},
		{name: "unknown name next to the configuration", path: func(p Paths) string {
			return filepath.Join(p.DatabaseDir, "renamed.db")
		}, contents: databaseContents, wantText: "renamed.db"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := DefaultFileNames()
			files.Database = "tg.db"
			home := shortTempDir(t)
			t.Setenv("HOME", home)
			p, err := ResolvePaths(home, dbRoot(t), "test", files)
			if err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{p.Dir, p.DatabaseDir} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			writeFile(t, tc.path(p), tc.contents)
			_, err = CheckFileNames(p)
			var conflict *FileConflictError
			if !errors.As(err, &conflict) || conflict.ExitCode() != 2 || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, err := os.Stat(p.Database); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the configured database was created: %v", err)
			}
		})
	}
}

// TestFileNameGuardMoveTempIsNotADatabase keeps the temporary copy of an
// interrupted move from looking like a database under an unknown name.
func TestFileNameGuardMoveTempIsNotADatabase(t *testing.T) {
	p := guardFixture(t)
	writeFile(t, filepath.Join(p.DatabaseDir, "."+filepath.Base(p.Database)+".move-4242.tmp"), databaseContents)
	writeFile(t, filepath.Join(p.DatabaseDir, "."+filepath.Base(p.Database)+".move-4242.tmp-wal"), databaseContents)
	if _, err := CheckFileNames(p); err != nil {
		t.Fatalf("a left-over move copy must not be a conflict: %v", err)
	}
	if _, err := os.Stat(p.Database); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the check created the database: %v", err)
	}
}

// TestRunMovesAWaitingDatabase checks the move at the start path: a database in
// the profile directory alone is not a conflict, and a start moves it before it
// opens the database. The socket path is taken by a directory, so Run stops after
// the move and the test needs no listener.
func TestRunMovesAWaitingDatabase(t *testing.T) {
	p := guardFixture(t)
	newDatabase(t, p.LegacyDatabase)
	// A non-empty directory on the socket path stops Run after the move: the
	// stale socket step cannot remove it. The test needs no listener.
	writeFile(t, filepath.Join(p.Socket, "keep"), "x")
	err := Run(context.Background(), Options{Profile: "test", Paths: p, Telegram: newFake()})
	var conflict *FileConflictError
	if errors.As(err, &conflict) {
		t.Fatalf("a waiting database must not be a conflict: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "remove stale socket") {
		t.Fatalf("Run error: %v", err)
	}
	checkGone(t, p.LegacyDatabase, "the old database")
	if raw := readState(t, p.Database); raw != `{"value":1}` {
		t.Errorf("the moved row = %s", raw)
	}
}

func TestFileNameGuardRenamedSessionAndAccess(t *testing.T) {
	for _, tc := range []struct {
		name, oldName, newName, contents string
		kind                             string
	}{
		{"access", "access.json", "acl.json", "", "access"},
		{"session", "session.json", "key.json", "", "session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := shortTempDir(t)
			t.Setenv("HOME", home)
			files := DefaultFileNames()
			switch tc.kind {
			case "access":
				files.Access = tc.newName
			case "session":
				files.Session = tc.newName
			}
			p, err := ResolvePaths(home, dbRoot(t), "test", files)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(p.Dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p.Dir, tc.oldName), []byte(tc.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			err = Run(context.Background(), Options{Paths: p})
			var conflict *FileConflictError
			if !errors.As(err, &conflict) || conflict.ExitCode() != 2 || !strings.Contains(err.Error(), tc.oldName) || !strings.Contains(err.Error(), tc.newName) {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, err := os.Stat(filepath.Join(p.Dir, tc.newName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("new file created: %v", err)
			}
		})
	}
}

func TestFileNameGuardWarnings(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	files := DefaultFileNames()
	files.Log = "other.log"
	files.Deletions = "other.jsonl"
	p, err := ResolvePaths(home, dbRoot(t), "test", files)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"daemon.log", "deletions.jsonl"} {
		if err := os.WriteFile(filepath.Join(p.Dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	warnings, err := CheckFileNames(p)
	if err != nil || len(warnings) != 2 {
		t.Fatalf("warnings=%v err=%v", warnings, err)
	}
}
