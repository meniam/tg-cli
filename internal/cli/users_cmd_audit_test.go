package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// audit03ShortDir returns a short private directory for TG_CLI_HOME. It stays
// under /tmp on purpose: a Unix socket path is limited to 103 bytes and
// t.TempDir is too long on macOS, so neither TMPDIR nor t.TempDir can be used.
func audit03ShortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "tgw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// audit03ConfigPath returns a configuration path inside a private temporary
// directory. The file does not exist, so Execute loads the defaults and never
// reads the real ~/.config/tg-cli/config.toml.
func audit03ConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.toml")
}

// audit03HomeEnv returns a Getenv that answers only TG_CLI_HOME.
func audit03HomeEnv(home string) func(string) string {
	return func(name string) string {
		if name == "TG_CLI_HOME" {
			return home
		}
		return ""
	}
}

// audit03SameJSON reports whether two JSON documents are equal as values, so
// the key order and the spacing of the encoder do not matter.
func audit03SameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %q: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}
