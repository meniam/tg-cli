package tgclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestAudit08_JoinSpecialTelegramAnswers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invite  bool
		tgError string
		want    rpc.ChatJoinResult
	}{
		{"channel already a member", false, "USER_ALREADY_PARTICIPANT", rpc.ChatJoinResult{ChatID: -1000000000042, Type: "supergroup", AlreadyMember: true}},
		{"channel request sent", false, "INVITE_REQUEST_SENT", rpc.ChatJoinResult{ChatID: -1000000000042, Type: "supergroup", Requested: true}},
		{"invite already a member", true, "USER_ALREADY_PARTICIPANT", rpc.ChatJoinResult{AlreadyMember: true}},
		{"invite request sent", true, "INVITE_REQUEST_SENT", rpc.ChatJoinResult{Requested: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return tgerr.New(400, tc.tgError) }))
			var p peers.Peer
			hash := ""
			if tc.invite {
				hash = "secret"
			} else {
				p = peers.Options{}.Build(api).Channel(&tg.Channel{ID: 42, AccessHash: 99, Megagroup: true})
			}
			got, err := joinAdminChat(context.Background(), api, hash, p)
			if err != nil || got != tc.want {
				t.Fatalf("got %+v err=%v, want %+v", got, err, tc.want)
			}
		})
	}
}

func TestAudit08_LeaveNotParticipant(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return tgerr.New(400, "USER_NOT_PARTICIPANT") }))
	m := peers.Options{}.Build(api)
	for name, peer := range map[string]peers.Peer{
		"channel": m.Channel(&tg.Channel{ID: 42, AccessHash: 99, Megagroup: true}),
		"chat":    m.Chat(&tg.Chat{ID: 42}),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := leaveAdminChat(context.Background(), api, peer)
			var e *rpc.Error
			if !errors.As(err, &e) || e.Code != rpc.CodeNotFound {
				t.Fatalf("err=%v, want a not_found rpc error", err)
			}
			// The generic mapper also yields not_found; only the leave branch speaks about "you".
			if e.Message != "you are not a member of this chat" {
				t.Errorf("message=%q", e.Message)
			}
			if data, _ := e.Data.(map[string]any); data["telegram_error"] != "USER_NOT_PARTICIPANT" {
				t.Errorf("error data=%v", e.Data)
			}
			if got.Left {
				t.Errorf("result %+v reports a leave that did not happen", got)
			}
		})
	}
}

func TestAudit08_EditTitleNotModified(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return tgerr.New(400, "CHAT_NOT_MODIFIED") }))
	m := peers.Options{}.Build(api)
	title := "same"
	for name, peer := range map[string]peers.Peer{
		"channel": m.Channel(&tg.Channel{ID: 42, AccessHash: 99, Megagroup: true}),
		"chat":    m.Chat(&tg.Chat{ID: 42}),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := editAdminChat(context.Background(), api, peer, rpc.ChatEditParams{Title: &title})
			if err != nil || got.TitleChanged || got.AboutChanged {
				t.Fatalf("got %+v err=%v, want unchanged result without error", got, err)
			}
		})
	}
}

func TestAudit08_OwnAdminRightsEachFlag(t *testing.T) {
	for _, tc := range []struct {
		name   string
		rights tg.ChatAdminRights
		want   rpc.ChatOwnAdminRights
	}{
		{"invite only", tg.ChatAdminRights{InviteUsers: true}, rpc.ChatOwnAdminRights{InviteUsers: true}},
		{"change info only", tg.ChatAdminRights{ChangeInfo: true}, rpc.ChatOwnAdminRights{ChangeInfo: true}},
		{"ban only", tg.ChatAdminRights{BanUsers: true}, rpc.ChatOwnAdminRights{BanUsers: true}},
		{"all", tg.ChatAdminRights{InviteUsers: true, ChangeInfo: true, BanUsers: true}, rpc.ChatOwnAdminRights{InviteUsers: true, ChangeInfo: true, BanUsers: true}},
		{"none", tg.ChatAdminRights{}, rpc.ChatOwnAdminRights{}},
	} {
		t.Run("channel "+tc.name, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
				return respond(out, &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 42, Title: "group", Photo: &tg.ChatPhotoEmpty{}, AdminRights: tc.rights}}})
			}))
			got, err := ownAdminRights(context.Background(), api, peers.Options{}.Build(api).Channel(&tg.Channel{ID: 42, AccessHash: 99}))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v err=%v, want %+v", got, err, tc.want)
			}
		})
		t.Run("chat "+tc.name, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
				return respond(out, &tg.MessagesChatFull{FullChat: &tg.ChatFull{ID: 42, Participants: &tg.ChatParticipantsForbidden{ChatID: 42}}, Chats: []tg.ChatClass{&tg.Chat{ID: 42, Title: "group", Photo: &tg.ChatPhotoEmpty{}, AdminRights: tc.rights}}})
			}))
			got, err := ownAdminRights(context.Background(), api, peers.Options{}.Build(api).Chat(&tg.Chat{ID: 42}))
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v err=%v, want %+v", got, err, tc.want)
			}
		})
	}
}
