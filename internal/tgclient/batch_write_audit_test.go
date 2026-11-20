package tgclient

import (
	"context"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

// audit08ForwardRandomIDs forwards 250 messages (three chunks) and returns the
// random IDs Telegram would see for every chunk, in call order.
func audit08ForwardRandomIDs(t *testing.T, params rpc.ForwardParams) [][]int64 {
	t.Helper()
	params.IDs = make([]int, 250)
	for i := range params.IDs {
		params.IDs[i] = i + 1
	}
	var chunks [][]int64
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
		req := input.(*tg.MessagesForwardMessagesRequest)
		chunks = append(chunks, append([]int64(nil), req.RandomID...))
		changes := make([]tg.UpdateClass, 0, len(req.ID))
		for i := range req.ID {
			changes = append(changes, &tg.UpdateMessageID{ID: req.ID[i] + 1000, RandomID: req.RandomID[i]})
		}
		return respond(out, &tg.Updates{Date: 1700000000, Updates: changes})
	}))
	peer := &tg.InputPeerUser{UserID: 1}
	res, err := forwardMessages(context.Background(), api, peer, peer, 1, params)
	if err != nil || len(res.Messages) != 250 || res.Partial {
		t.Fatalf("result=%+v err=%v", res, err)
	}
	return chunks
}

func TestAudit08_ForwardBatchRandomIDsAreStableUnderIdempotencyKey(t *testing.T) {
	params := rpc.ForwardParams{IdempotencyKey: "key", AccountID: 1}
	first := audit08ForwardRandomIDs(t, params)
	second := audit08ForwardRandomIDs(t, params)
	if len(first) != 3 {
		t.Fatalf("chunks=%d, want 3", len(first))
	}
	if !reflect.DeepEqual(first, second) {
		t.Error("repeating a forward with the same idempotency key must send the same random IDs")
	}
	all := map[int64]bool{}
	for _, chunk := range first {
		for _, id := range chunk {
			if id == 0 || all[id] {
				t.Fatalf("random id %d is zero or repeats across chunks", id)
			}
			all[id] = true
		}
	}
	other := audit08ForwardRandomIDs(t, rpc.ForwardParams{IdempotencyKey: "other", AccountID: 1})
	if reflect.DeepEqual(first, other) {
		t.Error("different idempotency keys must produce different random IDs")
	}
	otherAccount := audit08ForwardRandomIDs(t, rpc.ForwardParams{IdempotencyKey: "key", AccountID: 2})
	if reflect.DeepEqual(first, otherAccount) {
		t.Error("different accounts must produce different random IDs")
	}
}

func TestAudit08_ForwardBatchRandomIDsDifferWithoutKey(t *testing.T) {
	first := audit08ForwardRandomIDs(t, rpc.ForwardParams{})
	second := audit08ForwardRandomIDs(t, rpc.ForwardParams{})
	if reflect.DeepEqual(first, second) {
		t.Error("forwards without an idempotency key must not reuse random IDs")
	}
}

func TestAudit08_ForwardBatchDates(t *testing.T) {
	const fallback, own = 1700000000, 1700000500
	for _, tc := range []struct {
		name    string
		updates func(randomID int64) tg.UpdatesClass
		want    []rpc.ForwardedMessage
	}{
		{
			"date of the new message wins",
			func(randomID int64) tg.UpdatesClass {
				return &tg.Updates{Date: fallback, Updates: []tg.UpdateClass{
					&tg.UpdateMessageID{ID: 12, RandomID: randomID},
					&tg.UpdateNewMessage{Message: &tg.Message{ID: 12, Date: own, PeerID: &tg.PeerUser{UserID: 1}}},
				}}
			},
			[]rpc.ForwardedMessage{{SourceID: 4, ID: 12, Date: "2023-11-14T22:21:40Z"}},
		},
		{
			"channel message date",
			func(randomID int64) tg.UpdatesClass {
				return &tg.Updates{Date: fallback, Updates: []tg.UpdateClass{
					&tg.UpdateMessageID{ID: 12, RandomID: randomID},
					&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 12, Date: own, PeerID: &tg.PeerUser{UserID: 1}}},
				}}
			},
			[]rpc.ForwardedMessage{{SourceID: 4, ID: 12, Date: "2023-11-14T22:21:40Z"}},
		},
		{
			"envelope date is the fallback",
			func(randomID int64) tg.UpdatesClass {
				return &tg.Updates{Date: fallback, Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 12, RandomID: randomID}}}
			},
			[]rpc.ForwardedMessage{{SourceID: 4, ID: 12, Date: "2023-11-14T22:13:20Z"}},
		},
		{
			"combined updates fallback",
			func(randomID int64) tg.UpdatesClass {
				return &tg.UpdatesCombined{Date: fallback, Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 12, RandomID: randomID}}}
			},
			[]rpc.ForwardedMessage{{SourceID: 4, ID: 12, Date: "2023-11-14T22:13:20Z"}},
		},
		{
			"short update fallback",
			func(randomID int64) tg.UpdatesClass {
				return &tg.UpdateShort{Date: fallback, Update: &tg.UpdateMessageID{ID: 12, RandomID: randomID}}
			},
			[]rpc.ForwardedMessage{{SourceID: 4, ID: 12, Date: "2023-11-14T22:13:20Z"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
				req := input.(*tg.MessagesForwardMessagesRequest)
				return respond(out, tc.updates(req.RandomID[0]))
			}))
			peer := &tg.InputPeerUser{UserID: 1}
			res, err := forwardMessages(context.Background(), api, peer, peer, 1, rpc.ForwardParams{IDs: []int{4}})
			// Raw is checked by the raw column tests; this test is about dates.
			for i := range res.Messages {
				res.Messages[i].Raw = nil
			}
			if err != nil || !reflect.DeepEqual(res.Messages, tc.want) {
				t.Fatalf("messages=%+v err=%v, want %+v", res.Messages, err, tc.want)
			}
		})
	}
}
