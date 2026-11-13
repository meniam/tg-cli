//go:build unix

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Launch starts the program as a detached process: in a session of its own with
// no controlling terminal, its journal in logPath instead of the terminal, and
// nothing the caller writes to stdout or stderr.
//
// It returns the process id and a function that reports how the process ended.
// The function blocks until the process has exited, so a caller uses it only
// with a deadline: a start uses it to notice an exit before the socket answered.
//
// The arguments are the ones the caller passes and they never carry a
// credential: the application id and hash reach the daemon through the
// environment and the profile files, because the arguments of a process are
// visible to every user of the machine in `ps`.
func Launch(args []string, logPath string) (int, func() error, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, nil, fmt.Errorf("find the program: %w", err)
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, nil, fmt.Errorf("open the journal: %w", err)
	}
	defer log.Close()

	cmd := exec.Command(exe, args...)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Stdin = nil
	cmd.Env = os.Environ()
	// Setsid puts the process in a new session: no controlling terminal, so a
	// closed terminal sends it no SIGHUP and Ctrl-C does not reach it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("start the program: %w", err)
	}
	pid := cmd.Process.Pid

	// The exit is collected in a goroutine, so the caller can ask for it with a
	// deadline and the process is still reaped when this command exits first.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return pid, func() error { return <-exited }, nil
}
