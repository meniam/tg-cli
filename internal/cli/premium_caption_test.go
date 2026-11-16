package cli

import (
	"strings"
	"testing"
)

func TestPremiumCaptionLimit(t *testing.T) {
	r := runDiscoveryCLI(t, []string{"message", "send-media", "42", "--as", "document", "--file", "/tmp/a", "--caption", strings.Repeat("x", 1025), "--dry-run"}, nil, nil)
	if r.code != 2 || r.method != "" || !strings.Contains(r.stdout, `"reason":"premium_required"`) {
		t.Fatalf("result: %+v", r)
	}
}
