package cli

import (
	"os"
	"path/filepath"
	"testing"
	"tg-cli/internal/rpc"
	"time"
)

// audit02CallTimeout keeps a handler that never answers from hanging a test for
// the default two minutes.
const audit02CallTimeout = 10 * time.Second

// audit02Home returns a private HOME for tests that drive Execute through a
// fake RPC pipe. HOME and XDG_CONFIG_HOME point into it, so no test reads the
// developer's real ~/.config/tg-cli/config.toml.
func audit02Home(t *testing.T) string {
	t.Helper()
	// The CLI checks the length of the daemon socket path before it dials, and
	// t.TempDir is too long for a Unix socket on macOS, so use a short /tmp path.
	home, err := os.MkdirTemp("/tmp", "tga")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

func audit02ConfigPath(home string) string {
	return filepath.Join(home, ".config", "tg-cli", "config.toml")
}

// audit02Reject is for fake RPC handlers: they run on the server goroutine,
// where t.Fatal only ends that goroutine and leaves the CLI waiting for a reply.
// It records the failure and answers with an RPC error so the run ends at once.
func audit02Reject(t *testing.T, format string, args ...any) (any, error) {
	t.Helper()
	t.Errorf(format, args...)
	return nil, rpc.NewError(rpc.CodeInvalidParams, "unexpected request")
}
