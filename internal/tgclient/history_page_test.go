package tgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

func TestHistoryPageOneRequestAndCursors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		raw        []tg.MessageClass
		wantOldest int
		wantRaw    int
		wantCount  int
		wantEmpty  bool
	}{
		{"descending", []tg.MessageClass{testRawMessage(42, 12), testRawMessage(42, 11), testRawMessage(42, 10)}, 10, 10, 3, false},
		{"ascending", []tg.MessageClass{testRawMessage(42, 10), testRawMessage(42, 11), testRawMessage(42, 12)}, 10, 10, 3, false},
		{"empty", nil, 0, 0, 0, true},
		{"placeholders only", []tg.MessageClass{&tg.MessageEmpty{ID: 8}, &tg.MessageEmpty{ID: 9}}, 0, 8, 0, false},
		{"mixed", []tg.MessageClass{testRawMessage(42, 12), &tg.MessageEmpty{ID: 7}, testRawMessage(42, 10)}, 10, 7, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := tg.NewClient(writeInvoker(func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
				calls++
				req, ok := input.(*tg.MessagesGetHistoryRequest)
				if !ok || req.OffsetID != 13 || req.AddOffset != 0 || req.Limit != 100 || !historyPageFromContext(ctx) {
					t.Errorf("history request: %+v, marked=%v", input, historyPageFromContext(ctx))
				}
				return respond(output, &tg.MessagesMessages{Messages: tc.raw})
			}))
			page, err := historyPageAPI(context.Background(), api, &tg.InputPeerChat{ChatID: 42}, rpc.Chat{ID: -42, Type: "group", Title: "Group"}, 13)
			if err != nil || calls != 1 || page.Empty != tc.wantEmpty || page.OldestID != tc.wantOldest || page.RawNextOffsetID != tc.wantRaw || len(page.Messages) != tc.wantCount {
				t.Fatalf("page=%+v calls=%d err=%v", page, calls, err)
			}
			for _, message := range page.Messages {
				if message.ChatID != -42 || message.Text != "result" {
					t.Fatalf("converted message: %+v", message)
				}
			}
		})
	}
}

func TestHistoryPageKeepsAllHundredMessages(t *testing.T) {
	raw := make([]tg.MessageClass, 100)
	for i := range raw {
		raw[i] = testRawMessage(42, 100-i)
	}
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		return respond(output, &tg.MessagesMessagesSlice{Count: 500, Messages: raw})
	}))
	page, err := historyPageAPI(context.Background(), api, &tg.InputPeerChat{ChatID: 42}, rpc.Chat{ID: -42, Type: "group"}, 0)
	if err != nil || calls != 1 || len(page.Messages) != 100 || page.OldestID != 1 || page.RawNextOffsetID != 1 || page.Empty {
		t.Fatalf("page=%+v calls=%d err=%v", page, calls, err)
	}
}

func TestHistoryPageFailure(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return errors.New("offline") }))
	if _, err := historyPageAPI(context.Background(), api, &tg.InputPeerChat{ChatID: 42}, rpc.Chat{ID: -42}, 0); err == nil {
		t.Fatal("Telegram error lost")
	}
}
