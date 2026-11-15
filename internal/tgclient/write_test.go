package tgclient

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/rpc"
)

type writeInvoker func(context.Context, bin.Encoder, bin.Decoder) error

func (f writeInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	return f(ctx, input, output)
}
func respond(output bin.Decoder, value bin.Encoder) error {
	var buffer bin.Buffer
	if err := value.Encode(&buffer); err != nil {
		return err
	}
	return output.Decode(&buffer)
}

func TestWriteSendRequestAndResponse(t *testing.T) {
	for _, tc := range []struct {
		name         string
		topic, reply int
		silent       bool
		wantTop      int
	}{
		{"reply", 0, 8, true, 0}, {"topic", 501, 0, false, 0}, {"topic reply", 501, 502, true, 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				calls++
				req, ok := input.(*tg.MessagesSendMessageRequest)
				if !ok {
					t.Errorf("request: %T", input)
					return errors.New("wrong request")
				}
				reply, ok := req.ReplyTo.(*tg.InputReplyToMessage)
				wantReply := tc.reply
				if wantReply == 0 {
					wantReply = tc.topic
				}
				if !ok || reply.ReplyToMsgID != wantReply {
					t.Errorf("reply: %+v", req.ReplyTo)
				}
				if top, _ := reply.GetTopMsgID(); top != tc.wantTop {
					t.Errorf("top=%d want=%d", top, tc.wantTop)
				}
				if req.Silent != tc.silent || req.Message != "hello" || req.RandomID == 0 {
					t.Errorf("send: %+v", req)
				}
				return respond(output, &tg.UpdateShortSentMessage{ID: 17, Date: 1700000000})
			}))
			out, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 42, AccessHash: 99}, 42, rpc.SendParams{Text: "hello", ReplyTo: tc.reply, Topic: tc.topic, Silent: tc.silent})
			if err != nil || out.ChatID != 42 || out.ID != 17 || out.Date != time.Unix(1700000000, 0).UTC().Format(time.RFC3339) || calls != 1 {
				t.Fatalf("send=%+v calls=%d err=%v", out, calls, err)
			}
		})
	}
}

func TestWriteEditDeleteAndForwardRequests(t *testing.T) {
	user := &tg.InputPeerUser{UserID: 42, AccessHash: 99}
	channel := &tg.InputPeerChannel{ChannelID: 10, AccessHash: 88}
	t.Run("edit", func(t *testing.T) {
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
			req, ok := input.(*tg.MessagesEditMessageRequest)
			if !ok || req.ID != 5 || req.Message != "edited" {
				t.Errorf("edit request: %+v", input)
			}
			return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &tg.Message{ID: 5, Date: 1700000000, PeerID: &tg.PeerUser{UserID: 42}}}}})
		}))
		out, err := editText(context.Background(), api, user, 42, rpc.EditParams{ID: 5, Text: "edited"})
		if err != nil || out.ID != 5 || out.ChatID != 42 || out.Date == "" {
			t.Fatalf("edit=%+v %v", out, err)
		}
	})
	for _, tc := range []struct {
		name    string
		peer    tg.InputPeerClass
		revoke  bool
		channel bool
	}{
		{"for me", user, false, false}, {"revoke", user, true, false}, {"channel revoke", channel, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				if tc.channel {
					if _, ok := input.(*tg.ChannelsDeleteMessagesRequest); !ok {
						t.Errorf("channel delete: %T", input)
					}
				} else {
					req, ok := input.(*tg.MessagesDeleteMessagesRequest)
					if !ok || req.Revoke != tc.revoke || len(req.ID) != 2 {
						t.Errorf("delete request: %+v", input)
					}
				}
				return respond(output, &tg.MessagesAffectedMessages{Pts: 1, PtsCount: 1})
			}))
			out, err := deleteMessages(context.Background(), api, tc.peer, 42, rpc.DeleteParams{IDs: []int{5, 6}, Revoke: tc.revoke})
			if err != nil || len(out.Deleted) != 2 {
				t.Fatalf("delete=%+v %v", out, err)
			}
		})
	}
	t.Run("channel for me rejected", func(t *testing.T) {
		calls := 0
		api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return nil }))
		_, err := deleteMessages(context.Background(), api, channel, 42, rpc.DeleteParams{IDs: []int{5}})
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams || calls != 0 {
			t.Fatalf("error=%v calls=%d", err, calls)
		}
	})
	t.Run("forward", func(t *testing.T) {
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
			req, ok := input.(*tg.MessagesForwardMessagesRequest)
			if !ok || len(req.ID) != 2 || req.ID[0] != 5 || req.ID[1] != 6 || len(req.RandomID) != 2 || req.RandomID[0] == 0 || !req.Silent {
				t.Errorf("forward request: %+v", input)
			}
			return respond(output, &tg.Updates{Date: 1700000000, Updates: []tg.UpdateClass{&tg.UpdateMessageID{ID: 15, RandomID: req.RandomID[0]}, &tg.UpdateMessageID{ID: 16, RandomID: req.RandomID[1]}}})
		}))
		out, err := forwardMessages(context.Background(), api, user, channel, -1000000000010, rpc.ForwardParams{IDs: []int{5, 6}, Silent: true})
		if err != nil || out.ChatID != -1000000000010 || len(out.Messages) != 2 || out.Messages[0].ID != 15 || out.Messages[1].ID != 16 {
			t.Fatalf("forward=%+v %v", out, err)
		}
	})
}

func TestWriteFloodAndLengthDoNotRetry(t *testing.T) {
	calls := 0
	flood := tgerr.New(420, "FLOOD_WAIT_8")
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return flood }))
	_, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 1}, 1, rpc.SendParams{Text: "hello"})
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFloodWait || calls != 1 {
		t.Fatalf("flood=%v calls=%d", err, calls)
	}
	_, err = sendText(context.Background(), api, &tg.InputPeerUser{UserID: 1}, 1, rpc.SendParams{Text: strings.Repeat("я", 4097)})
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams || calls != 1 {
		t.Fatalf("length=%v calls=%d", err, calls)
	}
}

func TestWriteNeedsMessageIDInResponse(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, output bin.Decoder) error {
		return respond(output, &tg.Updates{Date: 1700000000})
	}))
	_, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 1}, 1, rpc.SendParams{Text: "hello"})
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeTelegram || !strings.Contains(rpcErr.Message, "message id") {
		t.Fatalf("missing id: %v", err)
	}
}
