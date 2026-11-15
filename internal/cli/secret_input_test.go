package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretInput(t *testing.T) {
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
