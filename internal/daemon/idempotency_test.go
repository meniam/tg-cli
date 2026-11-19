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
