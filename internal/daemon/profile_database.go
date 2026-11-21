package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"tg-cli/internal/store"
)

// ProfileDatabase exposes settings operations while the profile locks are held.
type ProfileDatabase interface {
	Settings(context.Context) (map[string]json.RawMessage, error)
	SetSettings(context.Context, map[string]json.RawMessage) error
	DeleteSetting(context.Context, string) error
	SettingsMigrated(context.Context) (bool, error)
	Moved() bool
}

type profileDatabase struct {
	*store.DB
	moved bool
}

func (db profileDatabase) Moved() bool { return db.moved }

// WithProfileDatabase opens the profile database under the same locks as Run.
func WithProfileDatabase(paths Paths, action func(ProfileDatabase) error) error {
	return WithProfileDatabasePreflight(paths, nil, action)
}

// WithProfileDatabasePreflight checks the profile under both locks before moving or opening its database.
func WithProfileDatabasePreflight(paths Paths, preflight func() error, action func(ProfileDatabase) error) error {
	return WithProfileLock(paths, func() error {
		release, err := LockDatabase(paths)
		if err != nil {
			return err
		}
		defer release()
		if err := CheckDatabaseBinding(paths); err != nil {
			return err
		}
		if preflight != nil {
			if err := preflight(); err != nil {
				return err
			}
		}
		moved, err := MoveLegacyDatabase(paths)
		if err != nil {
			return err
		}
		if err := BindDatabase(paths); err != nil {
			return err
		}
		db, err := store.Open(paths.Database)
		if err != nil {
			return fmt.Errorf("open profile database: %w", err)
		}
		return func() (result error) {
			defer func() { result = errors.Join(result, db.Close()) }()
			return action(profileDatabase{DB: db, moved: moved})
		}()
	})
}
