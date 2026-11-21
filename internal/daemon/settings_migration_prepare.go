package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"tg-cli/internal/access"
	"tg-cli/internal/config"
)

// MigrationInputError identifies an invalid source without exposing its content.
type MigrationInputError struct{ Source string }

func (e *MigrationInputError) Error() string {
	return fmt.Sprintf("invalid legacy settings source %s", e.Source)
}
func (e *MigrationInputError) ExitCode() int { return 2 }

// PreparedLegacySettings is a detached, validated snapshot for one profile.
type PreparedLegacySettings struct {
	Values       map[string]json.RawMessage
	AccessData   []byte
	AccessMode   os.FileMode
	AccessExists bool
}

// PrepareLegacySettings reads and validates sources without changing the database
// or the source files. The caller must hold the profile and configuration locks.
func PrepareLegacySettings(ctx context.Context, legacy config.Legacy, profile, configPath, accessPath string) (PreparedLegacySettings, error) {
	var result PreparedLegacySettings
	if err := ctx.Err(); err != nil {
		return result, err
	}
	values, err := legacy.SettingsForProfile(profile)
	if err != nil {
		return result, &MigrationInputError{Source: configPath}
	}
	info, err := os.Lstat(accessPath)
	if errors.Is(err, os.ErrNotExist) {
		result.Values = values
		return result, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return result, &MigrationInputError{Source: accessPath}
	}
	f, err := os.Open(accessPath)
	if err != nil {
		return result, &MigrationInputError{Source: accessPath}
	}
	actual, statErr := f.Stat()
	if statErr != nil || !actual.Mode().IsRegular() || actual.Mode().Perm()&0o022 != 0 || !os.SameFile(info, actual) {
		f.Close()
		return result, &MigrationInputError{Source: accessPath}
	}
	raw, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil || closeErr != nil {
		return result, &MigrationInputError{Source: accessPath}
	}
	after, err := os.Lstat(accessPath)
	if err != nil || !os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Mode() != info.Mode() {
		return result, &MigrationInputError{Source: accessPath}
	}
	fields, err := access.LegacySettings(raw)
	if err != nil {
		return result, &MigrationInputError{Source: accessPath}
	}
	for name, value := range fields {
		canonical, err := config.ParseSettingJSON(name, value)
		if err != nil {
			return result, &MigrationInputError{Source: accessPath}
		}
		values[name] = canonical
	}
	if err := config.ValidateSettings(values); err != nil {
		return result, &MigrationInputError{Source: configPath}
	}
	if _, err := access.FromSettings(values); err != nil {
		return result, &MigrationInputError{Source: accessPath}
	}
	result.Values = values
	result.AccessData = raw
	result.AccessMode = info.Mode().Perm()
	result.AccessExists = true
	return result, nil
}
