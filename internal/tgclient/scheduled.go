package tgclient

import (
	"context"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
	"sort"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"time"
)

func sendScheduledText(ctx context.Context, api *tg.Client, peer tg.InputPeerClass, chatID int64, p rpc.SendParams, text preparedText) (rpc.SendResult, error) {
	when, err := time.Parse(time.RFC3339, p.At)
	if err != nil {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "invalid schedule time")
	}
	random, err := newRandomID()
	if err != nil {
		return rpc.SendResult{}, err
	}
	req := &tg.MessagesSendMessageRequest{Peer: peer, Message: text.text, Entities: text.entities, RichMessage: text.rich, Silent: p.Silent, RandomID: random}
	reply := p.ReplyTo
	if reply == 0 {
		reply = p.Topic
	}
	if reply > 0 {
		r := &tg.InputReplyToMessage{ReplyToMsgID: reply}
		if p.Topic > 1 && p.ReplyTo > 0 && p.ReplyTo != p.Topic {
			r.SetTopMsgID(p.Topic)
		}
		req.ReplyTo = r
	}
	req.SetScheduleDate(int(when.Unix()))
	updates, err := api.MessagesSendMessage(ctx, req)
	if err != nil {
		return rpc.SendResult{}, mapScheduleError(err)
	}
	var changes []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		changes = u.Updates
	case *tg.UpdatesCombined:
		changes = u.Updates
	case *tg.UpdateShort:
		changes = []tg.UpdateClass{u.Update}
	}
	for _, change := range changes {
		if u, ok := change.(*tg.UpdateNewScheduledMessage); ok {
			if m, ok := u.Message.(*tg.Message); ok && m.ID > 0 && m.Date > 0 {
				return rpc.SendResult{ChatID: chatID, ID: m.ID, Date: formatDate(m.Date), Scheduled: true, PremiumDropped: premiumDropped(updates, m.ID, text.entities)}, nil
			}
		}
	}
	return rpc.SendResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram did not return the scheduled message")
}

func (c *Client) ScheduledList(ctx context.Context, ref chatref.Ref) (rpc.ScheduledResult, error) {
	r, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ScheduledResult{}, err
	}
	response, err := c.tg.API().MessagesGetScheduledHistory(ctx, &tg.MessagesGetScheduledHistoryRequest{Peer: r.InputPeer(), Hash: 0})
	if err != nil {
		return rpc.ScheduledResult{}, mapScheduleError(err)
	}
	modified, err := modifiedMessages(response)
	if err != nil {
		return rpc.ScheduledResult{}, err
	}
	id := int64(r.TDLibPeerID())
	items := convertMessages(modified, id, nil)
	sort.Slice(items, func(i, j int) bool { return items[i].Date < items[j].Date })
	return rpc.ScheduledResult{ChatID: id, Messages: items}, nil
}
func (c *Client) ScheduledDelete(ctx context.Context, ref chatref.Ref, ids []int) (rpc.ScheduledResult, error) {
	if err := validateMessageIDs(ids); err != nil {
		return rpc.ScheduledResult{}, err
	}
	r, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ScheduledResult{}, err
	}
	_, err = c.tg.API().MessagesDeleteScheduledMessages(ctx, &tg.MessagesDeleteScheduledMessagesRequest{Peer: r.InputPeer(), ID: ids})
	if err != nil {
		return rpc.ScheduledResult{}, mapScheduleError(err)
	}
	return rpc.ScheduledResult{ChatID: int64(r.TDLibPeerID()), Deleted: ids}, nil
}
func (c *Client) ScheduledSendNow(ctx context.Context, ref chatref.Ref, ids []int) (rpc.ScheduledResult, error) {
	if err := validateMessageIDs(ids); err != nil {
		return rpc.ScheduledResult{}, err
	}
	r, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.ScheduledResult{}, err
	}
	updates, err := c.tg.API().MessagesSendScheduledMessages(ctx, &tg.MessagesSendScheduledMessagesRequest{Peer: r.InputPeer(), ID: ids})
	if err != nil {
		return rpc.ScheduledResult{}, mapScheduleError(err)
	}
	var changes []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		changes = u.Updates
	case *tg.UpdatesCombined:
		changes = u.Updates
	case *tg.UpdateShort:
		changes = []tg.UpdateClass{u.Update}
	}
	out := rpc.ScheduledResult{ChatID: int64(r.TDLibPeerID()), Messages: []rpc.Message{}}
	for _, u := range changes {
		var m tg.MessageClass
		switch v := u.(type) {
		case *tg.UpdateNewMessage:
			m = v.Message
		case *tg.UpdateNewChannelMessage:
			m = v.Message
		}
		if m != nil {
			if v, ok := m.(tg.NotEmptyMessage); ok {
				if message, ok := toMessage(out.ChatID, v, peer.Entities{}, nil); ok {
					message.Out = true
					out.Messages = append(out.Messages, message)
				}
			}
		}
	}
	if len(out.Messages) == 0 {
		for _, change := range changes {
			if deleted, ok := change.(*tg.UpdateDeleteScheduledMessages); ok {
				ids, _ := deleted.GetSentMessages()
				for _, id := range ids {
					out.Messages = append(out.Messages, rpc.Message{ChatID: out.ChatID, ID: id, Out: true})
				}
			}
		}
	}
	return out, nil
}
