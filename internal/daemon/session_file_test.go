package daemon

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"tg-cli/internal/sessionstr"
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
	if _, err = guardSessionStart(q); !errors.Is(err, ErrSessionInUse) {
		t.Fatalf("running duplicate: %v, want ErrSessionInUse", err)
	}
	if err = WithProfileLock(p, func() error { return nil }); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("running profile: %v, want ErrAlreadyRunning", err)
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
	auditStartLockHolder(t, dir)
	p := Paths{Dir: dir, Lock: filepath.Join(dir, "daemon.lock")}
	if err := WithProfileLock(p, func() error { return nil }); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second process acquired lock: %v", err)
	}
}

func TestDuplicateKeyAcrossProcesses(t *testing.T) {
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
	auditStartLockHolder(t, first.Dir)
	release, err := guardSessionStart(second)
	if err == nil {
		release()
		t.Fatal("duplicate key accepted")
	}
	if !errors.Is(err, ErrSessionInUse) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDuplicateProfilesSkipsTheDatabaseDirectory keeps the db directory next to
// the config out of the profile walk: it is not a profile and has no session.
func TestDuplicateProfilesSkipsTheDatabaseDirectory(t *testing.T) {
	base := t.TempDir()
	p := Paths{Dir: filepath.Join(base, "one")}
	p.Lock = filepath.Join(p.Dir, "daemon.lock")
	p.Session = filepath.Join(p.Dir, "session.json")
	db := filepath.Join(base, ReservedProfile)
	if err := os.MkdirAll(db, 0o700); err != nil {
		t.Fatal(err)
	}
	// A session of the same key inside the db directory must not be read: the
	// walk would create a lock file there and report a false duplicate.
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:]}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(db, "session.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, keyID, err := sessionstr.Inspect(raw)
	if err != nil {
		t.Fatal(err)
	}
	matches, err := DuplicateProfiles(p, keyID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("the db directory was read as a profile: %v", matches)
	}
	if _, err := os.Stat(filepath.Join(db, "daemon.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a lock was created in the db directory: %v", err)
	}
}
