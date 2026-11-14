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
	for code, names := range map[int][]string{
		rpc.CodeTelegram:      {"CHAT_ADMIN_REQUIRED", "RIGHT_FORBIDDEN", "USER_ADMIN_INVALID", "CHAT_WRITE_FORBIDDEN", "USER_CREATOR", "CHANNELS_TOO_MUCH", "USER_CHANNELS_TOO_MUCH", "CHANNELS_ADMIN_PUBLIC_TOO_MUCH", "USERS_TOO_MUCH", "USER_RESTRICTED", "FROZEN_METHOD_INVALID", "STARS_PAYMENT_REQUIRED"},
		rpc.CodeNotFound:      {"USER_NOT_PARTICIPANT", "CHANNEL_PRIVATE", "CHANNEL_INVALID", "CHAT_ID_INVALID", "PEER_ID_INVALID", "USER_ID_INVALID", "PARTICIPANT_ID_INVALID", "INPUT_USER_DEACTIVATED", "INVITE_HASH_EMPTY", "INVITE_HASH_INVALID", "INVITE_HASH_EXPIRED"},
		rpc.CodeInvalidParams: {"CHAT_TITLE_EMPTY", "CHAT_ABOUT_TOO_LONG", "SECONDS_INVALID", "BANNED_RIGHTS_INVALID", "UNTIL_DATE_INVALID"},
	} {
		for _, name := range names {
			var got *rpc.Error
			if err := mapChatAdminError(tgerr.New(400, name)); !errors.As(err, &got) || got.Code != code {
				t.Errorf("%s: %v", name, err)
			}
		}
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

func TestChatAdminLeaveAndTitleByPeerType(t *testing.T) {
	for _, group := range []bool{true, false} {
		for _, edit := range []bool{true, false} {
			calls := 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				calls++
				switch req := input.(type) {
				case *tg.MessagesDeleteChatUserRequest:
					if !group || edit || req.ChatID != 42 || req.RevokeHistory {
						t.Fatalf("unexpected leave request: %#v", req)
					}
					if _, ok := req.UserID.(*tg.InputUserSelf); !ok {
						t.Fatalf("leave target: %T", req.UserID)
					}
				case *tg.ChannelsLeaveChannelRequest:
					if group || edit {
						t.Fatalf("unexpected request: %T", input)
					}
					channel, ok := req.Channel.(*tg.InputChannel)
					if !ok || channel.ChannelID != 42 || channel.AccessHash != 99 {
						t.Fatalf("channel: %#v", req.Channel)
					}
				case *tg.MessagesEditChatTitleRequest:
					if !group || !edit || req.ChatID != 42 || req.Title != "new" {
						t.Fatalf("title: %#v", req)
					}
				case *tg.ChannelsEditTitleRequest:
					if group || !edit || req.Title != "new" {
						t.Fatalf("title: %#v", req)
					}
				default:
					t.Fatalf("unexpected request: %T", input)
				}
				return respond(output, &tg.Updates{})
			}))
			m := peers.Options{}.Build(api)
			var peer peers.Peer
			if group {
				peer = m.Chat(&tg.Chat{ID: 42})
			} else {
				peer = m.Channel(&tg.Channel{ID: 42, AccessHash: 99, Megagroup: true})
			}
			var err error
			if edit {
				title := "new"
				_, err = editAdminChat(context.Background(), api, peer, rpc.ChatEditParams{Title: &title})
			} else {
				_, err = leaveAdminChat(context.Background(), api, peer)
			}
			if err != nil || calls != 1 {
				t.Fatalf("group=%v edit=%v calls=%d: %v", group, edit, calls, err)
			}
		}
	}
}

func TestChatAdminCreateRequest(t *testing.T) {
	for _, kind := range []string{"channel", "supergroup"} {
		calls := 0
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
			calls++
			req, ok := input.(*tg.ChannelsCreateChannelRequest)
			if !ok || req.Title != "title" || req.About != "about" || req.Broadcast != (kind == "channel") || req.Megagroup != (kind == "supergroup") {
				t.Fatalf("request: %#v", input)
			}
			return respond(output, &tg.Updates{Chats: []tg.ChatClass{&tg.Channel{ID: 42, Title: "title", Photo: &tg.ChatPhotoEmpty{}, Megagroup: kind == "supergroup"}}})
		}))
		got, err := createAdminChat(context.Background(), api, rpc.ChatCreateParams{Title: "title", About: "about", Type: kind})
		if err != nil || calls != 1 || got.ID != -1000000000042 || got.Type != kind {
			t.Fatalf("got %+v calls=%d err=%v", got, calls, err)
		}
	}
}

func TestChatAdminDefaultsAndSlowMode(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		switch req := input.(type) {
		case *tg.MessagesEditChatDefaultBannedRightsRequest:
			if !req.BannedRights.SendPhotos || req.BannedRights.SendMedia {
				t.Fatalf("rights: %#v", req.BannedRights)
			}
		case *tg.ChannelsToggleSlowModeRequest:
			if req.Seconds != 30 {
				t.Fatalf("seconds: %d", req.Seconds)
			}
		default:
			t.Fatalf("request: %T", input)
		}
		return respond(output, &tg.Updates{})
	}))
	m := peers.Options{}.Build(api)
	group := m.Chat(&tg.Chat{ID: 42})
	super := m.Channel(&tg.Channel{ID: 43, Megagroup: true})
	broadcast := m.Channel(&tg.Channel{ID: 44, Broadcast: true})
	for _, p := range []peers.Peer{group, super} {
		if _, err := setDefaultChatRights(context.Background(), api, p, []string{"send-photos"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := setDefaultChatRights(context.Background(), api, broadcast, nil); err == nil {
		t.Fatal("channel accepted")
	}
	if _, err := setChatSlowMode(context.Background(), api, super, 30); err != nil {
		t.Fatal(err)
	}
	for _, p := range []peers.Peer{group, broadcast} {
		if _, err := setChatSlowMode(context.Background(), api, p, 30); err == nil {
			t.Fatal("invalid chat accepted")
		}
	}
	if calls != 3 {
		t.Fatalf("calls: %d", calls)
	}
}

func TestChatAdminJoinRequests(t *testing.T) {
	for _, invite := range []bool{false, true} {
		calls := 0
		api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
			calls++
			switch req := input.(type) {
			case *tg.MessagesImportChatInviteRequest:
				if !invite || req.Hash != "secret" {
					t.Fatalf("request: %#v", req)
				}
			case *tg.ChannelsJoinChannelRequest:
				if invite {
					t.Fatalf("request: %T", req)
				}
				ch, ok := req.Channel.(*tg.InputChannel)
				if !ok || ch.ChannelID != 42 || ch.AccessHash != 99 {
					t.Fatalf("channel: %#v", req.Channel)
				}
			default:
				t.Fatalf("request: %T", input)
			}
			return respond(output, &tg.MessagesChatInviteJoinResultOk{Updates: &tg.Updates{Chats: []tg.ChatClass{&tg.Channel{ID: 42, Title: "test", Photo: &tg.ChatPhotoEmpty{}, Megagroup: true}}}})
		}))
		m := peers.Options{}.Build(api)
		var p peers.Peer = m.Channel(&tg.Channel{ID: 42, AccessHash: 99, Megagroup: true})
		hash := ""
		if invite {
			hash = "secret"
			p = nil
		}
		got, err := joinAdminChat(context.Background(), api, hash, p)
		if err != nil || calls != 1 || got.ChatID != -1000000000042 {
			t.Fatalf("result %+v calls=%d err=%v", got, calls, err)
		}
	}
}

func TestKickAdminMemberBasicGroup(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		req, ok := input.(*tg.MessagesDeleteChatUserRequest)
		if !ok || req.ChatID != 42 || req.RevokeHistory {
			t.Fatalf("request %#v", input)
		}
		u, ok := req.UserID.(*tg.InputUser)
		if !ok || u.UserID != 20 || u.AccessHash != 21 {
			t.Fatalf("user %#v", req.UserID)
		}
		return respond(output, &tg.Updates{})
	}))
	m := peers.Options{}.Build(api)
	got, err := kickAdminMember(context.Background(), api, m.Chat(&tg.Chat{ID: 42}), m.User(&tg.User{ID: 20, AccessHash: 21}))
	if err != nil || calls != 1 || !got.Kicked || got.UserID != 20 {
		t.Fatalf("got %+v calls=%d err=%v", got, calls, err)
	}
}

func TestChatAdminEditAbout(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		req, ok := input.(*tg.MessagesEditChatAboutRequest)
		if !ok || req.About != "" {
			t.Fatalf("request %#v", input)
		}
		if p, ok := req.Peer.(*tg.InputPeerChat); !ok || p.ChatID != 42 {
			t.Fatalf("peer %#v", req.Peer)
		}
		return respond(output, &tg.BoolTrue{})
	}))
	m := peers.Options{}.Build(api)
	about := ""
	got, err := editAdminChat(context.Background(), api, m.Chat(&tg.Chat{ID: 42}), rpc.ChatEditParams{About: &about})
	if err != nil || calls != 1 || !got.AboutChanged {
		t.Fatalf("got %+v calls=%d err=%v", got, calls, err)
	}
}

func TestChatAdminMemberRightsByChatType(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		req, ok := input.(*tg.ChannelsEditBannedRequest)
		if !ok {
			t.Fatalf("request %T", input)
		}
		user, ok := req.Participant.(*tg.InputPeerUser)
		if !ok || user.UserID != 20 || user.AccessHash != 21 {
			t.Fatalf("participant %#v", req.Participant)
		}
		switch calls {
		case 1:
			if !req.BannedRights.ViewMessages || req.BannedRights.UntilDate != 123 {
				t.Fatal("ban rights")
			}
		case 2:
			if !req.BannedRights.Zero() {
				t.Fatal("unban rights")
			}
		case 3:
			if !req.BannedRights.SendPhotos || req.BannedRights.ViewMessages {
				t.Fatal("restrict rights")
			}
		}
		return respond(output, &tg.Updates{})
	}))
	m := peers.Options{}.Build(api)
	user := m.User(&tg.User{ID: 20, AccessHash: 21})
	group := m.Chat(&tg.Chat{ID: 42})
	super := m.Channel(&tg.Channel{ID: 43, Megagroup: true})
	broadcast := m.Channel(&tg.Channel{ID: 44, Broadcast: true})
	if _, err := setMemberBanned(context.Background(), api, super, user, tg.ChatBannedRights{ViewMessages: true, UntilDate: 123}, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := setMemberBanned(context.Background(), api, broadcast, user, tg.ChatBannedRights{}, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := restrictAdminMember(context.Background(), api, super, user, []string{"send-photos"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := setMemberBanned(context.Background(), api, group, user, tg.ChatBannedRights{}, "", false); err == nil {
		t.Fatal("basic group accepted ban")
	}
	if _, err := restrictAdminMember(context.Background(), api, broadcast, user, nil, ""); err == nil {
		t.Fatal("broadcast accepted restriction")
	}
	if calls != 3 {
		t.Fatalf("calls %d", calls)
	}
}

func TestChatAdminOwnRightsChannel(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		req, ok := input.(*tg.ChannelsGetChannelsRequest)
		if !ok || len(req.ID) != 1 {
			t.Fatalf("request %#v", input)
		}
		return respond(output, &tg.MessagesChats{Chats: []tg.ChatClass{&tg.Channel{ID: 42, Title: "group", Photo: &tg.ChatPhotoEmpty{}, AdminRights: tg.ChatAdminRights{ChangeInfo: true, BanUsers: true}}}})
	}))
	m := peers.Options{}.Build(api)
	got, err := ownAdminRights(context.Background(), api, m.Channel(&tg.Channel{ID: 42, AccessHash: 99}))
	if err != nil || calls != 1 || !got.ChangeInfo || !got.BanUsers {
		t.Fatalf("got %+v calls=%d err=%v", got, calls, err)
	}
}

func TestChatAdminOwnRightsBasicGroup(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
		calls++
		req, ok := input.(*tg.MessagesGetFullChatRequest)
		if !ok || req.ChatID != 42 {
			t.Fatalf("request %#v", input)
		}
		return respond(output, &tg.MessagesChatFull{FullChat: &tg.ChatFull{ID: 42, Participants: &tg.ChatParticipantsForbidden{ChatID: 42}}, Chats: []tg.ChatClass{&tg.Chat{ID: 42, Title: "group", Photo: &tg.ChatPhotoEmpty{}, Creator: true}}})
	}))
	m := peers.Options{}.Build(api)
	got, err := ownAdminRights(context.Background(), api, m.Chat(&tg.Chat{ID: 42}))
	if err != nil || calls != 1 || !got.Creator {
		t.Fatalf("got %+v calls=%d err=%v", got, calls, err)
	}
}
