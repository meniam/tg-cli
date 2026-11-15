package cli

import (
	"errors"
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
