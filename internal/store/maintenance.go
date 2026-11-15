package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// Checkpoint folds the write-ahead journal of the database at path into the main
// file and truncates the journal. It opens the file without migrating the
// schema, so it also works for a database of a newer version.
//
// busy is true when another connection did not let the checkpoint finish: the
// journal may then still hold data. A missing file is an error, because a
// checkpoint of nothing would hide a wrong path.
func Checkpoint(path string) (busy bool, err error) {
	db, err := openInspect(path)
	if err != nil {
		return false, err
	}
	defer db.Close()
	// The pragma answers three columns: busy, the size of the journal and the
	// pages the checkpoint moved. Only busy decides whether the journal is
	// drained.
	var busyFlag, logPages, checkpointed int
	if err := db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busyFlag, &logPages, &checkpointed); err != nil {
		return false, fmt.Errorf("checkpoint database: %w", err)
	}
	return busyFlag != 0, nil
}

// IntegrityCheck runs PRAGMA integrity_check on the database at path and returns
// its verdict. detail is the engine text, "ok" for a healthy file; on a damaged
// file it names the first problem. The schema is not migrated.
func IntegrityCheck(path string) (ok bool, detail string, err error) {
	db, err := openInspect(path)
	if err != nil {
		return false, "", err
	}
	defer db.Close()
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return false, "", fmt.Errorf("check database integrity: %w", err)
	}
	defer rows.Close()
	var messages []string
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			return false, "", fmt.Errorf("check database integrity: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return false, "", fmt.Errorf("check database integrity: %w", err)
	}
	switch {
	case len(messages) == 0:
		return false, "", errors.New("check database integrity: no verdict")
	case len(messages) == 1 && messages[0] == "ok":
		return true, "ok", nil
	default:
		return false, messages[0], nil
	}
}

// UserVersion returns the schema version recorded in the database at path. It
// does not migrate the schema, so a copy and its source can be compared.
func UserVersion(path string) (int, error) {
	db, err := openInspect(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read database version: %w", err)
	}
	return version, nil
}

// openInspect opens an existing database file read-write without migrations and
// without creating it.
func openInspect(path string) (*sql.DB, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("inspect database: %s is not a regular file", path)
	}
	db, err := sql.Open("sqlite", dsn(path, "rw"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect database: %w", err)
	}
	return db, nil
}
