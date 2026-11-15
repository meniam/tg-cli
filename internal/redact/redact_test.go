package redact

import (
	"strings"
	"testing"
)

func TestTextSecrets(t *testing.T) {
	for _, secret := range []string{"+79991234567", "socks5://user:pass@", "password=hunter2", strings.Repeat("a", 32), strings.Repeat("z", 64)} {
		if got := Text(secret); strings.Contains(got, secret) {
			t.Errorf("not redacted: %q", secret)
		}
	}
	for _, s := range []string{"chat -1001234567", "message 12345", "2026-11-15"} {
		if got := Text(s); got != s {
			t.Errorf("%q changed to %q", s, got)
		}
	}
}
