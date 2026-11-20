package tgclient

import (
	"context"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"testing"
	"tg-cli/internal/rpc"
)

func TestUnreadChatsReadOnlyAndArchive(t *testing.T) {
	for _, archived := range []bool{false, true} {
		calls := 0
		api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			req, ok := in.(*tg.MessagesGetDialogsRequest)
			if !ok {
				t.Fatalf("unexpected request %T", in)
			}
			calls++
			if archived && req.FolderID != 1 {
				t.Fatalf("folder %d", req.FolderID)
			}
			d := &tg.Dialog{Peer: &tg.PeerUser{UserID: 42}, UnreadMark: true, TopMessage: 5}
			if archived {
				d.SetFolderID(1)
			}
			return respond(out, &tg.MessagesDialogs{Dialogs: []tg.DialogClass{d}, Users: []tg.UserClass{user(42, "A", "", "", false)}})
		}))
		result, states, err := unreadChatsAPI(context.Background(), api, 100, archived, func(id int64) bool { return id == 42 })
		if err != nil || calls != 1 || len(result.Chats) != 1 || len(states) != 1 || !result.Chats[0].UnreadMark || !result.Complete {
			t.Fatalf("result %+v states %+v calls %d err %v", result, states, calls, err)
		}
	}
}

func TestUnreadMessagesReadMarkerAndFilterOutgoing(t *testing.T) {
	calls := []string{}
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		switch v := in.(type) {
		case *tg.MessagesGetPeerDialogsRequest:
			calls = append(calls, "dialog")
			return respond(out, &tg.MessagesPeerDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChat{ChatID: 42}, ReadInboxMaxID: 9, UnreadCount: 2}}, State: tg.UpdatesState{}})
		case *tg.MessagesGetHistoryRequest:
			calls = append(calls, "history")
			if v.MinID != 9 {
				t.Fatalf("min id %d", v.MinID)
			}
			a := testRawMessage(42, 12)
			b := testRawMessage(42, 11)
			b.(*tg.Message).Out = true
			c := testRawMessage(42, 10)
			return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{a, b, c}})
		default:
			t.Fatalf("unexpected request %T", in)
			return nil
		}
	}))
	m := peers.Options{}.Build(api)
	peer := m.Chat(&tg.Chat{ID: 42, Title: "group"})
	out, state, err := unreadMessagesAPI(context.Background(), api, peer, rpc.MessageUnreadParams{Limit: 2})
	if err != nil || len(calls) != 2 || len(out.Messages) != 1 || out.Messages[0].ID != 12 || out.NextCursor == "" || state == nil || state.ReadInboxMaxID != 9 || out.Unread != 2 || out.ReadInboxMaxID != 9 {
		t.Fatalf("result %+v state %+v calls %v error %v", out, state, calls, err)
	}
}

func TestUnreadForumTopic(t *testing.T) {
	calls := 0
	deleted := false
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		switch v := in.(type) {
		case *tg.MessagesGetForumTopicsByIDRequest:
			if len(v.Topics) != 1 || v.Topics[0] != 5 {
				t.Fatalf("topics %+v", v.Topics)
			}
			if deleted {
				return respond(out, &tg.MessagesForumTopics{Topics: []tg.ForumTopicClass{&tg.ForumTopicDeleted{ID: 5}}})
			}
			return respond(out, &tg.MessagesForumTopics{Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 5, Peer: &tg.PeerChannel{ChannelID: 42}, FromID: &tg.PeerUser{UserID: 1}, ReadInboxMaxID: 9, UnreadCount: 1}}})
		case *tg.MessagesGetRepliesRequest:
			if v.MinID != 9 || v.MsgID != 5 {
				t.Fatalf("replies %+v", v)
			}
			return respond(out, &tg.MessagesMessages{Messages: []tg.MessageClass{testRawMessage(42, 10)}})
		default:
			t.Fatalf("unexpected request %T", in)
			return nil
		}
	}))
	m := peers.Options{}.Build(api)
	forum := m.Channel(&tg.Channel{ID: 42, AccessHash: 3, Forum: true, Megagroup: true})
	_, _, err := unreadMessagesAPI(context.Background(), api, forum, rpc.MessageUnreadParams{Limit: 20})
	if err == nil || calls != 0 || rpcErrorCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("forum without topic: %v calls %d", err, calls)
	}
	out, _, err := unreadMessagesAPI(context.Background(), api, forum, rpc.MessageUnreadParams{Limit: 20, Topic: 5})
	if err != nil || len(out.Messages) != 1 || calls != 2 {
		t.Fatalf("topic %+v %v calls %d", out, err, calls)
	}
	deleted = true
	_, _, err = unreadMessagesAPI(context.Background(), api, forum, rpc.MessageUnreadParams{Limit: 20, Topic: 5})
	if err == nil || rpcErrorCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("deleted topic: %v", err)
	}
}
