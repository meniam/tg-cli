package daemon

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileConflictError reports data that a start would hide or leave behind.
type FileConflictError struct {
	Old, New string
	// message replaces the generic text when the conflict needs its own words.
	message string
}

func (e *FileConflictError) Error() string {
	if e.message != "" {
		return e.message
	}
	return fmt.Sprintf("%s exists but configured file %s does not: rename the file or restore its name in config.toml", e.Old, e.New)
}

// ExitCode identifies a configuration error.
func (e *FileConflictError) ExitCode() int { return 2 }

// databaseInBothPlacesError reports a database that exists in the profile
// directory and next to the configuration file and cannot be told apart from a
// finished or interrupted move.
func databaseInBothPlacesError(old, current string) error {
	return &FileConflictError{
		Old:     old,
		New:     current,
		message: fmt.Sprintf("the database is in both places: %s and %s: keep one of them and remove the other", old, current),
	}
}

// CheckFileNames detects existing data hidden by a change of profile file names
// or of the database directory. It returns warnings for journal and home changes
// without opening session or access files.
//
// A database only in LegacyDatabase is not a conflict: the daemon moves it on
// start. Two databases with the same name are a conflict unless they are
// byte-identical and the old one has no pending journal.
func CheckFileNames(p Paths) ([]string, error) {
	var warnings []string
	dirInfo, err := os.Stat(p.Dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect profile directory: %w", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		base, e := os.UserConfigDir()
		if e != nil {
			return nil, fmt.Errorf("find config directory: %w", e)
		}
		old := filepath.Join(base, "tg-cli", filepath.Base(p.Dir))
		if filepath.Clean(old) != filepath.Clean(p.Dir) {
			if info, e := os.Stat(old); e == nil && info.IsDir() {
				warnings = append(warnings, fmt.Sprintf("profile directory %s does not exist; default profile directory %s exists", p.Dir, old))
			} else if e != nil && !errors.Is(e, os.ErrNotExist) {
				return nil, fmt.Errorf("inspect default profile directory: %w", e)
			}
		}
		return warnings, nil
	}
	if !dirInfo.IsDir() {
		return nil, fmt.Errorf("profile path is not a directory: %s", p.Dir)
	}
	if err := checkDatabase(p); err != nil {
		return nil, err
	}
	for _, item := range []struct {
		current, old string
		fatal        bool
	}{
		{p.Session, "session.json", true}, {p.Access, "access.json", true}, {p.Log, "daemon.log", false}, {p.Deletions, "deletions.jsonl", false},
	} {
		exists, e := fileExists(item.current)
		if e != nil {
			return nil, e
		}
		if exists {
			continue
		}
		old := filepath.Join(p.Dir, item.old)
		exists, e = fileExists(old)
		if e != nil {
			return nil, e
		}
		if exists && filepath.Clean(old) != filepath.Clean(item.current) {
			if item.fatal {
				return nil, &FileConflictError{Old: old, New: item.current}
			}
			warnings = append(warnings, fmt.Sprintf("%s exists but configured journal %s does not", old, item.current))
		}
	}
	return warnings, nil
}

// checkDatabase compares the database directories of both layouts: the profile
// directory of the old layout and the database directory next to the
// configuration file.
func checkDatabase(p Paths) error {
	legacy, err := databaseExists(p.LegacyDatabase)
	if err != nil {
		return err
	}
	current, err := databaseExists(p.Database)
	if err != nil {
		return err
	}
	if legacy && current {
		return checkInterruptedMove(p)
	}
	// A database left under a previous name is a changed name, not a move.
	if err := checkRenamedDatabase(p); err != nil {
		return err
	}
	if legacy || current {
		// One side holds the database of the profile: a database under an
		// unknown name in the other directory would be ambiguous.
		other := p.Dir
		if legacy {
			other = p.DatabaseDir
		}
		return checkStrayDatabase(other, p)
	}
	if err := checkStrayDatabase(p.Dir, p); err != nil {
		return err
	}
	return checkStrayDatabase(p.DatabaseDir, p)
}

// checkRenamedDatabase refuses a database under the original file name when the
// configuration names another file in either directory.
func checkRenamedDatabase(p Paths) error {
	original := DefaultFileNames().Database
	for _, dir := range []string{p.Dir, p.DatabaseDir} {
		path := filepath.Join(dir, original)
		if filepath.Clean(path) == filepath.Clean(p.LegacyDatabase) || filepath.Clean(path) == filepath.Clean(p.Database) {
			continue
		}
		exists, err := databaseExists(path)
		if err != nil {
			return err
		}
		if exists {
			return &FileConflictError{Old: path, New: p.Database}
		}
	}
	return nil
}

// checkInterruptedMove accepts a move that stopped after the copy and before the
// removal of the old file: identical files and no pending journal. Anything else
// would need a decision the daemon cannot make.
func checkInterruptedMove(p Paths) error {
	same, err := sameDatabase(p.LegacyDatabase, p.Database)
	if err != nil {
		return err
	}
	if !same {
		return databaseInBothPlacesError(p.LegacyDatabase, p.Database)
	}
	empty, err := journalIsEmpty(p.LegacyDatabase + "-wal")
	if err != nil {
		return err
	}
	if !empty {
		return databaseInBothPlacesError(p.LegacyDatabase, p.Database)
	}
	return nil
}

// checkStrayDatabase refuses a database under an unexpected name in dir.
func checkStrayDatabase(dir string, p Paths) error {
	path, err := findDatabase(dir, p)
	if err != nil {
		return err
	}
	if path != "" {
		return &FileConflictError{Old: path, New: p.Database}
	}
	return nil
}

// findDatabase returns a file of dir that looks like a database and is not one of
// the files the profile expects. It does not step into subdirectories: the
// database of a profile lies in exactly one of the two directories it is given.
func findDatabase(dir string, p Paths) (string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect directory %s: %w", dir, err)
	}
	protected := map[string]bool{}
	for _, path := range []string{p.Session, p.Access, p.Log, p.Deletions, p.Lock, p.Socket, p.Database, p.LegacyDatabase} {
		protected[filepath.Base(path)] = true
	}
	for _, name := range []string{"session.json", "access.json", "daemon.log", "deletions.jsonl", "proxy", "client", "device", "transport", ".session-start.lock"} {
		protected[name] = true
	}
	for _, entry := range entries {
		if protected[entry.Name()] || isDatabaseSidecar(entry.Name()) || isMoveTemp(entry.Name(), filepath.Base(p.Database)) {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return "", fmt.Errorf("inspect file %s: %w", filepath.Join(dir, entry.Name()), e)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		ok, e := hasDatabaseHeader(path)
		if e != nil {
			return "", e
		}
		if ok {
			return path, nil
		}
	}
	return "", nil
}

// isDatabaseSidecar reports the journal and shared-memory files of a database.
func isDatabaseSidecar(name string) bool {
	return strings.HasSuffix(name, "-wal") || strings.HasSuffix(name, "-shm")
}

// isMoveTemp reports the temporary copy of a move that an interrupted run left
// behind. It carries the SQLite header, but it is a copy, not a database.
func isMoveTemp(name, database string) bool {
	return strings.HasPrefix(name, "."+database+".move-") && strings.HasSuffix(strings.TrimSuffix(strings.TrimSuffix(name, "-wal"), "-shm"), ".tmp")
}

// hasDatabaseHeader reports whether the file starts with the SQLite signature.
func hasDatabaseHeader(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("inspect file %s: %w", path, err)
	}
	var header [16]byte
	_, err = io.ReadFull(f, header[:])
	closeErr := f.Close()
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false, fmt.Errorf("inspect file %s: %w", path, err)
	}
	if closeErr != nil {
		return false, fmt.Errorf("close file %s: %w", path, closeErr)
	}
	return bytes.Equal(header[:], []byte("SQLite format 3\x00")), nil
}

// sameDatabase reports whether two files are byte-identical, the signature of a
// move interrupted after the copy and before the removal of the old file.
func sameDatabase(a, b string) (bool, error) {
	infoA, err := os.Stat(a)
	if err != nil {
		return false, fmt.Errorf("inspect database %s: %w", a, err)
	}
	infoB, err := os.Stat(b)
	if err != nil {
		return false, fmt.Errorf("inspect database %s: %w", b, err)
	}
	if infoA.Size() != infoB.Size() {
		return false, nil
	}
	digestA, err := fileDigest(a)
	if err != nil {
		return false, err
	}
	digestB, err := fileDigest(b)
	if err != nil {
		return false, err
	}
	return digestA == digestB, nil
}

func fileDigest(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, fmt.Errorf("inspect database %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return sum, fmt.Errorf("inspect database %s: %w", path, err)
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// journalIsEmpty reports whether the write-ahead journal of path is missing or
// empty: a journal with content may hold history the main file does not.
func journalIsEmpty(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect journal %s: %w", path, err)
	}
	return info.Size() == 0, nil
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect profile file %s: %w", path, err)
	}
	return true, nil
}

// databaseExists reports whether path is an existing regular file. A directory
// or a device under the name of the database is not one.
func databaseExists(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect database %s: %w", path, err)
	}
	return info.Mode().IsRegular(), nil
}
