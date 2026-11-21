package daemon

import (
	"errors"
	"os"
	"path/filepath"
)

// CheckLegacyHomeSettings refuses to move a previous home containing settings
// sources before those sources can participate in the protected migration.
func CheckLegacyHomeSettings(oldHome, newHome string, files FileNames) error {
	if oldHome == "" || newHome == "" || filepath.Clean(oldHome) == filepath.Clean(newHome) {
		return nil
	}
	info, err := os.Stat(oldHome)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.New("cannot inspect legacy home")
	}
	if !info.IsDir() || sameDir(oldHome, newHome) {
		return nil
	}
	profiles, _, err := legacyEntries(oldHome)
	if err != nil {
		return errors.New("cannot inspect legacy home")
	}
	for _, profile := range profiles {
		names := []string{files.Access}
		for _, key := range standaloneProfileKeys {
			if key == files.Session || key == files.Database || key == files.Log || key == files.Deletions || key == files.Access {
				continue
			}
			names = append(names, key)
		}
		for _, name := range names {
			path := filepath.Join(oldHome, profile, name)
			_, err := os.Lstat(path)
			if err == nil {
				return &MigrationInputError{Source: path}
			}
			if !errors.Is(err, os.ErrNotExist) {
				return &MigrationInputError{Source: path}
			}
		}
	}
	return nil
}
