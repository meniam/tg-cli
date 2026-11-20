package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type homeFixture struct {
	oldHome, newHome, dbRoot string
}

// newHomeFixture lays out the previous default data directory with the profile
// "default" and the service leftovers of earlier migrations, next to an empty
// new directory that already holds the configuration file, as it does for real.
func newHomeFixture(t *testing.T) homeFixture {
	t.Helper()
	root := shortTempDir(t)
	f := homeFixture{oldHome: filepath.Join(root, "old"), newHome: filepath.Join(root, "new"), dbRoot: filepath.Join(root, "new", "db")}
	f.addProfile(t, "default")
	writeTestFile(t, filepath.Join(f.oldHome, ".config-migration-20260101T000000", "config.toml"), "old config", 0o600)
	writeTestFile(t, filepath.Join(f.oldHome, ".db-backup-20260102T000000", "store.db"), "SQLite format 3\x00 backup", 0o600)
	writeTestFile(t, filepath.Join(f.oldHome, ".db-move-backup-20260103T000000", "store.db"), "SQLite format 3\x00 move backup", 0o600)
	writeTestFile(t, filepath.Join(f.oldHome, sessionStartLock), "", 0o600)
	writeTestFile(t, filepath.Join(f.newHome, "config.toml"), "# config", 0o600)
	return f
}

// addProfile creates a profile directory with the secrets, a journal, a stale
// lock and the record of its database pointing at it.
func (f homeFixture) addProfile(t *testing.T, name string) {
	t.Helper()
	dir := filepath.Join(f.oldHome, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "session.json"), "session of "+name, 0o600)
	writeTestFile(t, filepath.Join(dir, "access.json"), `{"read":"all"}`+name, 0o600)
	writeTestFile(t, filepath.Join(dir, "deletions.jsonl"), "deleted "+name+"\n", 0o600)
	writeTestFile(t, filepath.Join(dir, "daemon.log"), "log of "+name, 0o600)
	writeTestFile(t, filepath.Join(dir, "daemon.lock"), "", 0o600)
	writeTestFile(t, filepath.Join(f.dbRoot, name, "data-dir"), dir+"\n", 0o600)
}

func (f homeFixture) options() HomeMoveOptions {
	return HomeMoveOptions{
		DBRoot: f.dbRoot,
		Now:    func() time.Time { return time.Date(2026, 11, 20, 21, 40, 5, 0, time.UTC) },
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s must not exist: %v", path, err)
	}
}

// snapshot describes a tree by path, mode and content, so that "nothing was
// changed" is a comparison and not a list of spot checks. The lock file of the
// start is skipped: taking it is not a change of data.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if filepath.Base(path) == sessionStartLock {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content := ""
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			content = string(b)
		}
		out[rel] = fmt.Sprintf("%v %q", info.Mode(), content)
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out
		}
		t.Fatal(err)
	}
	return out
}

func assertSame(t *testing.T, label string, before, after map[string]string) {
	t.Helper()
	for path, v := range before {
		if after[path] != v {
			t.Errorf("%s: %s was %s, now %s", label, path, v, after[path])
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("%s: %s appeared", label, path)
		}
	}
}

func asHomeMoveError(t *testing.T, err error) *HomeMoveError {
	t.Helper()
	var moveErr *HomeMoveError
	if !errors.As(err, &moveErr) {
		t.Fatalf("error = %v, want a *HomeMoveError", err)
	}
	if moveErr.ExitCode() != 1 {
		t.Fatalf("exit code = %d, want 1", moveErr.ExitCode())
	}
	return moveErr
}

func TestMigrateLegacyHomeMovesEverything(t *testing.T) {
	f := newHomeFixture(t)
	// A stale socket of a daemon that died: the file stays, nobody listens.
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(f.oldHome, "default", "daemon.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	f.addProfile(t, "work")

	res, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Moved || strings.Join(res.Profiles, ",") != "default,work" {
		t.Fatalf("result = %+v", res)
	}
	wantBackup := filepath.Join(f.newHome, ".home-move-backup-20261120T214005")
	if res.Backup != wantBackup {
		t.Fatalf("backup = %q, want %q", res.Backup, wantBackup)
	}

	for _, name := range []string{"default", "work"} {
		dir := filepath.Join(f.newHome, name)
		assertMode(t, dir, 0o700)
		for file, content := range map[string]string{"session.json": "session of " + name, "access.json": `{"read":"all"}` + name, "deletions.jsonl": "deleted " + name + "\n", "daemon.log": "log of " + name} {
			if got := readTestFile(t, filepath.Join(dir, file)); got != content {
				t.Fatalf("%s/%s = %q, want %q", name, file, got, content)
			}
			assertMode(t, filepath.Join(dir, file), 0o600)
		}
		assertMissing(t, filepath.Join(dir, "daemon.sock"))
		assertMissing(t, filepath.Join(dir, "daemon.lock"))

		// The backup holds exactly the three secrets, readable by the owner only.
		backup := filepath.Join(res.Backup, name)
		assertMode(t, backup, 0o700)
		for _, file := range []string{"session.json", "access.json", "deletions.jsonl"} {
			if got, want := readTestFile(t, filepath.Join(backup, file)), readTestFile(t, filepath.Join(dir, file)); got != want {
				t.Fatalf("backup of %s/%s = %q, want %q", name, file, got, want)
			}
			assertMode(t, filepath.Join(backup, file), 0o600)
		}
		assertMissing(t, filepath.Join(backup, "daemon.log"))

		// The database record follows the profile, or the next start would refuse it.
		if got := strings.TrimSpace(readTestFile(t, filepath.Join(f.dbRoot, name, "data-dir"))); got != dir {
			t.Fatalf("data-dir record = %q, want %q", got, dir)
		}
	}
	assertMode(t, res.Backup, 0o700)
	for _, service := range []string{".config-migration-20260101T000000", ".db-backup-20260102T000000", ".db-move-backup-20260103T000000"} {
		if _, err := os.Stat(filepath.Join(f.newHome, service)); err != nil {
			t.Fatalf("service directory %s did not move: %v", service, err)
		}
	}
	if got := readTestFile(t, filepath.Join(f.newHome, "config.toml")); got != "# config" {
		t.Fatalf("the configuration file changed: %q", got)
	}
	// Everything that was in the old home is gone, and so is the home.
	assertMissing(t, f.oldHome)
}

func TestMigrateLegacyHomeIsIdempotent(t *testing.T) {
	f := newHomeFixture(t)
	if _, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options()); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, f.newHome)
	res, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options())
	if err != nil || res.Moved || res.Backup != "" || len(res.Profiles) != 0 {
		t.Fatalf("second run = %+v, %v; want nothing done", res, err)
	}
	assertSame(t, "second run", before, snapshot(t, f.newHome))
}

func TestMigrateLegacyHomeNothingToDo(t *testing.T) {
	t.Run("no old directory", func(t *testing.T) {
		f := newHomeFixture(t)
		if err := os.RemoveAll(f.oldHome); err != nil {
			t.Fatal(err)
		}
		fresh := filepath.Join(filepath.Dir(f.newHome), "fresh")
		res, err := MigrateLegacyHome(context.Background(), f.oldHome, fresh, f.options())
		if err != nil || res.Moved {
			t.Fatalf("result = %+v, %v", res, err)
		}
		assertMissing(t, fresh) // a clean machine never gets a directory from this call
	})
	t.Run("old directory without a profile", func(t *testing.T) {
		f := newHomeFixture(t)
		if err := os.RemoveAll(filepath.Join(f.oldHome, "default")); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, f.oldHome)
		res, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options())
		if err != nil || res.Moved {
			t.Fatalf("result = %+v, %v", res, err)
		}
		assertSame(t, "old home", before, snapshot(t, f.oldHome))
	})
	t.Run("same directory", func(t *testing.T) {
		f := newHomeFixture(t)
		before := snapshot(t, f.oldHome)
		res, err := MigrateLegacyHome(context.Background(), f.oldHome, f.oldHome+"/", f.options())
		if err != nil || res.Moved {
			t.Fatalf("result = %+v, %v", res, err)
		}
		assertSame(t, "home", before, snapshot(t, f.oldHome))
	})
	t.Run("empty names", func(t *testing.T) {
		if res, err := MigrateLegacyHome(context.Background(), "", "/x", HomeMoveOptions{}); err != nil || res.Moved {
			t.Fatalf("result = %+v, %v", res, err)
		}
	})
}

func TestMigrateLegacyHomeRefusesProfileInBothPlaces(t *testing.T) {
	f := newHomeFixture(t)
	writeTestFile(t, filepath.Join(f.newHome, "default", "session.json"), "newer session", 0o600)
	oldBefore, newBefore := snapshot(t, f.oldHome), snapshot(t, f.newHome)

	res, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options())
	moveErr := asHomeMoveError(t, err)
	if res.Moved {
		t.Fatalf("result = %+v", res)
	}
	for _, want := range []string{f.oldHome, f.newHome, "default", "nothing was changed"} {
		if !strings.Contains(moveErr.Message, want) {
			t.Errorf("message %q does not mention %q", moveErr.Message, want)
		}
	}
	assertSame(t, "old home", oldBefore, snapshot(t, f.oldHome))
	assertSame(t, "new home", newBefore, snapshot(t, f.newHome))
}

func TestMigrateLegacyHomeRefusesWhileTheOldDaemonRuns(t *testing.T) {
	t.Run("socket answers", func(t *testing.T) {
		f := newHomeFixture(t)
		socket := filepath.Join(f.oldHome, "default", "daemon.sock")
		l, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		opts := f.options()
		var asked string
		opts.Ping = func(_ context.Context, s string) (int, error) { asked = s; return 4242, nil }
		oldBefore, newBefore := snapshot(t, f.oldHome), snapshot(t, f.newHome)

		_, err = MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, opts)
		moveErr := asHomeMoveError(t, err)
		if asked != socket || !strings.Contains(moveErr.Message, "tg daemon stop") {
			t.Fatalf("asked %q, message %q", asked, moveErr.Message)
		}
		assertSame(t, "old home", oldBefore, snapshot(t, f.oldHome))
		assertSame(t, "new home", newBefore, snapshot(t, f.newHome))
	})
	t.Run("socket cannot be judged", func(t *testing.T) {
		f := newHomeFixture(t)
		l, err := net.Listen("unix", filepath.Join(f.oldHome, "default", "daemon.sock"))
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		opts := f.options()
		opts.Ping = func(context.Context, string) (int, error) { return 0, errors.New("garbled answer") }
		_, err = MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, opts)
		asHomeMoveError(t, err)
		if _, statErr := os.Stat(filepath.Join(f.oldHome, "default", "session.json")); statErr != nil {
			t.Fatal(statErr)
		}
	})
	t.Run("lock is held", func(t *testing.T) {
		f := newHomeFixture(t)
		release, err := acquireLock(filepath.Join(f.oldHome, "default", "daemon.lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		oldBefore := snapshot(t, f.oldHome)
		_, err = MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options())
		moveErr := asHomeMoveError(t, err)
		if !strings.Contains(moveErr.Message, "tg daemon stop") {
			t.Fatalf("message = %q", moveErr.Message)
		}
		assertSame(t, "old home", oldBefore, snapshot(t, f.oldHome))
		assertMissing(t, filepath.Join(f.newHome, "default"))
	})
}

// TestMigrateLegacyHomeAcrossVolumes drives the fallback of a rename that fails
// with EXDEV: the tree is copied, checked, and only then is the source removed.
func TestMigrateLegacyHomeAcrossVolumes(t *testing.T) {
	f := newHomeFixture(t)
	writeTestFile(t, filepath.Join(f.oldHome, "default", "notes", "readable.txt"), "kept as is", 0o644)
	opts := f.options()
	opts.Rename = func(oldPath, newPath string) error {
		if strings.HasPrefix(oldPath, f.oldHome) {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
		}
		return os.Rename(oldPath, newPath)
	}
	want := map[string]string{}
	for _, file := range []string{"session.json", "access.json", "deletions.jsonl", "daemon.log"} {
		want[file] = readTestFile(t, filepath.Join(f.oldHome, "default", file))
	}

	res, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, opts)
	if err != nil || !res.Moved {
		t.Fatalf("result = %+v, %v", res, err)
	}
	dir := filepath.Join(f.newHome, "default")
	for file, content := range want {
		if got := readTestFile(t, filepath.Join(dir, file)); got != content {
			t.Fatalf("%s = %q, want %q", file, got, content)
		}
		assertMode(t, filepath.Join(dir, file), 0o600)
	}
	assertMode(t, filepath.Join(dir, "notes", "readable.txt"), 0o644)
	assertMode(t, dir, 0o700)
	assertMissing(t, filepath.Join(dir, "daemon.lock"))
	assertMissing(t, f.oldHome)
	entries, err := os.ReadDir(f.newHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".home-move-") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a temporary copy was left: %s", e.Name())
		}
	}
}

// TestMigrateLegacyHomeLeavesNoHalfMove proves the rule of the move: a failure at
// any step puts everything back, including the record of the database and the
// secrets copied to the backup.
func TestMigrateLegacyHomeLeavesNoHalfMove(t *testing.T) {
	t.Run("second profile cannot be renamed", func(t *testing.T) {
		f := newHomeFixture(t)
		f.addProfile(t, "work")
		opts := f.options()
		opts.Rename = func(oldPath, newPath string) error {
			if filepath.Base(oldPath) == "work" {
				return errors.New("disk on fire")
			}
			return os.Rename(oldPath, newPath)
		}
		oldBefore, newBefore, dbBefore := snapshot(t, f.oldHome), snapshot(t, f.newHome), snapshot(t, f.dbRoot)

		res, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, opts)
		moveErr := asHomeMoveError(t, err)
		if res.Moved || !strings.Contains(moveErr.Message, "undone") || !strings.Contains(err.Error(), "disk on fire") {
			t.Fatalf("result = %+v, error = %v", res, err)
		}
		// The first profile arrived, rewrote its record and went back.
		assertSame(t, "old home", oldBefore, snapshot(t, f.oldHome))
		assertSame(t, "new home", newBefore, snapshot(t, f.newHome))
		assertSame(t, "db root", dbBefore, snapshot(t, f.dbRoot))
	})
	t.Run("a copy across volumes meets a file it cannot copy", func(t *testing.T) {
		f := newHomeFixture(t)
		if err := syscall.Mkfifo(filepath.Join(f.oldHome, "default", "pipe"), 0o600); err != nil {
			t.Skipf("no fifo here: %v", err)
		}
		opts := f.options()
		opts.Rename = func(oldPath, newPath string) error {
			return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: syscall.EXDEV}
		}
		oldBefore, newBefore, dbBefore := snapshot(t, f.oldHome), snapshot(t, f.newHome), snapshot(t, f.dbRoot)

		_, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, opts)
		asHomeMoveError(t, err)
		assertSame(t, "old home", oldBefore, snapshot(t, f.oldHome))
		assertSame(t, "new home", newBefore, snapshot(t, f.newHome))
		assertSame(t, "db root", dbBefore, snapshot(t, f.dbRoot))
	})
	t.Run("a service directory cannot be renamed", func(t *testing.T) {
		f := newHomeFixture(t)
		opts := f.options()
		opts.Rename = func(oldPath, newPath string) error {
			if strings.HasPrefix(filepath.Base(oldPath), ".db-backup-") {
				return errors.New("read-only volume")
			}
			return os.Rename(oldPath, newPath)
		}
		oldBefore, newBefore, dbBefore := snapshot(t, f.oldHome), snapshot(t, f.newHome), snapshot(t, f.dbRoot)
		_, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, opts)
		asHomeMoveError(t, err)
		assertSame(t, "old home", oldBefore, snapshot(t, f.oldHome))
		assertSame(t, "new home", newBefore, snapshot(t, f.newHome))
		assertSame(t, "db root", dbBefore, snapshot(t, f.dbRoot))
	})
}

// TestMigrateLegacyHomeLeavesForeignRecordAlone: a database that belongs to a
// third directory keeps its record, so the start can report the conflict.
func TestMigrateLegacyHomeLeavesForeignRecordAlone(t *testing.T) {
	f := newHomeFixture(t)
	writeTestFile(t, filepath.Join(f.dbRoot, "default", "data-dir"), "/somewhere/else/default\n", 0o600)
	if _, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options()); err != nil {
		t.Fatal(err)
	}
	if got := readTestFile(t, filepath.Join(f.dbRoot, "default", "data-dir")); got != "/somewhere/else/default\n" {
		t.Fatalf("record = %q", got)
	}
}

// TestMigratedProfileOpensTheDatabase ties the move to its purpose: the check of
// the database binding that refuses another data directory accepts the moved one.
func TestMigratedProfileOpensTheDatabase(t *testing.T) {
	f := newHomeFixture(t)
	if _, err := MigrateLegacyHome(context.Background(), f.oldHome, f.newHome, f.options()); err != nil {
		t.Fatal(err)
	}
	p, err := ResolvePaths(f.newHome, f.dbRoot, "default", DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckDatabaseBinding(p); err != nil {
		t.Fatalf("the moved profile is refused: %v", err)
	}
}

// TestSocketPathOfTheNewDefault records the room the new default leaves for the
// user name: the socket path of profile "default" may not pass MaxSocketPath.
func TestSocketPathOfTheNewDefault(t *testing.T) {
	const tail = "/.config/tg-cli/default/daemon.sock"
	const legacyTail = "/Library/Application Support/tg-cli/default/daemon.sock"
	room := MaxSocketPath - len(tail)
	if room != 68 || len(legacyTail)-len(tail) != 20 {
		t.Fatalf("room = %d, the new path is %d bytes shorter than the old one", room, len(legacyTail)-len(tail))
	}
	for _, tc := range []struct {
		name, home string
		ok         bool
	}{
		{"longest accepted", "/" + strings.Repeat("u", room-1), true},
		{"one byte too long", "/" + strings.Repeat("u", room), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolvePaths(tc.home+"/.config/tg-cli", filepath.Join(tc.home, ".config", "tg-cli", "db"), "default", DefaultFileNames())
			if (err == nil) != tc.ok {
				t.Fatalf("home %d bytes: error = %v, want ok=%v", len(tc.home), err, tc.ok)
			}
		})
	}
}
