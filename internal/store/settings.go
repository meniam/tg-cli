package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"tg-cli/internal/config"
)

// Settings returns all stored settings after validating the complete snapshot.
// Absent keys are not included; callers can obtain defaults from config.
func (db *DB) Settings(ctx context.Context) (map[string]json.RawMessage, error) {
	values, err := readSettings(ctx, db.read)
	if err != nil {
		return nil, err
	}
	if err := config.ValidateSettings(values); err != nil {
		return nil, fmt.Errorf("validate settings: %w", err)
	}
	return values, nil
}

type settingsQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readSettings(ctx context.Context, queryer settingsQueryer) (map[string]json.RawMessage, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()
	values := make(map[string]json.RawMessage)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("read settings: %w", err)
		}
		values[key] = json.RawMessage(value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	return values, nil
}

// SetSettings validates and writes the supplied keys in one transaction.
func (db *DB) SetSettings(ctx context.Context, changes map[string]json.RawMessage) error {
	return db.setSettings(ctx, changes, false, nil)
}

// ImportLegacySettings writes imported settings and the migration marker atomically.
// It refuses to import again when a marker already exists.
func (db *DB) ImportLegacySettings(ctx context.Context, changes map[string]json.RawMessage) error {
	return db.setSettings(ctx, changes, true, nil)
}

// ImportLegacySettingsChecked verifies the transaction and performs the caller's
// pre-commit work before committing the values and migration marker. The
// callback must not write to this database while the transaction is open.
func (db *DB) ImportLegacySettingsChecked(ctx context.Context, changes map[string]json.RawMessage, beforeCommit func(map[string]json.RawMessage) error) error {
	if beforeCommit == nil {
		return errors.New("migration verification is required")
	}
	return db.setSettings(ctx, changes, true, beforeCommit)
}

func (db *DB) setSettings(ctx context.Context, changes map[string]json.RawMessage, markMigrated bool, beforeCommit func(map[string]json.RawMessage) error) error {
	canonical := make(map[string]json.RawMessage, len(changes))
	for key, value := range changes {
		parsed, err := config.ParseSettingJSON(key, value)
		if err != nil {
			return fmt.Errorf("validate setting: %w", err)
		}
		canonical[key] = parsed
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settings update: %w", err)
	}
	defer tx.Rollback()
	if markMigrated {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM settings_meta WHERE key=?`, settingsMigrationKey).Scan(&count); err != nil {
			return fmt.Errorf("read settings migration marker: %w", err)
		}
		if count != 0 {
			return errors.New("settings migration marker already exists")
		}
	}
	values, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	for key, value := range canonical {
		values[key] = value
	}
	if err := config.ValidateSettings(values); err != nil {
		return fmt.Errorf("validate settings: %w", err)
	}
	keys := make([]string, 0, len(canonical))
	for key := range canonical {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	now := db.now().Unix()
	for _, key := range keys {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, string(canonical[key]), now)
		if err != nil {
			return fmt.Errorf("write setting %s: %w", key, err)
		}
	}
	if markMigrated {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings_meta(key,value) VALUES(?, '1')`, settingsMigrationKey); err != nil {
			return fmt.Errorf("write settings migration marker: %w", err)
		}
	}
	if beforeCommit != nil {
		written, err := readSettings(ctx, tx)
		if err != nil {
			return err
		}
		for key, expected := range canonical {
			if !bytes.Equal(written[key], expected) {
				return errors.New("settings migration verification failed")
			}
		}
		if err := beforeCommit(written); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings update: %w", err)
	}
	return nil
}

// DeleteSetting removes one stored setting, restoring its default.
func (db *DB) DeleteSetting(ctx context.Context, key string) error {
	if _, ok := config.LookupSetting(key); !ok {
		return fmt.Errorf("unknown setting %q", key)
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin settings deletion: %w", err)
	}
	defer tx.Rollback()
	values, err := readSettings(ctx, tx)
	if err != nil {
		return err
	}
	delete(values, key)
	if err := config.ValidateSettings(values); err != nil {
		return fmt.Errorf("validate settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key); err != nil {
		return fmt.Errorf("delete setting: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit settings deletion: %w", err)
	}
	return nil
}

// OwnerSecret returns the stored passphrase hash and whether it exists.
func (db *DB) OwnerSecret(ctx context.Context) (string, bool, error) {
	var hash string
	err := db.read.QueryRowContext(ctx, `SELECT hash FROM owner_secret WHERE id=1`).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read owner secret: %w", err)
	}
	return hash, true, nil
}

// SetOwnerSecret stores a passphrase hash, retaining the creation time on update.
func (db *DB) SetOwnerSecret(ctx context.Context, hash string) error {
	if hash == "" {
		return errors.New("owner secret hash is empty")
	}
	now := db.now().Unix()
	_, err := db.write.ExecContext(ctx, `INSERT INTO owner_secret(id,hash,created_at,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET hash=excluded.hash,updated_at=excluded.updated_at`, hash, now, now)
	if err != nil {
		return fmt.Errorf("write owner secret: %w", err)
	}
	return nil
}

// DeleteOwnerSecret removes the passphrase hash.
func (db *DB) DeleteOwnerSecret(ctx context.Context) error {
	if _, err := db.write.ExecContext(ctx, `DELETE FROM owner_secret WHERE id=1`); err != nil {
		return fmt.Errorf("delete owner secret: %w", err)
	}
	return nil
}

const settingsMigrationKey = "legacy_settings_migrated"

// SettingsMigrated reports whether legacy settings have been imported.
func (db *DB) SettingsMigrated(ctx context.Context) (bool, error) {
	var value string
	err := db.read.QueryRowContext(ctx, `SELECT value FROM settings_meta WHERE key=?`, settingsMigrationKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read settings migration marker: %w", err)
	}
	if value != "1" {
		return false, errors.New("invalid settings migration marker")
	}
	return true, nil
}

// CreateOwnerSecret atomically initializes a previously absent passphrase hash.
func (db *DB) CreateOwnerSecret(ctx context.Context, hash string) (bool, error) {
	if hash == "" {
		return false, errors.New("owner secret hash is empty")
	}
	now := db.now().Unix()
	result, err := db.write.ExecContext(ctx, `INSERT INTO owner_secret(id,hash,created_at,updated_at) VALUES(1,?,?,?) ON CONFLICT(id) DO NOTHING`, hash, now, now)
	if err != nil {
		return false, fmt.Errorf("initialize owner secret: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("initialize owner secret: %w", err)
	}
	return n == 1, nil
}

// ReplaceOwnerSecret changes the hash only if it has not changed since it was read.
func (db *DB) ReplaceOwnerSecret(ctx context.Context, previous, next string) (bool, error) {
	if next == "" {
		return false, errors.New("owner secret hash is empty")
	}
	result, err := db.write.ExecContext(ctx, `UPDATE owner_secret SET hash=?, updated_at=? WHERE id=1 AND hash=?`, next, db.now().Unix(), previous)
	if err != nil {
		return false, fmt.Errorf("change owner secret: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("change owner secret: %w", err)
	}
	return n == 1, nil
}

// LegacyVerificationError identifies the setting whose staged value did not match.
type LegacyVerificationError struct{ Key string }

func (e *LegacyVerificationError) Error() string { return "cannot verify migration settings" }

// LegacyImport is a verified but not yet committed profile migration.
type LegacyImport struct{ tx *sql.Tx }

// BeginLegacyImport stages a complete migration and reads it back before commit.
func (db *DB) BeginLegacyImport(ctx context.Context, changes map[string]json.RawMessage) (*LegacyImport, error) {
	canonical := make(map[string]json.RawMessage, len(changes))
	for key, value := range changes {
		parsed, err := config.ParseSettingJSON(key, value)
		if err != nil {
			return nil, errors.New("invalid migration settings")
		}
		canonical[key] = parsed
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.New("cannot begin settings migration")
	}
	fail := func(err error) (*LegacyImport, error) { _ = tx.Rollback(); return nil, err }
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM settings_meta WHERE key=?`, settingsMigrationKey).Scan(&count); err != nil {
		return fail(errors.New("cannot read settings migration marker"))
	}
	if count != 0 {
		return fail(errors.New("settings migration marker already exists"))
	}
	values, err := readSettings(ctx, tx)
	if err != nil {
		return fail(errors.New("cannot read settings"))
	}
	for key, value := range canonical {
		values[key] = value
	}
	if err := config.ValidateSettings(values); err != nil {
		return fail(errors.New("invalid migration settings"))
	}
	keys := make([]string, 0, len(canonical))
	for key := range canonical {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	now := db.now().Unix()
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, string(canonical[key]), now); err != nil {
			return fail(errors.New("cannot write migration settings"))
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings_meta(key,value) VALUES(?, '1')`, settingsMigrationKey); err != nil {
		return fail(errors.New("cannot write settings migration marker"))
	}
	actual, err := readSettings(ctx, tx)
	if err != nil {
		return fail(errors.New("cannot verify migration settings"))
	}
	for key, value := range canonical {
		if !bytes.Equal(actual[key], value) {
			return fail(&LegacyVerificationError{Key: key})
		}
	}
	return &LegacyImport{tx: tx}, nil
}

// Commit publishes the staged values and marker.
func (m *LegacyImport) Commit() error {
	if err := m.tx.Commit(); err != nil {
		return errors.New("cannot commit settings migration")
	}
	return nil
}

// Rollback discards staged changes; it is safe after Commit.
func (m *LegacyImport) Rollback() { _ = m.tx.Rollback() }

// SettingsMigratedAt reads the marker without opening a writable database.
// A missing database or a database before the settings schema is not migrated.
func SettingsMigratedAt(path string) (bool, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, errors.New("cannot inspect profile database")
	}
	db, err := sql.Open("sqlite", dsn(path, "ro"))
	if err != nil {
		return false, errors.New("cannot open profile database")
	}
	defer db.Close()
	var name string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='settings_meta'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, errors.New("cannot read settings migration marker")
	}
	var value string
	err = db.QueryRow(`SELECT value FROM settings_meta WHERE key=?`, settingsMigrationKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || value != "1" {
		return false, errors.New("invalid settings migration marker")
	}
	return true, nil
}

// ReadOnlySettingsAt reads existing settings without creating or migrating a database.
func ReadOnlySettingsAt(ctx context.Context, path string) (map[string]json.RawMessage, error) {
	values := make(map[string]json.RawMessage)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return values, nil
	} else if err != nil {
		return nil, errors.New("cannot inspect profile database")
	}
	db, err := sql.Open("sqlite", dsn(path, "ro"))
	if err != nil {
		return nil, errors.New("cannot open profile database")
	}
	defer db.Close()
	var name string
	err = db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='settings'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return values, nil
	}
	if err != nil {
		return nil, errors.New("cannot read profile settings")
	}
	values, err = readSettings(ctx, db)
	if err != nil {
		return nil, errors.New("cannot read profile settings")
	}
	if err := config.ValidateSettings(values); err != nil {
		return nil, errors.New("invalid profile settings")
	}
	return values, nil
}
