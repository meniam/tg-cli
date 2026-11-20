package exec

import (
	"errors"
	"fmt"
	"math"
)

// ErrDisk means the volume has too little free space for the job. The worker
// maps it to "failed: disk".
var ErrDisk = errors.New("disk")

// errNoDiskInfo is returned by freeBytes on platforms without statfs.
var errNoDiskInfo = errors.New("free space is unknown on this platform")

// CheckDisk returns an error wrapping ErrDisk when the volume holding dir has
// less than 2 x needBytes free (the source file and the WAV both live there).
// A volume whose free space cannot be read is not an error here: the write
// itself fails later with its own message.
func CheckDisk(dir string, needBytes int64) error {
	free, err := freeBytes(dir)
	if err != nil {
		if errors.Is(err, errNoDiskInfo) {
			return nil
		}
		return fmt.Errorf("check free space: %w", err)
	}
	return enoughSpace(free, needBytes)
}

// enoughSpace applies the rule: free space must be at least twice the need.
func enoughSpace(free uint64, needBytes int64) error {
	need := uint64(max(needBytes, 0))
	if need > math.MaxUint64/2 || free < 2*need {
		return fmt.Errorf("%w: %d bytes free, need at least %d", ErrDisk, free, 2*need)
	}
	return nil
}
