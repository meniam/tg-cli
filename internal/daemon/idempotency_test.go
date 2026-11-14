package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"

	"tg-cli/internal/rpc"
)

func TestIdempotentSendReplayConflictAndDryRun(t *testing.T) {
	d, tg, _, path := writeDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	p := rpc.SendParams{Chat: "42", Text: "hello", IdempotencyKey: "same"}
	raw := params(t, p)
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(2)
	results := make([]any, 2)
	errs := make([]error, 2)
	for i := range results {
		go func(i int) { defer wg.Done(); results[i], errs[i] = d.messageSend(ctx, raw) }(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || len(tg.calls) != 1 {
		t.Fatalf("errors=%v calls=%v", errs, tg.calls)
	}
	a := results[0].(rpc.SendResult)
	b := results[1].(rpc.SendResult)
	if a.Replayed == b.Replayed {
		t.Fatalf("replay=%v %v", a.Replayed, b.Replayed)
	}
	p.DryRun = true
	got, err := d.messageSend(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	dry := got.(rpc.SendResult)
	if dry.ReplayAvailable == nil || !*dry.ReplayAvailable {
		t.Fatalf("dry=%+v", dry)
	}
	p.Text = "different"
	_, err = d.messageSend(ctx, params(t, p))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams {
		t.Fatalf("conflict: %v", err)
	}
}
func TestAccountGuard(t *testing.T) {
	var c accountCache
	req := rpc.Request{Method: rpc.MethodSubscribe, ExpectUser: "@alice"}
	if c.check(req) == nil {
		t.Fatal("unknown account accepted")
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
	if c.check(req) == nil {
		t.Fatal("wrong account accepted")
	}
	req.Method = rpc.MethodPing
	if err := c.check(req); err != nil {
		t.Fatal(err)
	}
	c.set(nil)
	req.Method = rpc.MethodUnsubscribe
	if c.check(req) == nil {
		t.Fatal("cleared account accepted")
	}
}
