package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/daemon"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/sessionstr"
)

// databaseFileContents stands for a database on disk. The import only checks that
// the file exists, so the bytes never have to be a real database.
const databaseFileContents = "SQLite format 3\x00"

func TestSessionTransferOffline(t *testing.T) {
	e := newEnv(t)
	p, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	raw := audit02SessionFixture(t, 42)
	if err = daemon.WithProfileLock(p, func() error { return daemon.WriteSession(p, raw) }); err != nil {
		t.Fatal(err)
	}
	e.setProfileSettings("default", map[string]json.RawMessage{"profile.client": []byte(`"desktop"`)})
	out := filepath.Join(t.TempDir(), "export")
	if r := e.run("session", "export", "--out", out); !audit02Refused(r, "--yes and --out are required") {
		t.Fatalf("export confirmation: %d", r.code)
	}
	if _, err = os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("unconfirmed export created file")
	}
	if r := e.run("session", "export", "--out", out, "--yes"); r.code != exitcode.OK {
		t.Fatalf("export: %d %s", r.code, r.stdout)
	}
	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("incorrect export permissions")
	}
	if r := e.run("session", "export", "--out", out, "--yes"); !audit02Refused(r, "cannot create export file") {
		t.Fatalf("overwrite: %d %s", r.code, r.stdout)
	}
	if r := e.run("session", "import", "--profile", "copy", "--from", out, "--yes"); r.code != exitcode.OK {
		t.Fatalf("import: %d %s", r.code, r.stdout)
	}
	if got := e.savedProfile("copy").Client; got != "desktop" {
		t.Fatalf("imported preset %q", got)
	}
	if r := e.run("session", "import", "--profile", "copy", "--from", out, "--yes"); !audit02Refused(r, "profile is not empty") {
		t.Fatalf("replacement without force: %d %s", r.code, r.stdout)
	}
	if r := e.run("session", "import", "--profile", "copy", "--from", out, "--force", "--yes"); r.code != exitcode.OK {
		t.Fatalf("same account replacement: %d %s", r.code, r.stdout)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	data, ok := decoded["Data"].(map[string]any)
	if !ok {
		t.Fatalf("the session JSON has no Data object: %s", raw)
	}
	data["UserID"] = int64(43)
	other, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	value, _, err := sessionstr.Encode(other, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	e.stdin = strings.NewReader(value)
	if r := e.run("session", "import", "--profile", "copy", "--from", "-", "--force", "--yes"); !audit02Refused(r, "account cannot be verified") || strings.Contains(r.stdout, value) {
		t.Fatalf("different account accepted or input leaked: %d %s", r.code, r.stdout)
	}
	if err = os.Chmod(out, 0644); err != nil {
		t.Fatal(err)
	}
	if r := e.run("session", "import", "--profile", "other", "--from", out, "--yes"); !audit02Refused(r, "secret file must be a regular owner-only file") {
		t.Fatalf("unsafe file accepted: %d %s", r.code, r.stdout)
	}
}

func TestImportPresetFailureDoesNotReplaceSession(t *testing.T) {
	e := newEnv(t)
	p, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	raw := audit02SessionFixture(t, 42)
	if err = daemon.WithProfileLock(p, func() error { return daemon.WriteSession(p, raw) }); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(e.home, ".config", "tg-cli", "config.lock")
	if err = os.MkdirAll(lock, 0700); err != nil {
		t.Fatal(err)
	}
	replacement := audit02SessionFixture(t, 42)
	value, _, err := sessionstr.Encode(replacement, "desktop", 0)
	if err != nil {
		t.Fatal(err)
	}
	e.stdin = strings.NewReader(value)
	if r := e.run("session", "import", "--from", "-", "--force", "--yes"); r.code != exitcode.Failure {
		t.Fatalf("expected failure: %d %s", r.code, r.stdout)
	}
	actual, err := os.ReadFile(p.Session)
	if err != nil || string(actual) != string(raw) {
		t.Fatal("failed import changed session")
	}
}

func TestSessionExportRejectsStdout(t *testing.T) {
	e := newEnv(t)
	r := e.run("session", "export", "--out", "-", "--yes")
	if !audit02Refused(r, "--yes and --out are required") {
		t.Fatalf("expected usage error, got %d %s", r.code, r.stdout)
	}
}

func TestConcurrentSessionImports(t *testing.T) {
	e := newEnv(t)
	raw := audit02SessionFixture(t, 42)
	encoded, _, err := sessionstr.Encode(raw, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input")
	if err = os.WriteFile(path, []byte(encoded), 0600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	type outcome struct {
		code int
		out  string
	}
	codes := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var out, stderr bytes.Buffer
			deps := Deps{Stdout: &out, Stderr: &stderr, Stdin: strings.NewReader(""), Getenv: func(k string) string {
				if k == "TG_CLI_HOME" {
					return e.home
				}
				return ""
			}}
			<-start
			code := Execute(context.Background(), []string{"session", "import", "--from", path, "--yes"}, deps)
			codes <- outcome{code, out.String()}
		}()
	}
	close(start)
	a, b := <-codes, <-codes
	if a.code != exitcode.OK {
		a, b = b, a
	}
	// The loser meets either the profile lock or the profile the winner filled.
	refused := (b.code == exitcode.Failure && strings.Contains(b.out, "cannot lock profile")) ||
		audit02Refused(result{code: b.code, stdout: b.out}, "profile is not empty")
	if a.code != exitcode.OK || !refused {
		t.Fatalf("expected one success and one refusal, got %d %s and %d %s", a.code, a.out, b.code, b.out)
	}
}

func TestSessionImportRejectsLockedAndCorruptProfile(t *testing.T) {
	e := newEnv(t)
	p, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	raw := audit02SessionFixture(t, 42)
	value, _, err := sessionstr.Encode(raw, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "session")
	if err = os.WriteFile(input, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	if err = daemon.WithProfileLock(p, func() error {
		r := e.run("session", "import", "--from", input, "--yes")
		if r.code != exitcode.Failure || !strings.Contains(r.stdout, "cannot lock profile") {
			t.Errorf("locked profile: exit %d %s", r.code, r.stdout)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p.Session, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	r := e.run("session", "import", "--from", input, "--force", "--yes")
	if !audit02Refused(r, "account cannot be verified") || strings.Contains(r.stdout, value) || strings.Contains(r.stderr, value) {
		t.Fatalf("corrupt profile accepted or input leaked: %d %s", r.code, r.stdout)
	}
	contents, err := os.ReadFile(p.Session)
	if err != nil || string(contents) != "corrupt" {
		t.Fatal("corrupt session was overwritten")
	}
}

// TestSessionImportSeesTheLegacyDatabase keeps an import without --force from
// overwriting a profile whose database still sits in the data directory: the
// history belongs to the account the user is about to replace.
func TestSessionImportSeesTheLegacyDatabase(t *testing.T) {
	e := newEnv(t)
	p, err := daemon.ResolvePaths(e.home, e.dbRoot(), "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A database of the old layout only: the new one does not exist yet.
	if err := os.WriteFile(p.LegacyDatabase, []byte(databaseFileContents), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "session.txt")
	raw := audit02SessionFixture(t, 42)
	value, _, err := sessionstr.Encode(raw, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("session", "import", "--from", out, "--yes")
	if r.code != exitcode.Usage || !strings.Contains(r.stdout, "profile is not empty") {
		t.Fatalf("import over the old database: %d %q", r.code, r.stdout)
	}
	if _, err := os.Stat(p.Session); !os.IsNotExist(err) {
		t.Fatalf("the session was written: %v", err)
	}
}

// audit02SessionFixture returns the JSON of a session of userID with a fresh
// random auth key.
func audit02SessionFixture(t *testing.T, userID int64) []byte {
	t.Helper()
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:], "UserID": userID}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
