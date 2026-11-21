//go:build !darwin && !linux

package daemon

import "os"

func restoreCapturedSource(staged, path string) error {
	if err := os.Link(staged, path); err != nil {
		return err
	}
	return os.Remove(staged)
}
