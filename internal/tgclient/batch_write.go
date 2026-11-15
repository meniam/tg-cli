package tgclient

import (
	"context"
	"errors"

	"github.com/gotd/td/tg"

	"tg-cli/internal/rpc"
)

func forwardBatches(ctx context.Context, api *tg.Client, source, destination tg.InputPeerClass, chatID int64, p rpc.ForwardParams) (rpc.ForwardResult, error) {
	if err := validateMessageIDs(p.IDs); err != nil {
		return rpc.ForwardResult{}, err
	}
	if p.IdempotencyKey != "" && p.AccountID <= 0 {
		return rpc.ForwardResult{}, errors.New("account id is required for idempotent writes")
	}
	result := rpc.ForwardResult{ChatID: chatID, Messages: []rpc.ForwardedMessage{}}
	var first, stopped error
	offset := 0
	for _, ids := range rpc.Chunks(p.IDs, 100) {
		if stopped != nil {
			for _, id := range ids {
				result.Errors = append(result.Errors, rpc.ItemErrorFrom(id, stopped))
			}
			continue
		}
		req := &tg.MessagesForwardMessagesRequest{FromPeer: source, ToPeer: destination, ID: ids, Silent: p.Silent, DropAuthor: p.Copy, DropMediaCaptions: p.NoCaptions, RandomID: make([]int64, len(ids))}
		var randomErr error
		for i := range ids {
			req.RandomID[i], randomErr = newRandomID()
			if randomErr != nil {
				break
			}
		}
		if randomErr != nil {
			if first == nil {
				first = randomErr
			}
			stopped = randomErr
			for _, id := range ids {
				result.Errors = append(result.Errors, rpc.ItemErrorFrom(id, randomErr))
			}
			continue
		}
		client := api
		if p.IdempotencyKey != "" {
			client = tg.NewClient(&idempotentInvoker{next: api.Invoker(), accountID: p.AccountID, chatID: chatID, key: p.IdempotencyKey, offset: uint64(offset)})
		}
		offset += len(ids)
		updates, err := client.MessagesForwardMessages(ctx, req)
		if err != nil {
			err = mapError(err)
			if first == nil {
				first = err
			}
			for _, id := range ids {
				result.Errors = append(result.Errors, rpc.ItemErrorFrom(id, err))
			}
			var e *rpc.Error
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &e) && (e.Code == rpc.CodeFloodWait || e.Code == rpc.CodeNotAuthorized)) {
				stopped = err
			}
			continue
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
		mapping := make(map[int64]int)
		for _, change := range changes {
			if u, ok := change.(*tg.UpdateMessageID); ok {
				mapping[u.RandomID] = u.ID
			}
		}
		// A response without correlation cannot safely identify successful source IDs.
		if len(mapping) == 0 {
			err = rpc.NewError(rpc.CodeTelegram, "Telegram did not return correlated message ids")
			if first == nil {
				first = err
			}
			for _, id := range ids {
				result.Errors = append(result.Errors, rpc.ItemErrorFrom(id, err))
			}
			continue
		}
		dates := make(map[int]int)
		fallback := 0
		switch u := updates.(type) {
		case *tg.Updates:
			fallback = u.Date
		case *tg.UpdatesCombined:
			fallback = u.Date
		case *tg.UpdateShort:
			fallback = u.Date
		}
		for _, change := range changes {
			switch v := change.(type) {
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
		for i, id := range ids {
			if target, ok := mapping[req.RandomID[i]]; ok {
				date := dates[target]
				if date == 0 {
					date = fallback
				}
				if target > 0 {
					m := datedMessage(target, date)
					m.SourceID = id
					result.Messages = append(result.Messages, m)
					continue
				}
				err = rpc.NewError(rpc.CodeTelegram, "Telegram did not return a message id and date")
			} else {
				err = rpc.NewError(rpc.CodeNotFound, "message not found or not returned by Telegram")
			}
			if first == nil {
				first = err
			}
			result.Errors = append(result.Errors, rpc.ItemErrorFrom(id, err))
		}
	}
	if len(result.Messages) == 0 && first != nil {
		return rpc.ForwardResult{}, first
	}
	result.Partial = len(result.Errors) > 0
	return result, nil
}
