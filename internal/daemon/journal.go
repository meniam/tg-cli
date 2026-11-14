package daemon

import (
	"fmt"
	"os"
	"sync"
)

const logCopies = 3

// Journal is the rotating writer owned by one background daemon. A write is
// kept whole, even when one entry is larger than the size limit.
type Journal struct {
	mu    sync.Mutex
	path  string
	limit int64
	file  *os.File
	size  int64
}

// OpenJournal opens the profile journal and rotates it when it exceeds 10 MiB.
func OpenJournal(path string) (*Journal, error) { return openJournal(path, maxLogBytes) }

func openJournal(path string, limit int64) (*Journal, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("journal size limit must be positive")
	}
	j := &Journal{path: path, limit: limit}
	if err := j.open(); err != nil {
		return nil, err
	}
	if j.size > limit {
		if err := j.rotate(); err != nil {
			if j.file != nil {
				_ = j.file.Close()
			}
			return nil, err
		}
	}
	return j, nil
}

func (j *Journal) open() error {
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open the daemon journal: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("set journal permissions: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat the daemon journal: %w", err)
	}
	j.file, j.size = f, info.Size()
	return nil
}

func (j *Journal) Write(p []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return 0, os.ErrClosed
	}
	if j.size > 0 && j.size+int64(len(p)) > j.limit {
		if err := j.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := j.file.Write(p)
	j.size += int64(n)
	return n, err
}

func (j *Journal) rotate() error {
	if err := j.file.Close(); err != nil {
		return err
	}
	j.file = nil
	if err := os.Remove(j.path + ".3"); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := logCopies - 1; i >= 1; i-- {
		old := fmt.Sprintf("%s.%d", j.path, i)
		if err := os.Rename(old, fmt.Sprintf("%s.%d", j.path, i+1)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(j.path, j.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return j.open()
}

func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file == nil {
		return nil
	}
	err := j.file.Close()
	j.file = nil
	return err
}
