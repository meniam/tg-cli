package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// The background start is tested with a fake launcher and a daemon running in
// this process: no process is created outside the test and no Telegram client
// beyond the fake the test environment already provides. The live check of a
// real detached process belongs to the owner's step.

// fakeLauncher stands for the detached process of a background start.
type fakeLauncher struct {
	args     []string
	logPath  string
	calls    int
	exited   chan error
	onLaunch func()
}

func (f *fakeLauncher) launch(args []string, logPath string) (int, func() error, error) {
	f.calls++
	f.args, f.logPath = args, logPath
	if f.onLaunch != nil {
		f.onLaunch()
	}
	return 4242, func() error { return <-f.exited }, nil
}

// backgroundEnv is a test environment whose background start uses a fake
// launcher. The daemon the child "becomes" is the one the test drives.
func backgroundEnv(t *testing.T) (*testEnv, *fakeLauncher) {
	t.Helper()
	e := newEnv(t)
	f := &fakeLauncher{exited: make(chan error, 1)}
	e.launch = f.launch
	return e, f
}

// TestDaemonStartInBackground is the contract of the decision: a start without
// --foreground answers when the socket is up and names the pid, the socket and
// the journal. The child is told to run in the foreground and its arguments
// carry no credential, because `ps` shows them to every user.
func TestDaemonStartInBackground(t *testing.T) {
	e, f := backgroundEnv(t)
	paths, err := daemon.ResolvePaths(e.home, daemon.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	f.onLaunch = func() { e.startDaemon() }

	r := e.run("daemon", "start")
	if r.code != exitcode.OK {
		t.Fatalf("background start: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	var res backgroundStartedResult
	if err := json.Unmarshal(parse(t, r).Data, &res); err != nil {
		t.Fatal(err)
	}
	if !res.Running || res.PID <= 0 || res.Socket != paths.Socket || res.Log != paths.Log {
		t.Errorf("the answer must name the running daemon: %+v", res)
	}
	want := "daemon start --foreground --profile default"
	if got := strings.Join(f.args, " "); got != want {
		t.Errorf("child arguments = %q, want %q", got, want)
	}
	for _, arg := range f.args {
		if strings.Contains(arg, "APIHASHVALUE") || arg == "12345" {
			t.Errorf("a credential reached the command line: %q", arg)
		}
	}
	// The journal path is the one thing a user needs when the daemon misbehaves.
	if !strings.Contains(r.stderr, paths.Log) {
		t.Errorf("the caller must be told where the journal is: %q", r.stderr)
	}
}

// TestDaemonStartReportsAnEarlyExit is the failure path of the decision: a
// process that dies before the socket answers produces an error with the tail of
// its journal, not a silent success.
func TestDaemonStartReportsAnEarlyExit(t *testing.T) {
	e, f := backgroundEnv(t)
	paths, err := daemon.ResolvePaths(e.home, daemon.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	// No daemon is started here: the child is supposed to fail, and the test
	// only checks what the caller is told.
	f.onLaunch = func() {
		line := "level=ERROR msg=\"open the database: permission denied\"\n"
		if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(paths.Log, []byte(line), 0o600); err != nil {
			t.Error(err)
		}
		f.exited <- nil
	}

	r := e.run("daemon", "start")
	wantFailure(t, r, exitcode.Failure)
	if !strings.Contains(r.stdout, "permission denied") {
		t.Errorf("the journal tail must reach the caller: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, paths.Log) {
		t.Errorf("the answer must name the journal: %q", r.stdout)
	}
}

// TestDaemonStartTwiceKeepsOneClient is the rule that protects the session: a
// start on a profile that already answers must not launch another process, since
// two clients on one session make Telegram log the account out.
func TestDaemonStartTwiceKeepsOneClient(t *testing.T) {
	e, f := backgroundEnv(t)
	f.onLaunch = func() { e.startDaemon() }
	if r := e.run("daemon", "start"); r.code != exitcode.OK {
		t.Fatalf("first start: %q", r.stdout)
	}
	before := f.calls
	r := e.run("daemon", "start")
	if r.code != exitcode.OK {
		t.Errorf("a second start must report the running daemon, exit %d, %q", r.code, r.stdout)
	}
	if f.calls != before {
		t.Errorf("a second start launched another process: %d new calls", f.calls-before)
	}
}

// TestDaemonStopConfirmsTheProcessIsGone checks that a stop waits for the daemon
// instead of only asking it to stop: after the answer the socket is closed and a
// second stop says the daemon is not running.
func TestDaemonStopConfirmsTheProcessIsGone(t *testing.T) {
	e, f := backgroundEnv(t)
	f.onLaunch = func() { e.startDaemon() }
	if r := e.run("daemon", "start"); r.code != exitcode.OK {
		t.Fatalf("start: %q", r.stdout)
	}
	if r := e.run("daemon", "stop"); r.code != exitcode.OK {
		t.Fatalf("stop: exit %d, %q", r.code, r.stdout)
	}
	wantFailure(t, e.run("daemon", "stop"), exitcode.DaemonNotRunning)
}
