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

// audit10RPCError returns the *rpc.Error inside err, or nil.
func audit10RPCError(err error) *rpc.Error {
	var e *rpc.Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

func TestAudit10_TypingWithoutTopicSendsNoTopMessage(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		req, ok := input.(*tg.MessagesSetTypingRequest)
		if !ok {
			t.Fatalf("request = %T", input)
		}
		if topic, set := req.GetTopMsgID(); set {
			t.Errorf("top_msg_id = %d was set for a chat without a topic", topic)
		}
		return respond(output, &tg.BoolTrue{})
	}))
	result, err := setTyping(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.TypingParams{Action: "typing"})
	if err != nil || result.ChatID != 42 || result.Action != "typing" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAudit10_TypingErrors(t *testing.T) {
	respondWith := func(handle func(out bin.Decoder) error) (*tg.Client, *int) {
		calls := new(int)
		return tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
			*calls++
			return handle(out)
		})), calls
	}
	t.Run("telegram says false", func(t *testing.T) {
		api, calls := respondWith(func(out bin.Decoder) error { return respond(out, &tg.BoolFalse{}) })
		_, err := setTyping(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.TypingParams{Action: "typing"})
		e := audit10RPCError(err)
		if e == nil || *calls != 1 || e.Code != rpc.CodeTelegram || e.Message != "Telegram rejected typing action" {
			t.Fatalf("err=%v calls=%d", err, *calls)
		}
	})
	t.Run("telegram error is mapped", func(t *testing.T) {
		api, _ := respondWith(func(bin.Decoder) error { return tgerr.New(400, "CHAT_ADMIN_REQUIRED") })
		_, err := setTyping(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, rpc.TypingParams{Action: "typing"})
		e := audit10RPCError(err)
		if e == nil || e.Code != rpc.CodeTelegram || e.Message != "chat admin rights are required" {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("invalid params never reach telegram", func(t *testing.T) {
		for _, p := range []rpc.TypingParams{{Action: "dance"}, {Action: ""}, {Action: "typing", Topic: -1}} {
			api, calls := respondWith(func(out bin.Decoder) error { return respond(out, &tg.BoolTrue{}) })
			_, err := setTyping(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, p)
			if err == nil || *calls != 0 || rpcErrorCode(t, err) != rpc.CodeInvalidParams {
				t.Errorf("%+v: err=%v calls=%d", p, err, *calls)
			}
		}
	})
}
