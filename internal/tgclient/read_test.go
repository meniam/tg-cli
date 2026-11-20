package tgclient

import (
	"errors"
	"testing"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/rpc"
)

func TestCursor(t *testing.T) {
	cursor := encodeCursor(-1001234567890, 4242)
	id, err := decodeCursor(cursor, -1001234567890)
	if err != nil || id != 4242 {
		t.Fatalf("round trip: %d, %v", id, err)
	}
	if id, err := decodeCursor("", 1); err != nil || id != 0 {
		t.Errorf("an empty cursor means the first page: %d, %v", id, err)
	}
	var rpcErr *rpc.Error
	if _, err := decodeCursor(cursor, 5); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams {
		t.Errorf("a cursor of another chat must be rejected: %v", err)
	}
	for _, bad := range []string{"%%%", "bm90LWpzb24", "e30", "eyJ2IjoyLCJjIjoxLCJpIjo1fQ"} {
		if _, err := decodeCursor(bad, 1); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams {
			t.Errorf("garbage %q must be rejected: %v", bad, err)
		}
	}
}

func TestToMessageText(t *testing.T) {
	ent := peer.NewEntities(
		map[int64]*tg.User{7: user(7, "Ann", "Lee", "ann", false)}, nil, nil)
	m := &tg.Message{ID: 10, Date: 1_700_000_000, Message: "hello", FromID: &tg.PeerUser{UserID: 7}, Pinned: true}
	m.SetEditDate(1_700_000_100)
	m.SetReplyTo(&tg.MessageReplyHeader{ReplyToMsgID: 9})

	got, ok := toMessage(77, m, ent, nil)
	if !ok {
		t.Fatal("a message must convert")
	}
	want := rpc.Message{
		ID: 10, ChatID: 77, Date: "2023-11-14T22:13:20Z", Text: "hello", ReplyTo: 9, Edited: true,
		Sender: &rpc.Sender{ID: 7, Name: "Ann Lee", Username: "ann"},
	}
	if got.ID != want.ID || got.ChatID != want.ChatID || got.Date != want.Date || got.Text != want.Text ||
		got.ReplyTo != want.ReplyTo || got.Edited != want.Edited || !got.Pinned || got.Sender == nil || *got.Sender != *want.Sender {
		t.Errorf("got %+v (sender %+v), want %+v", got, got.Sender, want)
	}
}

func TestToMessageChannelPostIsAttributedToTheChannel(t *testing.T) {
	fallback := &rpc.Sender{ID: -1001234567890, Name: "News", Username: "news"}
	got, _ := toMessage(-1001234567890, &tg.Message{ID: 1, Date: 1, Message: "post"}, peer.Entities{}, fallback)
	if got.Sender == nil || got.Sender.Name != "News" {
		t.Errorf("a post without an author belongs to the channel: %+v", got.Sender)
	}
}

func TestToMessageForumTopic(t *testing.T) {
	topicRoot := &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 500}
	m := &tg.Message{ID: 501}
	m.SetReplyTo(topicRoot)
	if got, _ := toMessage(1, m, peer.Entities{}, nil); got.TopicID != 500 || got.ReplyTo != 0 {
		t.Errorf("a message straight in a topic: %+v", got)
	}
	reply := &tg.MessageReplyHeader{ForumTopic: true, ReplyToMsgID: 501}
	reply.SetReplyToTopID(500)
	m = &tg.Message{ID: 502}
	m.SetReplyTo(reply)
	if got, _ := toMessage(1, m, peer.Entities{}, nil); got.TopicID != 500 || got.ReplyTo != 501 {
		t.Errorf("a reply inside a topic: %+v", got)
	}
	plain := &tg.MessageReplyHeader{ReplyToMsgID: 3}
	m = &tg.Message{ID: 503}
	m.SetReplyTo(plain)
	if got, _ := toMessage(1, m, peer.Entities{}, nil); got.TopicID != 0 || got.ReplyTo != 3 {
		t.Errorf("a plain reply: %+v", got)
	}
}

func TestToMessageService(t *testing.T) {
	m := &tg.MessageService{ID: 5, Date: 1, Action: &tg.MessageActionChatJoinedByLink{}}
	got, ok := toMessage(1, m, peer.Entities{}, nil)
	if !ok || got.Service != "chat_joined_by_link" || got.Text != "" {
		t.Errorf("service message: %+v", got)
	}
}

func TestToMessageSkipsEmpty(t *testing.T) {
	// An empty message is not a NotEmptyMessage, so nothing reaches toMessage.
	if _, ok := toMessage(1, nil, peer.Entities{}, nil); ok {
		t.Error("nil must not convert")
	}
}

func TestMediaType(t *testing.T) {
	doc := func(attrs ...tg.DocumentAttributeClass) tg.MessageMediaClass {
		return &tg.MessageMediaDocument{Document: &tg.Document{Attributes: attrs}}
	}
	tests := []struct {
		name  string
		media tg.MessageMediaClass
		want  string
	}{
		{"none", nil, ""},
		{"empty", &tg.MessageMediaEmpty{}, ""},
		{"photo", &tg.MessageMediaPhoto{}, "photo"},
		{"video", doc(&tg.DocumentAttributeVideo{}), "video"},
		{"video note", doc(&tg.DocumentAttributeVideo{RoundMessage: true}), "video_note"},
		{"voice", doc(&tg.DocumentAttributeAudio{Voice: true}), "voice"},
		{"audio", doc(&tg.DocumentAttributeAudio{}), "audio"},
		{"sticker", doc(&tg.DocumentAttributeSticker{}), "sticker"},
		{"gif", doc(&tg.DocumentAttributeAnimated{}), "gif"},
		{"file", doc(&tg.DocumentAttributeFilename{FileName: "a.zip"}), "document"},
		{"poll", &tg.MessageMediaPoll{}, "poll"},
		{"geo", &tg.MessageMediaGeo{}, "location"},
		{"contact", &tg.MessageMediaContact{}, "contact"},
		{"webpage", &tg.MessageMediaWebPage{}, "webpage"},
		{"unknown document", &tg.MessageMediaDocument{}, "document"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mediaType(tt.media); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSenderOfKinds(t *testing.T) {
	ent := peer.NewEntities(nil, map[int64]*tg.Chat{4: {ID: 4, Title: "Family"}},
		map[int64]*tg.Channel{9: channel(9, "News", "news", true, false)})
	if s := senderOf(&tg.PeerChat{ChatID: 4}, ent, nil); s.ID != -4 || s.Name != "Family" {
		t.Errorf("group sender: %+v", s)
	}
	if s := senderOf(&tg.PeerChannel{ChannelID: 9}, ent, nil); s.ID != -1000000000009 || s.Name != "News" || s.Username != "news" {
		t.Errorf("channel sender: %+v", s)
	}
	if s := senderOf(&tg.PeerUser{UserID: 1}, peer.Entities{}, nil); s.ID != 1 || s.Name != "" {
		t.Errorf("an author missing from the entities keeps its id: %+v", s)
	}
	fb := &rpc.Sender{ID: 5}
	if senderOf(nil, peer.Entities{}, fb) != fb {
		t.Error("no author means the fallback")
	}
}

func TestIsNotFound(t *testing.T) {
	for _, err := range []error{
		&peers.PeerNotFoundError{Peer: &tg.PeerUser{UserID: 1}},
		&peers.PhoneNotFoundError{Phone: "+1555"},
		tgerr.New(400, "USERNAME_NOT_OCCUPIED"),
		tgerr.New(400, "CHANNEL_PRIVATE"),
		tgerr.New(400, "INVITE_HASH_EXPIRED"),
	} {
		if !isNotFound(err) {
			t.Errorf("%v must count as not found", err)
		}
		var e *rpc.Error
		if m := mapError(err); !errors.As(m, &e) || e.Code != rpc.CodeNotFound {
			t.Errorf("mapError(%v) = %v, want a not found error", err, m)
		}
	}
	if !isNotFound(errors.New("got empty result for InputUser{UserID:1 AccessHash:0}")) {
		t.Error("a user the session never saw must count as not found")
	}
	if isNotFound(tgerr.New(400, "PHONE_CODE_INVALID")) || isNotFound(errors.New("boom")) {
		t.Error("other errors must not count as not found")
	}
}

func TestToInviteInfo(t *testing.T) {
	invite := &tg.ChatInvite{Title: "Team", ParticipantsCount: 42, Megagroup: true}
	invite.SetAbout("About us")
	got := toInviteInfo(invite)
	if got.Title != "Team" || got.Type != "supergroup" || got.Members != 42 || got.About != "About us" || got.AlreadyMember {
		t.Errorf("a supergroup invite: %+v", got)
	}
	if got := toInviteInfo(&tg.ChatInvite{Title: "News", Broadcast: true, RequestNeeded: true}); got.Type != "channel" || !got.RequestNeeded {
		t.Errorf("a channel invite: %+v", got)
	}
	if got := toInviteInfo(&tg.ChatInvite{Title: "Plain"}); got.Type != "group" {
		t.Errorf("a basic group invite: %+v", got)
	}
	already := &tg.ChatInviteAlready{Chat: &tg.Chat{Title: "Mine", ParticipantsCount: 3}}
	if got := toInviteInfo(already); !got.AlreadyMember || got.Title != "Mine" || got.Type != "group" || got.Members != 3 {
		t.Errorf("an invite to a chat the account is in: %+v", got)
	}
	if got := toInviteInfo(&tg.ChatInvitePeek{}); got.Type != "unknown" {
		t.Errorf("a peek: %+v", got)
	}
}

func TestServiceKind(t *testing.T) {
	for action, want := range map[tg.MessageActionClass]string{
		&tg.MessageActionChatCreate{}:       "chat_create",
		&tg.MessageActionPinMessage{}:       "pin_message",
		&tg.MessageActionChatJoinedByLink{}: "chat_joined_by_link",
	} {
		if got := serviceKind(action); got != want {
			t.Errorf("%s: got %q, want %q", action.TypeName(), got, want)
		}
	}
}
