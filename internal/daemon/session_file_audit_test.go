package daemon

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// audit06StartLockHolder starts a child test process that holds the profile lock
// of dir until the test ends. The child is killed after a safety timeout, so a
// hung child cannot hold the test until the go test deadline, and the child's
// exit status is checked at the end.
func audit06StartLockHolder(t *testing.T, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProfileLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "TG_TEST_LOCK_DIR="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		defer cancel()
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Errorf("the lock holder failed: %v", err)
		}
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("the child did not lock the profile: line %q, err %v", line, err)
	}
}
