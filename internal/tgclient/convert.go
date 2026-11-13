package tgclient

import (
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

// channelIDBase turns a channel id into the Bot API form: -1000000000000 - id.
const channelIDBase = 1_000_000_000_000

// toChat converts a dialog; it reports false for entries that are not plain
// dialogs (for example folders).
func toChat(e dialogs.Elem) (rpc.Chat, bool) {
	d, ok := e.Dialog.(*tg.Dialog)
	if !ok {
		return rpc.Chat{}, false
	}
	chat := rpc.Chat{Unread: d.UnreadCount}
	switch p := d.Peer.(type) {
	case *tg.PeerUser:
		chat.ID, chat.Type = p.UserID, "user"
		if u, ok := e.Entities.User(p.UserID); ok {
			chat.Title = displayName(u)
			chat.Username, _ = u.GetUsername()
			if u.Bot {
				chat.Type = "bot"
			}
		}
	case *tg.PeerChat:
		chat.ID, chat.Type = -p.ChatID, "group"
		if c, ok := e.Entities.Chat(p.ChatID); ok {
			chat.Title = c.Title
		}
	case *tg.PeerChannel:
		chat.ID, chat.Type = -channelIDBase-p.ChannelID, "channel"
		if c, ok := e.Entities.Channel(p.ChannelID); ok {
			chat.Title = c.Title
			chat.Username, _ = c.GetUsername()
			if c.Megagroup {
				chat.Type = "supergroup"
			}
		}
	default:
		return rpc.Chat{}, false
	}
	return chat, true
}
