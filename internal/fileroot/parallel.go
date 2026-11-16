package fileroot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

var ErrDownloadSize = errors.New("downloaded size does not match")

type downloadRange struct{ start, end int64 }
type parallelDownload struct {
	mu     sync.Mutex
	f      *os.File
	limit  int64
	offset int64
	ranges []downloadRange
}

func (w *parallelDownload) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.writeAt(p, w.offset)
	w.offset += int64(n)
	return n, err
}
func (w *parallelDownload) WriteAt(p []byte, offset int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writeAt(p, offset)
}
func (w *parallelDownload) writeAt(p []byte, offset int64) (int, error) {
	if offset < 0 || offset > w.limit || int64(len(p)) > w.limit-offset {
		return 0, ErrDownloadSize
	}
	n, err := w.f.WriteAt(p, offset)
	if n > 0 {
		w.ranges = append(w.ranges, downloadRange{offset, offset + int64(n)})
	}
	return n, err
}
func (w *parallelDownload) complete(expected int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	sort.Slice(w.ranges, func(i, j int) bool { return w.ranges[i].start < w.ranges[j].start })
	var end int64
	for _, r := range w.ranges {
		if r.start != end {
			return false
		}
		end = r.end
	}
	return end == expected
}

// WriteParallel publishes a complete, bounded download without replacing a file.
func (d *DownloadDir) WriteParallel(ctx context.Context, name string, maxSize, expected int64, download func(io.Writer) error) (string, error) {
	if name == "" || filepath.Base(name) != name || name == "." || name == ".." || maxSize < 0 || expected < 0 || expected > maxSize {
		return "", ErrDownloadSize
	}
	tmp := "." + name + ".part"
	f, err := d.root.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close(); _ = d.root.Remove(tmp) }()
	w := &parallelDownload{f: f, limit: maxSize}
	if err := download(w); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !w.complete(expected) {
		return "", ErrDownloadSize
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := d.root.Link(tmp, filepath.Join(d.rel, name)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
