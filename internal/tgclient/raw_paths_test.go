package tgclient

import (
	"context"
	"fmt"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type rawTestMiddleware struct{ invoke writeInvoker }

func (m rawTestMiddleware) Handle(_ tg.Invoker) telegram.InvokeFunc {
	return telegram.InvokeFunc(m.invoke)
}

func TestReadPathsCarryRaw(t *testing.T) {
	ctx := context.Background()
	ref := chatref.Ref{Kind: chatref.ID, ID: -42}
	for _, tc := range []struct {
		name  string
		call  func(*Client) ([]rpc.Message, error)
		check func(bin.Encoder) bool
	}{
		{"message", func(c *Client) ([]rpc.Message, error) { m, err := c.Message(ctx, ref, 7); return []rpc.Message{m}, err }, func(v bin.Encoder) bool { _, ok := v.(*tg.MessagesGetMessagesRequest); return ok }},
		{"pinned", func(c *Client) ([]rpc.Message, error) { r, err := c.Pinned(ctx, ref, 1); return r.Messages, err }, func(v bin.Encoder) bool {
			r, ok := v.(*tg.MessagesSearchRequest)
			if !ok {
				return false
			}
			_, ok = r.Filter.(*tg.InputMessagesFilterPinned)
			return ok
		}},
		{"saved search", func(c *Client) ([]rpc.Message, error) {
			r, err := c.SavedMessages(ctx, 42, nil, rpc.SavedMessagesParams{Limit: 1, Query: "query"}, true)
			return r.Messages, err
		}, func(v bin.Encoder) bool { r, ok := v.(*tg.MessagesSearchRequest); return ok && r.Q == "query" }},
		{"saved history", func(c *Client) ([]rpc.Message, error) {
			r, err := c.SavedMessages(ctx, 42, &ref, rpc.SavedMessagesParams{Limit: 1}, false)
			return r.Messages, err
		}, func(v bin.Encoder) bool { _, ok := v.(*tg.MessagesGetSavedHistoryRequest); return ok }},
		{"scheduled", func(c *Client) ([]rpc.Message, error) { r, err := c.ScheduledList(ctx, ref); return r.Messages, err }, func(v bin.Encoder) bool { _, ok := v.(*tg.MessagesGetScheduledHistoryRequest); return ok }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			inv := writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				if _, ok := in.(*tg.MessagesGetChatsRequest); ok {
					return respond(out, &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Chat{ID: 42, Title: "group"}}})
				}
				if !tc.check(in) {
					return fmt.Errorf("unexpected request %T", in)
				}
				calls++
				return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{testRawMessage(42, 7)}})
			})
			client := telegram.NewClient(1, "test", telegram.Options{Middlewares: []telegram.Middleware{rawTestMiddleware{invoke: inv}}})
			mgr := peers.Options{Storage: &peers.InmemoryStorage{}}.Build(client.API())
			if err := mgr.Apply(ctx, nil, []tg.ChatClass{&tg.Chat{ID: 42, Title: "group"}}); err != nil {
				t.Fatal(err)
			}
			c := &Client{tg: client, mgr: mgr, mgrReady: true}
			messages, err := tc.call(c)
			if err != nil || calls != 1 || len(messages) != 1 {
				t.Fatalf("messages=%+v calls=%d err=%v", messages, calls, err)
			}
			assertMessageRaw(t, messages[0].Raw)
		})
	}
}

func TestScheduledSendNowCarriesRaw(t *testing.T) {
	ctx := context.Background()
	calls := 0
	inv := writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch in.(type) {
		case *tg.MessagesGetChatsRequest:
			return respond(out, &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Chat{ID: 42, Title: "group"}}})
		case *tg.MessagesSendScheduledMessagesRequest:
			calls++
			return respond(out, &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewMessage{Message: testRawMessage(42, 7)}}})
		default:
			return fmt.Errorf("unexpected request %T", in)
		}
	})
	client := telegram.NewClient(1, "test", telegram.Options{Middlewares: []telegram.Middleware{rawTestMiddleware{invoke: inv}}})
	mgr := peers.Options{Storage: &peers.InmemoryStorage{}}.Build(client.API())
	if err := mgr.Apply(ctx, nil, []tg.ChatClass{&tg.Chat{ID: 42, Title: "group"}}); err != nil {
		t.Fatal(err)
	}
	c := &Client{tg: client, mgr: mgr, mgrReady: true}
	result, err := c.ScheduledSendNow(ctx, chatref.Ref{Kind: chatref.ID, ID: -42}, []int{7})
	if err != nil || calls != 1 || len(result.Messages) != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
	assertMessageRaw(t, result.Messages[0].Raw)
}
