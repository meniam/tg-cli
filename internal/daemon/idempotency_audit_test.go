package daemon

import (
	"context"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

// A key reused for a real send, not a dry run, with different parameters is
// refused and nothing reaches Telegram.
func TestAudit05_IdempotentSendConflictOnRealSend(t *testing.T) {
	d, tg, _, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	ctx := context.Background()
	p := rpc.SendParams{Chat: "42", Text: "hello", IdempotencyKey: "real"}
	if _, err := d.messageSend(ctx, params(t, p)); err != nil {
		t.Fatal(err)
	}
	p.Text = "different"
	_, err := d.messageSend(ctx, params(t, p))
	if audit05GuardCode(t, err) != rpc.CodeInvalidParams || !strings.Contains(err.Error(), "different parameters") {
		t.Fatalf("conflict: %v", err)
	}
	if len(tg.calls) != 1 {
		t.Fatalf("Telegram calls = %v, want exactly the first send", tg.calls)
	}
}

func TestAudit05_IdempotentSendRejectsMalformedKey(t *testing.T) {
	d, tg, _, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	for name, key := range map[string]string{
		"space":    "has space",
		"too long": strings.Repeat("k", 65),
		"slash":    "a/b",
	} {
		_, err := d.messageSend(context.Background(), params(t, rpc.SendParams{Chat: "42", Text: "hello", IdempotencyKey: key}))
		if audit05GuardCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(tg.calls) != 0 {
		t.Fatalf("a malformed key reached Telegram: %v", tg.calls)
	}
}

// The same key and parameters under another method is a conflict too.
func TestAudit05_IdempotencyKeyConflictsAcrossMethods(t *testing.T) {
	d, _, _, _ := writeDaemon(t)
	ctx := context.Background()
	body := map[string]string{"text": "x"}
	session, _, replayed, err := d.beginIdempotency(ctx, 42, "shared", rpc.MethodMessageSend, body, false)
	if err != nil || replayed || session == nil {
		t.Fatalf("first use: session=%v replayed=%v err=%v", session, replayed, err)
	}
	session.unlock()
	_, _, _, err = d.beginIdempotency(ctx, 42, "shared", rpc.MethodMessageForward, body, false)
	if audit05GuardCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("other method with the same key: %v", err)
	}
}
