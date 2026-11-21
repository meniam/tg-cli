package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"tg-cli/internal/config"
	"tg-cli/internal/store"
)

// SettingsMigrationResult reports whether sources were moved and where their copies are.
type SettingsMigrationResult struct {
	Migrated bool
	Backup   string
	Moved    []Paths
}

type migrationProfile struct {
	name       string
	paths      Paths
	prepared   PreparedLegacySettings
	db         *store.DB
	staged     *store.LegacyImport
	changes    map[string]json.RawMessage
	standalone []standaloneProfileFile
}

// MigrateSettings migrates all known profiles under the configuration and
// profile locks. A partially committed migration refuses old sources on retry.
func MigrateSettings(ctx context.Context, configPath, home, selected string) (SettingsMigrationResult, error) {
	var result SettingsMigrationResult
	err := config.WithLegacyConfigLock(configPath, func(doc *config.LockedLegacyConfig) error {
		cfg := doc.Config
		if home == "" {
			home = cfg.Home
		}
		dbRoot := filepath.Join(filepath.Dir(configPath), "db")
		names, err := LegacyProfileNames(cfg, dbRoot)
		if err != nil {
			return err
		}
		standaloneNames, err := standaloneProfileNames(home)
		if err != nil {
			return err
		}
		for _, name := range standaloneNames {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if selected != "" {
			found := false
			for _, name := range names {
				if name == selected {
					found = true
					break
				}
			}
			if !found {
				if !profileName.MatchString(selected) || selected == ReservedProfile {
					return errors.New("invalid profile name")
				}
				names = append(names, selected)
				sort.Strings(names)
			}
		}
		files := FileNames{Database: cfg.Files.Database, Session: cfg.Files.Session, Log: cfg.Files.Log, Deletions: cfg.Files.Deletions, Access: cfg.Files.Access}
		profiles := make([]migrationProfile, 0, len(names))
		for _, name := range names {
			paths, err := ResolvePaths(home, dbRoot, name, files)
			if err != nil {
				return errors.New("invalid profile paths")
			}
			profiles = append(profiles, migrationProfile{name: name, paths: paths})
		}
		for _, profile := range profiles {
			if err := checkDatabase(profile.paths); err != nil {
				return err
			}
		}
		// A completed migration with no legacy sources is a read-only no-op,
		// even while another profile's daemon is running.
		if len(standaloneNames) == 0 && cfg.Legacy.Behavior == nil && cfg.Legacy.BackgroundSync == nil && cfg.Legacy.SemanticSearch == nil && cfg.Legacy.Profiles == nil {
			allMigrated := true
			for i := range profiles {
				if err := CheckNoLegacySettingsSources(profiles[i].paths.Access, config.Legacy{}); err != nil {
					allMigrated = false
					break
				}
				marked, err := store.SettingsMigratedAt(profiles[i].paths.Database)
				if err != nil {
					return err
				}
				if !marked {
					allMigrated = false
				}
			}
			if allMigrated {
				return nil
			}
		}
		return withMigrationLocks(profiles, 0, func() error {
			checkProfiles := func() error {
				current, err := LegacyProfileNames(cfg, dbRoot)
				if err != nil {
					return err
				}
				standalone, err := standaloneProfileNames(home)
				if err != nil {
					return err
				}
				for _, name := range standalone {
					if !slices.Contains(current, name) {
						current = append(current, name)
					}
				}
				sort.Strings(current)
				if selected != "" && !slices.Contains(current, selected) {
					current = append(current, selected)
					sort.Strings(current)
				}
				if !slices.Equal(current, names) {
					return errors.New("profile databases changed during migration")
				}
				return nil
			}
			if err := checkProfiles(); err != nil {
				return err
			}
			if _, err := config.StripLegacy(doc.Content); err != nil {
				return &MigrationInputError{Source: configPath}
			}
			for i := range profiles {
				p := &profiles[i]
				if err := checkDatabase(p.paths); err != nil {
					return err
				}
				p.prepared, err = PrepareLegacySettings(ctx, cfg.Legacy, p.name, configPath, p.paths.Access)
				if err != nil {
					return err
				}
				p.standalone, err = prepareStandaloneProfile(p.paths, cfg.Legacy, p.name)
				if err != nil {
					return err
				}
				for _, source := range p.standalone {
					p.prepared.Values[source.key] = source.value
				}
				databasePath := p.paths.Database
				if _, statErr := os.Stat(databasePath); errors.Is(statErr, os.ErrNotExist) {
					databasePath = p.paths.LegacyDatabase
				} else if statErr != nil {
					return errors.New("cannot inspect profile database")
				}
				existing, readErr := store.ReadOnlySettingsAt(ctx, databasePath)
				if readErr != nil {
					return readErr
				}
				candidate := make(map[string]json.RawMessage, len(existing)+len(p.prepared.Values))
				for key, value := range p.prepared.Values {
					candidate[key] = value
				}
				for key, value := range existing {
					candidate[key] = value
				}
				if err := config.ValidateSettings(candidate); err != nil {
					if len(p.standalone) > 0 {
						return &MigrationInputError{Source: p.standalone[0].path}
					}
					return &MigrationInputError{Source: configPath}
				}
			}
			globalLegacy := cfg.Legacy.Behavior != nil || cfg.Legacy.BackgroundSync != nil || cfg.Legacy.SemanticSearch != nil || cfg.Legacy.Profiles != nil
			anySource := globalLegacy
			for i := range profiles {
				anySource = anySource || profiles[i].prepared.AccessExists || len(profiles[i].standalone) > 0
			}
			if !anySource {
				allMigrated := true
				for i := range profiles {
					marked, err := store.SettingsMigratedAt(profiles[i].paths.Database)
					if err != nil {
						return err
					}
					if !marked {
						allMigrated = false
					}
				}
				if allMigrated {
					return nil
				}
			}
			defer func() {
				for i := len(profiles) - 1; i >= 0; i-- {
					if profiles[i].staged != nil {
						profiles[i].staged.Rollback()
					}
					if profiles[i].db != nil {
						_ = profiles[i].db.Close()
					}
				}
			}()
			backups := make([]LegacyBackupFile, 0, len(profiles))
			for i := range profiles {
				p := &profiles[i]
				moved, err := MoveLegacyDatabase(p.paths)
				if err != nil {
					return errors.New("cannot move profile database")
				}
				if moved {
					result.Moved = append(result.Moved, p.paths)
				}
				if err := BindDatabase(p.paths); err != nil {
					return errors.New("cannot bind profile database")
				}
				p.db, err = store.Open(p.paths.Database)
				if err != nil {
					return errors.New("cannot open profile database")
				}
				migrated, err := p.db.SettingsMigrated(ctx)
				if err != nil {
					return errors.New("cannot read settings migration marker")
				}
				if migrated {
					if len(p.standalone) > 0 {
						return &LegacySettingsError{Source: p.standalone[0].path}
					}
					if err := CheckMigratedSettingsSources(ctx, profileDatabase{DB: p.db}, p.paths.Access, cfg.Legacy); err != nil {
						return err
					}
					continue
				}
				stored, err := p.db.Settings(ctx)
				if err != nil {
					return errors.New("cannot read profile settings")
				}
				if p.prepared.AccessExists {
					anySource = true
					backups = append(backups, LegacyBackupFile{Profile: p.name, Name: filepath.Base(p.paths.Access), Data: p.prepared.AccessData})
					for key := range stored {
						if strings.HasPrefix(key, "access.") {
							return &MigrationInputError{Source: p.paths.Access}
						}
					}
				}
				for _, source := range p.standalone {
					if current, exists := stored[source.key]; exists && !bytes.Equal(current, source.value) {
						return &MigrationInputError{Source: source.path}
					}
					backups = append(backups, LegacyBackupFile{Profile: p.name, Name: filepath.Base(source.path), Data: source.raw})
				}
				changes := make(map[string]json.RawMessage, len(p.prepared.Values))
				for key, value := range p.prepared.Values {
					if _, exists := stored[key]; !exists {
						changes[key] = value
					}
				}
				p.changes = changes
				p.staged, err = p.db.BeginLegacyImport(ctx, changes)
				if err != nil {
					return errors.New("cannot stage profile settings migration")
				}
			}
			// No transaction is committed until all profiles and source copies verify.
			if anySource {
				result.Backup, err = BackupLegacySettings(home, filepath.Base(configPath), doc.Content, doc.Exists, backups)
				if err != nil {
					return err
				}
			}
			if err := checkProfiles(); err != nil {
				return err
			}
			for i := range profiles {
				if profiles[i].staged != nil {
					if err := profiles[i].staged.Commit(); err != nil {
						return err
					}
				}
			}
			// Read the committed values and marker again before touching sources.
			for i := range profiles {
				p := &profiles[i]
				if p.staged == nil {
					continue
				}
				actual, err := p.db.Settings(ctx)
				if err != nil {
					return errors.New("cannot verify profile settings migration")
				}
				marked, err := p.db.SettingsMigrated(ctx)
				if err != nil || !marked {
					return errors.New("cannot verify profile settings migration")
				}
				for key, expected := range p.changes {
					if !bytes.Equal(actual[key], expected) {
						return errors.New("cannot verify profile settings migration")
					}
				}
			}
			for i := range profiles {
				p := &profiles[i]
				if p.staged == nil || len(p.changes) == 0 {
					continue
				}
				journal, err := OpenJournal(p.paths.Log)
				if err != nil {
					return errors.New("cannot open migration journal")
				}
				handler := slog.NewTextHandler(journal, nil)
				keys := make([]string, 0, len(p.changes))
				for key := range p.changes {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					definition, _ := config.LookupSetting(key)
					record := slog.NewRecord(time.Now(), slog.LevelInfo, "setting updated", 0)
					record.AddAttrs(slog.String("key", key), slog.String("source", "migration"))
					if definition.Type == "ids" || definition.Type == "paths" {
						var list []json.RawMessage
						_ = json.Unmarshal(p.changes[key], &list)
						record.AddAttrs(slog.Int("added", len(list)), slog.Int("removed", 0))
					}
					if err := handler.Handle(ctx, record); err != nil {
						_ = journal.Close()
						return errors.New("cannot write migration journal")
					}
				}
				if err := journal.Close(); err != nil {
					return errors.New("cannot close migration journal")
				}
			}
			// Close every database successfully before touching any source file.
			for i := range profiles {
				p := &profiles[i]
				if p.db == nil {
					continue
				}
				err := p.db.Close()
				p.db = nil
				if err != nil {
					return errors.New("cannot close profile database")
				}
			}
			// Detect a changed source before removing anything. A later error leaves the
			// marker in place, so a subsequent attempt refuses to re-import the source.
			for i := range profiles {
				p := &profiles[i]
				if !p.prepared.AccessExists {
					continue
				}
				now, err := PrepareLegacySettings(ctx, cfg.Legacy, p.name, configPath, p.paths.Access)
				if err != nil {
					return err
				}
				if !now.AccessExists || now.AccessMode != p.prepared.AccessMode || !bytes.Equal(now.AccessData, p.prepared.AccessData) {
					return &MigrationInputError{Source: p.paths.Access}
				}
			}
			for i := range profiles {
				p := &profiles[i]
				current, err := prepareStandaloneProfile(p.paths, cfg.Legacy, p.name)
				if err != nil {
					return err
				}
				if len(current) != len(p.standalone) {
					seen := make(map[string]bool, len(current))
					for _, source := range current {
						seen[source.path] = true
					}
					for _, source := range p.standalone {
						if !seen[source.path] {
							return &MigrationInputError{Source: source.path}
						}
					}
					for _, source := range current {
						found := false
						for _, old := range p.standalone {
							if old.path == source.path {
								found = true
								break
							}
						}
						if !found {
							return &MigrationInputError{Source: source.path}
						}
					}
					return errors.New("legacy sources changed during migration")
				}
				for j := range current {
					if current[j].path != p.standalone[j].path || current[j].mode != p.standalone[j].mode || !bytes.Equal(current[j].raw, p.standalone[j].raw) {
						return &MigrationInputError{Source: p.standalone[j].path}
					}
				}
			}
			type removedSource struct {
				path string
				data []byte
				mode os.FileMode
			}
			var removed []removedSource
			restore := func() error {
				for _, source := range removed {
					if err := restoreLegacyAccess(source.path, source.data, source.mode); err != nil {
						return errors.New("cannot restore legacy sources")
					}
				}
				return nil
			}
			removeSource := func(path string, data []byte, mode os.FileMode) error {
				if err := os.Remove(path); err != nil {
					return &MigrationInputError{Source: path}
				}
				removed = append(removed, removedSource{path: path, data: data, mode: mode})
				if err := syncDir(filepath.Dir(path)); err != nil {
					return &MigrationInputError{Source: path}
				}
				return nil
			}
			for i := range profiles {
				p := &profiles[i]
				if p.prepared.AccessExists {
					if err := removeSource(p.paths.Access, p.prepared.AccessData, p.prepared.AccessMode); err != nil {
						if restoreErr := restore(); restoreErr != nil {
							return restoreErr
						}
						return err
					}
				}
				for _, source := range p.standalone {
					if err := removeSource(source.path, source.raw, source.mode); err != nil {
						if restoreErr := restore(); restoreErr != nil {
							return restoreErr
						}
						return err
					}
				}
			}
			if err := doc.RemoveLegacy(); err != nil {
				if restoreErr := restore(); restoreErr != nil {
					return restoreErr
				}
				return &MigrationInputError{Source: configPath}
			}
			result.Migrated = anySource
			return nil
		})
	})
	if err != nil {
		var ce *config.Error
		var input *MigrationInputError
		var legacy *LegacySettingsError
		var conflict *FileConflictError
		switch {
		case errors.As(err, &input), errors.As(err, &legacy), errors.As(err, &conflict):
			return result, err
		case errors.As(err, &ce):
			return result, &MigrationInputError{Source: configPath}
		case errors.Is(err, ErrAlreadyRunning):
			return result, ErrAlreadyRunning
		case errors.Is(err, ErrDatabaseInUse):
			return result, ErrDatabaseInUse
		case errors.Is(err, context.Canceled):
			return result, context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			return result, context.DeadlineExceeded
		default:
			return result, errors.New("settings migration failed")
		}
	}
	return result, nil
}

// withMigrationLocks takes all profile and database locks in stable order.
func withMigrationLocks(profiles []migrationProfile, index int, action func() error) error {
	if index == len(profiles) {
		return action()
	}
	paths := profiles[index].paths
	return WithProfileLock(paths, func() error {
		release, err := LockDatabase(paths)
		if err != nil {
			return err
		}
		defer release()
		if err := CheckDatabaseBinding(paths); err != nil {
			return errors.New("cannot verify profile database binding")
		}
		return withMigrationLocks(profiles, index+1, action)
	})
}

// restoreLegacyAccess never replaces an entry created after migration began.
func restoreLegacyAccess(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".access-restore-*")
	if err != nil {
		return errors.New("cannot restore legacy access source")
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode.Perm()); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("cannot restore legacy access source")
	}
	if err := os.Link(tmp, path); err != nil {
		return errors.New("cannot restore legacy access source")
	}
	if err := syncDir(dir); err != nil {
		return errors.New("cannot restore legacy access source")
	}
	return nil
}
