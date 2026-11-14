package tgclient

import (
	"strings"
	"time"
	"unicode"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// toMessage converts a message of a chat. fallback is who a message without an
// author is attributed to: the channel itself for channel posts.
func toMessage(chatID int64, msg tg.NotEmptyMessage, ent peer.Entities, fallback *rpc.Sender) (rpc.Message, bool) {
	switch m := msg.(type) {
	case *tg.Message:
		text := m.Message
		if poll, ok := m.Media.(*tg.MessageMediaPoll); ok && text == "" {
			text = poll.Poll.Question.Text
		}
		out := rpc.Message{
			ID:        m.ID,
			ChatID:    chatID,
			Date:      formatDate(m.Date),
			Text:      text,
			Out:       m.Out,
			Edited:    hasEditDate(m),
			Pinned:    m.Pinned,
			MediaType: mediaType(m.Media),
			Sender:    senderOf(m.FromID, ent, fallback),
		}
		if h, ok := m.GetReplyTo(); ok {
			out.ReplyTo, out.TopicID = replyInfo(h)
		}
		return out, true
	case *tg.MessageService:
		out := rpc.Message{
			ID:      m.ID,
			ChatID:  chatID,
			Date:    formatDate(m.Date),
			Out:     m.Out,
			Service: serviceKind(m.Action),
			Sender:  senderOf(m.FromID, ent, fallback),
		}
		if h, ok := m.GetReplyTo(); ok {
			out.ReplyTo, out.TopicID = replyInfo(h)
		}
		return out, true
	}
	return rpc.Message{}, false
}

func formatDate(unix int) string {
	return time.Unix(int64(unix), 0).UTC().Format(time.RFC3339)
}

func hasEditDate(m *tg.Message) bool {
	_, ok := m.GetEditDate()
	return ok
}

// replyInfo returns the replied message and, in a forum, the topic.
func replyInfo(h tg.MessageReplyHeaderClass) (replyTo, topic int) {
	header, ok := h.(*tg.MessageReplyHeader)
	if !ok {
		return 0, 0
	}
	replyTo = header.ReplyToMsgID
	if header.ForumTopic {
		if top, ok := header.GetReplyToTopID(); ok {
			return replyTo, top
		}
		// A message sent straight into a topic replies to the topic's first message.
		return 0, replyTo
	}
	return replyTo, 0
}

// senderOf names the author of a message from the entities that came with it.
func senderOf(from tg.PeerClass, ent peer.Entities, fallback *rpc.Sender) *rpc.Sender {
	switch p := from.(type) {
	case *tg.PeerUser:
		s := &rpc.Sender{ID: p.UserID}
		if u, ok := ent.User(p.UserID); ok {
			s.Name = displayName(u)
			s.Username, _ = u.GetUsername()
		}
		return s
	case *tg.PeerChannel:
		s := &rpc.Sender{ID: -1_000_000_000_000 - p.ChannelID}
		if c, ok := ent.Channel(p.ChannelID); ok {
			s.Name = c.Title
			s.Username, _ = c.GetUsername()
		}
		return s
	case *tg.PeerChat:
		s := &rpc.Sender{ID: -p.ChatID}
		if c, ok := ent.Chat(p.ChatID); ok {
			s.Name = c.Title
		}
		return s
	}
	return fallback
}

// mediaType names the kind of an attachment, empty for plain text.
func mediaType(media tg.MessageMediaClass) string {
	switch m := media.(type) {
	case nil, *tg.MessageMediaEmpty:
		return ""
	case *tg.MessageMediaPhoto:
		return "photo"
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return "document"
		}
		for _, attr := range doc.Attributes {
			switch a := attr.(type) {
			case *tg.DocumentAttributeVideo:
				if a.RoundMessage {
					return "video_note"
				}
				return "video"
			case *tg.DocumentAttributeAudio:
				if a.Voice {
					return "voice"
				}
				return "audio"
			case *tg.DocumentAttributeSticker:
				return "sticker"
			case *tg.DocumentAttributeAnimated:
				return "gif"
			}
		}
		return "document"
	case *tg.MessageMediaPoll:
		return "poll"
	case *tg.MessageMediaGeo, *tg.MessageMediaGeoLive, *tg.MessageMediaVenue:
		return "location"
	case *tg.MessageMediaContact:
		return "contact"
	case *tg.MessageMediaWebPage:
		return "webpage"
	case *tg.MessageMediaDice:
		return "dice"
	case *tg.MessageMediaGame:
		return "game"
	case *tg.MessageMediaInvoice:
		return "invoice"
	}
	return "other"
}

// serviceKind turns messageActionChatJoinedByLink into chat_joined_by_link.
func serviceKind(action tg.MessageActionClass) string {
	name := strings.TrimPrefix(action.TypeName(), "messageAction")
	if name == "" {
		return "service"
	}
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// chatIDOf is the Bot API id of the chat a ref was resolved to.
func chatIDOf(ref chatref.Ref, resolved int64) int64 {
	if ref.Kind == chatref.ID {
		return ref.ID
	}
	return resolved
}
