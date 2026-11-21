package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"tg-cli/internal/config"
	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/sessionstr"
)

type sessionResult struct {
	Profile    string   `json:"profile"`
	Path       string   `json:"path,omitempty"`
	Format     string   `json:"format"`
	UserID     int64    `json:"user_id,omitempty"`
	DC         int      `json:"dc"`
	KeyID      string   `json:"key_id"`
	Client     string   `json:"client,omitempty"`
	Replaced   bool     `json:"replaced"`
	Duplicates []string `json:"duplicates,omitempty"`
}

func (a *app) sessionCommand() *cobra.Command {
	group := &cobra.Command{Use: "session", Short: "Transfer a session"}
	exp := a.annotate(&cobra.Command{Use: "export", Short: "Export a session", Args: cobra.NoArgs, RunE: a.sessionExport}, dangerous, commandSpec{Safety: safetyRead, Result: typeOf[sessionResult]()})
	exp.Flags().String("out", "", "new owner-only output file")
	exp.Flags().Bool("yes", false, "confirm the export")
	imp := a.annotate(&cobra.Command{Use: "import", Short: "Import a session", Args: cobra.NoArgs, RunE: a.sessionImport}, dangerous, commandSpec{Safety: safetyLocalWrite, Result: typeOf[sessionResult]()})
	imp.Flags().String("from", "", "input file, or - for stdin")
	imp.Flags().Bool("force", false, "replace the same account session")
	imp.Flags().Bool("yes", false, "confirm the import")
	group.AddCommand(exp, imp)
	return group
}
func (a *app) sessionExport(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	out, _ := cmd.Flags().GetString("out")
	if !yes || out == "" || out == "-" {
		return fail(exitcode.Usage, "--yes and --out are required")
	}
	if err := a.guardLegacyHomeSettings(); err != nil {
		return err
	}
	profile, p, err := a.profile(cmd)
	if err != nil {
		return err
	}
	absoluteOut, err := filepath.Abs(out)
	if err != nil {
		return fail(exitcode.Usage, "invalid output path")
	}
	if _, err := daemon.MigrateSettings(cmd.Context(), a.deps.ConfigPath, filepath.Dir(p.Dir), profile); err != nil {
		return a.daemonStartError(err)
	}
	var export rpc.SessionExportResult
	c, err := a.connect(cmd.Context(), cmd)
	if err == nil {
		defer c.Close()
		if err = a.call(cmd.Context(), cmd, c, rpc.MethodSessionExport, rpc.SessionExportParams{Path: absoluteOut, Yes: true}, &export); err != nil {
			return err
		}
	} else {
		var ce *cmdError
		if !errors.As(err, &ce) || ce.exit != exitcode.DaemonNotRunning {
			return err
		}
		err = daemon.WithProfileDatabase(p, func(db daemon.ProfileDatabase) error {
			stored, e := db.Settings(cmd.Context())
			if e != nil {
				return fail(exitcode.Failure, "cannot read profile settings")
			}
			migrated, e := db.SettingsMigrated(cmd.Context())
			if e != nil {
				return fail(exitcode.Failure, "cannot read profile settings")
			}
			client := ""
			if !migrated {
				client = a.config.Profiles[profile].Client
			}
			if value, ok := stored["profile.client"]; ok {
				if json.Unmarshal(value, &client) != nil {
					return fail(exitcode.Failure, "cannot read profile settings")
				}
			}
			raw, e := os.ReadFile(p.Session)
			if e != nil {
				return fail(exitcode.NotAuthorized, "session is unavailable")
			}
			_, uid, _, e := sessionstr.Inspect(raw)
			if e != nil || uid == 0 {
				return fail(exitcode.NotAuthorized, "session is unavailable")
			}
			value, d, e := sessionstr.Encode(raw, client, 0)
			if e != nil {
				return fail(exitcode.NotAuthorized, "session is unavailable")
			}
			if e = daemon.ExportSessionFile(absoluteOut, value); e != nil {
				return fail(exitcode.Usage, "cannot create export file")
			}
			export = rpc.SessionExportResult{Format: d.Format, DC: d.DC, UserID: d.UserID, KeyID: d.KeyID, Client: d.Client}
			return nil
		})
		if err != nil {
			if errors.As(err, &ce) {
				return err
			}
			return fail(exitcode.Failure, "cannot lock profile; stop the daemon and retry")
		}
	}
	result := sessionResult{Profile: profile, Format: export.Format, DC: export.DC, UserID: export.UserID, KeyID: export.KeyID, Client: export.Client}
	a.warn(cmd, "A session string grants full account access. Never run two copies of a key at once, including on different machines.")
	result.Path = out
	return a.success(cmd, result)
}
func (a *app) sessionImport(cmd *cobra.Command, _ []string) error {
	yes, _ := cmd.Flags().GetBool("yes")
	from, _ := cmd.Flags().GetString("from")
	force, _ := cmd.Flags().GetBool("force")
	if !yes || from == "" {
		return fail(exitcode.Usage, "--yes and --from are required")
	}
	profile, p, err := a.profile(cmd)
	if err != nil {
		return err
	}
	var text string
	if from == "-" {
		b, e := io.ReadAll(io.LimitReader(a.deps.Stdin, sessionstr.MaxSize+1))
		if e != nil || len(b) > sessionstr.MaxSize {
			return fail(exitcode.Usage, "cannot read session input")
		}
		text = string(b)
	} else {
		text, err = secretFile(from, sessionstr.MaxSize)
		if err != nil {
			return err
		}
	}
	data, err := sessionstr.Decode(text)
	if errors.Is(err, sessionstr.ErrAmbiguous) {
		return fail(exitcode.Usage, "ambiguous foreign session format")
	}
	if err != nil || (data.Client != "" && !presetName.MatchString(data.Client)) {
		return fail(exitcode.Usage, "invalid session string")
	}
	result := sessionResult{Profile: profile, Format: data.Format, UserID: data.UserID, DC: data.DC, KeyID: data.KeyID}
	err = daemon.WithProfileDatabasePreflight(p, func() error {
		old, readErr := os.ReadFile(p.Session)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return fail(exitcode.Failure, "cannot read profile session")
		}
		_, dbErr := os.Stat(p.Database)
		if dbErr != nil && !errors.Is(dbErr, os.ErrNotExist) {
			return fail(exitcode.Failure, "cannot inspect profile")
		}
		_, legacyErr := os.Stat(p.LegacyDatabase)
		if legacyErr != nil && !errors.Is(legacyErr, os.ErrNotExist) {
			return fail(exitcode.Failure, "cannot inspect profile")
		}
		result.Replaced = readErr == nil || dbErr == nil || legacyErr == nil
		if result.Replaced {
			if !force {
				return fail(exitcode.Usage, "profile is not empty; use --force for the same account")
			}
			_, oldID, _, err := sessionstr.Inspect(old)
			if err != nil || oldID == 0 || data.UserID == 0 || oldID != data.UserID {
				return fail(exitcode.Usage, "account cannot be verified; use a new profile")
			}
		}
		var err error
		result.Duplicates, err = daemon.DuplicateProfiles(p, data.KeyID, false)
		if err != nil {
			return fail(exitcode.Failure, "cannot inspect profiles")
		}
		return nil
	}, func(db daemon.ProfileDatabase) error {
		if err := daemon.CheckMigratedSettingsSources(cmd.Context(), db, p.Access, a.config.Legacy); err != nil {
			var legacy *daemon.LegacySettingsError
			var input *daemon.MigrationInputError
			if errors.As(err, &legacy) || errors.As(err, &input) {
				return fail(exitcode.Usage, "%v", err)
			}
			return fail(exitcode.Failure, "cannot inspect legacy settings")
		}
		writeSession := func() error {
			if e := daemon.WriteSession(p, data.Session); e != nil {
				return fail(exitcode.Failure, "cannot write session")
			}
			return nil
		}
		if data.Client == "" {
			return writeSession()
		}
		stored, e := db.Settings(cmd.Context())
		if e != nil {
			return fail(exitcode.Failure, "cannot read profile settings")
		}
		client := ""
		var journal *daemon.Journal
		value, exists := stored["profile.client"]
		if exists {
			if json.Unmarshal(value, &client) != nil {
				return fail(exitcode.Failure, "cannot read profile settings")
			}
		} else {
			migrated, err := db.SettingsMigrated(cmd.Context())
			if err != nil {
				return fail(exitcode.Failure, "cannot read profile settings")
			}
			client = data.Client
			if !migrated {
				legacy, found, err := config.LegacyProfileClient(a.deps.ConfigPath, profile)
				if err != nil {
					return fail(exitcode.Failure, "cannot read client preset")
				}
				if found {
					client = legacy
				}
			}
			journal, e = daemon.OpenJournal(p.Log)
			if e != nil {
				return fail(exitcode.Failure, "cannot open daemon journal")
			}
			defer journal.Close()
			encoded, _ := json.Marshal(client)
			if e := db.SetSettings(cmd.Context(), map[string]json.RawMessage{"profile.client": encoded}); e != nil {
				return fail(exitcode.Failure, "cannot save client preset")
			}
		}
		if e := writeSession(); e != nil {
			if !exists {
				rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				rollbackErr := db.DeleteSetting(rollbackCtx, "profile.client")
				cancel()
				if rollbackErr != nil {
					return fail(exitcode.Failure, "cannot restore profile settings")
				}
			}
			return e
		}
		if journal != nil {
			slog.New(slog.NewTextHandler(journal, nil)).Info("setting updated", "key", "profile.client", "source", "offline")
		}
		if client != data.Client {
			a.warn(cmd, "Imported client preset differs from this profile.")
		}
		return nil
	})
	if err != nil {
		var ce *cmdError
		if errors.As(err, &ce) {
			return err
		}
		return fail(exitcode.Failure, "cannot lock profile; run `tg daemon stop` first")
	}
	if result.Replaced {
		a.warn(cmd, "The old key remains active on Telegram; terminate it in the device list.")
	}
	return a.success(cmd, result)
}
