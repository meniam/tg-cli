package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"tg-cli/internal/rpc"
)

// The background start. `daemon start` runs the same command again as a detached
// process with --foreground and waits for its socket, so the caller gets an
// answer only when the daemon really accepts requests or has already died.

const (
	// StartTimeout bounds the wait for the socket of a background daemon. A
	// cold Telegram connection can be slow, so it is generous.
	StartTimeout = 15 * time.Second
	// readyPollEvery is how often the socket is asked during that wait.
	readyPollEvery = 50 * time.Millisecond
	// StopTimeout bounds the wait of a stop: a daemon that does not stop within
	// it is reported as still running rather than waited for for ever.
	StopTimeout = 10 * time.Second
	// logTailSize is how much of the journal an error carries: enough to show
	// the reason, small enough to read in a terminal.
	logTailSize = 4096
)

// Background is what Start reports about a daemon it started.
type Background struct {
	// PID is the process id of the daemon. It is 0 when the command could not
	// learn it from the socket, and Stop then only waits for the socket.
	PID    int
	Socket string
	Log    string
}

// StartOptions are the ways a background start differs from running.
type StartOptions struct {
	// Paths locate the profile, its socket, its lock and its journal.
	Paths Paths
	// IdleTimeout is passed to the daemon; zero disables the idle stop.
	IdleTimeout time.Duration
	// Args are the arguments of the detached command, without the program name.
	// They never carry a credential: see Launcher.
	Args []string
	// Launcher starts the detached process. Tests replace it; in the program it
	// is the function main installs for the current platform.
	Launcher func(args []string, logPath string) (pid int, wait func() error, err error)
	// Dial connects to the daemon socket. Tests replace it.
	Dial func(ctx context.Context, socket string) (pinger, error)
	// StartTimeout overrides the wait for the socket; zero means StartTimeout.
	StartTimeout time.Duration
}

// pinger is the part of the rpc client a start needs: it asks whether the
// daemon is alive and takes the process id from the answer.
type pinger interface {
	Call(ctx context.Context, method string, params, result any) error
	Close() error
}

// Start launches the daemon in the background and waits until its socket
// answers. It returns the error of the process when it died before the socket
// appeared, with the tail of the journal, so the caller can see why.
//
// A daemon already running for the profile is not an error: Start reports it and
// does not launch a second process, because two processes must never share one
// Telegram session.
func Start(ctx context.Context, opts StartOptions) (Background, error) {
	bg := Background{Socket: opts.Paths.Socket, Log: opts.Paths.Log}
	if err := os.MkdirAll(opts.Paths.Dir, 0o700); err != nil {
		return bg, fmt.Errorf("create profile directory: %w", err)
	}
	timeout := opts.StartTimeout
	if timeout == 0 {
		timeout = StartTimeout
	}

	// An answer on the socket means a daemon already owns the profile: report it
	// rather than start a second client on the same session.
	if pid, err := pingOnce(ctx, opts); err == nil {
		bg.PID = pid
		return bg, nil
	} else if !isNotRunning(err) {
		return bg, err
	}

	launch := opts.Launcher
	if launch == nil {
		launch = Launch
	}
	pid, wait, err := launch(opts.Args, opts.Paths.Log)
	if err != nil {
		return bg, fmt.Errorf("start the daemon in the background: %w", err)
	}
	bg.PID = pid

	// The exit of the process is collected once, in its own goroutine, so the
	// wait below can look at it repeatedly without losing the answer.
	exited := make(chan error, 1)
	if wait != nil {
		go func() { exited <- wait() }()
	}

	deadline := time.Now().Add(timeout)
	for {
		pollCtx, cancel := context.WithTimeout(ctx, min(readyPollEvery, time.Until(deadline)))
		gotPID, pingErr := pingOnce(pollCtx, opts)
		cancel()
		switch {
		case pingErr == nil:
			if gotPID != 0 {
				bg.PID = gotPID
			}
			return bg, nil
		case ctx.Err() != nil:
			return bg, ctx.Err()
		case !isNotRunning(pingErr) && !errors.Is(pingErr, context.DeadlineExceeded):
			// The socket exists and answers something else: the process is
			// alive, so this is a broken profile rather than a failed start.
			return bg, pingErr
		}

		// Has the process given up? A short wait between two polls is enough to
		// notice it, and it keeps the loop moving when it has not.
		select {
		case waitErr := <-exited:
			return bg, earlyExit(waitErr, opts.Paths.Log)
		case <-time.After(readyPollEvery):
		}
		if time.Now().After(deadline) {
			return bg, fmt.Errorf("the daemon did not answer on %s within %s; see %s", opts.Paths.Socket, timeout, opts.Paths.Log)
		}
	}
}

// Stop asks the daemon to stop and waits until it is gone. A daemon already gone
// is not an error: the state the caller wants already holds.
//
// The daemon is gone when its socket stops answering, or when the process is no
// longer there. The socket is what a caller observes, so a daemon that runs in
// the caller's own process (a test, a foreground start) is handled as well.
func Stop(ctx context.Context, socket string, pid int, timeout time.Duration) error {
	if ok, err := processAlive(pid); pid > 0 && pid != os.Getpid() && err == nil && !ok {
		return nil // it has already exited
	}
	if c, err := rpc.Dial(ctx, "unix", socket); err == nil {
		var res rpc.ShutdownResult
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		callErr := c.Call(callCtx, rpc.MethodShutdown, nil, &res)
		cancel()
		_ = c.Close()
		if callErr != nil && !isNotRunning(callErr) {
			return fmt.Errorf("ask the daemon to stop: %w", callErr)
		}
	} else if !isNotRunning(err) {
		return fmt.Errorf("connect to the daemon: %w", err)
	}

	ownProcess := pid == os.Getpid()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := pingOnce(ctx, StartOptions{Paths: Paths{Socket: socket}}); isNotRunning(err) {
			return nil // the socket is gone: the daemon has stopped
		}
		if pid > 0 && !ownProcess {
			if alive, err := processAlive(pid); err == nil && !alive {
				return nil
			}
		}
		select {
		case <-time.After(readyPollEvery):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("the daemon did not stop within %s", timeout)
}

// Ping asks the daemon of a profile for its process id. It is what a start
// waits for and what a stop confirms.
func Ping(ctx context.Context, socket string) (int, error) {
	return pingOnce(ctx, StartOptions{Paths: Paths{Socket: socket}})
}

// pingOnce asks the socket once for the process id of the daemon.
func pingOnce(ctx context.Context, opts StartOptions) (int, error) {
	dial := opts.Dial
	if dial == nil {
		dial = func(ctx context.Context, socket string) (pinger, error) {
			return rpc.Dial(ctx, "unix", socket)
		}
	}
	c, err := dial(ctx, opts.Paths.Socket)
	if err != nil {
		return 0, err
	}
	defer c.Close()
	var st rpc.StatusResult
	if err := c.Call(ctx, rpc.MethodStatus, nil, &st); err != nil {
		return 0, err
	}
	return st.PID, nil
}

// isNotRunning reports whether an error means the daemon is not there, which is
// the state a start expects before it launches and a stop accepts as done.
func isNotRunning(err error) bool {
	return err != nil && rpc.IsNotRunning(err)
}

// earlyExit builds the error of a daemon that died before its socket answered,
// with the tail of its journal: the journal is the only thing that says why.
func earlyExit(waitErr error, logPath string) error {
	tail := logTail(logPath, logTailSize)
	reason := "the daemon exited before it answered"
	if waitErr != nil {
		reason = fmt.Sprintf("%s: %v", reason, waitErr)
	}
	if waitErr != nil {
		if tail == "" {
			return fmt.Errorf("the daemon exited before it answered: %w; its journal is empty", waitErr)
		}
		return fmt.Errorf("the daemon exited before it answered: %w; last lines of %s:\n%s", waitErr, logPath, tail)
	}
	if tail == "" {
		return errors.New(reason + "; its journal is empty")
	}
	return fmt.Errorf("%s; last lines of %s:\n%s", reason, logPath, tail)
}

// logTail returns the last bytes of a file as lines, or "" when it cannot be
// read. A journal that does not exist is not an error of the caller: the error
// it decorates is the one that matters.
func logTail(path string, limit int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	offset := max(info.Size()-limit, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return ""
	}
	text := strings.TrimRight(string(data), "\n")
	if offset > 0 {
		// The first line is probably cut in the middle.
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		}
	}
	return text
}

// processAlive reports whether the process exists. On a Unix system signal 0
// checks for it without touching it.
func processAlive(pid int) (bool, error) {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	return signalZero(p) == nil, nil
}
