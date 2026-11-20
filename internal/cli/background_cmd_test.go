package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/store"
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
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
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
	// The daemon runs inside this process, so its pid is ours, not the launcher's 4242.
	if !res.Running || res.PID != os.Getpid() || res.Socket != paths.Socket || res.Log != paths.Log {
		t.Errorf("the answer must name the running daemon: %+v", res)
	}
	want := "daemon start --foreground --profile default --journal " + paths.Log
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
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
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

// A live daemon is reported before the caller needs credentials to launch one.
func TestDaemonStartReportsLiveProfileWithoutCredentials(t *testing.T) {
	e, f := backgroundEnv(t)
	e.startDaemon()
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	pid, err := daemon.Ping(t.Context(), paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	delete(e.env, "TG_API_ID")
	delete(e.env, "TG_API_HASH")
	r := e.run("daemon", "start")
	if r.code != exitcode.OK {
		t.Fatalf("existing daemon: exit %d, %q", r.code, r.stdout)
	}
	var got backgroundStartedResult
	if err := json.Unmarshal(parse(t, r).Data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Running || got.PID != pid || got.Profile != daemon.DefaultProfile || got.Socket != paths.Socket || got.Log != paths.Log {
		t.Errorf("existing daemon response: %+v", got)
	}
	if f.calls != 0 {
		t.Errorf("launcher called %d times for a live profile", f.calls)
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

func TestProxySecretDoesNotReachBackgroundOutputs(t *testing.T) {
	e, f := backgroundEnv(t)
	secret := "unique-test-proxy-secret"
	e.env["TG_CLI_PROXY"] = "socks5://user:" + secret + "@proxy.example:1080"
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	f.onLaunch = func() {
		if err := os.WriteFile(paths.Log, []byte("startup failed\n"), 0600); err != nil {
			t.Error(err)
		}
		f.exited <- nil
	}
	result := e.run("--verbose", "daemon", "start")
	wantFailure(t, result, exitcode.Failure)
	for _, part := range append(append([]string{}, f.args...), result.stdout, result.stderr) {
		if strings.Contains(part, secret) || strings.Contains(part, "--proxy") {
			t.Fatalf("proxy secret or flag reached output or child arguments: %q", part)
		}
	}
	if !strings.Contains(e.savedProfile("default").Proxy, secret) {
		t.Fatal("proxy was not persisted")
	}
}

// TestBackgroundStartMovesTheDatabase is the rule of the decision: the parent
// moves the old database before it launches the child, so a big move does not eat
// the start timeout and a failure is an answer of this command. The child finds
// the database in place and only opens it.
func TestBackgroundStartMovesTheDatabase(t *testing.T) {
	e, f := backgroundEnv(t)
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A database of the old layout with a row, so the moved file can be read back.
	db, err := store.Open(paths.LegacyDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAccountState(t.Context(), "move.test", json.RawMessage(`{"value":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f.onLaunch = func() {
		// The child never sees the old file: the parent has moved it already.
		if _, err := os.Stat(paths.LegacyDatabase); !os.IsNotExist(err) {
			t.Errorf("the child was launched before the move: %v", err)
		}
		if _, err := os.Stat(paths.Database); err != nil {
			t.Errorf("the database is not in place at launch: %v", err)
		}
		e.startDaemon()
	}
	r := e.run("daemon", "start")
	if r.code != exitcode.OK {
		t.Fatalf("background start: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	// The parent did the move, so it tells the caller: the child logs the line only
	// in a foreground run.
	want := "note: moved the database from " + paths.LegacyDatabase + " to " + paths.Database
	if !strings.Contains(r.stderr, want) {
		t.Errorf("the note about the move is missing: %q", r.stderr)
	}
	// The answer does not change: the note is not a field.
	if strings.Contains(r.stdout, "moved the database") {
		t.Errorf("the note reached the answer: %q", r.stdout)
	}
	if _, err := os.Stat(paths.LegacyDatabase); !os.IsNotExist(err) {
		t.Fatalf("the old database remains: %v", err)
	}
	moved, err := store.Open(paths.Database)
	if err != nil {
		t.Fatalf("open the moved database: %v", err)
	}
	defer moved.Close()
	raw, found, err := moved.GetAccountState(t.Context(), "move.test")
	if err != nil || !found || string(raw) != `{"value":1}` {
		t.Fatalf("the moved row: %q %v %v", raw, found, err)
	}
}

// TestBackgroundStartMoveNoteStaysQuiet keeps --quiet silencing the note, like
// every other hint of the command.
func TestBackgroundStartMoveNoteStaysQuiet(t *testing.T) {
	e, f := backgroundEnv(t)
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(paths.LegacyDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAccountState(t.Context(), "move.test", json.RawMessage(`{"value":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f.onLaunch = func() { e.startDaemon() }
	r := e.run("daemon", "start", "--quiet")
	if r.code != exitcode.OK {
		t.Fatalf("background start: exit %d, %q, stderr %q", r.code, r.stdout, r.stderr)
	}
	if strings.Contains(r.stderr, "moved the database") {
		t.Errorf("--quiet did not silence the note: %q", r.stderr)
	}
	if _, err := os.Stat(paths.Database); err != nil {
		t.Errorf("the database was not moved: %v", err)
	}
}

// TestBackgroundStartReportsAMoveConflict keeps a failed move an answer of the
// command, not a journal line of a child that never started: the child is not
// launched and the exit code is the configuration class.
func TestBackgroundStartReportsAMoveConflict(t *testing.T) {
	e, f := backgroundEnv(t)
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{paths.Dir, paths.DatabaseDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Two databases with the same name and different content: a conflict.
	if err := os.WriteFile(paths.LegacyDatabase, []byte(databaseFileContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Database, []byte(databaseFileContents+" newer"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("daemon", "start")
	if r.code != exitcode.Usage || !strings.Contains(r.stdout, "both places") {
		t.Fatalf("move conflict: exit %d, %q", r.code, r.stdout)
	}
	if f.calls != 0 {
		t.Errorf("the child was launched after a failed move: %d calls", f.calls)
	}
	if _, err := os.Stat(paths.LegacyDatabase); err != nil {
		t.Errorf("a refused move must leave the old database: %v", err)
	}
}
