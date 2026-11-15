//go:build !darwin && !linux

package fileroot

import (
	"errors"
	"os"
)

func (a *Archive) linkArchiveFile(_, _ string) error {
	return errors.New("archive access is unsupported on this platform")
}
func (a *Archive) openArchiveFile(_ string) (*os.File, error) {
	return nil, errors.New("archive access is unsupported on this platform")
}
func (a *Archive) openArchiveNames() ([]string, error) {
	return nil, errors.New("archive access is unsupported on this platform")
}

func (a *Archive) removeArchiveFiles() error {
	return errors.New("archive access is unsupported on this platform")
}
