package tgclient

import (
	"context"
	"errors"
	"testing"
	"tg-cli/internal/rpc"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

func TestChannelSettingRequests(t *testing.T) {
	for _, flag := range []string{"signatures", "antispam", "forum", "prehistory", "join-requests", "join-to-send"} {
		t.Run(flag, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch v := in.(type) {
				case *tg.ChannelsToggleSignaturesRequest:
					if flag != "signatures" || !v.SignaturesEnabled || !v.ProfilesEnabled {
						t.Fatalf("request %#v", v)
					}
				case *tg.ChannelsToggleAntiSpamRequest:
					if flag != "antispam" || !v.Enabled {
						t.Fatalf("request %#v", v)
					}
				case *tg.ChannelsToggleForumRequest:
					if flag != "forum" || !v.Enabled || v.Tabs || v.Sidebar {
						t.Fatalf("request %#v", v)
					}
				case *tg.ChannelsTogglePreHistoryHiddenRequest:
					if flag != "prehistory" || !v.Enabled {
						t.Fatalf("request %#v", v)
					}
				case *tg.ChannelsToggleJoinRequestRequest:
					if flag != "join-requests" || !v.Enabled || v.ApplyToInvites || v.GuardBot != nil {
						t.Fatalf("request %#v", v)
					}
				case *tg.ChannelsToggleJoinToSendRequest:
					if flag != "join-to-send" || !v.Enabled {
						t.Fatalf("request %#v", v)
					}
				default:
					t.Fatalf("request %T", in)
				}
				return respond(out, &tg.Updates{})
			}))
			m := testPeers(api)
			ch := m.Channel(channel(9, "Group", "group", false, true))
			on := "on"
			value := "on"
			if flag == "prehistory" {
				value = "hidden"
			}
			changed, err := setChannelSettingAPI(context.Background(), api, ch, flag, value, &on, &on)
			if err != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
		})
	}
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { return tgerr.New(400, "CHAT_NOT_MODIFIED") }))
	changed, err := setChannelSettingAPI(context.Background(), api, testPeers(api).Channel(channel(9, "Group", "group", false, true)), "forum", "on", nil, nil)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

func TestAccountSetRequests(t *testing.T) {
	for _, field := range []string{"birthday", "personal-channel", "emoji-status"} {
		t.Run(field, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				switch req := in.(type) {
				case *tg.AccountUpdateBirthdayRequest:
					if field != "birthday" {
						t.Fatalf("%T", in)
					}
					if _, ok := req.GetBirthday(); ok {
						t.Fatal("birthday not cleared")
					}
				case *tg.AccountUpdatePersonalChannelRequest:
					if field != "personal-channel" {
						t.Fatalf("%T", in)
					}
					if _, ok := req.Channel.(*tg.InputChannelEmpty); !ok {
						t.Fatalf("channel %T", req.Channel)
					}
				case *tg.AccountUpdateEmojiStatusRequest:
					if field != "emoji-status" {
						t.Fatalf("%T", in)
					}
					if _, ok := req.EmojiStatus.(*tg.EmojiStatusEmpty); !ok {
						t.Fatalf("status %T", req.EmojiStatus)
					}
				default:
					t.Fatalf("request %T", in)
				}
				return respond(out, &tg.BoolTrue{})
			}))
			changed, err := setAccountFieldAPI(context.Background(), api, field, "none", "", &tg.InputChannelEmpty{})
			if err != nil || !changed {
				t.Fatalf("changed %v err %v", changed, err)
			}
		})
	}
}

func TestChannelSignaturesPreserveOtherFlag(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		switch req := in.(type) {
		case *tg.ChannelsGetChannelsRequest:
			ch := channel(9, "Channel", "channel", true, false)
			ch.SignatureProfiles = true
			ch.Photo = &tg.ChatPhotoEmpty{}
			return respond(out, &tg.MessagesChats{Chats: []tg.ChatClass{ch}})
		case *tg.ChannelsToggleSignaturesRequest:
			if !req.SignaturesEnabled || !req.ProfilesEnabled {
				t.Fatalf("request %#v", req)
			}
			return respond(out, &tg.Updates{})
		default:
			t.Fatalf("unexpected %T", in)
			return nil
		}
	}))
	on := "on"
	ch := testPeers(api).Channel(channel(9, "Channel", "channel", true, false))
	if _, err := setChannelSettingAPI(context.Background(), api, ch, "signatures", "on", &on, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls %d", calls)
	}
}

func TestEmojiStatusPremiumError(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error {
		return tgerr.New(400, "PREMIUM_ACCOUNT_REQUIRED")
	}))
	_, err := setAccountFieldAPI(context.Background(), api, "emoji-status", "123", "", &tg.InputChannelEmpty{})
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeTelegram {
		t.Fatalf("error %v", err)
	}
	data, ok := re.Data.(map[string]any)
	if !ok || data["reason"] != "premium_required" {
		t.Fatalf("data %#v", re.Data)
	}
}
