package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

func (a *app) saveProfile(paths daemon.Paths, changes map[string]string, onlyMissing bool) error {
	cfg, err := config.UpdateProfile(a.deps.ConfigPath, filepath.Base(paths.Dir), changes, onlyMissing)
	if err != nil {
		var invalid *config.Error
		if errors.As(err, &invalid) {
			return fail(exitcode.Usage, "%v", err)
		}
		return fail(exitcode.Failure, "cannot save profile settings")
	}
	a.config = cfg
	return nil
}

func (a *app) migrateProfiles() error {
	home := a.deps.Getenv("TG_CLI_HOME")
	if home == "" {
		home = a.config.Home
	}
	result, err := config.MigrateProfiles(a.deps.ConfigPath, home)
	for _, warning := range result.Warnings {
		fmt.Fprintln(a.deps.Stderr, "warning: "+warning)
	}
	if err != nil {
		var invalid *config.Error
		if errors.As(err, &invalid) {
			return fail(exitcode.Usage, "%v", err)
		}
		return fail(exitcode.Failure, "migrate profile settings: %v", err)
	}
	if result.Backup != "" || len(result.Warnings) > 0 {
		cfg, err := config.Load(a.deps.ConfigPath)
		if err != nil {
			return fail(exitcode.Failure, "reload config: %v", err)
		}
		a.config = cfg
	}
	return nil
}
