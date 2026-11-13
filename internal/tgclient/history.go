package tgclient

import (
	"context"
	"encoding/json"

	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// telegramPageSize is the most messages Telegram returns for one request.
const telegramPageSize = 100

// History reads a chat from the newest message to the oldest. It stops at the
// limit, at Since, or when the encoded page would pass MaxBytes; NextCursor
// then continues after the last message of the page.
//
// The call only reads: it never marks anything as read.
func (c *Client) History(ctx context.Context, ref chatref.Ref, q rpc.HistoryQuery) (rpc.MessagesResult, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	chat := toChatOf(p)
	offsetID, err := decodeCursor(q.Cursor, chat.ID)
	if err != nil {
		return rpc.MessagesResult{}, err
	}

	// One more than asked: seeing it tells whether a next page exists.
	batch := min(q.Limit+1, telegramPageSize)
	api := c.tg.API()
	var it *messages.Iterator
	if q.Topic != 0 {
		it = query.Messages(api).GetReplies(p.InputPeer()).MsgID(q.Topic).BatchSize(batch).Iter()
	} else {
		it = query.Messages(api).GetHistory(p.InputPeer()).BatchSize(batch).Iter()
	}
	if offsetID != 0 {
		it = it.OffsetID(offsetID)
	} else if !q.Until.IsZero() {
		// Only the first page is cut by date: later pages continue by id.
		it = it.OffsetDate(int(q.Until.Unix()))
	}

	fallback := &rpc.Sender{ID: chat.ID, Name: chat.Title, Username: chat.Username}
	res := rpc.MessagesResult{Messages: []rpc.Message{}}
	size := 0
	for it.Next(ctx) {
		elem := it.Value()
		msg, ok := toMessage(chat.ID, elem.Msg, elem.Entities, fallback)
		if !ok {
			continue
		}
		if !q.Since.IsZero() && elem.Msg.GetDate() < int(q.Since.Unix()) {
			return res, nil // older messages only follow: nothing more to return
		}
		encoded, err := json.Marshal(msg)
		if err != nil {
			return rpc.MessagesResult{}, err
		}
		tooBig := q.MaxBytes > 0 && size+len(encoded)+1 > q.MaxBytes && len(res.Messages) > 0
		if len(res.Messages) >= q.Limit || tooBig {
			res.NextCursor = encodeCursor(chat.ID, res.Messages[len(res.Messages)-1].ID)
			return res, nil
		}
		res.Messages = append(res.Messages, msg)
		size += len(encoded) + 1
	}
	if err := it.Err(); err != nil {
		return rpc.MessagesResult{}, mapError(err)
	}
	return res, nil
}

// Message returns one message of a chat by its id.
func (c *Client) Message(ctx context.Context, ref chatref.Ref, id int) (rpc.Message, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.Message{}, err
	}
	chat := toChatOf(p)
	elems, err := query.Messages(c.tg.API()).GetMessagesWithEntities(ctx, p.InputPeer(), id)
	if err != nil {
		if isNotFound(err) {
			return rpc.Message{}, messageNotFound()
		}
		return rpc.Message{}, mapError(err)
	}
	fallback := &rpc.Sender{ID: chat.ID, Name: chat.Title, Username: chat.Username}
	for _, elem := range elems {
		if elem.Msg == nil {
			continue
		}
		if msg, ok := toMessage(chat.ID, elem.Msg, elem.Entities, fallback); ok {
			return msg, nil
		}
	}
	return rpc.Message{}, messageNotFound()
}

func messageNotFound() error {
	return rpc.NewError(rpc.CodeNotFound, "message not found: it does not exist or was deleted")
}

// Pinned returns up to limit pinned messages of a chat, newest first.
func (c *Client) Pinned(ctx context.Context, ref chatref.Ref, limit int) (rpc.MessagesResult, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	chat := toChatOf(p)
	it := query.Messages(c.tg.API()).Search(p.InputPeer()).
		Filter(&tg.InputMessagesFilterPinned{}).BatchSize(min(limit, telegramPageSize)).Iter()

	fallback := &rpc.Sender{ID: chat.ID, Name: chat.Title, Username: chat.Username}
	res := rpc.MessagesResult{Messages: []rpc.Message{}}
	for len(res.Messages) < limit && it.Next(ctx) {
		elem := it.Value()
		if msg, ok := toMessage(chat.ID, elem.Msg, elem.Entities, fallback); ok {
			res.Messages = append(res.Messages, msg)
		}
	}
	if err := it.Err(); err != nil {
		return rpc.MessagesResult{}, mapError(err)
	}
	return res, nil
}
