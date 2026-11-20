package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/rpc"
)

func audit03ReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The copies keep the newest lines in order: .1 is the previous journal, .3
// the oldest, and the journal itself holds the latest write.
func TestAudit03JournalCopiesKeepOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	j, err := openJournal(path, 12)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	for _, line := range []string{"line-1\n", "line-2\n", "line-3\n", "line-4\n", "line-5\n"} {
		if _, err := j.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for suffix, want := range map[string]string{"": "line-5\n", ".1": "line-4\n", ".2": "line-3\n", ".3": "line-2\n"} {
		if got := audit03ReadFile(t, path+suffix); got != want {
			t.Errorf("journal%s = %q, want %q", suffix, got, want)
		}
	}
}

// An old journal over the limit is rotated when it is opened.
func TestAudit03OpenRotatesAnOversizedJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	old := strings.Repeat("old\n", 10)
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := openJournal(path, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if got := audit03ReadFile(t, path+".1"); got != old {
		t.Fatalf("copy .1 = %q, want the old journal", got)
	}
	if info, err := os.Stat(path); err != nil || info.Size() != 0 {
		t.Fatalf("the new journal is not empty: %v %v", info, err)
	}
}

// A write after Close fails instead of reopening the file.
func TestAudit03WriteAfterCloseFails(t *testing.T) {
	j, err := openJournal(filepath.Join(t.TempDir(), "daemon.log"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := j.Write([]byte("late\n")); !errors.Is(err, os.ErrClosed) || n != 0 {
		t.Fatalf("write after close = %d, %v, want os.ErrClosed", n, err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// A single write larger than the limit is still written whole, into a fresh
// journal, and what was there before moves to .1.
func TestAudit03WriteLargerThanTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	j, err := openJournal(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, err := j.Write([]byte("small\n")); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 50) + "\n"
	if n, err := j.Write([]byte(big)); err != nil || n != len(big) {
		t.Fatalf("big write = %d, %v", n, err)
	}
	if got := audit03ReadFile(t, path); got != big {
		t.Fatalf("journal = %q, want the big line", got)
	}
	if got := audit03ReadFile(t, path+".1"); got != "small\n" {
		t.Fatalf("copy .1 = %q", got)
	}
}

func audit03Paths(t *testing.T) Paths {
	t.Helper()
	paths, err := ResolvePaths(shortTempDir(t), dbRoot(t), "test", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// A launcher that fails is reported, and no pid is claimed.
func TestAudit03StartReportsALauncherError(t *testing.T) {
	bg, err := Start(context.Background(), StartOptions{
		Paths: audit03Paths(t),
		Launcher: func([]string, string) (int, func() error, error) {
			return 0, nil, errors.New("exec format error")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "start the daemon in the background") || !strings.Contains(err.Error(), "exec format error") {
		t.Fatalf("Start = %v", err)
	}
	if bg.PID != 0 {
		t.Fatalf("pid = %d after a failed launch", bg.PID)
	}
}

// A canceled caller stops the wait at once, with its own error.
func TestAudit03StartStopsWhenTheContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	block := make(chan struct{})
	defer close(block)
	_, err := Start(ctx, StartOptions{
		Paths:        audit03Paths(t),
		StartTimeout: time.Minute,
		Launcher: func([]string, string) (int, func() error, error) {
			cancel()
			return 4242, func() error { <-block; return nil }, nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start = %v, want context.Canceled", err)
	}
}

// A socket that answers with something other than "not running" after the
// launch is a broken profile: Start returns that error instead of waiting.
func TestAudit03StartReturnsAnUnexpectedSocketError(t *testing.T) {
	boom := errors.New("permission denied on the socket")
	calls := 0
	block := make(chan struct{})
	defer close(block)
	_, err := Start(context.Background(), StartOptions{
		Paths:        audit03Paths(t),
		StartTimeout: time.Minute,
		Dial: func(context.Context, string) (pinger, error) {
			calls++
			if calls == 1 {
				return nil, os.ErrNotExist // nothing runs before the launch
			}
			return nil, boom
		},
		Launcher: func([]string, string) (int, func() error, error) {
			return 4242, func() error { <-block; return nil }, nil
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Start = %v, want the socket error", err)
	}
}

// A pid that is already gone ends Stop at once, and the daemon on the socket
// is left alone: the pid, not the socket, decided.
func TestAudit03StopIgnoresAnExitedProcess(t *testing.T) {
	paths := audit03Paths(t)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stop := startBackgroundDaemon(t, paths)
	defer stop()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(context.Background(), paths.Socket, cmd.Process.Pid, 5*time.Second); err != nil {
		t.Fatalf("Stop of an exited pid = %v", err)
	}
	if _, err := Ping(context.Background(), paths.Socket); err != nil {
		t.Fatalf("the daemon was stopped although the pid was gone: %v", err)
	}
}

// A daemon that accepts the shutdown but stays up is reported after the timeout.
func TestAudit03StopReportsADaemonThatStaysUp(t *testing.T) {
	socket := filepath.Join(shortTempDir(t), "d.sock")
	srv := rpc.NewServer(nil)
	srv.Handle(rpc.MethodShutdown, func(context.Context, json.RawMessage) (any, error) { return rpc.ShutdownResult{}, nil })
	srv.Handle(rpc.MethodStatus, func(context.Context, json.RawMessage) (any, error) { return rpc.StatusResult{PID: 1}, nil })
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(listener) }()
	defer srv.Close()
	err = Stop(context.Background(), socket, 0, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not stop within") {
		t.Fatalf("Stop = %v, want the timeout error", err)
	}
}

// The tail of a long journal starts at a whole line: the first line, probably
// cut by the byte offset, is dropped.
func TestAudit03LogTailDropsTheCutFirstLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.log")
	if err := os.WriteFile(path, []byte("aaaaaaaaaa-first\nsecond line\nthird line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 25 bytes from the end start in the middle of "aaaa…-first".
	if got := logTail(path, 28); got != "second line\nthird line" {
		t.Fatalf("tail = %q", got)
	}
	// A journal shorter than the limit is returned whole.
	if got := logTail(path, 4096); got != "aaaaaaaaaa-first\nsecond line\nthird line" {
		t.Fatalf("whole tail = %q", got)
	}
}
