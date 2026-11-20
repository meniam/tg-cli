package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// DB owns separate SQLite connection pools for writes and reads.
type DB struct {
	path     string
	write    *sql.DB
	read     *sql.DB
	close    sync.Once
	closeErr error
	now      func() time.Time
}

type migration struct {
	version int
	name    string
}

// Open creates or opens a database and applies known migrations.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	migrations, err := listMigrations()
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	created := err == nil
	if created {
		if err = file.Chmod(0600); err != nil {
			file.Close()
			return nil, fmt.Errorf("set database permissions: %w", err)
		}
		if err = file.Close(); err != nil {
			return nil, fmt.Errorf("close new database: %w", err)
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create database: %w", err)
	}

	if !created {
		version, err := versionAt(path)
		if err != nil {
			return nil, fmt.Errorf("inspect database version: %w", err)
		}
		if version > migrations[len(migrations)-1].version {
			return nil, fmt.Errorf("database is newer than this program: version %d", version)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("set database permissions: %w", err)
		}
	}

	write, err := sql.Open("sqlite", dsn(path, "rw"))
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}
	write.SetMaxOpenConns(1)
	if err := write.Ping(); err != nil {
		write.Close()
		return nil, fmt.Errorf("connect write pool: %w", err)
	}
	var mode string
	if err := write.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		write.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if mode != "wal" {
		write.Close()
		return nil, fmt.Errorf("enable WAL: got %q", mode)
	}
	if err := applyMigrations(write, migrations); err != nil {
		write.Close()
		return nil, err
	}
	if err := backfillTranscribedSearchText(context.Background(), write); err != nil {
		write.Close()
		return nil, err
	}

	read, err := sql.Open("sqlite", dsn(path, "ro"))
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	read.SetMaxOpenConns(4)
	if err := read.Ping(); err != nil {
		read.Close()
		write.Close()
		return nil, fmt.Errorf("connect read pool: %w", err)
	}
	return &DB{path: path, write: write, read: read, now: time.Now}, nil
}

func dsn(path, mode string) string {
	query := url.Values{"mode": {mode}}
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "synchronous(NORMAL)")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: query.Encode()}).String()
}

func versionAt(path string) (int, error) {
	db, err := sql.Open("sqlite", dsn(path, "ro"))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var version int
	err = db.QueryRow(`PRAGMA user_version`).Scan(&version)
	return version, err
}

func listMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var migrations []migration
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		version, err := strconv.Atoi(prefix)
		if !ok || err != nil {
			return nil, fmt.Errorf("invalid migration name %q", name)
		}
		migrations = append(migrations, migration{version: version, name: name})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i, migration := range migrations {
		if migration.version != i+1 {
			return nil, fmt.Errorf("migration versions must start at 1 and be consecutive")
		}
	}
	if len(migrations) == 0 {
		return nil, errors.New("no database migrations")
	}
	return migrations, nil
}

func applyMigrations(db *sql.DB, migrations []migration) error {
	var current int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("read database version: %w", err)
	}
	for _, migration := range migrations {
		if migration.version <= current {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + migration.name)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", migration.version, err)
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", migration.version, err)
		}
		if _, err := tx.Exec(string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", migration.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set migration version %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.version, err)
		}
	}
	return nil
}

// Path returns the database file path supplied to Open.
func (db *DB) Path() string { return db.path }

// Version returns the applied migration version.
func (db *DB) Version() (int, error) {
	var version int
	err := db.read.QueryRow(`PRAGMA user_version`).Scan(&version)
	return version, err
}

// Close releases both connection pools. Repeated calls return the same result.
func (db *DB) Close() error {
	db.close.Do(func() { db.closeErr = errors.Join(db.read.Close(), db.write.Close()) })
	return db.closeErr
}
