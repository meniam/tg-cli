package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileNameGuard(t *testing.T) {
	for _, tc := range []struct {
		name, oldName, newName, contents string
		kind                             string
	}{
		{"database", "store.db", "tg.db", "", "database"},
		{"other sqlite", "old.db", "tg.db", "SQLite format 3\x00", "database"},
		{"access", "access.json", "acl.json", "", "access"},
		{"session", "session.json", "key.json", "", "session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := shortTempDir(t)
			t.Setenv("HOME", home)
			files := DefaultFileNames()
			switch tc.kind {
			case "database":
				files.Database = tc.newName
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
