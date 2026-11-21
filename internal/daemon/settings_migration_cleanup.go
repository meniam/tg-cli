package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"tg-cli/internal/config"
)

type changedSource struct {
	path     string
	retained string
}

func (e *changedSource) Error() string {
	if e.retained != "" {
		return fmt.Sprintf("source changed during the move: %s; retained at %s", e.path, e.retained)
	}
	return "source changed during the move: " + e.path
}
func (e *changedSource) ExitCode() int { return 2 }

// finishSource claims the directory entry before inspecting it. A process crash
// after rename can leave the source in the backup's staging directory. Recovery
// of this short interval is manual; never clean a nonempty staging directory.
func finishSource(backup, path string, expected []byte, mode os.FileMode, replacement string, rename func(string, string) error) error {
	dir, err := os.MkdirTemp(backup, ".move-*")
	if err != nil {
		return err
	}
	defer os.Remove(dir)
	staged := filepath.Join(dir, filepath.Base(path))
	if err := rename(path, staged); err != nil {
		return &migrationSourceFailure{Source: path, Operation: "move"}
	}
	restore := func(cause error) error {
		if err := restoreCapturedSource(staged, path); err != nil {
			// On filesystems without exclusive rename, keep a non-overwriting
			// reference to the retained entry so the next start still sees it.
			if !errors.Is(err, os.ErrExist) {
				_ = os.Symlink(staged, path)
			}
			return errors.Join(cause, &changedSource{path, staged})
		}
		return errors.Join(cause, syncDir(filepath.Dir(path)))
	}
	if !matchesSource(staged, expected, mode) {
		return restore(&changedSource{path: path})
	}
	if replacement != "" {
		if err := os.Link(replacement, path); err != nil {
			if errors.Is(err, os.ErrExist) {
				return &changedSource{path, staged}
			}
			return restore(err)
		}
	} else if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return &changedSource{path, staged}
	}
	if err := os.Remove(staged); err != nil {
		if replacement == "" {
			return restore(err)
		}
		return &migrationSourceFailure{Source: staged, Operation: "remove"}
	}
	return nil
}

func matchesSource(path string, expected []byte, mode os.FileMode) bool {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() != int64(len(expected)) || (mode != 0 && info.Mode().Perm() != mode) {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(len(expected))+1))
	return err == nil && bytes.Equal(data, expected)
}

func finishConfig(backup, path string, doc *config.LockedLegacyConfig, rename func(string, string) error) error {
	updated, err := config.StripLegacy(doc.Content)
	if err != nil {
		return err
	}
	if bytes.Equal(updated, doc.Content) {
		return doc.CheckUnchanged()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(updated)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := finishSource(backup, path, doc.Content, 0, f.Name(), rename); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}
