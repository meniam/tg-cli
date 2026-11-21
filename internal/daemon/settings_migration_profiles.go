package daemon

import (
	"errors"
	"os"
	"sort"

	"tg-cli/internal/config"
)

// LegacyProfileNames finds the default, configured and database profiles.
// A symlink for a valid database profile is rejected rather than skipped.
func LegacyProfileNames(cfg config.Config, dbRoot string) ([]string, error) {
	names := map[string]bool{}
	add := func(name string) error {
		if !profileName.MatchString(name) || name == ReservedProfile {
			return errors.New("invalid legacy profile name")
		}
		names[name] = true
		return nil
	}
	if err := add(cfg.DefaultProfile); err != nil {
		return nil, err
	}
	for name := range cfg.Legacy.Profiles {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	entries, err := os.ReadDir(dbRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot inspect profile databases")
	}
	for _, entry := range entries {
		name := entry.Name()
		if !profileName.MatchString(name) || name == ReservedProfile {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, errors.New("cannot inspect profile databases")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("invalid profile database directory")
		}
		if info.IsDir() {
			names[name] = true
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}
