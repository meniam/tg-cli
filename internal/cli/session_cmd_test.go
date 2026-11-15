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

func TestSessionTransferOffline(t *testing.T) {
	e := newEnv(t)
	p, err := daemon.ResolvePaths(e.home, "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 256)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:], "UserID": 42}})
	if err != nil {
		t.Fatal(err)
	}
	if err = daemon.WithProfileLock(p, func() error { return daemon.WriteSession(p, raw) }); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "export")
	if r := e.run("session", "export", "--out", out); r.code != exitcode.Usage {
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
	if r := e.run("session", "export", "--out", out, "--yes"); r.code != exitcode.Usage {
		t.Fatalf("overwrite: %d", r.code)
	}
	if r := e.run("session", "import", "--profile", "copy", "--from", out, "--yes"); r.code != exitcode.OK {
		t.Fatalf("import: %d %s", r.code, r.stdout)
	}
	if r := e.run("session", "import", "--profile", "copy", "--from", out, "--yes"); r.code != exitcode.Usage {
		t.Fatalf("replacement without force: %d", r.code)
	}
	if r := e.run("session", "import", "--profile", "copy", "--from", out, "--force", "--yes"); r.code != exitcode.OK {
		t.Fatalf("same account replacement: %d %s", r.code, r.stdout)
	}
	var decoded map[string]any
	_ = json.Unmarshal(raw, &decoded)
	decoded["Data"].(map[string]any)["UserID"] = int64(43)
	other, _ := json.Marshal(decoded)
	value, _, err := sessionstr.Encode(other, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	e.stdin = strings.NewReader(value)
	if r := e.run("session", "import", "--profile", "copy", "--from", "-", "--force", "--yes"); r.code != exitcode.Usage || strings.Contains(r.stdout, value) {
		t.Fatal("different account accepted or input leaked")
	}
	if err = os.Chmod(out, 0644); err != nil {
		t.Fatal(err)
	}
	if r := e.run("session", "import", "--profile", "other", "--from", out, "--yes"); r.code != exitcode.Usage {
		t.Fatal("unsafe file accepted")
	}
}

func TestImportPresetFailureDoesNotReplaceSession(t *testing.T) {
	e := newEnv(t)
	p, err := daemon.ResolvePaths(e.home, "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 256)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:], "UserID": 42}})
	if err != nil {
		t.Fatal(err)
	}
	if err = daemon.WithProfileLock(p, func() error { return daemon.WriteSession(p, raw) }); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(p.Client, 0700); err != nil {
		t.Fatal(err)
	}
	newKey := make([]byte, 256)
	if _, err = rand.Read(newKey); err != nil {
		t.Fatal(err)
	}
	newID := sha1.Sum(newKey)
	replacement, _ := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": newKey, "AuthKeyID": newID[12:], "UserID": 42}})
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
	if r.code != exitcode.Usage {
		t.Fatalf("expected usage error, got %d", r.code)
	}
}

func TestConcurrentSessionImports(t *testing.T) {
	e := newEnv(t)
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, err := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:], "UserID": 42}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := sessionstr.Encode(raw, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "input")
	if err = os.WriteFile(path, []byte(encoded), 0600); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	codes := make(chan int, 2)
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
			codes <- Execute(context.Background(), []string{"session", "import", "--from", path, "--yes"}, deps)
		}()
	}
	close(start)
	a, b := <-codes, <-codes
	if !((a == exitcode.OK && b == exitcode.Usage) || (b == exitcode.OK && a == exitcode.Usage)) {
		t.Fatalf("expected one success and one refusal, got %d and %d", a, b)
	}
}

func TestSessionImportRejectsLockedAndCorruptProfile(t *testing.T) {
	e := newEnv(t)
	p, err := daemon.ResolvePaths(e.home, "default", daemon.DefaultFileNames())
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 256)
	if _, err = rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	raw, _ := json.Marshal(map[string]any{"Version": 1, "Data": map[string]any{"DC": 2, "AuthKey": key, "AuthKeyID": id[12:], "UserID": 42}})
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
		if r.code != exitcode.Usage {
			t.Errorf("locked profile: exit %d", r.code)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(p.Session, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	r := e.run("session", "import", "--from", input, "--force", "--yes")
	if r.code != exitcode.Usage || strings.Contains(r.stdout, value) || strings.Contains(r.stderr, value) {
		t.Fatal("corrupt profile accepted or input leaked")
	}
	contents, err := os.ReadFile(p.Session)
	if err != nil || string(contents) != "corrupt" {
		t.Fatal("corrupt session was overwritten")
	}
}
