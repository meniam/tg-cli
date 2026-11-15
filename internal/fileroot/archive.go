package fileroot

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Archive uses the download root for every filesystem operation.
type Archive struct {
	root  *os.Root
	rel   string
	info  os.FileInfo
	owned bool
	files []string
}

func validArchiveName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name
}

// OpenArchiveDownloadDir rejects symlinks in the requested path below the allowed root.
func OpenArchiveDownloadDir(roots []string, dir string) (*DownloadDir, error) {
	d, err := OpenDownloadDir(roots, dir)
	if err != nil {
		return nil, err
	}
	for _, candidate := range roots {
		base, err := filepath.EvalSymlinks(candidate)
		if err != nil || base != d.root.Name() {
			continue
		}
		for _, prefix := range []string{candidate, base} {
			rel, err := filepath.Rel(filepath.Clean(prefix), filepath.Clean(dir))
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			if err := rejectLinks(d.root, rel); err != nil {
				d.Close()
				return nil, ErrOutsideRoots
			}
			return d, nil
		}
	}
	d.Close()
	return nil, ErrOutsideRoots
}

// rejectLinks rejects symlinks present in the path at the time it is opened.
// All subsequent filesystem operations remain relative to root.
func rejectLinks(root *os.Root, path string) error {
	current := "."
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		if part == ".." || part == "" {
			return errors.New("invalid archive path")
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symlink in archive path")
		}
	}
	return nil
}

func (d *DownloadDir) OpenArchive(name string, create bool) (*Archive, error) {
	if !validArchiveName(name) {
		return nil, errors.New("invalid archive name")
	}
	if err := rejectLinks(d.root, d.rel); err != nil {
		return nil, err
	}
	rel := filepath.Join(d.rel, name)
	if create {
		if err := d.root.Mkdir(rel, 0700); err != nil {
			return nil, err
		}
	}
	info, err := d.root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("archive is not a directory")
	}
	return &Archive{root: d.root, rel: rel, info: info, owned: create}, nil
}
func (a *Archive) Close() {}
func (a *Archive) RemoveOwned() error {
	if !a.owned {
		return errors.New("archive is not owned")
	}
	if err := a.removeArchiveFiles(); err != nil {
		return err
	}
	info, err := a.root.Lstat(a.rel)
	if err != nil {
		return err
	}
	if !os.SameFile(a.info, info) {
		return errors.New("archive directory changed")
	}
	return a.root.Remove(a.rel)
}
func (a *Archive) Write(ctx context.Context, name string, max int64, write func(io.Writer) error) (int64, string, error) {
	if !validArchiveName(name) {
		return 0, "", errors.New("invalid file name")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return 0, "", err
	}
	tmp := ".tg-export-" + hex.EncodeToString(nonce[:]) + ".part"
	f, err := a.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close(); _ = a.root.Remove(tmp) }()
	h := sha256.New()
	w := &limitedDownload{w: io.MultiWriter(f, h), remaining: max}
	cw := &archiveContextWriter{ctx: ctx, w: w}
	if err = write(cw); err != nil {
		return w.written, "", err
	}
	if err = ctx.Err(); err != nil {
		return w.written, "", err
	}
	if err = f.Close(); err != nil {
		return w.written, "", err
	}
	if err = a.linkArchiveFile(tmp, name); err != nil {
		return w.written, "", err
	}
	a.files = append(a.files, name)
	return w.written, hex.EncodeToString(h.Sum(nil)), nil
}

type archiveContextWriter struct {
	ctx context.Context
	w   io.Writer
}

func (w *archiveContextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.w.Write(p)
}
func (a *Archive) Open(name string) (*os.File, error) {
	if !validArchiveName(name) {
		return nil, errors.New("invalid file name")
	}
	return a.openArchiveFile(name)
}
func (a *Archive) Names() ([]string, error) { return a.openArchiveNames() }
