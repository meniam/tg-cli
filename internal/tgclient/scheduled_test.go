package tgclient

import (
	"context"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"testing"
	"tg-cli/internal/rpc"
	"time"
)

func TestSendScheduledText(t *testing.T) {
	date := int(time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC).Unix())
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesSendMessageRequest)
		if !ok {
			t.Fatalf("request %T", input)
		}
		at, ok := req.GetScheduleDate()
		if !ok || at != date || req.Message != "hello" || req.RandomID == 0 {
			t.Fatalf("request %+v", req)
		}
		return respond(output, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewScheduledMessage{Message: &tg.Message{ID: 11, Date: date, PeerID: &tg.PeerUser{UserID: 42}}}}})
	}))
	result, err := sendScheduledText(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.SendParams{At: "2026-11-15T10:00:00Z"}, preparedText{text: "hello"})
	if err != nil || !result.Scheduled || result.ID != 11 || result.Date != "2026-11-15T10:00:00Z" {
		t.Fatalf("result %+v: %v", result, err)
	}
}
