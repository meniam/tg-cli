package daemon

import "golang.org/x/sys/unix"

// An exclusive rename restores the directory entry itself, even for a symlink.
func restoreCapturedSource(staged, path string) error {
	return unix.RenamexNp(staged, path, unix.RENAME_EXCL)
}
