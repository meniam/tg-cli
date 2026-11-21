package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/exitcode"
)

// homeMoveEnv is a machine with a previous default data directory and a current
// one. HOME points at a short temporary directory, so the default home that
// config.Load derives is <root>/.config/tg-cli on every system, and that is also
// the directory of the configuration file, as it is for a real user.
type homeMoveEnv struct {
	t                          *testing.T
	root, oldHome, newHome     string
	env                        map[string]string
	legacy                     func() string
	configPath, secretFilePath string
}

func newHomeMoveEnv(t *testing.T) *homeMoveEnv {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "tgh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", "")
	e := &homeMoveEnv{t: t, root: root, env: map[string]string{}}
	e.oldHome = filepath.Join(root, "Library", "Application Support", "tg-cli")
	e.newHome = filepath.Join(root, ".config", "tg-cli")
	e.configPath = filepath.Join(e.newHome, "config.toml")
	e.legacy = func() string { return e.oldHome }
	e.secretFilePath = filepath.Join(e.oldHome, "default", "session.json")
	return e
}

func (e *homeMoveEnv) write(path, content string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *homeMoveEnv) run(args ...string) result {
	e.t.Helper()
	var stdout, stderr safeBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code := Execute(ctx, args, Deps{
		ConfigPath:  e.configPath,
		Stdout:      &stdout,
		Stderr:      &stderr,
		Getenv:      func(k string) string { return e.env[k] },
		CallTimeout: 5 * time.Second,
		LegacyHome:  e.legacy,
	})
	return result{code, stdout.String(), stderr.String()}
}

func (e *homeMoveEnv) requireMissing(path string) {
	e.t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		e.t.Fatalf("%s must not exist: %v", path, err)
	}
}

func (e *homeMoveEnv) requireFile(path, content string) {
	e.t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || string(b) != content {
		e.t.Fatalf("%s = %q, %v; want %q", path, b, err, content)
	}
}

// TestDaemonStatusMovesThePreviousDefaultHome: a command that reaches the daemon
// brings the data of the old default directory along, says so once on stderr and
// then finds no daemon, which is the normal answer of a machine without one.
func TestDaemonStatusMovesThePreviousDefaultHome(t *testing.T) {
	e := newHomeMoveEnv(t)
	e.write(e.secretFilePath, "session secret")
	e.write(filepath.Join(e.oldHome, "default", "access.json"), "{}")

	first := e.run("daemon", "status")
	if first.code != exitcode.DaemonNotRunning {
		t.Fatalf("code = %d, stderr = %s", first.code, first.stderr)
	}
	for _, want := range []string{"moved the data directory from " + e.oldHome + " to " + e.newHome, ".home-move-backup-"} {
		if !strings.Contains(first.stderr, want) {
			t.Fatalf("stderr %q does not contain %q", first.stderr, want)
		}
	}
	e.requireFile(filepath.Join(e.newHome, "default", "session.json"), "session secret")
	e.requireMissing(e.oldHome)

	second := e.run("daemon", "status")
	if second.code != exitcode.DaemonNotRunning || strings.Contains(second.stderr, "moved the data directory") {
		t.Fatalf("second run: code = %d, stderr = %q", second.code, second.stderr)
	}
	e.requireFile(filepath.Join(e.newHome, "default", "session.json"), "session secret")
}

func TestHomeMoveIsSilentUnderQuiet(t *testing.T) {
	e := newHomeMoveEnv(t)
	e.write(e.secretFilePath, "session secret")
	r := e.run("daemon", "status", "--quiet")
	if r.code != exitcode.DaemonNotRunning || strings.Contains(r.stderr, "moved the data directory") {
		t.Fatalf("code = %d, stderr = %q", r.code, r.stderr)
	}
	e.requireFile(filepath.Join(e.newHome, "default", "session.json"), "session secret")
}

// TestExplicitHomeIsNeverMoved: a home chosen by the owner, in config.toml or in
// TG_CLI_HOME, is where the data is, whatever lies in the previous default.
func TestExplicitHomeIsNeverMoved(t *testing.T) {
	t.Run("home in config.toml", func(t *testing.T) {
		e := newHomeMoveEnv(t)
		e.write(e.secretFilePath, "session secret")
		explicit := filepath.Join(e.root, "mine")
		e.write(e.configPath, "home = '"+explicit+"'\n")
		if r := e.run("daemon", "status"); r.code != exitcode.DaemonNotRunning {
			t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
		}
		e.requireFile(e.secretFilePath, "session secret")
		e.requireMissing(filepath.Join(e.newHome, "default"))
	})
	t.Run("home equal to the new default", func(t *testing.T) {
		e := newHomeMoveEnv(t)
		e.write(e.secretFilePath, "session secret")
		e.write(e.configPath, "home = '"+e.newHome+"'\n")
		if r := e.run("daemon", "status"); r.code != exitcode.DaemonNotRunning {
			t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
		}
		e.requireFile(e.secretFilePath, "session secret")
	})
	t.Run("TG_CLI_HOME", func(t *testing.T) {
		e := newHomeMoveEnv(t)
		e.write(e.secretFilePath, "session secret")
		e.env["TG_CLI_HOME"] = filepath.Join(e.root, "env-home")
		if r := e.run("daemon", "status"); r.code != exitcode.DaemonNotRunning {
			t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
		}
		e.requireFile(e.secretFilePath, "session secret")
		e.requireMissing(filepath.Join(e.newHome, "default"))
	})
	t.Run("no legacy directory known", func(t *testing.T) {
		e := newHomeMoveEnv(t)
		e.write(e.secretFilePath, "session secret")
		e.legacy = nil
		if r := e.run("daemon", "status"); r.code != exitcode.DaemonNotRunning {
			t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
		}
		e.requireFile(e.secretFilePath, "session secret")
	})
	t.Run("the legacy directory is the current one", func(t *testing.T) {
		e := newHomeMoveEnv(t)
		e.legacy = func() string { return "" }
		e.write(filepath.Join(e.newHome, "default", "session.json"), "session secret")
		if r := e.run("daemon", "status"); r.code != exitcode.DaemonNotRunning {
			t.Fatalf("code = %d, stderr = %s", r.code, r.stderr)
		}
		e.requireFile(filepath.Join(e.newHome, "default", "session.json"), "session secret")
	})
}

func TestHomeMoveRefusalIsAFailure(t *testing.T) {
	e := newHomeMoveEnv(t)
	e.write(e.secretFilePath, "old session")
	e.write(filepath.Join(e.newHome, "default", "session.json"), "new session")
	r := e.run("daemon", "status")
	if r.code != exitcode.Failure {
		t.Fatalf("code = %d, want %d; stdout = %s", r.code, exitcode.Failure, r.stdout)
	}
	if !strings.Contains(r.stdout, e.oldHome) || !strings.Contains(r.stdout, e.newHome) {
		t.Fatalf("the answer names neither directory: %s", r.stdout)
	}
	e.requireFile(e.secretFilePath, "old session")
	e.requireFile(filepath.Join(e.newHome, "default", "session.json"), "new session")
}

// TestDaemonStatusWithNoDataAnywhere: a fresh machine, or one where the move is
// done, has no old directory and perhaps no new one; the command must answer
// "not running" and leave the previous default alone.
func TestDaemonStatusWithNoDataAnywhere(t *testing.T) {
	t.Run("nothing exists", func(t *testing.T) {
		e := newHomeMoveEnv(t)
		if r := e.run("daemon", "status"); r.code != exitcode.DaemonNotRunning {
			t.Fatalf("code = %d, stderr = %s, stdout = %s", r.code, r.stderr, r.stdout)
		}
		e.requireMissing(e.oldHome)
	})
	t.Run("both directories exist and are empty", func(t *testing.T) {
		e := newHomeMoveEnv(t)
		for _, dir := range []string{e.oldHome, e.newHome} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if r := e.run("daemon", "status"); r.code != exitcode.DaemonNotRunning {
			t.Fatalf("code = %d, stderr = %s, stdout = %s", r.code, r.stderr, r.stdout)
		}
	})
}

func TestSettingsDoesNotMoveUnverifiedLegacyHome(t *testing.T) {
	e := newHomeMoveEnv(t)
	source := filepath.Join(e.oldHome, "default", "access.json")
	e.write(source, `{"allow":[0]}`)
	r := e.run("settings", "list")
	if r.code != exitcode.Usage || !strings.Contains(r.stdout, source) {
		t.Fatalf("unverified home: %d %s", r.code, r.stdout)
	}
	e.requireFile(source, `{"allow":[0]}`)
	e.requireMissing(filepath.Join(e.newHome, "default"))
}

func TestSettingsResetDoesNotMoveUnverifiedLegacyHome(t *testing.T) {
	e := newHomeMoveEnv(t)
	source := filepath.Join(e.oldHome, "default", "access.json")
	e.write(source, `{"allow":[0]}`)
	var stdout, stderr safeBuffer
	code := Execute(context.Background(), []string{"settings", "token", "reset"}, Deps{ConfigPath: e.configPath, Stdout: &stdout, Stderr: &stderr, Getenv: func(k string) string { return e.env[k] }, LegacyHome: e.legacy, ReadSecret: func(string) (string, error) { return "yes", nil }})
	if code != exitcode.Usage || !strings.Contains(stdout.String(), source) {
		t.Fatalf("unverified home reset: %d %s", code, stdout.String())
	}
	e.requireFile(source, `{"allow":[0]}`)
	e.requireMissing(filepath.Join(e.newHome, "default"))
}
