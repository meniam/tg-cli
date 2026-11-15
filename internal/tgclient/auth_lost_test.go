package tgclient

import (
	"github.com/gotd/td/tgerr"
	"testing"
)

func TestAuthLostKinds(t *testing.T) {
	c := New(Config{})
	for _, tc := range []struct{ message, want string }{{"AUTH_KEY_DUPLICATED", "key_invalid"}, {"SESSION_REVOKED", "revoked"}} {
		got := ""
		c.SetAuthLostSink(func(s string) { got = s })
		c.notifyAuthLost(tgerr.New(401, tc.message))
		if got != tc.want {
			t.Fatalf("%s: got %q", tc.message, got)
		}
	}
}
