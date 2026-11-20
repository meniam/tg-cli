package daemon

import (
	"errors"
	"testing"

	"tg-cli/internal/rpc"
)

// audit05GuardCode returns the RPC code of a refusal and fails when the guard
// accepted the request or refused it with something that is not an RPC error.
func audit05GuardCode(t *testing.T, err error) int {
	t.Helper()
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("want a refusal, got %v", err)
	}
	return rpcErr.Code
}

func TestAccountGuard(t *testing.T) {
	var c accountCache
	req := rpc.Request{Method: rpc.MethodSubscribe, ExpectUser: "@alice"}
	if code := audit05GuardCode(t, c.check(req)); code != rpc.CodeNotAuthorized {
		t.Fatalf("unknown account: code %d", code)
	}
	c.set(&rpc.User{ID: 42, Username: "Alice"})
	if err := c.check(req); err != nil {
		t.Fatal(err)
	}
	req.Method = rpc.MethodSearch
	req.ExpectUser = "42"
	if err := c.check(req); err != nil {
		t.Fatal(err)
	}
	req.ExpectUser = "43"
	if code := audit05GuardCode(t, c.check(req)); code != rpc.CodeNotAuthorized {
		t.Fatalf("wrong account: code %d", code)
	}
	req.Method = rpc.MethodPing
	if err := c.check(req); err != nil {
		t.Fatal(err)
	}
	c.set(nil)
	req.Method = rpc.MethodUnsubscribe
	if code := audit05GuardCode(t, c.check(req)); code != rpc.CodeNotAuthorized {
		t.Fatalf("cleared account: code %d", code)
	}
}
