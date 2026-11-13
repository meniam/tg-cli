package store

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesPrivateFileAndMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("database exists before Open: %v", err)
	}
	observed := make(chan os.FileMode, 1)
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		for {
			select {
			case <-done:
				return
			default:
			}
			if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0600 {
				select {
				case observed <- info.Mode().Perm():
				default:
				}
				return
			}
		}
	}()
	db, err := Open(path)
	close(done)
	<-stopped
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	select {
	case mode := <-observed:
		t.Fatalf("database was observed with mode %o", mode)
	default:
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
	if db.Path() != path {
		t.Errorf("Path() = %q, want %q", db.Path(), path)
	}
	version, err := db.Version()
	if err != nil || version != 1 {
		t.Errorf("Version() = %d, %v, want 1", version, err)
	}
	var table string
	if err := db.read.QueryRow(`SELECT name FROM sqlite_master WHERE name='meta'`).Scan(&table); err != nil || table != "meta" {
		t.Errorf("meta table = %q, %v", table, err)
	}
}

func TestReopenPreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.write.Exec(`INSERT INTO meta(key,value) VALUES('marker','kept')`); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var value string
	if err := second.read.QueryRow(`SELECT value FROM meta WHERE key='marker'`).Scan(&value); err != nil || value != "kept" {
		t.Errorf("marker = %q, %v", value, err)
	}
	if version, err := second.Version(); err != nil || version != 1 {
		t.Errorf("version = %d, %v", version, err)
	}
}

func TestFutureDatabaseIsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	plain, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.Exec(`PRAGMA user_version=999`); err != nil {
		t.Fatal(err)
	}
	plain.Close()
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Open(path)
	if err == nil || !strings.Contains(err.Error(), "database is newer than this program") {
		t.Fatalf("Open future database error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("future database changed")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0640 {
		t.Errorf("future database mode = %v, %v, want 0640", info, err)
	}
}

func TestPragmasAndConcurrentRead(t *testing.T) {
	db := openTestDB(t)
	for _, pool := range []*sql.DB{db.write, db.read} {
		var mode string
		if err := pool.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
			t.Errorf("journal_mode = %q, %v", mode, err)
		}
		for _, tc := range []struct {
			pragma string
			want   int
		}{{"foreign_keys", 1}, {"busy_timeout", 5000}, {"synchronous", 1}} {
			var got int
			if err := pool.QueryRow("PRAGMA " + tc.pragma).Scan(&got); err != nil || got != tc.want {
				t.Errorf("%s = %d, %v, want %d", tc.pragma, got, err, tc.want)
			}
		}
	}
	ctx := context.Background()
	connections := make([]*sql.Conn, 4)
	for i := range connections {
		conn, err := db.read.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections[i] = conn
		defer conn.Close()
		var foreignKeys, timeout, syncMode int
		for _, check := range []struct {
			pragma string
			value  *int
		}{{"foreign_keys", &foreignKeys}, {"busy_timeout", &timeout}, {"synchronous", &syncMode}} {
			if err := conn.QueryRowContext(ctx, "PRAGMA "+check.pragma).Scan(check.value); err != nil {
				t.Fatal(err)
			}
		}
		if foreignKeys != 1 || timeout != 5000 || syncMode != 1 {
			t.Errorf("read connection %d PRAGMAs = %d, %d, %d", i, foreignKeys, timeout, syncMode)
		}
	}
	for _, conn := range connections {
		conn.Close()
	}
	if _, err := db.write.Exec(`INSERT INTO meta(key,value) VALUES('committed','visible')`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.write.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO meta(key,value) VALUES('pending','hidden')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var count int
	if err := db.read.QueryRowContext(ctx, `SELECT count(*) FROM meta`).Scan(&count); err != nil {
		t.Fatalf("read while writer transaction is open: %v", err)
	}
	if count != 1 {
		t.Errorf("reader count = %d, want 1 committed row", count)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenErrors(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name string
		path string
	}{{"empty", ""}, {"missing parent", filepath.Join(base, "missing", "store.db")}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if db, err := Open(tc.path); err == nil {
				db.Close()
				t.Fatal("Open succeeded")
			}
		})
	}
	t.Run("unwritable directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can write to mode 0500 directories")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0700)
		if db, err := Open(filepath.Join(dir, "store.db")); err == nil {
			db.Close()
			t.Fatal("Open succeeded in unwritable directory")
		}
	})
}
