package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"

	"tg-cli/internal/config"
)

// LegacySettingsError names a legacy source that must not be imported again.
type LegacySettingsError struct{ Source string }

func (e *LegacySettingsError) Error() string {
	return fmt.Sprintf("legacy settings source %s conflicts with migration; restore from the migration backup and rerun", e.Source)
}
func (e *LegacySettingsError) ExitCode() int { return 2 }

// CheckMigratedSettingsSources rejects old sources after the migration marker.
func CheckMigratedSettingsSources(ctx context.Context, db ProfileDatabase, accessPath string, legacy config.Legacy) error {
	migrated, err := db.SettingsMigrated(ctx)
	if err != nil {
		return errors.New("cannot read settings migration marker")
	}
	if !migrated {
		return nil
	}
	return CheckNoLegacySettingsSources(accessPath, legacy)
}

// CheckNoLegacySettingsSources fails closed if an old source exists.
func CheckNoLegacySettingsSources(accessPath string, legacy config.Legacy) error {
	if accessPath != "" {
		_, err := os.Lstat(accessPath)
		if err == nil {
			return &LegacySettingsError{Source: accessPath}
		}
		if !errors.Is(err, os.ErrNotExist) {
			return &MigrationInputError{Source: accessPath}
		}
	}
	switch {
	case legacy.Behavior != nil:
		return &LegacySettingsError{Source: "[behavior]"}
	case legacy.BackgroundSync != nil:
		return &LegacySettingsError{Source: "[background_sync]"}
	case legacy.SemanticSearch != nil:
		return &LegacySettingsError{Source: "[semantic_search]"}
	case legacy.Profiles != nil:
		return &LegacySettingsError{Source: "[profiles]"}
	}
	return nil
}
