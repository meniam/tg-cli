// Package fileroot opens regular files within human-approved directory roots.
package fileroot

import (
	"errors"
	"os"
	"path/filepath"
)

const MaxFileSize int64 = 100 << 20

var (
	ErrNotAbsolute  = errors.New("file path must be absolute")
	ErrOutsideRoots = errors.New("file is outside allowed roots")
	ErrNotFound     = errors.New("file not found")
	ErrNotRegular   = errors.New("file must be a regular file")
	ErrEmpty        = errors.New("file is empty")
	ErrTooLarge     = errors.New("file is too large")
	ErrChanged      = errors.New("file changed while opening")
	ErrInvalidLimit = errors.New("file size limit must be positive")
)

// Open returns one opened file and its size. The caller owns the descriptor.
// No candidate path is inspected before lexical membership is established.
func Open(roots []string, path string, maxSize int64) (*os.File, int64, error) {
	return open(roots, path, maxSize, os.Open)
}

func open(roots []string, path string, maxSize int64, openFile func(string) (*os.File, error)) (*os.File, int64, error) {
	if !filepath.IsAbs(path) {
		return nil, 0, ErrNotAbsolute
	}
	if maxSize <= 0 {
		return nil, 0, ErrInvalidLimit
	}
	path = filepath.Clean(path)
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			continue
		}
		root = filepath.Clean(root)
		if !within(root, path) {
			continue
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		rootInfo, err := os.Stat(resolvedRoot)
		if err != nil || !rootInfo.IsDir() {
			continue
		}
		resolvedPath, err := filepath.EvalSymlinks(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, ErrNotFound
		}
		if err != nil {
			return nil, 0, ErrNotRegular
		}
		if !within(resolvedRoot, resolvedPath) {
			return nil, 0, ErrOutsideRoots
		}
		before, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, ErrNotFound
		}
		if err != nil {
			return nil, 0, ErrNotRegular
		}
		if !before.Mode().IsRegular() {
			return nil, 0, ErrNotRegular
		}
		file, err := openFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, ErrNotFound
		}
		if err != nil {
			return nil, 0, ErrNotRegular
		}
		info, statErr := file.Stat()
		after, pathStatErr := os.Stat(path)
		currentPath, resolveErr := filepath.EvalSymlinks(path)
		if statErr != nil || pathStatErr != nil || resolveErr != nil || !within(resolvedRoot, currentPath) ||
			!info.Mode().IsRegular() || !os.SameFile(before, info) || !os.SameFile(after, info) {
			_ = file.Close()
			return nil, 0, ErrChanged
		}
		if info.Size() == 0 {
			_ = file.Close()
			return nil, 0, ErrEmpty
		}
		if info.Size() > maxSize {
			_ = file.Close()
			return nil, 0, ErrTooLarge
		}
		return file, info.Size(), nil
	}
	return nil, 0, ErrOutsideRoots
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !filepath.IsAbs(rel) &&
		(len(rel) < 3 || rel[:3] != ".."+string(filepath.Separator))
}
