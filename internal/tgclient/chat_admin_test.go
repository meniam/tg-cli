package tgclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestChatAdminBannedRights(t *testing.T) {
	got := bannedRights(rpc.ChatAdminRights, 123)
	want := tg.ChatBannedRights{UntilDate: 123, SendPlain: true, SendMedia: true, SendPhotos: true, SendVideos: true, SendRoundvideos: true, SendAudios: true, SendVoices: true, SendDocs: true, SendStickers: true, SendGifs: true, SendGames: true, SendInline: true, SendPolls: true, EmbedLinks: true, SendReactions: true, ChangeInfo: true, InviteUsers: true, PinMessages: true, ManageTopics: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rights: %#v", got)
	}
}
func TestChatAdminJoinResult(t *testing.T) {
	got, err := joinResult(&tg.MessagesChatInviteJoinResultOk{Updates: &tg.Updates{Chats: []tg.ChatClass{&tg.Channel{ID: 42, Title: "test", Megagroup: true}}}})
	if err != nil || got.ChatID != -1000000000042 || got.Type != "supergroup" {
		t.Fatalf("got %+v: %v", got, err)
	}
	if _, err = joinResult(&tg.MessagesChatInviteJoinResultWebView{}); err == nil {
		t.Fatal("web view accepted")
	}
	if _, err = joinResult(&tg.MessagesChatInviteJoinResultOk{Updates: &tg.Updates{}}); err == nil {
		t.Fatal("empty result accepted")
	}
}
func TestChatAdminErrors(t *testing.T) {
	cases := []struct {
		typ  string
		code int
	}{{"CHAT_ADMIN_REQUIRED", rpc.CodeTelegram}, {"USER_CREATOR", rpc.CodeTelegram}, {"USER_NOT_PARTICIPANT", rpc.CodeNotFound}, {"PARTICIPANT_ID_INVALID", rpc.CodeNotFound}, {"INVITE_HASH_EXPIRED", rpc.CodeNotFound}, {"SECONDS_INVALID", rpc.CodeInvalidParams}, {"USER_RESTRICTED", rpc.CodeTelegram}, {"CHANNELS_TOO_MUCH", rpc.CodeTelegram}, {"STARS_PAYMENT_REQUIRED", rpc.CodeTelegram}, {"FLOOD_WAIT_10", rpc.CodeFloodWait}}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			err := mapChatAdminError(tgerr.New(400, tc.typ))
			var e *rpc.Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("%v", err)
			}
		})
	}
}
func TestChatAdminWriteRequests(t *testing.T) {
	requests := []bin.Encoder{&tg.ChannelsJoinChannelRequest{}, &tg.MessagesImportChatInviteRequest{}, &tg.ChannelsLeaveChannelRequest{}, &tg.MessagesDeleteChatUserRequest{}, &tg.ChannelsCreateChannelRequest{}, &tg.ChannelsEditTitleRequest{}, &tg.MessagesEditChatTitleRequest{}, &tg.MessagesEditChatAboutRequest{}, &tg.ChannelsEditBannedRequest{}, &tg.MessagesEditChatDefaultBannedRightsRequest{}, &tg.ChannelsToggleSlowModeRequest{}}
	for _, req := range requests {
		if !writeRequest(req) {
			t.Errorf("not classified as write: %T", req)
		}
		limiter := limiterWithClock(t, newThrottleClock(), nil)
		flood := tgerr.New(420, "FLOOD_WAIT_2")
		calls := 0
		invoke := throttleMiddleware(limiter).Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return flood }))
		if err := invoke(context.Background(), req, nil); err != flood || calls != 1 {
			t.Errorf("%T: err=%v calls=%d", req, err, calls)
		}
	}
}
func TestChatAdminEditBannedRequest(t *testing.T) {
	var calls int
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		req, ok := input.(*tg.ChannelsEditBannedRequest)
		if !ok || !req.BannedRights.ViewMessages || req.BannedRights.UntilDate != 123 {
			t.Fatalf("request: %#v", input)
		}
		return respond(output, &tg.Updates{})
	}))
	m := peers.Options{}.Build(api)
	ch := m.Channel(&tg.Channel{ID: 10, AccessHash: 11})
	u := m.User(&tg.User{ID: 20, AccessHash: 21})
	if err := editBanned(context.Background(), api, ch, u, tg.ChatBannedRights{ViewMessages: true, UntilDate: 123}); err != nil || calls != 1 {
		t.Fatalf("calls %d: %v", calls, err)
	}
}

func TestKickChannelPartialFailure(t *testing.T) {
	for _, fail := range []int{0, 1, 2} {
		calls := 0
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
			calls++
			req, ok := input.(*tg.ChannelsEditBannedRequest)
			if !ok || req.BannedRights.ViewMessages != (calls == 1) {
				t.Fatalf("call %d: %#v", calls, input)
			}
			if calls == fail {
				return tgerr.New(400, "CHAT_ADMIN_REQUIRED")
			}
			return respond(output, &tg.Updates{})
		}))
		m := peers.Options{}.Build(api)
		err := kickChannel(context.Background(), api, m.Channel(&tg.Channel{ID: 10, AccessHash: 11}), m.User(&tg.User{ID: 20, AccessHash: 21}))
		wantCalls := 2
		if fail == 1 {
			wantCalls = 1
		}
		if calls != wantCalls || (err != nil) != (fail != 0) {
			t.Fatalf("fail=%d calls=%d err=%v", fail, calls, err)
		}
		if fail == 2 {
			var e *rpc.Error
			if !errors.As(err, &e) || e.Code != rpc.CodeTelegram || e.Data.(map[string]any)["still_banned"] != true {
				t.Fatalf("partial failure: %v", err)
			}
		}
	}
}
