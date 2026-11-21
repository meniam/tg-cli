package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"

	"tg-cli/internal/config"
	"tg-cli/internal/rpc"
)

// OfflineSettingsWrite migrates legacy data, then performs one guarded local
// settings operation while both profile locks and the config lock are held.
func OfflineSettingsWrite(ctx context.Context, configPath, home, profile, method string, params json.RawMessage) (any, error) {
	switch method {
	case rpc.MethodSettingsSet, rpc.MethodSettingsUnset, rpc.MethodSettingsAdd, rpc.MethodSettingsRemove, rpc.MethodSettingsTokenInit, rpc.MethodSettingsTokenChange:
	default:
		return nil, rpc.NewError(rpc.CodeInvalidParams, "unsupported offline settings operation")
	}
	if _, err := MigrateSettings(ctx, configPath, home, profile); err != nil {
		return nil, err
	}
	var result any
	err := config.WithLegacyConfigLock(configPath, func(doc *config.LockedLegacyConfig) error {
		cfg := doc.Config
		if home == "" {
			home = cfg.Home
		}
		if profile == "" {
			profile = cfg.DefaultProfile
		}
		files := FileNames{Database: cfg.Files.Database, Session: cfg.Files.Session, Log: cfg.Files.Log, Deletions: cfg.Files.Deletions, Access: cfg.Files.Access}
		paths, err := ResolvePaths(home, filepath.Join(filepath.Dir(configPath), "db"), profile, files)
		if err != nil {
			return errors.New("invalid profile paths")
		}
		return WithProfileDatabase(paths, func(db ProfileDatabase) error {
			if err := CheckMigratedSettingsSources(ctx, db, paths.Access, cfg.Legacy); err != nil {
				return err
			}
			raw, ok := db.(profileDatabase)
			if !ok {
				return errors.New("profile database unavailable")
			}
			journal, err := OpenJournal(paths.Log)
			if err != nil {
				return errors.New("cannot open settings journal")
			}
			defer journal.Close()
			d := &daemon{store: raw.DB, opts: Options{Paths: paths, ConfigPath: configPath}, settingsSource: "offline", log: slog.New(slog.NewTextHandler(journal, nil)), ownerSecrets: NewOwnerSecrets(raw.DB)}
			d.refreshAccess(ctx)
			d.initSettings(ctx)
			if d.legacyAccessPresent() {
				return &LegacySettingsError{Source: paths.Access}
			}
			switch method {
			case rpc.MethodSettingsSet:
				result, err = d.settingsSet(ctx, params)
			case rpc.MethodSettingsUnset:
				result, err = d.settingsUnset(ctx, params)
			case rpc.MethodSettingsAdd:
				result, err = d.settingsListChange(ctx, params, true)
			case rpc.MethodSettingsRemove:
				result, err = d.settingsListChange(ctx, params, false)
			case rpc.MethodSettingsTokenInit:
				result, err = d.settingsTokenInit(ctx, params)
			case rpc.MethodSettingsTokenChange:
				result, err = d.settingsTokenChange(ctx, params)
			}
			if err != nil {
				return err
			}
			switch value := result.(type) {
			case rpc.SettingsWriteResult:
				value.Applied = "next_start"
				result = value
			case rpc.SettingsBatchWriteResult:
				for i := range value.Settings {
					value.Settings[i].Applied = "next_start"
				}
				result = value
			}
			return nil
		})
	})
	if err != nil {
		var input *MigrationInputError
		var legacy *LegacySettingsError
		var rpcErr *rpc.Error
		switch {
		case errors.As(err, &input), errors.As(err, &legacy), errors.As(err, &rpcErr):
			return nil, err
		case errors.Is(err, ErrAlreadyRunning):
			return nil, ErrAlreadyRunning
		case errors.Is(err, ErrDatabaseInUse):
			return nil, ErrDatabaseInUse
		case errors.Is(err, context.Canceled):
			return nil, context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			return nil, context.DeadlineExceeded
		default:
			return nil, errors.New("offline settings operation failed")
		}
	}
	return result, nil
}
