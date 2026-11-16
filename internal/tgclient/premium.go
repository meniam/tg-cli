package tgclient

import (
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func premiumError(e *tgerr.Error) error {
	var reason string
	switch e.Type {
	case "PREMIUM_ACCOUNT_REQUIRED":
		reason = "premium_required"
	case "PRIVACY_PREMIUM_REQUIRED":
		reason = "recipient_requires_premium"
	default:
		return nil
	}
	return &rpc.Error{Code: rpc.CodeTelegram, Message: e.Type, Data: map[string]any{"reason": reason}}
}

func customEmojiCount(entities []tg.MessageEntityClass) int {
	count := 0
	for _, entity := range entities {
		if _, ok := entity.(*tg.MessageEntityCustomEmoji); ok {
			count++
		}
	}
	return count
}

func premiumDropped(updates tg.UpdatesClass, id int, requested []tg.MessageEntityClass) []string {
	want := customEmojiCount(requested)
	if want == 0 {
		return nil
	}
	var entities []tg.MessageEntityClass
	var present bool
	inspect := func(message tg.MessageClass) {
		if message.GetID() != id {
			return
		}
		if m, ok := message.(interface {
			GetEntities() ([]tg.MessageEntityClass, bool)
		}); ok {
			entities, present = m.GetEntities()
		}
	}
	var changes []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.UpdateShortSentMessage:
		entities, present = u.GetEntities()
	case *tg.UpdateShort:
		changes = []tg.UpdateClass{u.Update}
	case *tg.Updates:
		changes = u.Updates
	case *tg.UpdatesCombined:
		changes = u.Updates
	}
	for _, change := range changes {
		switch v := change.(type) {
		case *tg.UpdateNewScheduledMessage:
			inspect(v.Message)
		case *tg.UpdateNewMessage:
			inspect(v.Message)
		case *tg.UpdateNewChannelMessage:
			inspect(v.Message)
		case *tg.UpdateEditMessage:
			inspect(v.Message)
		case *tg.UpdateEditChannelMessage:
			inspect(v.Message)
		}
	}
	if present && customEmojiCount(entities) < want {
		return []string{"custom_emoji"}
	}
	return nil
}
