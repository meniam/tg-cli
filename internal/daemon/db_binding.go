package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BindingError reports a database that belongs to another data directory. It is a
// configuration error, so the caller reports exit code 2.
type BindingError struct {
	// DatabaseDir is the directory next to the configuration file.
	DatabaseDir string
	// Recorded is the data directory written down by the profile that created
	// the database.
	Recorded string
	// Current is the data directory of this start.
	Current string
	// Record is the file that names the recorded data directory. Removing it is
	// the deliberate act that adopts the database from another data directory;
	// the database itself is never removed.
	Record string
}

func (e *BindingError) Error() string {
	return fmt.Sprintf("the database in %s belongs to the data directory %s, but this start uses %s: use the home or TG_CLI_HOME of the recorded data directory, or, when you moved that data directory on purpose, remove the record %s and start again; the database in %s stays and is never deleted by the tool",
		e.DatabaseDir, e.Recorded, e.Current, e.Record, e.DatabaseDir)
}

// ExitCode identifies a configuration error.
func (e *BindingError) ExitCode() int { return 2 }

// DatabaseBindingFile is the data directory written down next to the database.
func DatabaseBindingFile(p Paths) string { return filepath.Join(p.DatabaseDir, "data-dir") }

// DatabaseLockFile is held for as long as a daemon runs on the profile database.
func DatabaseLockFile(p Paths) string { return filepath.Join(p.DatabaseDir, "db.lock") }

// CheckDatabaseBinding compares the recorded data directory with the one of this
// start. It must run before store.Open: a database of another data directory is
// another account for the same profile name, and opening it would mix the two.
//
// A missing record is not a mismatch: BindDatabase writes it for a profile that
// has a database. A record that cannot be read is an error, not a mismatch.
func CheckDatabaseBinding(p Paths) error {
	recorded, err := os.ReadFile(DatabaseBindingFile(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the data directory record: %w", err)
	}
	value := strings.TrimSpace(string(recorded))
	if value == "" {
		return fmt.Errorf("read the data directory record: %s is empty", DatabaseBindingFile(p))
	}
	if sameDir(value, p.Dir) {
		return nil
	}
	return &BindingError{DatabaseDir: p.DatabaseDir, Recorded: value, Current: p.Dir, Record: DatabaseBindingFile(p)}
}

// BindDatabase writes the data directory next to the profile database, so that a
// second data directory with the same profile name cannot open it. An existing
// record is never rewritten: CheckDatabaseBinding has accepted it already, or the
// caller would be writing a second account into one database.
func BindDatabase(p Paths) error {
	path := DatabaseBindingFile(p)
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect the data directory record: %w", err)
	}
	if err := writeFileAtomic(path, []byte(p.Dir+"\n")); err != nil {
		return fmt.Errorf("write the data directory record: %w", err)
	}
	return nil
}

// LockDatabase takes the database lock of the profile and returns the release
// function. It is separate from the profile lock, so that two data directories
// with one profile name cannot both open the database. The error is
// ErrDatabaseInUse when another process holds the lock.
func LockDatabase(p Paths) (func(), error) {
	if err := os.MkdirAll(p.DatabaseDir, 0o700); err != nil {
		return nil, fmt.Errorf("create the database directory: %w", err)
	}
	if err := os.Chmod(p.DatabaseDir, 0o700); err != nil {
		return nil, fmt.Errorf("restrict the database directory: %w", err)
	}
	release, err := acquireLock(DatabaseLockFile(p))
	if errors.Is(err, ErrAlreadyRunning) {
		return nil, fmt.Errorf("%w: another data directory already holds %s", ErrDatabaseInUse, p.DatabaseDir)
	}
	if err != nil {
		return nil, fmt.Errorf("lock the database: %w", err)
	}
	return release, nil
}

// sameDir compares two directory paths as the filesystem would after resolving
// them, so that a home behind a symlink is not a second account.
func sameDir(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	resolvedA, errA := filepath.EvalSymlinks(a)
	resolvedB, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && resolvedA == resolvedB
}

// writeFileAtomic writes data to a new file in one rename, so that a crash leaves
// either no file or the whole content. It never replaces an existing file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
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
	// A record that appeared meanwhile belongs to a concurrent start that got
	// there first: replacing it would hide the other data directory, so the
	// start stops instead.
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s appeared during this start", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return syncDir(dir)
}
