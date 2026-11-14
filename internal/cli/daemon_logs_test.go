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
	paths, err := daemon.ResolvePaths(e.home, daemon.DefaultProfile)
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
	paths, err := daemon.ResolvePaths(e.home, daemon.DefaultProfile)
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan int, 1)
	go func() {
		done <- Execute(ctx, []string{"daemon", "logs", "--follow", "--format", "jsonl"}, e.deps(&stdout, &stderr))
	}()
	deadline := time.After(2 * time.Second)
	for !strings.Contains(stdout.String(), "initial") {
		select {
		case <-deadline:
			t.Fatal("initial log line was not printed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	f, err := os.OpenFile(paths.Log, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("next\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	for !strings.Contains(stdout.String(), "next") {
		select {
		case <-deadline:
			t.Fatal("appended log line was not printed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := os.Rename(paths.Log, paths.Log+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Log, []byte("rotated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(stdout.String(), "rotated") {
		select {
		case <-deadline:
			t.Fatal("rotated log line was not printed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if code := <-done; code != exitcode.OK {
		t.Errorf("follow exit = %d, stderr %q", code, stderr.String())
	}
}
