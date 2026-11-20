package daemon

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tg-cli/internal/store"
)

// MoveError is a failure of the database move. It is an environment problem, not
// a wrong configuration, so the caller reports exit code 1 and not 2.
type MoveError struct {
	// Path is the database or directory the move stopped at.
	Path string
	// Step names the step or the verification that failed, empty for a plain
	// failure.
	Step string
	// Err is the underlying error.
	Err error
}

func (e *MoveError) Error() string {
	switch {
	case e.Step != "":
		return fmt.Sprintf("move the database: %s failed for %s: %v", e.Step, e.Path, e.Err)
	default:
		return fmt.Sprintf("move the database: %s: %v", e.Path, e.Err)
	}
}

func (e *MoveError) Unwrap() error { return e.Err }

// ExitCode identifies a failure that is not a configuration error.
func (e *MoveError) ExitCode() int { return 1 }

// moveTempName returns the temporary copy of the move: the same directory as the
// new database, so that the final step is a rename inside one filesystem.
func moveTempName(database string, pid int) string {
	return filepath.Join(filepath.Dir(database), fmt.Sprintf(".%s.move-%d.tmp", filepath.Base(database), pid))
}

// MoveLegacyDatabase moves the database of the old layout next to the
// configuration file. The call must hold the profile lock: it reads and deletes
// the old database and creates the new one.
//
// The old file is deleted only after the copy passed every check, so a failure
// at any step leaves it untouched. moved reports whether this call changed the
// disk; a call with nothing to do returns false, and so does a move that had
// already finished.
func MoveLegacyDatabase(p Paths) (moved bool, err error) {
	return moveLegacyDatabase(p, 5*time.Second)
}

func moveLegacyDatabase(p Paths, busyTimeout time.Duration) (moved bool, err error) {
	if err := removeMoveTemps(p); err != nil {
		return false, err
	}
	legacy, err := databaseExists(p.LegacyDatabase)
	if err != nil {
		return false, err
	}
	current, err := databaseExists(p.Database)
	if err != nil {
		return false, err
	}
	switch {
	case legacy && current:
		// An interrupted move left a checked copy behind: the old file goes,
		// after a fresh comparison, and nothing is copied again.
		if err := finishInterruptedMove(p); err != nil {
			return false, err
		}
		return true, nil
	case legacy:
		if err := moveDatabase(p, busyTimeout); err != nil {
			return false, err
		}
		return true, nil
	case current:
		// The move has already finished.
		return false, nil
	default:
		// A new profile: the directory is created, store.Open creates the file.
		if err := makeDatabaseDir(p.DatabaseDir); err != nil {
			return false, err
		}
		return false, nil
	}
}

// moveDatabase runs the move of one database: prepare, checkpoint, copy, verify,
// rename and remove the old file.
func moveDatabase(p Paths, busyTimeout time.Duration) error {
	if err := makeDatabaseDir(p.DatabaseDir); err != nil {
		return err
	}
	// A journal with content may hold history the main file does not: the
	// checkpoint folds it in, and a busy database is in use by someone else.
	if err := checkpointLegacy(p, busyTimeout); err != nil {
		return err
	}
	before, err := os.Stat(p.LegacyDatabase)
	if err != nil {
		return moveError(p.LegacyDatabase, "inspect the database", err)
	}
	temp := moveTempName(p.Database, os.Getpid())
	if err := copyDatabase(p.LegacyDatabase, temp); err != nil {
		return err
	}
	// From here on, every failure removes the copy: the old file stays.
	if err := verifyCopy(p, temp, before); err != nil {
		removeFile(temp)
		return err
	}
	if err := commitMove(p, temp); err != nil {
		return err
	}
	return nil
}

// removeMoveTemps deletes the copies of an earlier interrupted run. The old
// database of that run is still in place, so the files carry nothing the checks
// would miss.
func removeMoveTemps(p Paths) error {
	dir := p.DatabaseDir
	prefix := "." + filepath.Base(p.Database) + ".move-"
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return moveError(dir, "inspect the database directory", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return moveError(path, "remove the temporary copy of an earlier move", err)
		}
	}
	return nil
}

// makeDatabaseDir creates the database directory and restricts both it and the
// db directory above it to the owner.
func makeDatabaseDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return moveError(dir, "create the database directory", err)
	}
	for _, path := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(path, 0o700); err != nil {
			return moveError(path, "restrict the database directory", err)
		}
	}
	return nil
}

// checkpointLegacy folds the journal of the old database into its main file. A
// database held by another process cannot be copied whole, so the move stops.
func checkpointLegacy(p Paths, busyTimeout time.Duration) error {
	busy, err := store.CheckpointTimeout(p.LegacyDatabase, busyTimeout)
	if err != nil {
		return moveError(p.LegacyDatabase, "checkpoint the database", err)
	}
	if busy {
		return moveError(p.LegacyDatabase, "checkpoint the database", errors.New("database is in use"))
	}
	return nil
}

// copyDatabase writes a private copy of src to dst and flushes it to the disk.
// A failed copy leaves no temporary file behind.
func copyDatabase(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return moveError(src, "read the database", err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return moveError(dst, "create the temporary copy", err)
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		removeFile(dst)
		return moveError(dst, "write the temporary copy", copyErr)
	}
	return nil
}

// verifyCopy checks the copy against the old database and against the state the
// old file had before the copy started.
func verifyCopy(p Paths, temp string, before os.FileInfo) error {
	after, err := os.Stat(p.LegacyDatabase)
	if err != nil {
		return moveError(p.LegacyDatabase, "inspect the database", err)
	}
	if after.Size() != before.Size() {
		return moveError(p.LegacyDatabase, "the size of the source", errors.New("the database changed during the copy"))
	}
	copied, err := os.Stat(temp)
	if err != nil {
		return moveError(temp, "inspect the copy", err)
	}
	if copied.Size() != before.Size() {
		return moveError(temp, "the size of the copy", fmt.Errorf("%d bytes, expected %d", copied.Size(), before.Size()))
	}
	source, err := fileDigest(p.LegacyDatabase)
	if err != nil {
		return moveError(p.LegacyDatabase, "the checksum of the source", err)
	}
	target, err := fileDigest(temp)
	if err != nil {
		return moveError(temp, "the checksum of the copy", err)
	}
	if source != target {
		return moveError(temp, "the checksum of the copy", errors.New("the copy differs from the source"))
	}
	// The journal check runs before the engine opens the copy: opening it would
	// fold a journal into the main file or delete it, and the copy would look
	// clean.
	for _, suffix := range []string{"-wal", "-shm"} {
		exists, err := fileExists(temp + suffix)
		if err != nil {
			return moveError(temp+suffix, "inspect the copy", err)
		}
		if exists {
			return moveError(temp+suffix, "the copy must not carry a journal", errors.New("a journal next to the copy"))
		}
	}
	ok, detail, err := store.IntegrityCheck(temp)
	if err != nil {
		return moveError(temp, "the integrity check of the copy", err)
	}
	if !ok {
		return moveError(temp, "the integrity check of the copy", errors.New(detail))
	}
	sourceVersion, err := store.UserVersion(p.LegacyDatabase)
	if err != nil {
		return moveError(p.LegacyDatabase, "the schema version of the source", err)
	}
	copyVersion, err := store.UserVersion(temp)
	if err != nil {
		return moveError(temp, "the schema version of the copy", err)
	}
	if sourceVersion != copyVersion {
		return moveError(temp, "the schema version of the copy", fmt.Errorf("%d, expected %d", copyVersion, sourceVersion))
	}
	// Reading the copy above opened it as a database, so the engine may have
	// created its journals again. They were drained by the checkpoint, so at
	// most empty ones are left; a non-empty one is data the copy must not carry.
	if err := removeEmptyJournals(temp); err != nil {
		return err
	}
	// The source must look the same as before the copy: a writer that started
	// in between would make the copy a torn picture of the database.
	settled, err := os.Stat(p.LegacyDatabase)
	if err != nil {
		return moveError(p.LegacyDatabase, "inspect the database", err)
	}
	if settled.Size() != before.Size() || !settled.ModTime().Equal(before.ModTime()) {
		return moveError(p.LegacyDatabase, "the source did not change during the copy", errors.New("the database changed during the copy"))
	}
	empty, err := journalIsEmpty(p.LegacyDatabase + "-wal")
	if err != nil {
		return moveError(p.LegacyDatabase+"-wal", "inspect the journal of the source", err)
	}
	if !empty {
		return moveError(p.LegacyDatabase+"-wal", "the journal of the source is empty", errors.New("data was written during the copy"))
	}
	return nil
}

// removeEmptyJournals deletes the empty journal files next to a database and
// reports a journal that holds data.
func removeEmptyJournals(path string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		empty, err := journalIsEmpty(path + suffix)
		if err != nil {
			return err
		}
		if !empty {
			return moveError(path+suffix, "the copy must not carry a journal", errors.New("a journal with data"))
		}
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return moveError(path+suffix, "remove the journal of the copy", err)
		}
	}
	return nil
}

// commitMove puts the verified copy in place and removes the old database with
// its empty journal files. temp may already be the new database, which is the
// case of a move that stopped after the rename.
func commitMove(p Paths, temp string) error {
	if filepath.Clean(temp) != filepath.Clean(p.Database) {
		if err := os.Rename(temp, p.Database); err != nil {
			removeFile(temp)
			return moveError(p.Database, "put the copy in place", err)
		}
		if err := syncDir(p.DatabaseDir); err != nil {
			return err
		}
	}
	if err := os.Remove(p.LegacyDatabase); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return moveError(p.LegacyDatabase, "remove the old database", err)
	}
	// The journal files of the old database are empty: verifyCopy drained them
	// and saw nothing written since. A non-empty one would have stopped the
	// move, so only empty ones can be removed here.
	for _, suffix := range []string{"-wal", "-shm"} {
		path := p.LegacyDatabase + suffix
		empty, err := journalIsEmpty(path)
		if err != nil {
			return err
		}
		if !empty {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return moveError(path, "remove the journal of the old database", err)
		}
	}
	return syncDir(p.Dir)
}

// finishInterruptedMove completes a move that stopped after the rename: the copy
// is already checked, the old file is still there. The comparison runs again, so
// no stale copy can pass.
func finishInterruptedMove(p Paths) error {
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
	ok, detail, err := store.IntegrityCheck(p.Database)
	if err != nil {
		return moveError(p.Database, "the integrity check of the copy", err)
	}
	if !ok {
		return moveError(p.Database, "the integrity check of the copy", errors.New(detail))
	}
	return commitMove(p, p.Database)
}

// syncDir flushes a directory entry change to the disk.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return moveError(dir, "open the directory", err)
	}
	err = f.Sync()
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return moveError(dir, "flush the directory", err)
	}
	return nil
}

// removeFile deletes a file and keeps a failure from hiding the original error.
func removeFile(path string) {
	_ = os.Remove(path)
}

// moveError wraps a failure of the move and names what was being done.
func moveError(path, what string, err error) error {
	var moveErr *MoveError
	if errors.As(err, &moveErr) {
		return err
	}
	return &MoveError{Path: path, Step: what, Err: err}
}
