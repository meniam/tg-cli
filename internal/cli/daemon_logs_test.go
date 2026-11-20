package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

func TestDaemonLogsTailWithoutDaemon(t *testing.T) {
	e := newEnv(t)
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	wantFailure(t, e.run("daemon", "logs"), exitcode.NotFound)
	wantFailure(t, e.run("daemon", "logs", "--tail", "0"), exitcode.Usage)
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Log, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("daemon", "logs", "--tail", "2")
	if r.code != exitcode.OK {
		t.Fatalf("logs: %d %s", r.code, r.stdout)
	}
	var data daemonLogsResult
	if err := json.Unmarshal(parse(t, r).Data, &data); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(data.Lines, ","); got != "two,three" {
		t.Errorf("tail = %q", got)
	}
	r = e.run("daemon", "logs", "--format", "jsonl", "--tail", "1")
	if r.code != exitcode.OK || r.stdout != "\"three\"\n" {
		t.Errorf("jsonl: %d %q", r.code, r.stdout)
	}
}

func TestDaemonLogsFollowSurvivesRotation(t *testing.T) {
	e := newEnv(t)
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Log, []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr safeBuffer
	// A safety net only: every phase below waits on its own deadline.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- Execute(ctx, []string{"daemon", "logs", "--follow", "--format", "jsonl"}, e.deps(&stdout, &stderr))
	}()
	audit01WaitFor(t, &stdout, "initial", "initial log line")
	audit01Append(t, paths.Log, "next\n")
	audit01WaitFor(t, &stdout, "next", "appended log line")
	if err := os.Rename(paths.Log, paths.Log+".1"); err != nil {
		t.Fatal(err)
	}
	// The new journal is longer than the offset already read (13 bytes), so only the
	// change of file identity, not the size, tells the follower about the rotation.
	if err := os.WriteFile(paths.Log, []byte("rotated-journal-with-a-long-first-line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	audit01WaitFor(t, &stdout, "rotated-journal-with-a-long-first-line", "rotated log line")
	cancel()
	if code := <-done; code != exitcode.OK {
		t.Errorf("follow exit = %d, stderr %q", code, stderr.String())
	}
}
