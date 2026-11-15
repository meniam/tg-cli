package tgclient

import (
	"context"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) MessageReaders(ctx context.Context, ref chatref.Ref, id int) (rpc.MessageReadersResult, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MessageReadersResult{}, err
	}
	return messageReadersResolved(ctx, c.tg.API(), p, id)
}
func messageReadersResolved(ctx context.Context, api *tg.Client, p peers.Peer, id int) (rpc.MessageReadersResult, error) {
	chat := toChatOf(p)
	result := rpc.MessageReadersResult{ChatID: chat.ID, ID: id, Readers: []rpc.MessageReader{}}
	switch chat.Type {
	case "user":
		date, err := api.MessagesGetOutboxReadDate(ctx, &tg.MessagesGetOutboxReadDateRequest{Peer: p.InputPeer(), MsgID: id})
		if err != nil {
			return result, mapError(err)
		}
		if date.Date != 0 {
			result.Readers = append(result.Readers, rpc.MessageReader{UserID: chat.ID, Date: formatDate(date.Date)})
		}
	case "group", "supergroup":
		dates, err := api.MessagesGetMessageReadParticipants(ctx, &tg.MessagesGetMessageReadParticipantsRequest{Peer: p.InputPeer(), MsgID: id})
		if err != nil {
			return result, mapError(err)
		}
		for _, date := range dates {
			result.Readers = append(result.Readers, rpc.MessageReader{UserID: date.UserID, Date: formatDate(date.Date)})
		}
	default:
		return result, rpc.NewError(rpc.CodeInvalidParams, "readers require a group or private user chat")
	}
	return result, nil
}

func (c *Client) AccountProfile(ctx context.Context) (rpc.AccountProfile, error) {
	return accountProfileAPI(ctx, c.tg.API())
}
func accountProfileAPI(ctx context.Context, api *tg.Client) (rpc.AccountProfile, error) {
	full, err := api.UsersGetFullUser(ctx, &tg.InputUserSelf{})
	if err != nil {
		return rpc.AccountProfile{}, mapError(err)
	}
	result := rpc.AccountProfile{ID: full.FullUser.ID, Bio: full.FullUser.About}
	if birthday, ok := full.FullUser.GetBirthday(); ok {
		result.Birthday = &rpc.Birthday{Day: birthday.Day, Month: birthday.Month}
		if year, present := birthday.GetYear(); present {
			result.Birthday.Year = year
		}
	}
	if id, ok := full.FullUser.GetPersonalChannelID(); ok {
		result.PersonalChannelID = -channelIDBase - id
	}
	for _, raw := range full.Users {
		if u, ok := raw.(*tg.User); ok && u.ID == result.ID {
			result.FirstName, result.LastName, result.Username, result.Premium = u.FirstName, u.LastName, u.Username, u.Premium
			switch status := u.EmojiStatus.(type) {
			case *tg.EmojiStatus:
				result.EmojiStatus = &rpc.EmojiStatus{DocumentID: status.DocumentID}
				if until, ok := status.GetUntil(); ok {
					result.EmojiStatus.Until = formatDate(until)
				}
			case *tg.EmojiStatusCollectible:
				result.EmojiStatus = &rpc.EmojiStatus{DocumentID: status.DocumentID, Collectible: true}
				if until, ok := status.GetUntil(); ok {
					result.EmojiStatus.Until = formatDate(until)
				}
			}
			return result, nil
		}
	}
	return rpc.AccountProfile{}, rpc.NewError(rpc.CodeTelegram, "Telegram returned no user")
}

func (c *Client) AccountEmojiStatuses(ctx context.Context, recent bool) (rpc.EmojiStatusesResult, error) {
	return accountEmojiStatusesAPI(ctx, c.tg.API(), recent)
}
func accountEmojiStatusesAPI(ctx context.Context, api *tg.Client, recent bool) (rpc.EmojiStatusesResult, error) {
	var raw tg.AccountEmojiStatusesClass
	var err error
	if recent {
		raw, err = api.AccountGetRecentEmojiStatuses(ctx, 0)
	} else {
		raw, err = api.AccountGetDefaultEmojiStatuses(ctx, 0)
	}
	if err != nil {
		return rpc.EmojiStatusesResult{}, mapError(err)
	}
	list, ok := raw.(*tg.AccountEmojiStatuses)
	if !ok {
		return rpc.EmojiStatusesResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram returned an incomplete emoji status list")
	}
	result := rpc.EmojiStatusesResult{Statuses: []rpc.EmojiStatusOption{}}
	for _, s := range list.Statuses {
		switch v := s.(type) {
		case *tg.EmojiStatus:
			item := rpc.EmojiStatusOption{DocumentID: v.DocumentID}
			if until, ok := v.GetUntil(); ok {
				item.Until = formatDate(until)
			}
			result.Statuses = append(result.Statuses, item)
		case *tg.EmojiStatusCollectible:
			item := rpc.EmojiStatusOption{DocumentID: v.DocumentID, Collectible: true, Title: v.Title, Slug: v.Slug}
			if until, ok := v.GetUntil(); ok {
				item.Until = formatDate(until)
			}
			result.Statuses = append(result.Statuses, item)
		}
	}
	return result, nil
}

func (c *Client) ChatRequests(ctx context.Context, ref chatref.Ref, params rpc.JoinRequestsParams) (rpc.JoinRequestsResult, error) {
	p, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.JoinRequestsResult{}, err
	}
	return chatRequestsAPI(ctx, c.tg.API(), p, params)
}
func chatRequestsAPI(ctx context.Context, api *tg.Client, p peers.Peer, params rpc.JoinRequestsParams) (rpc.JoinRequestsResult, error) {
	chat := toChatOf(p)
	req := &tg.MessagesGetChatInviteImportersRequest{Peer: p.InputPeer(), Requested: true, OffsetUser: &tg.InputUserEmpty{}, Limit: params.Limit}
	if params.Search != "" {
		req.Q = params.Search
	}
	if params.Cursor != "" {
		cursor, err := openJoinCursor(params.Cursor)
		if err != nil {
			return rpc.JoinRequestsResult{}, err
		}
		if cursor.Chat != chat.ID || cursor.Search != params.Search {
			return rpc.JoinRequestsResult{}, invalidCursor()
		}
		req.OffsetDate = cursor.Date
		req.OffsetUser = &tg.InputUser{UserID: cursor.User, AccessHash: cursor.Hash}
	}
	data, err := api.MessagesGetChatInviteImporters(ctx, req)
	if err != nil {
		return rpc.JoinRequestsResult{}, mapChatAdminError(err)
	}
	users := map[int64]*tg.User{}
	for _, raw := range data.Users {
		if u, ok := raw.(*tg.User); ok {
			users[u.ID] = u
		}
	}
	result := rpc.JoinRequestsResult{Requests: []rpc.JoinRequest{}}
	for _, v := range data.Importers {
		u := users[v.UserID]
		if u == nil {
			return rpc.JoinRequestsResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram returned a request without a user")
		}
		result.Requests = append(result.Requests, rpc.JoinRequest{IsBot: u.Bot, AccessHash: u.AccessHash, UserID: v.UserID, Name: displayName(u), Username: u.Username, Date: formatDate(v.Date), About: v.About})
	}
	if len(data.Importers) == params.Limit && len(data.Importers) > 0 {
		last := data.Importers[len(data.Importers)-1]
		u := users[last.UserID]
		cursor, err := sealJoinCursor(joinCursor{Chat: chat.ID, User: last.UserID, Hash: u.AccessHash, Date: last.Date, Search: params.Search})
		if err != nil {
			return rpc.JoinRequestsResult{}, rpc.NewError(rpc.CodeInternal, "could not create cursor")
		}
		result.NextCursor = cursor
	}
	return result, nil
}
