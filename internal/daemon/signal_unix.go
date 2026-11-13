//go:build unix

package daemon

import (
	"os"
	"syscall"
)

// signalZero sends signal 0 to the process, which checks that it exists and that
// the caller may signal it without changing anything about it.
func signalZero(p *os.Process) error { return p.Signal(syscall.Signal(0)) }
