package tgclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

type managerHandler struct{ client *Client }

func (h managerHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	return h.client.updates.Handle(ctx, u)
}

func botPeerID(value tg.PeerClass) int64 {
	var id constant.TDLibPeerID
	switch p := value.(type) {
	case *tg.PeerUser:
		id.User(p.UserID)
	case *tg.PeerChat:
		id.Chat(p.ChatID)
	case *tg.PeerChannel:
		id.Channel(p.ChannelID)
	}
	return int64(id)
}

func eventTime() string { return time.Now().UTC().Format(time.RFC3339) }

func (c *Client) event(typ string, chatID *int64, data any, key string, when string) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if when == "" {
		when = eventTime()
	}
	c.emit(rpc.Event{Type: typ, ChatID: chatID, Time: when, Data: raw, DedupKey: key})
	return nil
}

func (c *Client) messageEvent(typ string, entities tg.Entities, input tg.MessageClass) error {
	msg, ok := input.(tg.NotEmptyMessage)
	if !ok {
		return nil
	}
	chatID := botPeerID(msg.GetPeerID())
	if chatID == 0 {
		return nil
	}
	fallback := &rpc.Sender{ID: chatID}
	switch p := msg.GetPeerID().(type) {
	case *tg.PeerChannel:
		if channel := entities.Channels[p.ChannelID]; channel != nil {
			fallback.Name = channel.Title
			fallback.Username, _ = channel.GetUsername()
		}
	case *tg.PeerChat:
		if chat := entities.Chats[p.ChatID]; chat != nil {
			fallback.Name = chat.Title
		}
	}
	converted, ok := toMessage(chatID, msg, peer.EntitiesFromUpdate(entities), fallback)
	if !ok {
		return nil
	}
	key := fmt.Sprintf("new:%d:%d", chatID, converted.ID)
	when := converted.Date
	if typ == rpc.EventMessageEdit {
		date := msg.GetDate()
		if m, ok := msg.(*tg.Message); ok {
			if edited, has := m.GetEditDate(); has {
				date = edited
			}
		}
		key = fmt.Sprintf("edit:%d:%d:%d", chatID, converted.ID, date)
		when = formatDate(date)
	}
	return c.event(typ, &chatID, converted, key, when)
}

func (c *Client) deleteEvent(channelID int64, ids []int) error {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	var chatID *int64
	value := int64(0)
	if channelID != 0 {
		var id constant.TDLibPeerID
		id.Channel(channelID)
		value = int64(id)
		chatID = &value
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	key := fmt.Sprintf("del:%d:%s", value, strings.Join(parts, ","))
	return c.event(rpc.EventMessageDelete, chatID, rpc.MessageDeleteData{ChatID: chatID, IDs: ids}, key, "")
}

func reactionLabel(value tg.ReactionClass) string {
	switch r := value.(type) {
	case *tg.ReactionEmoji:
		return r.Emoticon
	case *tg.ReactionCustomEmoji:
		return fmt.Sprintf("custom:%d", r.DocumentID)
	case *tg.ReactionPaid:
		return "paid"
	case nil:
		return ""
	default:
		return value.TypeName()
	}
}

func (c *Client) reactionEvent(update *tg.UpdateMessageReactions) error {
	chatID := botPeerID(update.Peer)
	if chatID == 0 {
		return nil
	}
	counts := make([]rpc.ReactionCount, 0, len(update.Reactions.Results))
	for _, count := range update.Reactions.Results {
		counts = append(counts, rpc.ReactionCount{Emoji: reactionLabel(count.Reaction), Count: count.Count})
	}
	slices.SortFunc(counts, func(a, b rpc.ReactionCount) int { return strings.Compare(a.Emoji, b.Emoji) })
	raw, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	key := fmt.Sprintf("react:%d:%d:%s", chatID, update.MsgID, hex.EncodeToString(hash[:]))
	return c.event(rpc.EventReaction, &chatID, rpc.ReactionData{ChatID: chatID, MessageID: update.MsgID, Reactions: counts}, key, "")
}

func (c *Client) gapEvent(channelID int64, reason string) {
	var chatID *int64
	if channelID != 0 {
		var id constant.TDLibPeerID
		id.Channel(channelID)
		value := int64(id)
		chatID = &value
	}
	_ = c.event(rpc.EventGap, chatID, rpc.GapData{ChatID: chatID, Reason: reason}, "", "")
}

func (c *Client) updateDispatcher() tg.UpdateDispatcher {
	d := tg.NewUpdateDispatcher()
	d.OnTranscribedAudio(func(_ context.Context, _ tg.Entities, u *tg.UpdateTranscribedAudio) error {
		c.transcribedAudio(u)
		return nil
	})
	d.OnAnyNewMessage(func(_ context.Context, e tg.Entities, m tg.MessageClass) error {
		return c.messageEvent(rpc.EventMessageNew, e, m)
	})
	d.OnAnyEditMessage(func(_ context.Context, e tg.Entities, m tg.MessageClass) error {
		return c.messageEvent(rpc.EventMessageEdit, e, m)
	})
	d.OnAnyDeleteMessages(func(_ context.Context, _ tg.Entities, channelID int64, ids []int) error {
		return c.deleteEvent(channelID, ids)
	})
	d.OnMessageReactions(func(_ context.Context, _ tg.Entities, u *tg.UpdateMessageReactions) error { return c.reactionEvent(u) })
	d.OnAnyReadInbox(func(_ context.Context, _ tg.Entities, r tg.AnyRead) error {
		chatID := botPeerID(r.Peer)
		if chatID == 0 {
			return nil
		}
		return c.event(rpc.EventRead, &chatID, rpc.ReadData{ChatID: chatID, MaxID: r.MaxID, Outbox: false}, "", "")
	})
	d.OnAnyReadOutbox(func(_ context.Context, _ tg.Entities, r tg.AnyRead) error {
		chatID := botPeerID(r.Peer)
		if chatID == 0 {
			return nil
		}
		return c.event(rpc.EventRead, &chatID, rpc.ReadData{ChatID: chatID, MaxID: r.MaxID, Outbox: true}, "", "")
	})
	d.OnAnyTyping(func(_ context.Context, _ tg.Entities, u tg.AnyTyping) error {
		chatID := botPeerID(u.Peer)
		if chatID == 0 {
			return nil
		}
		action := ""
		if u.Action != nil {
			action = u.Action.TypeName()
		}
		return c.event(rpc.EventTyping, &chatID, rpc.TypingData{ChatID: chatID, UserID: botPeerID(u.From), Action: action}, "", "")
	})
	d.OnUserStatus(func(_ context.Context, _ tg.Entities, u *tg.UpdateUserStatus) error {
		return c.event(rpc.EventStatus, nil, rpc.StatusData{UserID: u.UserID, Online: statusOnline(u.Status)}, "", "")
	})
	return d
}

func statusOnline(status tg.UserStatusClass) bool { _, ok := status.(*tg.UserStatusOnline); return ok }

func (c *Client) updateConfig() updates.Config {
	cfg := updates.Config{Handler: catchupHandler{client: c, next: c.updateDispatcher()}, AccessHasher: c.mgr, StopOnApplyError: true, MaxChannelDifferenceConcurrency: 2,
		BeforeRun:             updates.BeforeRunBuffer,
		OnChannelTooLong:      func(id int64) { c.gapEvent(id, "channel_too_long") },
		OnChannelGap:          func(gap updates.ChannelGap) { c.gapEvent(gap.ChannelID, "channel_gap") },
		OnChannelInaccessible: func(id int64) { c.gapEvent(id, "channel_inaccessible") },
		OnTooLong:             func() { c.gapEvent(0, "too_long") },
	}
	if c.stateKV != nil {
		cfg.Storage = &updateStateStorage{kv: c.stateKV}
		hasher := persistentHasher{delegate: c.mgr, kv: c.stateKV}
		cfg.AccessHasher = hasher
		cfg.UserAccessHasher = hasher
	}
	return cfg
}
