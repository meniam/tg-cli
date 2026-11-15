package tgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestForwardBatchPartialAndStableIDs(t *testing.T) {
	ids := make([]int, 250)
	for i := range ids {
		ids[i] = i + 1
	}
	calls := 0
	seen := map[int64]bool{}
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
		req := input.(*tg.MessagesForwardMessagesRequest)
		calls++
		for _, id := range req.RandomID {
			if seen[id] {
				t.Fatalf("duplicate random id %d", id)
			}
			seen[id] = true
		}
		if calls == 2 {
			return tgerr.New(420, "FLOOD_WAIT_9")
		}
		changes := make([]tg.UpdateClass, 0, len(req.ID))
		for i := range req.ID {
			changes = append(changes, &tg.UpdateMessageID{ID: req.ID[i] + 1000, RandomID: req.RandomID[i]})
		}
		return respond(out, &tg.Updates{Date: 1700000000, Updates: changes})
	}))
	peer := &tg.InputPeerUser{UserID: 1}
	res, err := forwardMessages(context.Background(), api, peer, peer, 1, rpc.ForwardParams{IDs: ids, IdempotencyKey: "key", AccountID: 1})
	if err != nil || calls != 2 || len(res.Messages) != 100 || len(res.Errors) != 150 || !res.Partial || res.Messages[0].SourceID != 1 || res.Errors[0].Code != "flood_wait" || res.Errors[149].RetryAfterSeconds != 9 {
		t.Fatalf("result=%+v calls=%d err=%v", res, calls, err)
	}
}

func TestForwardBatchMissingID(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
		req := input.(*tg.MessagesForwardMessagesRequest)
		return respond(out, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 12, RandomID: req.RandomID[0]}}})
	}))
	peer := &tg.InputPeerUser{UserID: 1}
	res, err := forwardMessages(context.Background(), api, peer, peer, 1, rpc.ForwardParams{IDs: []int{4, 5}})
	if err != nil || !res.Partial || len(res.Messages) != 1 || res.Messages[0].SourceID != 4 || len(res.Errors) != 1 || res.Errors[0].ID != 5 || res.Errors[0].Code != "not_found" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestForwardBatchAllFailed(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, _ bin.Decoder) error {
		calls++
		return tgerr.New(420, "FLOOD_WAIT_5")
	}))
	ids := make([]int, 101)
	for i := range ids {
		ids[i] = i + 1
	}
	peer := &tg.InputPeerUser{UserID: 1}
	_, err := forwardMessages(context.Background(), api, peer, peer, 1, rpc.ForwardParams{IDs: ids})
	var e *rpc.Error
	if !errors.As(err, &e) || e.Code != rpc.CodeFloodWait || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestForwardBatchKeepsCorrelatedIDWithoutDate(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
		req := input.(*tg.MessagesForwardMessagesRequest)
		return respond(out, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 12, RandomID: req.RandomID[0]}}})
	}))
	peer := &tg.InputPeerUser{UserID: 1}
	res, err := forwardMessages(context.Background(), api, peer, peer, 1, rpc.ForwardParams{IDs: []int{4}})
	if err != nil || len(res.Messages) != 1 || res.Messages[0].SourceID != 4 || res.Messages[0].Date != "" {
		t.Fatalf("result=%+v err=%v", res, err)
	}
}
