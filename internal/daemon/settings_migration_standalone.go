package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tg-cli/internal/config"
)

var standaloneProfileKeys = []string{"client", "device", "transport", "proxy"}

type standaloneProfileFile struct {
	path, key string
	raw       []byte
	mode      os.FileMode
	value     json.RawMessage
}

// standaloneProfileNames discovers old per-key files without opening them.
func standaloneProfileNames(home string, files config.Files) ([]string, error) {
	entries, err := os.ReadDir(home)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot inspect legacy profiles")
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !profileName.MatchString(name) || name == ReservedProfile {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, errors.New("cannot inspect legacy profiles")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("invalid legacy profile directory")
		}
		if !info.IsDir() {
			continue
		}
		for _, key := range append(append([]string(nil), standaloneProfileKeys...), files.Database, files.Access) {
			path := filepath.Join(home, name, key)
			_, err := os.Lstat(path)
			if err == nil {
				names = append(names, name)
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				return nil, &MigrationInputError{Source: path}
			}
		}
	}
	sort.Strings(names)
	return names, nil
}

// prepareStandaloneProfile reads old per-key files only after checking that
// their names cannot refer to any configured session or profile data file.
func prepareStandaloneProfile(paths Paths, legacy config.Legacy, profile string) ([]standaloneProfileFile, error) {
	var result []standaloneProfileFile
	for _, key := range standaloneProfileKeys {
		path := filepath.Join(paths.Dir, key)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, &MigrationInputError{Source: path}
		}
		for _, reserved := range []string{paths.Session, paths.Access, paths.Log, paths.Deletions, paths.LegacyDatabase, paths.Lock, paths.Socket} {
			if filepath.Clean(path) == filepath.Clean(reserved) {
				return nil, &MigrationInputError{Source: path}
			}
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
			return nil, &MigrationInputError{Source: path}
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, &MigrationInputError{Source: path}
		}
		actual, statErr := f.Stat()
		if statErr != nil || !actual.Mode().IsRegular() || actual.Mode().Perm()&0o022 != 0 || !os.SameFile(info, actual) {
			f.Close()
			return nil, &MigrationInputError{Source: path}
		}
		raw, readErr := io.ReadAll(f)
		closeErr := f.Close()
		after, statErr := os.Lstat(path)
		if readErr != nil || closeErr != nil || statErr != nil || !os.SameFile(info, after) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Mode() != info.Mode() {
			return nil, &MigrationInputError{Source: path}
		}
		value := strings.TrimSpace(string(raw))
		if value == "" {
			continue
		}
		name := "profile." + key
		parsed, err := config.ParseSettingString(name, value)
		if err != nil {
			return nil, &MigrationInputError{Source: path}
		}
		if original, found := legacy.Profiles[profile][key]; found {
			text, ok := original.(string)
			if !ok {
				return nil, &MigrationInputError{Source: path}
			}
			if key == "transport" && text == "" {
				text = "tcp"
			}
			if key == "proxy" && text == "" {
				text = "none"
			}
			old, err := config.ParseSettingString(name, text)
			if err != nil || !bytes.Equal(old, parsed) {
				return nil, &MigrationInputError{Source: path}
			}
		}
		result = append(result, standaloneProfileFile{path: path, key: name, raw: raw, mode: info.Mode().Perm(), value: parsed})
	}
	return result, nil
}
