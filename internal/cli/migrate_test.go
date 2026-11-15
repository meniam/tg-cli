package cli

import (
	"os"
	"path/filepath"
	"testing"

	"tg-cli/internal/exitcode"
)

func TestMigrationAutomaticBeforeSessionImport(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.home, "default")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client"), []byte("desktop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := e.run("session", "import")
	if result.code != exitcode.Usage {
		t.Fatalf("exit = %d", result.code)
	}
	if got := e.savedProfile("default").Client; got != "desktop" {
		t.Fatalf("client = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "client")); !os.IsNotExist(err) {
		t.Fatalf("legacy client remains: %v", err)
	}
}
