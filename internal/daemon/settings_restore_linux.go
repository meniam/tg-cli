package daemon

import "golang.org/x/sys/unix"

func restoreCapturedSource(staged, path string) error {
	return unix.Renameat2(unix.AT_FDCWD, staged, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE)
}
