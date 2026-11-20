// Package storetest provides isolated, migrated databases to tests.
package storetest

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"tg-cli/internal/store"
)

var template = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "tg-store-template-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "store.db")
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	busy, err := store.Checkpoint(path)
	if err != nil {
		return nil, err
	}
	if busy {
		return nil, fmt.Errorf("checkpoint template: database is busy")
	}
	return os.ReadFile(path)
})

// Open copies a clean migrated database to a new path and opens it.
// The caller owns the returned database and must close it.
func Open(path string) (*store.DB, error) {
	data, err := template()
	if err != nil {
		return nil, fmt.Errorf("prepare store template: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create store copy: %w", err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return nil, fmt.Errorf("write store copy: %w", writeErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close store copy: %w", closeErr)
	}
	return store.Open(path)
}
