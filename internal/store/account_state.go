package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// GetAccountState returns the JSON value for key, or found=false when absent.
func (db *DB) GetAccountState(ctx context.Context, key string) (json.RawMessage, bool, error) {
	var value string
	err := db.read.QueryRowContext(ctx, `SELECT value FROM account_state WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get account state: %w", err)
	}
	return json.RawMessage(value), true, nil
}

// SetAccountState stores one JSON value for a future updates manager adapter.
func (db *DB) SetAccountState(ctx context.Context, key string, value json.RawMessage) error {
	if key == "" || !json.Valid(value) {
		return errors.New("account state key and JSON value are required")
	}
	_, err := db.write.ExecContext(ctx, `INSERT INTO account_state(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, string(value), db.now().Unix())
	if err != nil {
		return fmt.Errorf("set account state: %w", err)
	}
	return nil
}

// LastAliveAt returns the last saved daemon heartbeat.
func (db *DB) LastAliveAt(ctx context.Context) (int64, bool, error) {
	raw, found, err := db.GetAccountState(ctx, "last_alive_at")
	if err != nil || !found {
		return 0, found, err
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false, fmt.Errorf("decode last alive: %w", err)
	}
	return value, true, nil
}

// SetLastAliveAt saves a daemon heartbeat as a Unix timestamp.
func (db *DB) SetLastAliveAt(ctx context.Context, value int64) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return db.SetAccountState(ctx, "last_alive_at", raw)
}
