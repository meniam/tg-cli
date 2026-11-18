package tgclient

import (
	"context"
	"encoding/json"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"testing"
	"tg-cli/internal/rpc"

	"github.com/gotd/td/tdp"
	"github.com/gotd/td/tg"
)

func TestMessageRaw(t *testing.T) {
	for _, object := range []interface {
		tdp.Object
		TypeName() string
	}{&tg.Message{ID: 12, Message: "hello"}, &tg.MessageService{ID: 13}} {
		raw := messageRaw(object)
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		var kind string
		if err := json.Unmarshal(fields["_"], &kind); err != nil || kind != object.TypeName() {
			t.Fatalf("type = %q, error = %v", kind, err)
		}
	}
	if raw := messageRaw(nil); raw != nil {
		t.Fatalf("nil object raw = %s", raw)
	}
}

func TestWrittenMessagesRaw(t *testing.T) {
	message := &tg.Message{ID: 7, Date: 1700000000, Message: "sent"}
	for name, update := range map[string]tg.UpdateClass{
		"new":          &tg.UpdateNewMessage{Message: message},
		"new channel":  &tg.UpdateNewChannelMessage{Message: message},
		"edit":         &tg.UpdateEditMessage{Message: message},
		"edit channel": &tg.UpdateEditChannelMessage{Message: message},
	} {
		t.Run(name, func(t *testing.T) {
			items, err := writtenMessages(&tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 7}, update}})
			if err != nil || len(items) != 1 {
				t.Fatalf("items = %+v, err = %v", items, err)
			}
			assertMessageRaw(t, items[0].Raw)
		})
	}
	items, err := writtenMessages(&tg.UpdateShortSentMessage{ID: 7, Date: 1700000000})
	if err != nil || len(items) != 1 || len(items[0].Raw) != 0 {
		t.Fatalf("short = %+v, err = %v", items, err)
	}
}

func assertMessageRaw(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var fields struct {
		Type string `json:"_"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil || (fields.Type != "message" && fields.Type != "messageService") {
		t.Fatalf("raw = %s, err = %v", raw, err)
	}
}

func TestHistoryRaw(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{testRawMessage(42, 12)}})
	}))
	p := peers.Options{}.Build(api).Chat(&tg.Chat{ID: 42, Title: "group"})
	result, err := historyResolved(context.Background(), api, p, rpc.HistoryQuery{Limit: 1})
	if err != nil || len(result.Messages) != 1 {
		t.Fatalf("history = %+v, err = %v", result, err)
	}
	assertMessageRaw(t, result.Messages[0].Raw)
}

func TestInlineFullUpdateRaw(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		return respond(out, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 9, Date: 1700000000, PeerID: &tg.PeerUser{UserID: 42}, Message: "inline"}}}})
	}))
	_, msg, err := sendInline(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, 5, rpc.SendInlineParams{QueryID: "123", Result: "r"})
	if err != nil || msg == nil {
		t.Fatalf("inline = %+v, err = %v", msg, err)
	}
	assertMessageRaw(t, msg.Raw)
}

func TestSendFullUpdateRaw(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
		return respond(out, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: &tg.Message{ID: 9, Date: 1700000000, PeerID: &tg.PeerUser{UserID: 42}, Message: "hello"}}}})
	}))
	result, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.SendParams{Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	assertMessageRaw(t, result.Raw)
}
