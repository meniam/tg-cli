package daemon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// LegacyBackupFile holds one validated source snapshot, grouped by profile.
type LegacyBackupFile struct {
	Profile, Name string
	Data          []byte
}

// BackupLegacySettings writes and verifies private copies without changing sources.
// Callers must complete the backup before committing any migration transaction.
func BackupLegacySettings(home, configName string, configData []byte, configExists bool, files []LegacyBackupFile) (path string, err error) {
	if (!configExists && len(configData) != 0) || !filepath.IsAbs(home) || configName == "" || (configName == "." || configName == "..") || filepath.Base(configName) != configName {
		return "", errors.New("invalid migration backup destination")
	}
	for _, f := range files {
		if !profileName.MatchString(f.Profile) || f.Profile == ReservedProfile || f.Name == "" || (f.Name == "." || f.Name == "..") || filepath.Base(f.Name) != f.Name {
			return "", errors.New("invalid migration backup destination")
		}
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return "", errors.New("cannot create migration backup")
	}
	dir, err := os.MkdirTemp(home, ".config-migration-"+time.Now().UTC().Format("20060102T150405Z")+"-")
	if err != nil {
		return "", errors.New("cannot create migration backup")
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	if err = os.Chmod(dir, 0700); err != nil {
		return "", errors.New("cannot secure migration backup")
	}
	if configExists {
		if err = writeLegacyBackup(filepath.Join(dir, configName), configData); err != nil {
			return "", err
		}
	}
	made := make(map[string]bool)
	for _, f := range files {
		profileDir := filepath.Join(dir, f.Profile)
		if !made[f.Profile] {
			if err = os.Mkdir(profileDir, 0700); err != nil {
				return "", errors.New("cannot create migration backup")
			}
			if err = os.Chmod(profileDir, 0700); err != nil {
				return "", errors.New("cannot secure migration backup")
			}
			made[f.Profile] = true
		}
		if err = writeLegacyBackup(filepath.Join(profileDir, f.Name), f.Data); err != nil {
			return "", err
		}
	}
	for profile := range made {
		if err = syncDir(filepath.Join(dir, profile)); err != nil {
			return "", errors.New("cannot sync migration backup")
		}
	}
	if err = syncDir(dir); err != nil {
		return "", errors.New("cannot sync migration backup")
	}
	if err = syncDir(home); err != nil {
		return "", errors.New("cannot sync migration backup")
	}
	return dir, nil
}

func writeLegacyBackup(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot write migration backup")
	}
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("cannot write migration backup")
	}
	copied, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(copied, data) {
		return errors.New("cannot verify migration backup")
	}
	return nil
}
