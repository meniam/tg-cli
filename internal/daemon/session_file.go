package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"tg-cli/internal/sessionstr"
)

var ErrSessionInUse = errors.New("session key is already in use")

// WithProfileLock excludes the daemon and other offline operations.
func WithProfileLock(p Paths, fn func() error) error {
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return err
	}
	release, err := acquireLock(p.Lock)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}

// WriteSession atomically replaces the session file while its profile lock is held.
func WriteSession(p Paths, raw []byte) error {
	f, err := os.CreateTemp(p.Dir, ".session-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, p.Session)
}

// DuplicateProfiles finds profiles with the same key. Invalid sessions are ignored.
func DuplicateProfiles(p Paths, id string, runningOnly bool) ([]string, error) {
	entries, err := os.ReadDir(filepath.Dir(p.Dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == ReservedProfile || entry.Name() == filepath.Base(p.Dir) || !profileName.MatchString(entry.Name()) {
			continue
		}
		dir := filepath.Join(filepath.Dir(p.Dir), entry.Name())
		running := false
		if runningOnly {
			release, e := acquireLock(filepath.Join(dir, filepath.Base(p.Lock)))
			if e == nil {
				release()
				continue
			}
			if !errors.Is(e, ErrAlreadyRunning) {
				return nil, e
			}
			running = true
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.Base(p.Session)))
		if err != nil {
			if running {
				return nil, fmt.Errorf("cannot inspect running profile %s", entry.Name())
			}
			continue
		}
		_, _, key, err := sessionstr.Inspect(b)
		if err != nil {
			if running {
				return nil, fmt.Errorf("cannot inspect running profile %s", entry.Name())
			}
			continue
		}
		if key != id {
			continue
		}
		matches = append(matches, entry.Name())
	}
	return matches, nil
}

func guardSessionStart(p Paths) (func(), error) {
	// Serialize the checks while each contender already holds its profile lock.
	release, err := acquireLock(filepath.Join(filepath.Dir(p.Dir), ".session-start.lock"))
	if err != nil {
		return nil, fmt.Errorf("another daemon start is in progress: %w", err)
	}
	b, err := os.ReadFile(p.Session)
	if errors.Is(err, os.ErrNotExist) {
		return release, nil
	}
	if err != nil {
		release()
		return nil, err
	}
	_, _, id, err := sessionstr.Inspect(b)
	if err != nil {
		release()
		return nil, fmt.Errorf("invalid session file")
	}
	profiles, err := DuplicateProfiles(p, id, true)
	if err != nil {
		release()
		return nil, err
	}
	if len(profiles) > 0 {
		release()
		return nil, fmt.Errorf("%w by profile %s", ErrSessionInUse, profiles[0])
	}
	return release, nil
}
