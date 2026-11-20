package cli

import (
	"strings"
	"testing"
)

// The command is run on the helper that registers message.send_media, so the
// refusal before the call is observable: a request that reached the server
// would set r.method.
func TestPremiumCaptionLimit(t *testing.T) {
	r := runWriteCLI(t, []string{"message", "send-media", "42", "--as", "document", "--file", "/tmp/a", "--caption", strings.Repeat("x", 1025), "--dry-run"}, "", nil, nil)
	if r.code != 2 || r.method != "" || !strings.Contains(r.stdout, `"reason":"premium_required"`) {
		t.Fatalf("result: %+v", r)
	}
}
