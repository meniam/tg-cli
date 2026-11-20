package cli

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
)

// audit01WaitFor waits for want to appear in the output of a command that runs in
// another goroutine. Each call has its own deadline.
func audit01WaitFor(t *testing.T, out *safeBuffer, want, what string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !strings.Contains(out.String(), want) {
		select {
		case <-deadline:
			t.Fatalf("%s was not printed; output so far: %q", what, out.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func audit01Append(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func audit01StartFollow(t *testing.T, e *testEnv, initial string) (path string, stdout *safeBuffer, stop func()) {
	t.Helper()
	paths, err := daemon.ResolvePaths(e.home, e.dbRoot(), daemon.DefaultProfile, daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.Log, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout = &safeBuffer{}
	var stderr safeBuffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	done := make(chan int, 1)
	go func() {
		done <- Execute(ctx, []string{"daemon", "logs", "--follow", "--format", "jsonl"}, e.deps(stdout, &stderr))
	}()
	return paths.Log, stdout, func() {
		cancel()
		if code := <-done; code != exitcode.OK {
			t.Errorf("follow exit = %d, stderr %q", code, stderr.String())
		}
	}
}

func TestAudit01_DaemonLogsTailUpperBound(t *testing.T) {
	e := newEnv(t)
	r := e.run("daemon", "logs", "--tail", "100000000")
	wantFailure(t, r, exitcode.Usage)
	if !strings.Contains(r.stdout, "--tail must be between 1 and 10000") {
		t.Fatalf("message: %q", r.stdout)
	}
}

// An unfinished line must wait for its newline: a writer that appends in two steps
// must not be printed as two entries.
func TestAudit01_DaemonLogsFollowHoldsBackAnUnfinishedLine(t *testing.T) {
	e := newEnv(t)
	path, stdout, stop := audit01StartFollow(t, e, "initial\n")
	defer stop()
	audit01WaitFor(t, stdout, "initial", "initial log line")
	audit01Append(t, path, "par")
	// There is no event to wait for when nothing must happen: give the follower more
	// than three polling ticks (100 ms each) to wrongly print the fragment.
	time.Sleep(350 * time.Millisecond)
	if got := stdout.String(); strings.Contains(got, "par") {
		t.Fatalf("an unfinished line was printed: %q", got)
	}
	audit01Append(t, path, "tial\n")
	audit01WaitFor(t, stdout, "partial", "completed line")
	if got := stdout.String(); got != "\"initial\"\n\"partial\"\n" {
		t.Fatalf("output = %q", got)
	}
}

// The journal can also be truncated in place (same file, smaller size).
func TestAudit01_DaemonLogsFollowSurvivesTruncation(t *testing.T) {
	e := newEnv(t)
	path, stdout, stop := audit01StartFollow(t, e, "initial-line-before-truncation\n")
	defer stop()
	audit01WaitFor(t, stdout, "initial-line-before-truncation", "initial log line")
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	audit01Append(t, path, "tr\n")
	audit01WaitFor(t, stdout, `"tr"`, "line after truncation")
}
