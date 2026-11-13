package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

// TestStartWaitsForTheSocket is the contract of the decision: a background start
// answers only when the daemon accepts requests, and reports the socket, the
// journal and the pid the daemon itself names.
func TestStartWaitsForTheSocket(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	stop := startBackgroundDaemon(t, paths)
	defer stop()

	// The launcher must not run: the socket already answers, so a second client
	// on the session would make Telegram log the account out.
	bg, err := Start(context.Background(), StartOptions{
		Paths: paths,
		Launcher: func([]string, string) (int, func() error, error) {
			t.Error("a start on a running profile launched another process")
			return 0, nil, nil
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if bg.PID != os.Getpid() {
		t.Errorf("pid = %d, want the daemon pid %d", bg.PID, os.Getpid())
	}
	if bg.Socket != paths.Socket || bg.Log != paths.Log {
		t.Errorf("start must report the socket and the journal: %+v", bg)
	}
}

// TestStartLaunchesAndWaits checks the other branch: with nothing on the socket
// Start launches the child and waits for it to answer.
func TestStartLaunchesAndWaits(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	var launched []string
	bg, err := Start(context.Background(), StartOptions{
		Paths: paths,
		Args:  []string{"daemon", "start", "--foreground", "--profile", "test"},
		Launcher: func(args []string, logPath string) (int, func() error, error) {
			launched = args
			if logPath != paths.Log {
				t.Errorf("the child journal = %q, want %q", logPath, paths.Log)
			}
			// The "child" is a daemon in this process on the same socket.
			stop := startBackgroundDaemon(t, paths)
			t.Cleanup(stop)
			return 4242, func() error { return nil }, nil
		},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(launched) == 0 {
		t.Fatal("a start with no daemon must launch the child")
	}
	for _, arg := range launched {
		if strings.Contains(arg, "APIHASH") || arg == "12345" {
			t.Errorf("a credential reached the command line of the child: %q", arg)
		}
	}
	if bg.PID != os.Getpid() {
		t.Errorf("pid = %d, want the daemon pid %d", bg.PID, os.Getpid())
	}
}

// TestStartReportsAnEarlyExit checks the failure path the decision names: a
// process that dies before its socket answers produces an error with the tail of
// its journal, not a silent success.
func TestStartReportsAnEarlyExit(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := "level=ERROR msg=\"open the database: permission denied\"\n"
	if err := os.WriteFile(paths.Log, []byte(journal), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Start(context.Background(), StartOptions{
		Paths: paths,
		Launcher: func([]string, string) (int, func() error, error) {
			exited := make(chan error, 1)
			exited <- errors.New("exit status 1")
			return 4242, func() error { return <-exited }, nil
		},
	})
	if err == nil {
		t.Fatal("a process that died must not be reported as started")
	}
	for _, want := range []string{"permission denied", paths.Log, "exit status 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must contain %q: %v", want, err)
		}
	}
}

// TestStartTimesOut checks the bound of the decision: a process that never
// answers makes Start fail within the timeout instead of waiting for ever.
func TestStartTimesOut(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	defer close(block)
	_, err = Start(context.Background(), StartOptions{
		Paths:        paths,
		StartTimeout: 150 * time.Millisecond,
		Launcher: func([]string, string) (int, func() error, error) {
			return 4242, func() error { <-block; return nil }, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("a daemon that never answers must be reported: %v", err)
	}
}

// TestStopWaitsUntilTheDaemonIsGone checks that a stop confirms the daemon has
// stopped instead of only asking it to.
func TestStopWaitsUntilTheDaemonIsGone(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	stop := startBackgroundDaemon(t, paths)
	defer stop()
	if err := Stop(context.Background(), paths.Socket, os.Getpid(), 5*time.Second); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := Ping(context.Background(), paths.Socket); err == nil {
		t.Error("after a stop the socket must not answer")
	}
}

// startBackgroundDaemon runs a daemon on paths and returns a function that stops
// it and waits for the process.
func startBackgroundDaemon(t *testing.T, paths Paths) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	listening := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, Options{
			Profile:     "test",
			Paths:       paths,
			Telegram:    newFake(),
			Log:         slog.New(slog.DiscardHandler),
			OnListening: func() { close(listening) },
		})
	}()
	select {
	case <-listening:
	case err := <-finished:
		cancel()
		t.Fatalf("daemon exited early: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("daemon did not start")
	}
	return func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	}
}

// TestJournalIsTrimmedOnStart keeps the journal bounded, which the decision
// requires: an old journal only explains an old failure.
func TestJournalIsTrimmedOnStart(t *testing.T) {
	paths, err := ResolvePaths(shortTempDir(t), "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Larger than the limit, with a marker at the start that must be gone.
	line := strings.Repeat("x", 1023) + "\n"
	content := "OLDEST-MARKER\n" + strings.Repeat(line, maxLogBytes/len(line)+2)
	if err := os.WriteFile(paths.Log, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := trimLog(paths.Log, maxLogBytes); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > maxLogBytes {
		t.Errorf("the journal is %d bytes, the limit is %d", info.Size(), maxLogBytes)
	}
	data, err := os.ReadFile(paths.Log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "OLDEST-MARKER") {
		t.Error("the journal must keep its end, not its start")
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("journal mode = %o, want 600", perm)
	}
}
