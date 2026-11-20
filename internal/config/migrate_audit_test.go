package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A legacy setting that is a symbolic link is refused: the link may point
// anywhere, and the migration reads and deletes the file.
func TestAudit03MigrateRefusesASymlinkedLegacyFile(t *testing.T) {
	path, home, dir := migrationFixture(t, nil)
	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("desktop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "client")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are not available: %v", err)
	}
	_, err := MigrateProfiles(path, home)
	if err == nil || !strings.Contains(err.Error(), "invalid legacy file default/client") {
		t.Fatalf("MigrateProfiles = %v, want an invalid legacy file error", err)
	}
	if _, statErr := os.Lstat(link); statErr != nil {
		t.Fatalf("the link was touched: %v", statErr)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != "desktop\n" {
		t.Fatalf("the link target changed: %q %v", got, readErr)
	}
}
