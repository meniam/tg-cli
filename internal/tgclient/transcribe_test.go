package tgclient

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestTranscribeRequestAndPremiumError(t *testing.T) {
	for _, failure := range []bool{false, true} {
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, out bin.Decoder) error {
			r, ok := input.(*tg.MessagesTranscribeAudioRequest)
			if !ok || r.MsgID != 17 {
				t.Fatalf("request: %T %+v", input, input)
			}
			if failure {
				return tgerr.New(400, "PREMIUM_ACCOUNT_REQUIRED")
			}
			answer := &tg.MessagesTranscribedAudio{TranscriptionID: 8, Text: "hello"}
			answer.SetTrialRemainsNum(0)
			return respond(out, answer)
		}))
		p := testPeers(api).User(user(9, "A", "", "", false))
		got, err := transcribeResolved(context.Background(), api, p, 17)
		if failure {
			var e *rpc.Error
			if !errors.As(err, &e) || e.Code != rpc.CodeTelegram || audit10Reason(e) != "premium_required" {
				t.Fatalf("error: %v", err)
			}
		} else if err != nil || got.Text != "hello" || got.TrialRemains == nil || *got.TrialRemains != 0 {
			t.Fatalf("result: %+v %v", got, err)
		}
		if !failure {
			var keys map[string]json.RawMessage
			if e := json.Unmarshal(got.Raw, &keys); e != nil || len(keys[rawTranscribe]) == 0 {
				t.Fatalf("request raw=%s error=%v", got.Raw, e)
			}
		}
	}
}

func TestTranscriptionUpdateDispatch(t *testing.T) {
	c := New(Config{AppID: 1, AppHash: "test"})
	var got rpc.Transcription
	events := 0
	c.SetTranscriptionSink(func(v rpc.Transcription) { got = v })
	c.SetEventSink(func(rpc.Event) { events++ })
	err := c.updateDispatcher().Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateTranscribedAudio{Peer: &tg.PeerChannel{ChannelID: 8}, MsgID: 7, TranscriptionID: 9, Text: "hello"}}})
	if err != nil || got.ChatID != -1000000000008 || got.ID != 7 || got.TranscriptionID != 9 || got.Text != "hello" || events != 0 {
		t.Fatalf("update: %+v events=%d error=%v", got, events, err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(got.Raw, &keys); err != nil || len(keys[rawTranscribed]) == 0 {
		t.Fatalf("update raw=%s err=%v", got.Raw, err)
	}
}
