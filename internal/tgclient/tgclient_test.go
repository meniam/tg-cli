package tgclient

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/rpc"
)

// user builds a tg.User the way the decoder does: optional fields are set
// through setters, which also raise their flag bits.
func user(id int64, first, last, username string, bot bool) *tg.User {
	u := &tg.User{ID: id, Bot: bot}
	if first != "" {
		u.SetFirstName(first)
	}
	if last != "" {
		u.SetLastName(last)
	}
	if username != "" {
		u.SetUsername(username)
	}
	return u
}

func channel(id int64, title, username string, broadcast, megagroup bool) *tg.Channel {
	c := &tg.Channel{ID: id, Title: title, Broadcast: broadcast, Megagroup: megagroup}
	if username != "" {
		c.SetUsername(username)
	}
	return c
}

func entities() peer.Entities {
	return peer.NewEntities(
		map[int64]*tg.User{
			1: user(1, "Ann", "Lee", "ann", false),
			2: user(2, "HelperBot", "", "helper_bot", true),
			3: user(3, "", "", "only_username", false),
		},
		map[int64]*tg.Chat{7: {ID: 7, Title: "Family"}},
		map[int64]*tg.Channel{
			8: channel(8, "News", "news", true, false),
			9: channel(9, "Chatty", "", false, true),
		},
	)
}

func TestToChat(t *testing.T) {
	tests := []struct {
		name string
		peer tg.PeerClass
		want rpc.Chat
	}{
		{"user", &tg.PeerUser{UserID: 1}, rpc.Chat{ID: 1, Type: "user", Title: "Ann Lee", Username: "ann", Unread: 3}},
		{"bot", &tg.PeerUser{UserID: 2}, rpc.Chat{ID: 2, Type: "bot", Title: "HelperBot", Username: "helper_bot", Unread: 3}},
		{"user without name", &tg.PeerUser{UserID: 3}, rpc.Chat{ID: 3, Type: "user", Title: "only_username", Username: "only_username", Unread: 3}},
		{"unknown user", &tg.PeerUser{UserID: 99}, rpc.Chat{ID: 99, Type: "user", Unread: 3}},
		{"group", &tg.PeerChat{ChatID: 7}, rpc.Chat{ID: -7, Type: "group", Title: "Family", Unread: 3}},
		// A channel object carries the flags, so a dialog reads them; a basic group
		// and a user have none, and their record says the flags are unknown.
		{"channel", &tg.PeerChannel{ChannelID: 8}, rpc.Chat{ID: -1000000000008, Type: "channel", Title: "News", Username: "news", Unread: 3, FlagsKnown: true}},
		{"supergroup", &tg.PeerChannel{ChannelID: 9}, rpc.Chat{ID: -1000000000009, Type: "supergroup", Title: "Chatty", Unread: 3, FlagsKnown: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toChat(dialogs.Elem{Dialog: &tg.Dialog{Peer: tt.peer, UnreadCount: 3}, Entities: entities()})
			// rpc.Chat is not comparable since it carries json.RawMessage.
			if !ok || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v (ok=%v), want %+v", got, ok, tt.want)
			}
		})
	}
}

func TestToChatSkipsFolders(t *testing.T) {
	if _, ok := toChat(dialogs.Elem{Dialog: &tg.DialogFolder{}, Entities: entities()}); ok {
		t.Error("a dialog folder is not a chat")
	}
}

func TestMapError(t *testing.T) {
	var rpcErr *rpc.Error

	if mapError(nil) != nil {
		t.Error("nil must stay nil")
	}
	if err := mapError(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("context errors must pass through, got %v", err)
	}
	original := rpc.NewError(rpc.CodeNotAuthorized, "x")
	if err := mapError(original); err != error(original) {
		t.Errorf("an *rpc.Error must pass through unchanged, got %v", err)
	}

	flood := tgerr.New(420, "FLOOD_WAIT_30")
	if err := mapError(flood); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeFloodWait {
		t.Fatalf("flood wait: %v", err)
	}
	if data, _ := rpcErr.Data.(map[string]any); data["retry_after_seconds"] != 30 {
		t.Errorf("retry_after_seconds lost: %v", rpcErr.Data)
	}

	if err := mapError(tgerr.New(401, "AUTH_KEY_UNREGISTERED")); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotAuthorized {
		t.Errorf("unauthorized: %v", err)
	}
	if err := mapError(tgerr.New(400, "PHONE_CODE_INVALID")); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeTelegram || rpcErr.Message != "PHONE_CODE_INVALID" {
		t.Errorf("telegram error: %v", err)
	}
	if err := mapError(errors.New("password=hunter2")); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeTelegram || rpcErr.Message != "telegram request failed: unexpected response" {
		t.Errorf("unknown error: %v", err)
	}
}

func TestDelivery(t *testing.T) {
	tests := []struct {
		name string
		sent *tg.AuthSentCode
		want string
	}{
		{"nil", nil, rpc.DeliveryUnsent},
		{"app", &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{}}, rpc.DeliveryApp},
		{"sms", &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeSMS{}}, rpc.DeliverySMS},
		{"call", &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeCall{}}, rpc.DeliveryCall},
		{"flash call", &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeFlashCall{}}, rpc.DeliveryCall},
		{"email", &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeEmailCode{}}, rpc.DeliveryEmail},
		{"unknown kind", &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeFragmentSMS{}}, rpc.DeliveryOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := delivery(tt.sent); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCodePrompt(t *testing.T) {
	sent := &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeApp{}}
	sent.SetNextType(&tg.AuthCodeTypeSMS{})
	sent.SetTimeout(60)
	got := codePrompt(sent)
	want := rpc.CodePrompt{Delivery: rpc.DeliveryApp, NextDelivery: rpc.DeliverySMS, ResendAfterSeconds: 60}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	// No next type and no timeout: nothing to offer.
	if got := codePrompt(&tg.AuthSentCode{Type: &tg.AuthSentCodeTypeSMS{}}); got.NextDelivery != "" || got.ResendAfterSeconds != 0 {
		t.Errorf("nothing must be offered, got %+v", got)
	}
	next := func(c tg.AuthCodeTypeClass) string {
		s := &tg.AuthSentCode{Type: &tg.AuthSentCodeTypeSMS{}}
		s.SetNextType(c)
		return codePrompt(s).NextDelivery
	}
	if next(&tg.AuthCodeTypeCall{}) != rpc.DeliveryCall || next(&tg.AuthCodeTypeFlashCall{}) != rpc.DeliveryCall {
		t.Error("call types must map to the call delivery")
	}
}

func TestDeviceConfig(t *testing.T) {
	if got := deviceConfig("").DeviceModel; got != DefaultDeviceModel {
		t.Errorf("default device model: %q", got)
	}
	if got := deviceConfig("MacBook Pro M1 BRN").DeviceModel; got != "MacBook Pro M1 BRN" {
		t.Errorf("configured device model: %q", got)
	}
}
