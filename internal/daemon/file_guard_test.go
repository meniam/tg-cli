package daemon

import (
	"context"
	"errors"
	"fmt"
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
			// A check never creates a database: every path the test did not
			// create itself must still be absent afterwards.
			created := map[string]bool{}
			if tc.old != "" {
				created[p.LegacyDatabase] = true
			}
			if tc.new != "" {
				created[p.Database] = true
			}
			if tc.writeJournal {
				created[p.LegacyDatabase+"-wal"] = true
			}
			for _, path := range []string{p.Database, p.LegacyDatabase, p.LegacyDatabase + "-wal", filepath.Join(p.Dir, "tg.db"), filepath.Join(p.DatabaseDir, "tg.db")} {
				if created[path] {
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
		name, oldName, newName string
	}{
		{"access", "access.json", "acl.json"},
		{"session", "session.json", "key.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := shortTempDir(t)
			t.Setenv("HOME", home)
			files := DefaultFileNames()
			switch tc.name {
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
			if err := os.WriteFile(filepath.Join(p.Dir, tc.oldName), nil, 0o600); err != nil {
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
	for i, name := range []string{"daemon.log", "deletions.jsonl"} {
		current := map[string]string{"daemon.log": p.Log, "deletions.jsonl": p.Deletions}[name]
		want := fmt.Sprintf("%s exists but configured journal %s does not", filepath.Join(p.Dir, name), current)
		if warnings[i] != want {
			t.Errorf("warning %d = %q, want %q", i, warnings[i], want)
		}
	}
}

// A missing profile directory is not an error; a default profile directory that
// does exist is worth a warning, because the profile may have been renamed.
func TestAudit05_FileNameGuardMissingProfileDirectory(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	p, err := ResolvePaths(home, dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(p.Dir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the profile directory must not exist yet: %v", statErr)
	}
	warnings, err := CheckFileNames(p)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("nothing to warn about: %v %v", warnings, err)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(base, "tg-cli", filepath.Base(p.Dir))
	if filepath.Clean(old) == filepath.Clean(p.Dir) {
		t.Fatalf("fixture: the default profile directory %s is the profile directory", old)
	}
	if err = os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	warnings, err = CheckFileNames(p)
	want := fmt.Sprintf("profile directory %s does not exist; default profile directory %s exists", p.Dir, old)
	if err != nil || len(warnings) != 1 || warnings[0] != want {
		t.Fatalf("warnings=%q err=%v, want %q", warnings, err, want)
	}
}

func TestAudit05_FileNameGuardProfilePathIsAFile(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("HOME", home)
	p, err := ResolvePaths(home, dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(p.Dir), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, p.Dir, "not a directory")
	_, err = CheckFileNames(p)
	if err == nil || !strings.Contains(err.Error(), "profile path is not a directory") {
		t.Fatalf("got %v", err)
	}
}
