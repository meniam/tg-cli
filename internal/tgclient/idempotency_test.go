package tgclient

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

func TestIdempotentInvokerIDsAndRetry(t *testing.T) {
	var ids [][]int64
	calls := 0
	inv := writeInvoker(func(_ context.Context, input bin.Encoder, _ bin.Decoder) error {
		calls++
		switch req := input.(type) {
		case *tg.MessagesSendMessageRequest:
			ids = append(ids, []int64{req.RandomID})
		case *tg.MessagesSendMediaRequest:
			ids = append(ids, []int64{req.RandomID})
		case *tg.MessagesSendMultiMediaRequest:
			ids = append(ids, []int64{req.MultiMedia[0].RandomID, req.MultiMedia[1].RandomID})
		case *tg.MessagesForwardMessagesRequest:
			ids = append(ids, append([]int64(nil), req.RandomID...))
		}
		if calls == 1 {
			return io.EOF
		}
		return nil
	})
	api, err := idempotentAPI(tg.NewClient(inv), 1, 2, "key")
	if err != nil {
		t.Fatal(err)
	}
	requests := []bin.Encoder{&tg.MessagesSendMessageRequest{}, &tg.MessagesSendMediaRequest{}, &tg.MessagesSendMultiMediaRequest{MultiMedia: make([]tg.InputSingleMedia, 2)}, &tg.MessagesForwardMessagesRequest{ID: []int{3, 4}}}
	for _, req := range requests {
		if err := api.Invoker().Invoke(context.Background(), req, nil); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 5 || ids[0][0] == 0 || ids[0][0] != ids[1][0] || ids[1][0] != ids[2][0] || ids[3][0] == ids[3][1] || ids[4][0] != ids[3][0] || ids[4][1] != ids[3][1] {
		t.Fatalf("calls=%d ids=%v", calls, ids)
	}
	if deterministicID(2, 2, "key", 0) == ids[0][0] || deterministicID(1, 3, "key", 0) == ids[0][0] || deterministicID(1, 2, "other", 0) == ids[0][0] {
		t.Fatal("ID does not depend on tuple")
	}
}

func TestIdempotentInvokerDoesNotRetryPermanent(t *testing.T) {
	calls := 0
	sentinel := errors.New("permanent")
	base := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return sentinel }))
	api, err := idempotentAPI(base, 1, 2, "key")
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(api.Invoker().Invoke(context.Background(), &tg.MessagesSendMessageRequest{}, nil), sentinel) || calls != 1 {
		t.Fatal("permanent error retried")
	}
}

func TestAudit09_IdempotentAPIUnkeyedRequestIsNotRetried(t *testing.T) {
	calls := 0
	base := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return io.EOF }))
	api, err := idempotentAPI(base, 0, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	// io.EOF is retryable for keyed writes; without a key the request must run exactly once.
	if err := api.Invoker().Invoke(context.Background(), &tg.MessagesSendMessageRequest{}, nil); !errors.Is(err, io.EOF) || calls != 1 {
		t.Fatalf("unkeyed request: err=%v calls=%d", err, calls)
	}
}

func TestAudit09_IdempotentAPIRequiresAccountForKey(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return nil }))
	for _, id := range []int64{0, -1} {
		got, err := idempotentAPI(api, id, 2, "key")
		if err == nil || got != nil || err.Error() != "account id is required for idempotent writes" {
			t.Fatalf("account %d: client=%v err=%v", id, got, err)
		}
	}
}

// audit09AfterFirstCheckContext reports no error on the first Err call and context.Canceled
// afterwards, with Done already closed. This reproduces cancellation that arrives
// while the invoker waits to retry, without a real one-second wait or a race.
type audit09AfterFirstCheckContext struct {
	context.Context
	done  chan struct{}
	calls int
}

func (c *audit09AfterFirstCheckContext) Done() <-chan struct{} { return c.done }
func (c *audit09AfterFirstCheckContext) Err() error {
	c.calls++
	if c.calls == 1 {
		return nil
	}
	return context.Canceled
}

func TestAudit09_IdempotentInvokerCancelledWhileWaitingToRetry(t *testing.T) {
	calls := 0
	base := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return io.EOF }))
	api, err := idempotentAPI(base, 1, 2, "key")
	if err != nil {
		t.Fatal(err)
	}
	ctx := &audit09AfterFirstCheckContext{Context: context.Background(), done: make(chan struct{})}
	close(ctx.done)
	start := time.Now()
	err = api.Invoker().Invoke(ctx, &tg.MessagesSendMessageRequest{}, nil)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want context.Canceled and one call", err, calls)
	}
	if time.Since(start) >= time.Second {
		t.Fatal("cancellation waited for the retry delay")
	}
}

func TestSendTextIdempotencyThroughBuilder(t *testing.T) {
	var ids []int64
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesSendMessageRequest)
		if !ok {
			t.Fatalf("unexpected request %T", input)
		}
		ids = append(ids, req.RandomID)
		return respond(output, sentUpdate(42))
	}))
	p := rpc.SendParams{Text: "hello", IdempotencyKey: "key", AccountID: 123}
	for range 2 {
		if _, err := sendText(context.Background(), api, &tg.InputPeerSelf{}, 42, p); err != nil {
			t.Fatal(err)
		}
	}
	p.IdempotencyKey = ""
	for range 2 {
		if _, err := sendText(context.Background(), api, &tg.InputPeerSelf{}, 42, p); err != nil {
			t.Fatal(err)
		}
	}
	if ids[0] == 0 || ids[0] != ids[1] || ids[2] == 0 || ids[3] == 0 || ids[2] == ids[3] {
		t.Fatalf("IDs: %v", ids)
	}
}
