package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// moveLegacyHome moves the profile data of the previous default directory to the
// current one, once per process. It applies only to the default: a home in
// config.toml or in TG_CLI_HOME is the owner's choice and stays where it is. A
// refusal (a profile in both places, a daemon still running in the old place) is
// a failure with exit code 1, because starting on an empty directory would look
// like a lost account.
func (a *app) moveLegacyHome(cmd *cobra.Command) error {
	if a.homeChecked {
		return nil
	}
	a.homeChecked = true
	if a.deps.LegacyHome == nil || a.config.HomeSet || a.deps.Getenv("TG_CLI_HOME") != "" {
		return nil
	}
	legacy := a.deps.LegacyHome()
	if legacy == "" {
		return nil
	}
	files := a.config.Files
	res, err := daemon.MigrateLegacyHome(cmd.Context(), legacy, a.config.Home, daemon.HomeMoveOptions{
		Files:  daemon.FileNames{Database: files.Database, Session: files.Session, Log: files.Log, Deletions: files.Deletions, Access: files.Access},
		DBRoot: filepath.Join(filepath.Dir(a.deps.ConfigPath), "db"),
	})
	if err != nil {
		return fail(exitcode.Failure, "%v", err)
	}
	if res.Moved {
		message := "note: moved the data directory from " + legacy + " to " + a.config.Home
		if res.Backup != "" {
			message += "; copies of the secret files are in " + res.Backup + ", delete them when the tool works"
		}
		a.warn(cmd, "%s", message)
	}
	return nil
}
