package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"

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
	if err := a.migrateProfiles(); err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	out, _ := cmd.Flags().GetString("out")
	if !yes || out == "" || out == "-" {
		return fail(exitcode.Usage, "--yes and --out are required")
	}
	profile, p, err := a.profile(cmd)
	if err != nil {
		return err
	}
	absoluteOut, err := filepath.Abs(out)
	if err != nil {
		return fail(exitcode.Usage, "invalid output path")
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
		err = daemon.WithProfileLock(p, func() error {
			raw, e := os.ReadFile(p.Session)
			if e != nil {
				return fail(exitcode.NotAuthorized, "session is unavailable")
			}
			_, uid, _, e := sessionstr.Inspect(raw)
			if e != nil || uid == 0 {
				return fail(exitcode.NotAuthorized, "session is unavailable")
			}
			value, d, e := sessionstr.Encode(raw, a.config.Profiles[profile].Client, 0)
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
			return fail(exitcode.Usage, "cannot lock profile; stop the daemon and retry")
		}
	}
	result := sessionResult{Profile: profile, Format: export.Format, DC: export.DC, UserID: export.UserID, KeyID: export.KeyID, Client: export.Client}
	a.warn(cmd, "A session string grants full account access. Never run two copies of a key at once, including on different machines.")
	result.Path = out
	return a.success(cmd, result)
}
func (a *app) sessionImport(cmd *cobra.Command, _ []string) error {
	if err := a.migrateProfiles(); err != nil {
		return err
	}
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
	err = daemon.WithProfileLock(p, func() error {
		old, e := os.ReadFile(p.Session)
		if e != nil && !errors.Is(e, os.ErrNotExist) {
			return fail(exitcode.Failure, "cannot read profile session")
		}
		// A database of the old layout is not moved yet, and an import without
		// --force must not pass over it either: the history it holds belongs to
		// the account the user is about to replace.
		_, dbErr := os.Stat(p.Database)
		if dbErr != nil && !errors.Is(dbErr, os.ErrNotExist) {
			return fail(exitcode.Failure, "cannot inspect profile")
		}
		_, legacyErr := os.Stat(p.LegacyDatabase)
		if legacyErr != nil && !errors.Is(legacyErr, os.ErrNotExist) {
			return fail(exitcode.Failure, "cannot inspect profile")
		}
		result.Replaced = e == nil || dbErr == nil || legacyErr == nil
		if result.Replaced {
			if !force {
				return fail(exitcode.Usage, "profile is not empty; use --force for the same account")
			}
			_, oldID, _, e := sessionstr.Inspect(old)
			if e != nil || oldID == 0 || data.UserID == 0 || oldID != data.UserID {
				return fail(exitcode.Usage, "account cannot be verified; use a new profile")
			}
		}
		result.Duplicates, e = daemon.DuplicateProfiles(p, data.KeyID, false)
		if e != nil {
			return fail(exitcode.Failure, "cannot inspect profiles")
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
		updated, e := config.UpdateProfileWith(a.deps.ConfigPath, profile, map[string]string{"client": data.Client}, true, writeSession)
		if e != nil {
			var ce *cmdError
			if errors.As(e, &ce) {
				return e
			}
			var configErr *config.Error
			if errors.As(e, &configErr) {
				return fail(exitcode.Usage, "%v", e)
			}
			return fail(exitcode.Failure, "cannot save client preset")
		}
		a.config = updated
		if a.config.Profiles[profile].Client != data.Client {
			a.warn(cmd, "Imported client preset differs from this profile.")
		}
		return nil
	})
	if err != nil {
		var ce *cmdError
		if errors.As(err, &ce) {
			return err
		}
		return fail(exitcode.Usage, "cannot lock profile; run `tg daemon stop` first")
	}
	if result.Replaced {
		a.warn(cmd, "The old key remains active on Telegram; terminate it in the device list.")
	}
	return a.success(cmd, result)
}
