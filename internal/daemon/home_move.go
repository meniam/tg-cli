package daemon

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"tg-cli/internal/rpc"
)

// HomeMoveError is a failure of the move of the data directory. It is an
// environment or state problem, not a wrong configuration, so the caller reports
// exit code 1.
type HomeMoveError struct {
	// Message says what stopped the move and what the owner can do.
	Message string
	// Err is the underlying error, if any.
	Err error
}

func (e *HomeMoveError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *HomeMoveError) Unwrap() error { return e.Err }

// ExitCode identifies a failure that is not a configuration error.
func (e *HomeMoveError) ExitCode() int { return 1 }

// HomeMoveOptions tune MigrateLegacyHome. The zero value is the production
// behavior; the function fields are the places where tests substitute the world.
type HomeMoveOptions struct {
	// Files names the secret profile files that are copied to the backup first.
	// An empty name falls back to the default one.
	Files FileNames
	// DBRoot is the db directory next to the configuration file. The record of
	// the data directory next to each profile database is rewritten to the new
	// directory; empty means no record is touched.
	DBRoot string
	// Now supplies the time of the backup directory name; time.Now when nil.
	Now func() time.Time
	// Ping asks the socket of a daemon for its process id; Ping when nil.
	Ping func(ctx context.Context, socket string) (int, error)
	// Rename moves a directory; os.Rename when nil. A cross-device error makes
	// the move copy the directory and verify the copy.
	Rename func(oldPath, newPath string) error
}

// HomeMoveResult tells what MigrateLegacyHome did. A call with nothing to do
// returns the zero value.
type HomeMoveResult struct {
	// Moved reports that the data directory changed place.
	Moved bool
	// Profiles lists the moved profile directories.
	Profiles []string
	// Backup is the directory with the copies of the secret files.
	Backup string
}

// homeServiceDirs are the directories that earlier migrations left in the data
// directory. They move with the profiles, under the same names.
var homeServiceDirs = []string{".config-migration-", ".db-backup-", ".db-move-backup-"}

const sessionStartLock = ".session-start.lock"

// MigrateLegacyHome moves the profile directories of the previous default data
// directory, oldHome, into newHome. The caller decides that the move applies:
// no home in the configuration and no TG_CLI_HOME. The function does nothing
// when oldHome does not exist, is the same directory as newHome or holds no
// profile directory, so a second call after a move is a no-op.
//
// It refuses, and changes nothing, in two cases: a profile directory with the
// same name exists in both places, and a daemon still runs in oldHome (it holds
// a socket there that no new process would find). Otherwise it copies the secret
// files to <newHome>/.home-move-backup-<time>/<profile>/, renames each profile
// directory (a copy with a SHA-256 check across volumes), deletes the stale
// socket and lock, points the record of the data directory of the profile
// database to the new place, moves the service directories of earlier
// migrations and removes oldHome when it is empty. A failure on the way undoes
// every step already made.
func MigrateLegacyHome(ctx context.Context, oldHome, newHome string, opts HomeMoveOptions) (HomeMoveResult, error) {
	if oldHome == "" || newHome == "" || filepath.Clean(oldHome) == filepath.Clean(newHome) {
		return HomeMoveResult{}, nil
	}
	info, err := os.Stat(oldHome)
	if errors.Is(err, os.ErrNotExist) {
		return HomeMoveResult{}, nil
	}
	if err != nil {
		return HomeMoveResult{}, &HomeMoveError{Message: "inspect the previous data directory " + oldHome, Err: err}
	}
	if !info.IsDir() || sameDir(oldHome, newHome) {
		return HomeMoveResult{}, nil
	}
	profiles, services, err := legacyEntries(oldHome)
	if err != nil {
		return HomeMoveResult{}, err
	}
	if len(profiles) == 0 {
		return HomeMoveResult{}, nil
	}
	m := &homeMover{oldHome: oldHome, newHome: newHome, opts: opts}
	if m.opts.Now == nil {
		m.opts.Now = time.Now
	}
	if m.opts.Ping == nil {
		m.opts.Ping = Ping
	}
	if m.opts.Rename == nil {
		m.opts.Rename = os.Rename
	}
	return m.run(ctx, profiles, services)
}

// legacyEntries lists the profile directories and the service directories of the
// old data directory, in name order. Only real directories count: a symlink or a
// file under a profile name is left alone.
func legacyEntries(oldHome string) (profiles, services []string, err error) {
	entries, err := os.ReadDir(oldHome)
	if err != nil {
		return nil, nil, &HomeMoveError{Message: "read the previous data directory " + oldHome, Err: err}
	}
	for _, entry := range entries {
		if !entry.Type().IsDir() {
			continue
		}
		name := entry.Name()
		if profileName.MatchString(name) && name != ReservedProfile {
			profiles = append(profiles, name)
			continue
		}
		for _, prefix := range homeServiceDirs {
			if strings.HasPrefix(name, prefix) {
				services = append(services, name)
				break
			}
		}
	}
	sort.Strings(profiles)
	sort.Strings(services)
	return profiles, services, nil
}

type homeMover struct {
	oldHome, newHome string
	opts             HomeMoveOptions
	// undo holds the steps to reverse, in the order they were made.
	undo []func() error
	// commit holds the deletions that run only after every step has succeeded:
	// the sources of the copies made across volumes.
	commit []func() error
	backup string
}

// record is the data directory record of one profile database that has to follow
// the profile directory.
type record struct {
	path     string
	original []byte
}

func (m *homeMover) run(ctx context.Context, profiles, services []string) (HomeMoveResult, error) {
	if err := os.MkdirAll(m.newHome, 0o700); err != nil {
		return HomeMoveResult{}, &HomeMoveError{Message: "create the data directory " + m.newHome, Err: err}
	}
	// The lock of the session start also keeps a daemon from starting in the new
	// directory while the profiles arrive there.
	release, err := acquireLock(filepath.Join(m.newHome, sessionStartLock))
	if errors.Is(err, ErrAlreadyRunning) {
		return HomeMoveResult{}, &HomeMoveError{Message: "another tg process is starting a daemon or moving the data directory in " + m.newHome + ": run the command again in a moment"}
	}
	if err != nil {
		return HomeMoveResult{}, &HomeMoveError{Message: "lock the data directory " + m.newHome, Err: err}
	}
	defer release()

	if err := m.preflight(ctx, profiles, services); err != nil {
		return HomeMoveResult{}, err
	}
	records := map[string]record{}
	for _, name := range profiles {
		rec, err := m.planRecord(name)
		if err != nil {
			return HomeMoveResult{}, err
		}
		if rec.path != "" {
			records[name] = rec
		}
	}
	if err := m.copySecrets(profiles); err != nil {
		return HomeMoveResult{}, m.abort(err)
	}
	for _, name := range profiles {
		if err := m.moveProfile(name, records[name]); err != nil {
			return HomeMoveResult{}, m.abort(err)
		}
	}
	for _, name := range services {
		if err := m.moveDir(filepath.Join(m.oldHome, name), filepath.Join(m.newHome, name)); err != nil {
			return HomeMoveResult{}, m.abort(err)
		}
	}
	result := HomeMoveResult{Moved: true, Profiles: profiles, Backup: m.backup}
	// From here nothing is undone. The socket and the lock of a daemon that is
	// gone are only files that name a dead process, and the old lock file is only
	// a lock: a daemon in the old place was ruled out above. Failing to remove
	// them costs nothing, so the errors are not reported.
	for _, name := range profiles {
		for _, stale := range []string{"daemon.sock", "daemon.lock"} {
			_ = os.Remove(filepath.Join(m.newHome, name, stale))
		}
	}
	_ = os.Remove(filepath.Join(m.oldHome, sessionStartLock))
	for _, step := range m.commit {
		if err := step(); err != nil {
			return result, &HomeMoveError{Message: "the data directory was copied to " + m.newHome + " and checked, but the old copy in " + m.oldHome + " could not be removed: remove it by hand", Err: err}
		}
	}
	if left, err := os.ReadDir(m.oldHome); err == nil && len(left) == 0 {
		_ = os.Remove(m.oldHome)
	}
	return result, nil
}

// preflight refuses the move when it would hide or break something. It reads
// only; every refusal leaves the disk as it was.
func (m *homeMover) preflight(ctx context.Context, profiles, services []string) error {
	var clash []string
	for _, name := range append(append([]string{}, profiles...), services...) {
		if _, err := os.Lstat(filepath.Join(m.newHome, name)); err == nil {
			clash = append(clash, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return &HomeMoveError{Message: "inspect " + filepath.Join(m.newHome, name), Err: err}
		}
	}
	if len(clash) > 0 {
		return &HomeMoveError{Message: fmt.Sprintf("cannot move the data directory: %s exist in both %s (previous default) and %s (new default); nothing was changed. Keep the one you need: move or remove the other by hand, then run the command again, or set home in config.toml to the directory you use",
			strings.Join(clash, ", "), m.oldHome, m.newHome)}
	}
	for _, name := range profiles {
		if err := m.checkNoDaemon(ctx, name); err != nil {
			return err
		}
	}
	return nil
}

// checkNoDaemon refuses a profile whose daemon is alive in the old place. The
// socket answers and the lock is held while it runs; either one is enough.
func (m *homeMover) checkNoDaemon(ctx context.Context, name string) error {
	dir := filepath.Join(m.oldHome, name)
	busy := &HomeMoveError{Message: fmt.Sprintf("a daemon of profile %q is running in the previous data directory %s: stop it with the previous binary (`tg daemon stop`), then run the command again; nothing was changed", name, dir)}
	socket := filepath.Join(dir, "daemon.sock")
	if _, err := os.Lstat(socket); err == nil {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := m.opts.Ping(pingCtx, socket)
		cancel()
		switch {
		case err == nil:
			return busy
		case !rpc.IsNotRunning(err):
			return &HomeMoveError{Message: fmt.Sprintf("cannot tell whether a daemon runs in %s: stop it with the previous binary (`tg daemon stop`) if it does; nothing was changed", dir), Err: err}
		}
	}
	lock := filepath.Join(dir, "daemon.lock")
	if _, err := os.Lstat(lock); err == nil {
		release, err := acquireLock(lock)
		if errors.Is(err, ErrAlreadyRunning) {
			return busy
		}
		if err != nil {
			return &HomeMoveError{Message: "inspect the lock " + lock, Err: err}
		}
		release()
	}
	return nil
}

// planRecord finds the data directory record of the profile database when it
// names the old profile directory. A record of another directory is left for
// the start to report.
func (m *homeMover) planRecord(profile string) (record, error) {
	if m.opts.DBRoot == "" {
		return record{}, nil
	}
	path := filepath.Join(m.opts.DBRoot, profile, "data-dir")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return record{}, nil
	}
	if err != nil {
		return record{}, &HomeMoveError{Message: "read the data directory record " + path, Err: err}
	}
	value := strings.TrimSpace(string(data))
	if value == "" || !sameDir(value, filepath.Join(m.oldHome, profile)) {
		return record{}, nil
	}
	return record{path: path, original: data}, nil
}

// secretNames are the profile files whose copy goes to the backup.
func (m *homeMover) secretNames() []string {
	defaults := DefaultFileNames()
	pick := func(name, fallback string) string {
		if name == "" {
			return fallback
		}
		return name
	}
	return []string{pick(m.opts.Files.Session, defaults.Session), pick(m.opts.Files.Access, defaults.Access), pick(m.opts.Files.Deletions, defaults.Deletions)}
}

// copySecrets writes a copy of the secret files of every profile to
// <newHome>/.home-move-backup-<time>/<profile>/, before anything moves. The
// copy is the way back when the rename or a later start goes wrong.
func (m *homeMover) copySecrets(profiles []string) error {
	type pair struct{ src, dst string }
	var files []pair
	base := ""
	for attempt := 0; attempt < 100 && base == ""; attempt++ {
		name := ".home-move-backup-" + m.opts.Now().Format("20060102T150405")
		if attempt > 0 {
			name += "-" + strconv.Itoa(attempt)
		}
		if _, err := os.Lstat(filepath.Join(m.newHome, name)); errors.Is(err, os.ErrNotExist) {
			base = filepath.Join(m.newHome, name)
		}
	}
	if base == "" {
		return &HomeMoveError{Message: "choose a backup directory name in " + m.newHome}
	}
	for _, profile := range profiles {
		for _, name := range m.secretNames() {
			src := filepath.Join(m.oldHome, profile, name)
			info, err := os.Lstat(src)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return &HomeMoveError{Message: "inspect " + src, Err: err}
			}
			if info.Mode().IsRegular() {
				files = append(files, pair{src, filepath.Join(base, profile, name)})
			}
		}
	}
	if len(files) == 0 {
		return nil
	}
	if err := os.Mkdir(base, 0o700); err != nil {
		return &HomeMoveError{Message: "create the backup directory " + base, Err: err}
	}
	m.backup = base
	for _, f := range files {
		if err := os.MkdirAll(filepath.Dir(f.dst), 0o700); err != nil {
			return &HomeMoveError{Message: "create the backup directory " + filepath.Dir(f.dst), Err: err}
		}
		if err := copyVerified(f.src, f.dst, 0o600); err != nil {
			return &HomeMoveError{Message: "back up " + f.src, Err: err}
		}
	}
	return nil
}

// moveProfile moves one profile directory and points the record of its database
// to the new directory.
func (m *homeMover) moveProfile(name string, rec record) error {
	oldDir, newDir := filepath.Join(m.oldHome, name), filepath.Join(m.newHome, name)
	if err := m.moveDir(oldDir, newDir); err != nil {
		return err
	}
	if rec.path != "" {
		if err := replaceFile(rec.path, []byte(newDir+"\n")); err != nil {
			return &HomeMoveError{Message: "point the data directory record " + rec.path + " to " + newDir, Err: err}
		}
		m.undo = append(m.undo, func() error { return replaceFile(rec.path, rec.original) })
	}
	return nil
}

// moveDir renames src to dst. When the two sit on different volumes it copies the
// tree, checks every file by SHA-256 and leaves the removal of src for the end,
// so that a later failure still finds the original.
func (m *homeMover) moveDir(src, dst string) error {
	err := m.opts.Rename(src, dst)
	if err == nil {
		m.undo = append(m.undo, func() error { return os.Rename(dst, src) })
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return &HomeMoveError{Message: "move " + src + " to " + dst, Err: err}
	}
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".home-move-"+strconv.Itoa(os.Getpid())+".tmp")
	_ = os.RemoveAll(tmp)
	if err := copyTree(src, tmp, true); err != nil {
		_ = os.RemoveAll(tmp)
		return &HomeMoveError{Message: "copy " + src + " to " + dst, Err: err}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return &HomeMoveError{Message: "finish the copy of " + src + " at " + dst, Err: err}
	}
	m.undo = append(m.undo, func() error { return os.RemoveAll(dst) })
	m.commit = append(m.commit, func() error { return os.RemoveAll(src) })
	return nil
}

// abort reverses the steps made so far and says what state is left. The backup
// of the secrets is deleted when everything is back, and kept when it is not.
func (m *homeMover) abort(cause error) error {
	var failed []error
	for i := len(m.undo) - 1; i >= 0; i-- {
		if err := m.undo[i](); err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		msg := "the move of the data directory failed and could not be fully undone: check " + m.oldHome + " and " + m.newHome
		if m.backup != "" {
			msg += "; the copies of the secret files are in " + m.backup
		}
		return &HomeMoveError{Message: msg, Err: errors.Join(append([]error{cause}, failed...)...)}
	}
	if m.backup != "" {
		_ = os.RemoveAll(m.backup)
	}
	var moveErr *HomeMoveError
	if errors.As(cause, &moveErr) {
		return &HomeMoveError{Message: "the move of the data directory failed and was undone, the data stays in " + m.oldHome + ": " + moveErr.Message, Err: moveErr.Err}
	}
	return &HomeMoveError{Message: "the move of the data directory failed and was undone, the data stays in " + m.oldHome, Err: cause}
}

// copyTree copies a directory with its modes and times. At the top level the
// socket and the lock of a daemon are skipped: they are stale by now. Anything
// but a directory or a regular file is an error: the copy must be exact.
func copyTree(src, dst string, top bool) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.Mkdir(dst, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if top && (name == "daemon.sock" || name == "daemon.lock") {
			continue
		}
		from, to := filepath.Join(src, name), filepath.Join(dst, name)
		switch {
		case entry.Type().IsDir():
			err = copyTree(from, to, false)
		case entry.Type().IsRegular():
			var fi os.FileInfo
			if fi, err = entry.Info(); err == nil {
				err = copyVerified(from, to, fi.Mode().Perm())
			}
		default:
			err = fmt.Errorf("%s is neither a file nor a directory", from)
		}
		if err != nil {
			return err
		}
	}
	if err := os.Chmod(dst, info.Mode().Perm()); err != nil {
		return err
	}
	return os.Chtimes(dst, info.ModTime(), info.ModTime())
}

// copyVerified copies a file with the given mode and compares the SHA-256 of the
// copy with the one of the source.
func copyVerified(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, hash), in)
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(dst, perm)
	}
	if err == nil {
		err = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	if err != nil {
		return err
	}
	sum, err := fileDigest(dst)
	if err != nil {
		return err
	}
	if string(sum[:]) != string(hash.Sum(nil)) {
		return fmt.Errorf("the copy %s differs from %s", dst, src)
	}
	return nil
}

// replaceFile writes data over path in one rename, mode 0600, so that a crash
// leaves the old or the new content and never a mix.
func replaceFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
