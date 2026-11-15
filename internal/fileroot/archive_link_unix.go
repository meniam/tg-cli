//go:build darwin || linux

package fileroot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openArchiveDirectory opens each component without following symlinks.
func (a *Archive) openArchiveDirectory() (*os.File, error) {
	base, err := a.root.Open(".")
	if err != nil {
		return nil, err
	}
	current := base
	for _, part := range strings.Split(filepath.Clean(a.rel), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		if part == "" || part == ".." {
			current.Close()
			return nil, errors.New("invalid archive path")
		}
		fd, err := unix.Openat(int(current.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		current.Close()
		if err != nil {
			return nil, err
		}
		current = os.NewFile(uintptr(fd), "archive")
	}
	info, err := current.Stat()
	if err != nil || !os.SameFile(a.info, info) {
		current.Close()
		return nil, errors.New("archive directory changed")
	}
	return current, nil
}
func (a *Archive) linkArchiveFile(tmp, name string) error {
	dir, err := a.openArchiveDirectory()
	if err != nil {
		return err
	}
	defer dir.Close()
	base, err := a.root.Open(".")
	if err != nil {
		return err
	}
	defer base.Close()
	return unix.Linkat(int(base.Fd()), tmp, int(dir.Fd()), name, 0)
}
func (a *Archive) openArchiveFile(name string) (*os.File, error) {
	dir, err := a.openArchiveDirectory()
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("archive entry is not a regular file")
	}
	return f, nil
}
func (a *Archive) openArchiveNames() ([]string, error) {
	f, err := a.openArchiveDirectory()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Readdirnames(-1)
}

func (a *Archive) removeArchiveFiles() error {
	dir, err := a.openArchiveDirectory()
	if err != nil {
		return err
	}
	defer dir.Close()
	for _, name := range a.files {
		if err := unix.Unlinkat(int(dir.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}
