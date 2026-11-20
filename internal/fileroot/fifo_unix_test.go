//go:build darwin || linux

package fileroot

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestOpenRejectsFIFO(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	file, _, err := Open([]string{root}, path, MaxFileSize)
	if !errors.Is(err, ErrNotRegular) || file != nil {
		t.Fatalf("FIFO: file=%v err=%v", file, err)
	}
}
