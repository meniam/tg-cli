//go:build unix

package exec

import (
	"errors"
	"os"
	osexec "os/exec"
	"syscall"
)

// configureProcess puts the program in its own process group and kills the
// whole group on cancellation, so helper children cannot outlive the job.
func configureProcess(cmd *osexec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// freeBytes returns the space available to an unprivileged user on the volume of dir.
func freeBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
