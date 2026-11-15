package daemon

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionFileAndDuplicate(t *testing.T) {
	base := t.TempDir()
	p := Paths{Dir: filepath.Join(base, "one")}
	p.Lock = filepath.Join(p.Dir, "daemon.lock")
	p.Session = filepath.Join(p.Dir, "session.json")
	q := Paths{Dir: filepath.Join(base, "two")}
	q.Lock = filepath.Join(q.Dir, "daemon.lock")
	q.Session = filepath.Join(q.Dir, "session.json")
	var err error
	key := make([]byte, 256)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	b, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:]}})
	if err != nil {
		t.Fatal(err)
	}
	if err = WithProfileLock(p, func() error { return WriteSession(p, b) }); err != nil {
		t.Fatal(err)
	}
	if err = WithProfileLock(q, func() error { return WriteSession(q, b) }); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(q.Session)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("session permissions: %v %v", st, err)
	}
	release, err := acquireLock(p.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = guardSessionStart(q); err == nil {
		t.Fatal("running duplicate accepted")
	}
	if err = WithProfileLock(p, func() error { return nil }); err == nil {
		t.Fatal("running profile accepted")
	}
	files, err := filepath.Glob(filepath.Join(q.Dir, ".session-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("temporary files left")
	}
}

// A child exit keeps its exit status available to the CLI.
type testExitStatus int

func (e testExitStatus) Error() string { return "child failed" }
func (e testExitStatus) ExitCode() int { return int(e) }
func TestEarlyExitPreservesStatus(t *testing.T) {
	err := earlyExit(testExitStatus(2), filepath.Join(t.TempDir(), "absent"))
	var status interface{ ExitCode() int }
	if !errors.As(err, &status) || status.ExitCode() != 2 {
		t.Fatal("child exit status was lost")
	}
}

func TestConcurrentProfileLocksExcludeImport(t *testing.T) {
	dir := t.TempDir()
	p := Paths{Dir: dir, Lock: filepath.Join(dir, "daemon.lock"), Session: filepath.Join(dir, "session.json")}
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- WithProfileLock(p, func() error { close(entered); <-release; return WriteSession(p, []byte("first")) })
	}()
	<-entered
	if err := WithProfileLock(p, func() error { return WriteSession(p, []byte("second")) }); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second import acquired locked profile: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(p.Session)
	if err != nil || string(contents) != "first" {
		t.Fatal("concurrent write changed session")
	}
	st, err := os.Stat(p.Session)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("session mode is not 0600")
	}
}

func TestProfileLockAcrossProcesses(t *testing.T) {
	dir := os.Getenv("TG_TEST_LOCK_DIR")
	if dir != "" {
		p := Paths{Dir: dir, Lock: filepath.Join(dir, "daemon.lock")}
		err := WithProfileLock(p, func() error { fmt.Println("locked"); _, err := io.Copy(io.Discard, os.Stdin); return err })
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	dir = t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestProfileLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "TG_TEST_LOCK_DIR="+dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("child did not lock profile: %v", err)
	}
	p := Paths{Dir: dir, Lock: filepath.Join(dir, "daemon.lock")}
	if err = WithProfileLock(p, func() error { return nil }); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second process acquired lock: %v", err)
	}
}

func TestDuplicateKeyAcrossProcesses(t *testing.T) {
	if os.Getenv("TG_TEST_LOCK_DIR") != "" {
		return
	}
	base := t.TempDir()
	first := Paths{Dir: filepath.Join(base, "first")}
	first.Lock = filepath.Join(first.Dir, "daemon.lock")
	first.Session = filepath.Join(first.Dir, "session.json")
	second := Paths{Dir: filepath.Join(base, "second")}
	second.Lock = filepath.Join(second.Dir, "daemon.lock")
	second.Session = filepath.Join(second.Dir, "session.json")
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:]}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []Paths{first, second} {
		if err = WithProfileLock(p, func() error { return WriteSession(p, raw) }); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestProfileLockAcrossProcesses$")
	cmd.Env = append(os.Environ(), "TG_TEST_LOCK_DIR="+first.Dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Wait() }()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatal("child did not acquire lock")
	}
	release, err := guardSessionStart(second)
	if err == nil {
		release()
		t.Fatal("duplicate key accepted")
	}
	if !errors.Is(err, ErrSessionInUse) {
		t.Fatalf("unexpected error: %v", err)
	}
}
