package tgclient

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func audit10Dialog(userID int64, unread int, archived bool) *tg.Dialog {
	d := &tg.Dialog{Peer: &tg.PeerUser{UserID: userID}, UnreadCount: unread, TopMessage: 5}
	if archived {
		d.SetFolderID(1)
	}
	return d
}

// audit10UnreadAPI serves one fixed dialog list for every messages.getDialogs call.
func audit10UnreadAPI(t *testing.T, dialogs []tg.DialogClass, chats []tg.ChatClass, requests *[]*tg.MessagesGetDialogsRequest) *tg.Client {
	t.Helper()
	// gotd resolves every dialog peer from the response, so each user needs an entry.
	var users []tg.UserClass
	for _, d := range dialogs {
		if peer, ok := d.(*tg.Dialog).Peer.(*tg.PeerUser); ok {
			users = append(users, user(peer.UserID, "U", "", "", false))
		}
	}
	return tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesGetDialogsRequest)
		if !ok {
			return fmt.Errorf("unexpected request %T", in)
		}
		if requests != nil {
			*requests = append(*requests, req)
		}
		return respond(out, &tg.MessagesDialogs{Dialogs: dialogs, Chats: chats, Users: users})
	}))
}

// TestAudit10_UnreadChatsFilters feeds one list that holds an archived dialog, a
// read one, a forbidden one, a muted forum and a past mute, and compares the
// whole result for the plain and for the archive request.
func TestAudit10_UnreadChatsFilters(t *testing.T) {
	const farFuture = 2000000000 // 2033-05-18, the largest sensible int32 mute deadline
	muted := audit10Dialog(0, 3, false)
	muted.Peer = &tg.PeerChannel{ChannelID: 50}
	muted.NotifySettings.SetMuteUntil(farFuture)
	pastMute := audit10Dialog(46, 1, false)
	pastMute.NotifySettings.SetMuteUntil(1)
	marked := audit10Dialog(47, 0, false)
	marked.UnreadMark = true
	dialogs := []tg.DialogClass{
		audit10Dialog(42, 2, false),
		audit10Dialog(43, 1, true),
		audit10Dialog(44, 0, false),
		audit10Dialog(45, 4, false),
		muted,
		pastMute,
		marked,
	}
	forum := channel(50, "Forum", "", false, true)
	forum.Forum = true
	forum.Photo = &tg.ChatPhotoEmpty{}
	forum.DefaultBannedRights = tg.ChatBannedRights{}
	allowed := func(id int64) bool { return id != 45 }
	forumID := int64(-channelIDBase - 50)

	type row struct {
		id           int64
		unread       int
		forum, muted bool
		mark         bool
	}
	got := func(r rpc.UnreadChatsResult) []row {
		rows := make([]row, 0, len(r.Chats))
		for _, c := range r.Chats {
			rows = append(rows, row{c.ID, c.Unread, c.Forum, c.Muted, c.UnreadMark})
		}
		return rows
	}

	t.Run("plain", func(t *testing.T) {
		var requests []*tg.MessagesGetDialogsRequest
		api := audit10UnreadAPI(t, dialogs, []tg.ChatClass{forum}, &requests)
		result, states, err := unreadChatsAPI(context.Background(), api, 100, false, allowed)
		if err != nil {
			t.Fatal(err)
		}
		want := []row{{42, 2, false, false, false}, {forumID, 3, true, true, false}, {46, 1, false, false, false}, {47, 0, false, false, true}}
		if !slices.Equal(got(result), want) || len(states) != len(want) || result.Scanned != 7 || !result.Complete {
			t.Fatalf("rows=%+v states=%d scanned=%d complete=%v, want %+v", got(result), len(states), result.Scanned, result.Complete, want)
		}
		if len(requests) == 0 {
			t.Fatal("no request reached Telegram")
		}
		if requests[0].FolderID != 0 {
			t.Fatalf("the plain request asked for folder %d", requests[0].FolderID)
		}
	})
	t.Run("archive", func(t *testing.T) {
		var requests []*tg.MessagesGetDialogsRequest
		api := audit10UnreadAPI(t, dialogs, []tg.ChatClass{forum}, &requests)
		result, _, err := unreadChatsAPI(context.Background(), api, 100, true, allowed)
		if err != nil {
			t.Fatal(err)
		}
		if want := []row{{43, 1, false, false, false}}; !slices.Equal(got(result), want) || !result.Chats[0].Archived {
			t.Fatalf("rows=%+v, want %+v archived", got(result), want)
		}
		if requests[0].FolderID != 1 {
			t.Fatalf("the archive request asked for folder %d", requests[0].FolderID)
		}
	})
	t.Run("limit stops early", func(t *testing.T) {
		api := audit10UnreadAPI(t, dialogs, []tg.ChatClass{forum}, nil)
		result, _, err := unreadChatsAPI(context.Background(), api, 2, false, allowed)
		if err != nil {
			t.Fatal(err)
		}
		if want := []row{{42, 2, false, false, false}, {forumID, 3, true, true, false}}; !slices.Equal(got(result), want) || result.Complete {
			t.Fatalf("rows=%+v complete=%v, want %+v and Complete=false", got(result), result.Complete, want)
		}
	})
	t.Run("scan budget", func(t *testing.T) {
		var many []tg.DialogClass
		for i := 0; i < 1100; i++ {
			many = append(many, audit10Dialog(int64(10000+i), 0, false))
		}
		api := audit10UnreadAPI(t, many, nil, nil)
		result, _, err := unreadChatsAPI(context.Background(), api, 100, false, allowed)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Chats) != 0 || result.Scanned != 1000 || result.Complete {
			t.Fatalf("chats=%d scanned=%d complete=%v, want 0, 1000 and Complete=false", len(result.Chats), result.Scanned, result.Complete)
		}
	})
}

func TestAudit10_UnreadMessagesErrors(t *testing.T) {
	t.Run("topic requires a forum chat", func(t *testing.T) {
		calls := 0
		api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return nil }))
		chat := peers.Options{}.Build(api).Chat(&tg.Chat{ID: 42, Title: "group"})
		_, _, err := unreadMessagesAPI(context.Background(), api, chat, rpc.MessageUnreadParams{Limit: 5, Topic: 3})
		if err == nil || rpcErrorCode(t, err) != rpc.CodeInvalidParams || calls != 0 {
			t.Fatalf("err=%v calls=%d, want invalid_params without a request", err, calls)
		}
	})
	t.Run("chat missing from peer dialogs", func(t *testing.T) {
		api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
			if _, ok := in.(*tg.MessagesGetPeerDialogsRequest); !ok {
				return fmt.Errorf("unexpected request %T", in)
			}
			return respond(out, &tg.MessagesPeerDialogs{})
		}))
		chat := peers.Options{}.Build(api).Chat(&tg.Chat{ID: 42, Title: "group"})
		_, _, err := unreadMessagesAPI(context.Background(), api, chat, rpc.MessageUnreadParams{Limit: 5})
		if err == nil || rpcErrorCode(t, err) != rpc.CodeNotFound {
			t.Fatalf("err=%v, want not_found", err)
		}
	})
}
