package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
)

// audit01Isolate points HOME, XDG_CONFIG_HOME and TG_CLI_HOME at a fresh temporary
// directory, so a CLI run that has no ConfigPath never reads the real
// ~/.config/tg-cli/config.toml of the machine that runs the tests.
func audit01Isolate(t *testing.T) string {
	t.Helper()
	dir := audit01ShortDir(t)
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, ".config"))
	t.Setenv("TG_CLI_HOME", dir)
	return dir
}

// audit01ShortDir returns a temporary directory short enough for a daemon socket
// path (macOS limits it to 103 bytes and its default TMPDIR is too long for that).
// It honors TMPDIR whenever the result is short enough and falls back to /tmp only
// when it is not.
func audit01ShortDir(t *testing.T) string {
	t.Helper()
	const maxLen = 48
	for _, base := range []string{"", "/tmp"} {
		dir, err := os.MkdirTemp(base, "tgc")
		if err != nil {
			t.Fatal(err)
		}
		if len(dir) <= maxLen || base != "" {
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			return dir
		}
		_ = os.RemoveAll(dir)
	}
	panic("unreachable")
}

// audit01Failure reads the error envelope a failed command prints to stdout.
func audit01Failure(t *testing.T, stdout string) (code, message string) {
	t.Helper()
	var env struct {
		OK    bool `json:"ok"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("stdout is not an error envelope: %v\n%s", err, stdout)
	}
	if env.OK || env.Error == nil {
		t.Fatalf("stdout is not a failure envelope: %s", stdout)
	}
	return env.Error.Code, env.Error.Message
}

// audit01WantUsage checks that a run failed with the usage exit code, the usage
// error code and a message that contains want. A bare exit code 2 is not enough:
// any earlier usage error (for example a broken config) would satisfy it.
func audit01WantUsage(t *testing.T, exit int, stdout, want string) {
	t.Helper()
	if exit != exitcode.Usage {
		t.Fatalf("exit code %d, want %d; stdout: %s", exit, exitcode.Usage, stdout)
	}
	code, message := audit01Failure(t, stdout)
	if code != "usage" {
		t.Fatalf("error.code %q, want usage; message: %s", code, message)
	}
	if !strings.Contains(message, want) {
		t.Fatalf("error.message %q does not contain %q", message, want)
	}
}
