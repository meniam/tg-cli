package tgclient

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

const maxMessageRunes = 4096

func validateWriteText(text string) error {
	if text == "" {
		return rpc.NewError(rpc.CodeInvalidParams, "message text must not be empty")
	}
	if utf8.RuneCountInString(text) > maxMessageRunes {
		return rpc.NewError(rpc.CodeInvalidParams, "message text must be at most 4096 characters")
	}
	return nil
}

func validateMessageIDs(ids []int) error {
	if len(ids) == 0 {
		return rpc.NewError(rpc.CodeInvalidParams, "at least one message id is required")
	}
	for _, id := range ids {
		if id <= 0 {
			return rpc.NewError(rpc.CodeInvalidParams, "message ids must be positive")
		}
	}
	return nil
}

func (c *Client) Send(ctx context.Context, ref chatref.Ref, p rpc.SendParams) (rpc.SendResult, error) {
	if p.DryRun {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	prepared, err := prepareWriteText(p.Text, p.Parse, p.Entities, c.mentionResolver(ctx))
	if err != nil {
		return rpc.SendResult{}, err
	}
	if p.ReplyTo < 0 || p.Topic < 0 {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "reply and topic ids must not be negative")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return sendPreparedText(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p, prepared)
}

func (c *Client) Edit(ctx context.Context, ref chatref.Ref, p rpc.EditParams) (rpc.SendResult, error) {
	if p.DryRun {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	prepared, err := prepareWriteText(p.Text, p.Parse, p.Entities, c.mentionResolver(ctx))
	if err != nil {
		return rpc.SendResult{}, err
	}
	if p.ID <= 0 {
		return rpc.SendResult{}, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return editPreparedText(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p, prepared)
}

func (c *Client) mentionResolver(ctx context.Context) userResolver {
	return func(id int64) (tg.InputUserClass, error) {
		peer, err := c.resolve(ctx, chatref.Ref{Kind: chatref.ID, ID: id})
		if err != nil {
			var rpcErr *rpc.Error
			if errors.As(err, &rpcErr) && rpcErr.Code != rpc.CodeNotFound {
				return nil, err
			}
			return nil, rpc.NewError(rpc.CodeInvalidParams, "unknown user id for this session")
		}
		user, ok := peer.(peers.User)
		if !ok {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "unknown user id for this session")
		}
		return user.InputUser(), nil
	}
}

func (c *Client) Delete(ctx context.Context, ref chatref.Ref, p rpc.DeleteParams) (rpc.DeleteResult, error) {
	if p.DryRun {
		return rpc.DeleteResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if err := validateMessageIDs(p.IDs); err != nil {
		return rpc.DeleteResult{}, err
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.DeleteResult{}, err
	}
	return deleteMessages(ctx, c.tg.API(), resolved.InputPeer(), int64(resolved.TDLibPeerID()), p)
}

func (c *Client) Forward(ctx context.Context, from, to chatref.Ref, p rpc.ForwardParams) (rpc.ForwardResult, error) {
	if p.DryRun {
		return rpc.ForwardResult{}, rpc.NewError(rpc.CodeInvalidParams, "dry-run must be handled by the daemon")
	}
	if err := validateMessageIDs(p.IDs); err != nil {
		return rpc.ForwardResult{}, err
	}
	source, err := c.resolve(ctx, from)
	if err != nil {
		return rpc.ForwardResult{}, err
	}
	destination, err := c.resolve(ctx, to)
	if err != nil {
		return rpc.ForwardResult{}, err
	}
	return forwardMessages(ctx, c.tg.API(), source.InputPeer(), destination.InputPeer(), int64(destination.TDLibPeerID()), p)
}

func sendText(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, chatID int64, p rpc.SendParams) (rpc.SendResult, error) {
	prepared, err := prepareWriteText(p.Text, p.Parse, p.Entities, nil)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return sendPreparedText(ctx, api, destination, chatID, p, prepared)
}

func sendPreparedText(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, chatID int64, p rpc.SendParams, prepared preparedText) (rpc.SendResult, error) {
	sender := message.NewSender(api)
	builder := sender.To(destination)
	if p.Silent {
		builder.Silent()
	}
	reply := p.ReplyTo
	if reply == 0 {
		reply = p.Topic
	}
	var updates tg.UpdatesClass
	var err error
	if p.Topic > 1 && p.ReplyTo > 0 && p.ReplyTo != p.Topic {
		randomID, randomErr := newRandomID()
		if randomErr != nil {
			return rpc.SendResult{}, randomErr
		}
		replyTo := &tg.InputReplyToMessage{ReplyToMsgID: p.ReplyTo}
		replyTo.SetTopMsgID(p.Topic)
		updates, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: destination, Message: prepared.text, Entities: prepared.entities, RichMessage: prepared.rich, Silent: p.Silent, ReplyTo: replyTo, RandomID: randomID})
	} else {
		if reply > 0 {
			builder.Reply(reply)
		}
		switch {
		case prepared.rich != nil:
			updates, err = builder.RichMessage(ctx, prepared.rich)
		case p.Parse != "" || p.Entities != nil:
			randomID, randomErr := newRandomID()
			if randomErr != nil {
				return rpc.SendResult{}, randomErr
			}
			var replyTo tg.InputReplyToClass
			if reply > 0 {
				replyTo = &tg.InputReplyToMessage{ReplyToMsgID: reply}
			}
			updates, err = api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{Peer: destination, Message: prepared.text, Entities: prepared.entities, Silent: p.Silent, ReplyTo: replyTo, RandomID: randomID})
		default:
			updates, err = builder.Text(ctx, p.Text)
		}
	}
	if err != nil {
		return rpc.SendResult{}, writeFormatError(err, prepared.rich != nil)
	}
	messages, err := writtenMessages(updates)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return rpc.SendResult{ChatID: chatID, ID: messages[0].ID, Date: messages[0].Date, PlainText: prepared.text, Rich: prepared.rich != nil}, nil
}

func editText(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, chatID int64, p rpc.EditParams) (rpc.SendResult, error) {
	prepared, err := prepareWriteText(p.Text, p.Parse, p.Entities, nil)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return editPreparedText(ctx, api, destination, chatID, p, prepared)
}

func editPreparedText(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, chatID int64, p rpc.EditParams, prepared preparedText) (rpc.SendResult, error) {
	var updates tg.UpdatesClass
	var err error
	switch {
	case prepared.rich != nil:
		updates, err = message.NewSender(api).To(destination).Edit(p.ID).RichMessage(ctx, prepared.rich)
	case p.Parse != "" || p.Entities != nil:
		updates, err = api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: destination, ID: p.ID, Message: prepared.text, Entities: prepared.entities})
	default:
		updates, err = message.NewSender(api).To(destination).Edit(p.ID).Text(ctx, p.Text)
	}
	if err != nil {
		return rpc.SendResult{}, writeFormatError(err, prepared.rich != nil)
	}
	messages, err := writtenMessages(updates)
	if err != nil {
		return rpc.SendResult{}, err
	}
	return rpc.SendResult{ChatID: chatID, ID: messages[0].ID, Date: messages[0].Date, PlainText: prepared.text, Rich: prepared.rich != nil}, nil
}

func writeFormatError(err error, rich bool) error {
	mapped := mapError(err)
	if rich {
		var rpcErr *rpc.Error
		if _, telegramError := tgerr.As(err); telegramError && errors.As(mapped, &rpcErr) && rpcErr.Code == rpc.CodeTelegram {
			return rpc.NewError(rpc.CodeTelegram, "Telegram rejected rich message")
		}
	}
	return mapped
}

func deleteMessages(ctx context.Context, api *tg.Client, destination tg.InputPeerClass, chatID int64, p rpc.DeleteParams) (rpc.DeleteResult, error) {
	if err := validateMessageIDs(p.IDs); err != nil {
		return rpc.DeleteResult{}, err
	}
	_, channel := peer.ToInputChannel(destination)
	if channel && !p.Revoke {
		return rpc.DeleteResult{}, rpc.NewError(rpc.CodeInvalidParams, "delete for me is not supported in channels")
	}
	sender := message.NewSender(api)
	var err error
	if p.Revoke {
		_, err = sender.To(destination).Revoke().Messages(ctx, p.IDs...)
	} else {
		_, err = sender.Delete().Messages(ctx, p.IDs...)
	}
	if err != nil {
		return rpc.DeleteResult{}, mapError(err)
	}
	return rpc.DeleteResult{ChatID: chatID, Deleted: append([]int(nil), p.IDs...)}, nil
}

func forwardMessages(ctx context.Context, api *tg.Client, source, destination tg.InputPeerClass, chatID int64, p rpc.ForwardParams) (rpc.ForwardResult, error) {
	if err := validateMessageIDs(p.IDs); err != nil {
		return rpc.ForwardResult{}, err
	}
	builder := message.NewSender(api).To(destination)
	if p.Silent {
		builder.Silent()
	}
	updates, err := builder.ForwardIDs(source, p.IDs[0], p.IDs[1:]...).Send(ctx)
	if err != nil {
		return rpc.ForwardResult{}, mapError(err)
	}
	messages, err := writtenMessages(updates)
	if err != nil {
		return rpc.ForwardResult{}, err
	}
	if len(messages) != len(p.IDs) {
		return rpc.ForwardResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram did not return all forwarded message ids")
	}
	return rpc.ForwardResult{ChatID: chatID, Messages: messages}, nil
}

func newRandomID() (int64, error) {
	for {
		var data [8]byte
		if _, err := rand.Read(data[:]); err != nil {
			return 0, fmt.Errorf("generate random id: %w", err)
		}
		if id := int64(binary.LittleEndian.Uint64(data[:])); id != 0 {
			return id, nil
		}
	}
}

func writtenMessages(updates tg.UpdatesClass) ([]rpc.ForwardedMessage, error) {
	var items []rpc.ForwardedMessage
	var fallback int
	var changes []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.UpdateShortSentMessage:
		items = append(items, datedMessage(u.ID, u.Date))
	case *tg.UpdateShortMessage:
		items = append(items, datedMessage(u.ID, u.Date))
	case *tg.UpdateShortChatMessage:
		items = append(items, datedMessage(u.ID, u.Date))
	case *tg.UpdateShort:
		fallback = u.Date
		changes = []tg.UpdateClass{u.Update}
	case *tg.Updates:
		fallback = u.Date
		changes = u.Updates
	case *tg.UpdatesCombined:
		fallback = u.Date
		changes = u.Updates
	}
	if len(changes) > 0 {
		ids := make([]int, 0)
		dates := make(map[int]int)
		for _, change := range changes {
			switch v := change.(type) {
			case *tg.UpdateMessageID:
				ids = append(ids, v.ID)
			case *tg.UpdateNewMessage:
				dates[v.Message.GetID()] = messageDate(v.Message)
			case *tg.UpdateNewChannelMessage:
				dates[v.Message.GetID()] = messageDate(v.Message)
			case *tg.UpdateEditMessage:
				dates[v.Message.GetID()] = messageDate(v.Message)
			case *tg.UpdateEditChannelMessage:
				dates[v.Message.GetID()] = messageDate(v.Message)
			}
		}
		if len(ids) == 0 {
			for _, change := range changes {
				switch v := change.(type) {
				case *tg.UpdateNewMessage:
					ids = append(ids, v.Message.GetID())
				case *tg.UpdateNewChannelMessage:
					ids = append(ids, v.Message.GetID())
				case *tg.UpdateEditMessage:
					ids = append(ids, v.Message.GetID())
				case *tg.UpdateEditChannelMessage:
					ids = append(ids, v.Message.GetID())
				}
			}
		}
		for _, id := range ids {
			date := dates[id]
			if date == 0 {
				date = fallback
			}
			items = append(items, datedMessage(id, date))
		}
	}
	if len(items) == 0 {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram did not return a message id")
	}
	for _, item := range items {
		if item.ID <= 0 || item.Date == "" {
			return nil, rpc.NewError(rpc.CodeTelegram, "Telegram did not return a message id and date")
		}
	}
	return items, nil
}

func datedMessage(id, date int) rpc.ForwardedMessage {
	if date <= 0 {
		return rpc.ForwardedMessage{ID: id}
	}
	return rpc.ForwardedMessage{ID: id, Date: time.Unix(int64(date), 0).UTC().Format(time.RFC3339)}
}

func messageDate(value tg.MessageClass) int {
	if dated, ok := value.(interface{ GetDate() int }); ok {
		return dated.GetDate()
	}
	return 0
}
