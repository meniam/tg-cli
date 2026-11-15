package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// MigrationResult describes a migration without exposing profile values.
type MigrationResult struct {
	Backup   string
	Warnings []string
}

type legacyFile struct {
	profile, key, path, value string
	raw                       []byte
}

// MigrateProfiles moves legacy profile settings under the config lock.
func MigrateProfiles(path, home string) (MigrationResult, error) {
	return migrateProfiles(path, home, os.Remove)
}

func migrateProfiles(path, home string, remove func(string) error) (MigrationResult, error) {
	var result MigrationResult
	entries, err := os.ReadDir(home)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("inspect profiles: %w", err)
	}
	found := false
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == reservedProfile || !profileName.MatchString(entry.Name()) {
			continue
		}
		for _, key := range []string{"client", "device", "transport", "proxy"} {
			_, e := os.Lstat(filepath.Join(home, entry.Name(), key))
			if e == nil {
				found = true
			} else if !errors.Is(e, os.ErrNotExist) {
				return result, fmt.Errorf("inspect legacy settings: %w", e)
			}
		}
	}
	if !found {
		return result, nil
	}
	err = withConfigLock(path, func() error {
		cfg, e := Load(path)
		if e != nil {
			return e
		}
		original, e := os.ReadFile(path)
		if errors.Is(e, os.ErrNotExist) {
			original = nil
		} else if e != nil {
			return fmt.Errorf("read config: %w", e)
		}
		dirs, e := os.ReadDir(home)
		if e != nil {
			return fmt.Errorf("inspect profiles: %w", e)
		}
		var files []legacyFile
		for _, dir := range dirs {
			if !dir.IsDir() || dir.Name() == reservedProfile || !profileName.MatchString(dir.Name()) {
				continue
			}
			for _, key := range []string{"client", "device", "transport", "proxy"} {
				file := filepath.Join(home, dir.Name(), key)
				info, e := os.Lstat(file)
				if errors.Is(e, os.ErrNotExist) {
					continue
				}
				if e != nil {
					return fmt.Errorf("inspect %s/%s: %w", dir.Name(), key, e)
				}
				if key == cfg.Files.Database || key == cfg.Files.Session || key == cfg.Files.Access || key == cfg.Files.Log || key == cfg.Files.Deletions {
					return invalid("legacy file %s/%s conflicts with a configured profile file", dir.Name(), key)
				}
				if !info.Mode().IsRegular() {
					return invalid("invalid legacy file %s/%s", dir.Name(), key)
				}
				raw, e := os.ReadFile(file)
				if e != nil {
					return fmt.Errorf("read legacy file %s/%s", dir.Name(), key)
				}
				value := strings.TrimSpace(string(raw))
				if value == "" {
					continue
				}
				p := Profile{}
				switch key {
				case "client":
					p.Client = value
				case "device":
					p.Device = value
				case "transport":
					p.Transport = value
				case "proxy":
					p.Proxy = value
				}
				if _, e = validateProfile(dir.Name(), p); e != nil {
					return invalid("invalid legacy setting %s/%s", dir.Name(), key)
				}
				files = append(files, legacyFile{profile: dir.Name(), key: key, path: file, value: value, raw: raw})
			}
		}
		var rawConfig map[string]any
		if e = toml.Unmarshal(original, &rawConfig); e != nil {
			return invalid("invalid TOML configuration")
		}
		var transferable []legacyFile
		working := append([]byte(nil), original...)
		changes := map[string]map[string]string{}
		for _, f := range files {
			p := cfg.Profiles[f.profile]
			var current string
			switch f.key {
			case "client":
				current = p.Client
			case "device":
				current = p.Device
			case "transport":
				current = p.Transport
			case "proxy":
				current = p.Proxy
			}
			present := legacyKeyPresent(rawConfig, f.profile, f.key)
			if f.key == "proxy" && (current == "" || current == "none") && f.value == "none" {
				present = true
				current = "none"
			}
			if present && current != f.value {
				result.Warnings = append(result.Warnings, fmt.Sprintf("legacy setting %s/%s differs from config; file retained", f.profile, f.key))
				continue
			}
			if !present {
				working, e = editProfile(working, f.profile, f.key, f.value, profilePresent(cfg.Profiles, f.profile) || len(changes[f.profile]) > 0)
				if e != nil {
					return e
				}
				if changes[f.profile] == nil {
					changes[f.profile] = map[string]string{}
				}
				changes[f.profile][f.key] = f.value
			}
			transferable = append(transferable, f)
		}
		var check Config
		if e = toml.NewDecoder(bytes.NewReader(working)).DisallowUnknownFields().Decode(&check); e != nil {
			return invalid("invalid migrated profile settings")
		}
		for name, p := range check.Profiles {
			if _, e = validateProfile(name, p); e != nil {
				return invalid("invalid migrated settings for profile %s", name)
			}
		}
		if len(transferable) == 0 {
			return nil
		}
		backup, e := newMigrationBackup(home)
		if e != nil {
			return e
		}
		result.Backup = backup
		if original != nil {
			if e = writeBackup(filepath.Join(backup, "config.toml"), original); e != nil {
				return e
			}
		}
		for _, f := range transferable {
			dir := filepath.Join(backup, f.profile)
			if e = os.MkdirAll(dir, 0o700); e != nil {
				return fmt.Errorf("create backup directory: %w", e)
			}
			if e = writeBackup(filepath.Join(dir, f.key), f.raw); e != nil {
				return e
			}
		}
		for name, updates := range changes {
			if _, e = updateProfileLocked(path, name, updates, true, nil); e != nil {
				return e
			}
		}
		verified, e := Load(path)
		if e != nil {
			return e
		}
		for _, f := range transferable {
			p := verified.Profiles[f.profile]
			var got string
			switch f.key {
			case "client":
				got = p.Client
			case "device":
				got = p.Device
			case "transport":
				got = p.Transport
			case "proxy":
				got = p.Proxy
			}
			if f.key == "proxy" && f.value == "none" && got == "" {
				got = "none"
			}
			if got != f.value {
				return fmt.Errorf("verify migrated setting %s/%s", f.profile, f.key)
			}
		}
		for _, f := range transferable {
			current, readErr := os.ReadFile(f.path)
			if readErr != nil || !bytes.Equal(current, f.raw) {
				return fmt.Errorf("legacy setting %s/%s changed during migration", f.profile, f.key)
			}
		}
		for _, f := range transferable {
			if e = remove(f.path); e != nil && !errors.Is(e, os.ErrNotExist) {
				return fmt.Errorf("remove migrated setting %s/%s: %w", f.profile, f.key, e)
			}
		}
		return nil
	})
	return result, err
}

func newMigrationBackup(home string) (string, error) {
	base := filepath.Join(home, ".config-migration-"+time.Now().Format("20060102T150405"))
	for i := 0; i < 100; i++ {
		path := base
		if i > 0 {
			path = fmt.Sprintf("%s-%d", base, i)
		}
		err := os.Mkdir(path, 0o700)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create migration backup: %w", err)
		}
	}
	return "", errors.New("cannot allocate migration backup")
}
func writeBackup(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create migration backup file: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return fmt.Errorf("write migration backup: %w", err)
	}
	return nil
}

func legacyKeyPresent(raw map[string]any, profile, key string) bool {
	profiles, ok := raw["profiles"].(map[string]any)
	if !ok {
		return false
	}
	settings, ok := profiles[profile].(map[string]any)
	if !ok {
		return false
	}
	_, ok = settings[key]
	return ok
}
