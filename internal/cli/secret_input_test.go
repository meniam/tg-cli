package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tg-cli/internal/exitcode"
)

func TestSecretInput(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(p, []byte("  value  \r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := secretFile(p, 1024)
	if err != nil || got != "  value  " {
		t.Fatalf("password whitespace: %q %v", got, err)
	}
	if err = os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = secretFile(p, 1024); err == nil || strings.Contains(err.Error(), "value") {
		t.Fatal("unsafe file accepted or secret exposed")
	}
	got, err = secretLine(strings.NewReader("  value  \n"), 1024)
	if err != nil || got != "  value  " {
		t.Fatalf("stdin whitespace: %q %v", got, err)
	}
	if _, err = secretLine(strings.NewReader(strings.Repeat("x", 65)), 64); err == nil {
		t.Fatal("oversize code accepted")
	}
}

// TestAudit02_SecretInputRefusals covers every refusal of secretFile and
// secretLine and checks that each is a usage error with its own reason.
func TestAudit02_SecretInputRefusals(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"missing file", func() error { _, err := secretFile(filepath.Join(dir, "absent"), 1024); return err }, "cannot open secret file"},
		{"directory", func() error { _, err := secretFile(dir, 1024); return err }, "regular owner-only file"},
		{"file over the limit", func() error { _, err := secretFile(write("big", strings.Repeat("x", 65)), 64); return err }, "too large"},
		{"empty file", func() error { _, err := secretFile(write("empty", ""), 64); return err }, "secret must not be empty"},
		{"only a newline", func() error { _, err := secretFile(write("newline", "\r\n"), 64); return err }, "secret must not be empty"},
		{"empty line", func() error { _, err := secretLine(strings.NewReader("\n"), 64); return err }, "empty or too long"},
		{"no input", func() error { _, err := secretLine(strings.NewReader(""), 64); return err }, "empty or too long"},
		{"line over the limit", func() error { _, err := secretLine(strings.NewReader(strings.Repeat("x", 65)), 64); return err }, "empty or too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			var ce *cmdError
			if !errors.As(err, &ce) || ce.exit != exitcode.Usage || !strings.Contains(ce.msg, tc.want) {
				t.Fatalf("want a usage error with %q, got %#v", tc.want, err)
			}
		})
	}
	if got, err := secretFile(write("exact", strings.Repeat("x", 64)), 64); err != nil || len(got) != 64 {
		t.Fatalf("a secret of exactly the limit must pass: %d %v", len(got), err)
	}
}
