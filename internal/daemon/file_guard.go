package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileConflictError reports a changed name that would hide an existing file.
type FileConflictError struct{ Old, New string }

func (e *FileConflictError) Error() string {
	return fmt.Sprintf("%s exists but configured file %s does not: rename the file or restore its name in config.toml", e.Old, e.New)
}

// ExitCode identifies a configuration error.
func (e *FileConflictError) ExitCode() int { return 2 }

// CheckFileNames detects existing data hidden by a change of profile file names.
// It returns warnings for journal and home changes without opening session or access files.
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
	for _, item := range []struct {
		current, old string
		fatal        bool
	}{
		{p.Database, "store.db", true}, {p.Session, "session.json", true}, {p.Access, "access.json", true}, {p.Log, "daemon.log", false}, {p.Deletions, "deletions.jsonl", false},
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
	exists, err := fileExists(p.Database)
	if err != nil {
		return nil, err
	}
	if exists {
		return warnings, nil
	}
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return nil, fmt.Errorf("inspect profile directory: %w", err)
	}
	protected := map[string]bool{}
	for _, path := range []string{p.Session, p.Access, p.Log, p.Deletions, p.Lock, p.Socket} {
		protected[filepath.Base(path)] = true
	}
	for _, name := range []string{"session.json", "access.json", "daemon.log", "deletions.jsonl", "proxy", "client", "device", "transport", ".session-start.lock"} {
		protected[name] = true
	}
	for _, entry := range entries {
		if protected[entry.Name()] || strings.HasSuffix(entry.Name(), "-wal") || strings.HasSuffix(entry.Name(), "-shm") {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return nil, fmt.Errorf("inspect profile file: %w", e)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(p.Dir, entry.Name())
		f, e := os.Open(path)
		if e != nil {
			return nil, fmt.Errorf("inspect profile file %s: %w", path, e)
		}
		var header [16]byte
		_, e = io.ReadFull(f, header[:])
		closeErr := f.Close()
		if e != nil && !errors.Is(e, io.EOF) && !errors.Is(e, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("inspect profile file %s: %w", path, e)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close profile file %s: %w", path, closeErr)
		}
		if bytes.Equal(header[:], []byte("SQLite format 3\x00")) {
			return nil, &FileConflictError{Old: path, New: p.Database}
		}
	}
	return warnings, nil
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
