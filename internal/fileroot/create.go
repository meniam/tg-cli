package fileroot

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DownloadDir confines subsequent file operations to an allowed directory.
type DownloadDir struct {
	root *os.Root
	path string
	rel  string
}

func OpenDownloadDir(roots []string, dir string) (*DownloadDir, error) {
	if !filepath.IsAbs(dir) {
		return nil, ErrNotAbsolute
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, ErrOutsideRoots
	}
	for _, candidate := range roots {
		base, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		before, err := os.Stat(base)
		if err != nil {
			continue
		}
		root, err := os.OpenRoot(base)
		if err != nil {
			continue
		}
		after, err := root.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			_ = root.Close()
			continue
		}
		sub, err := root.OpenRoot(rel)
		if err != nil {
			_ = root.Close()
			continue
		}
		info, err := sub.Stat(".")
		if err != nil || !info.IsDir() {
			_ = sub.Close()
			_ = root.Close()
			continue
		}
		_ = sub.Close()
		return &DownloadDir{root: root, path: resolved, rel: rel}, nil
	}
	return nil, ErrOutsideRoots
}
func (d *DownloadDir) Close() error            { return d.root.Close() }
func (d *DownloadDir) Path(name string) string { return filepath.Join(d.path, name) }
func (d *DownloadDir) Write(name string, max int64, download func(io.Writer) error) (size int64, err error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." {
		return 0, errors.New("invalid file name")
	}
	tmp := "." + name + ".part"
	f, err := d.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close(); _ = d.root.Remove(tmp) }()
	w := &limitedDownload{w: f, remaining: max}
	if err = download(w); err != nil {
		return w.written, err
	}
	if err = f.Close(); err != nil {
		return w.written, err
	}
	if err = d.root.Link(tmp, filepath.Join(d.rel, name)); err != nil {
		return w.written, err
	}
	return w.written, nil
}

type limitedDownload struct {
	w                  io.Writer
	remaining, written int64
}

func (l *limitedDownload) Write(p []byte) (int, error) {
	if int64(len(p)) > l.remaining {
		return 0, fmt.Errorf("file exceeds size limit")
	}
	n, err := l.w.Write(p)
	l.written += int64(n)
	l.remaining -= int64(n)
	return n, err
}
