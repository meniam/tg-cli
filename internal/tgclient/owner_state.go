package tgclient

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func peerID(p tg.PeerClass) (int64, bool) {
	switch v := p.(type) {
	case *tg.PeerUser:
		return v.UserID, true
	case *tg.PeerChat:
		return -v.ChatID, true
	case *tg.PeerChannel:
		return -channelIDBase - v.ChannelID, true
	}
	return 0, false
}
func dialogState(d *tg.Dialog) (rpc.ChatState, bool) {
	id, ok := peerID(d.Peer)
	if !ok {
		return rpc.ChatState{}, false
	}
	folder, _ := d.GetFolderID()
	return rpc.ChatState{ChatID: id, ReadInboxMaxID: d.ReadInboxMaxID, ReadOutboxMaxID: d.ReadOutboxMaxID, Unread: d.UnreadCount, UnreadMentions: d.UnreadMentionsCount, UnreadReactions: d.UnreadReactionsCount, UnreadMark: d.UnreadMark, Pinned: d.Pinned, Archived: folder == 1, TopMessage: d.TopMessage, TTLPeriod: optInt(d.GetTTLPeriod()), MuteUntil: optInt(d.NotifySettings.GetMuteUntil()), Silent: optBool(d.NotifySettings.GetSilent()), ShowPreviews: optBool(d.NotifySettings.GetShowPreviews())}, true
}
func peerDialogsAPI(ctx context.Context, api *tg.Client, peer tg.InputPeerClass) (*tg.MessagesPeerDialogs, error) {
	response, err := api.MessagesGetPeerDialogs(ctx, []tg.InputDialogPeerClass{&tg.InputDialogPeer{Peer: peer}})
	if err != nil {
		return nil, mapError(err)
	}
	return response, nil
}

func (c *Client) ChatStates(ctx context.Context, ref *chatref.Ref, limit int) (rpc.ChatStatesResult, error) {
	out := rpc.ChatStatesResult{States: []rpc.ChatState{}}
	if ref != nil {
		p, err := c.resolve(ctx, *ref)
		if err != nil {
			return out, err
		}
		response, err := peerDialogsAPI(ctx, c.tg.API(), p.InputPeer())
		if err != nil {
			return out, mapError(err)
		}
		m, err := c.manager(ctx)
		if err != nil {
			return out, err
		}
		if err = m.Apply(ctx, response.Users, response.Chats); err != nil {
			return out, mapError(err)
		}
		for _, raw := range response.Dialogs {
			if d, ok := raw.(*tg.Dialog); ok {
				if s, ok := dialogState(d); ok {
					out.States = append(out.States, s)
				}
			}
		}
		return out, nil
	}
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		return out, rpc.NewError(rpc.CodeInvalidParams, "limit must be 1 to 500")
	}
	m, err := c.manager(ctx)
	if err != nil {
		return out, err
	}
	it := query.GetDialogs(c.tg.API()).BatchSize(min(limit, 100)).Iter()
	users := map[int64]tg.UserClass{}
	chats := map[int64]tg.ChatClass{}
	seen := map[int64]bool{}
	for len(out.States) < limit && it.Next(ctx) {
		elem := it.Value()
		for id, u := range elem.Entities.Users() {
			users[id] = u
		}
		for id, ch := range elem.Entities.Chats() {
			chats[-id] = ch
		}
		for id, ch := range elem.Entities.Channels() {
			chats[id] = ch
		}
		if d, ok := elem.Dialog.(*tg.Dialog); ok {
			if s, ok := dialogState(d); ok && !seen[s.ChatID] {
				seen[s.ChatID] = true
				out.States = append(out.States, s)
			}
		}
	}
	if err = it.Err(); err != nil {
		return out, mapError(err)
	}
	ul := make([]tg.UserClass, 0, len(users))
	cl := make([]tg.ChatClass, 0, len(chats))
	for _, u := range users {
		ul = append(ul, u)
	}
	for _, ch := range chats {
		cl = append(cl, ch)
	}
	if err = m.Apply(ctx, ul, cl); err != nil {
		return out, mapError(err)
	}
	return out, nil
}
func (c *Client) Sessions(ctx context.Context) (rpc.SessionsResult, error) {
	return sessionsAPI(ctx, c.tg.API())
}

func sessionsAPI(ctx context.Context, api *tg.Client) (rpc.SessionsResult, error) {
	response, err := api.AccountGetAuthorizations(ctx)
	if err != nil {
		return rpc.SessionsResult{}, mapError(err)
	}
	out := rpc.SessionsResult{AuthorizationTTLDays: response.AuthorizationTTLDays, Sessions: make([]rpc.Session, 0, len(response.Authorizations))}
	for _, a := range response.Authorizations {
		out.Sessions = append(out.Sessions, rpc.Session{Hash: a.Hash, Current: a.Current, OfficialApp: a.OfficialApp, PasswordPending: a.PasswordPending, Unconfirmed: a.Unconfirmed, DeviceModel: a.DeviceModel, Platform: a.Platform, SystemVersion: a.SystemVersion, APIID: a.APIID, AppName: a.AppName, AppVersion: a.AppVersion, DateCreated: a.DateCreated, DateActive: a.DateActive, Country: a.Country, Region: a.Region})
	}
	return out, nil
}
func blockedPage(ctx context.Context, api *tg.Client, p rpc.BlockedParams) (rpc.BlockedResult, []tg.UserClass, []tg.ChatClass, error) {
	out := rpc.BlockedResult{Peers: []rpc.BlockedPeer{}}
	offset, err := rpc.BlockedOffset(p)
	if err != nil {
		return out, nil, nil, err
	}
	limit := p.Limit
	if limit == 0 {
		limit = 100
	}
	response, err := api.ContactsGetBlocked(ctx, &tg.ContactsGetBlockedRequest{Offset: offset, Limit: limit})
	if err != nil {
		return out, nil, nil, mapError(err)
	}
	var blocked []tg.PeerBlocked
	var users []tg.UserClass
	var chats []tg.ChatClass
	var total int
	switch v := response.(type) {
	case *tg.ContactsBlocked:
		blocked, users, chats = v.Blocked, v.Users, v.Chats
		total = offset + len(blocked)
	case *tg.ContactsBlockedSlice:
		blocked, users, chats, total = v.Blocked, v.Users, v.Chats, v.Count
	default:
		return out, nil, nil, fmt.Errorf("unsupported blocked response %T", response)
	}
	for _, b := range blocked {
		id, ok := peerID(b.PeerID)
		if !ok {
			continue
		}
		item := rpc.BlockedPeer{PeerID: id, Date: b.Date}
		switch b.PeerID.(type) {
		case *tg.PeerUser:
			item.Type = "user"
			for _, raw := range users {
				if u, ok := raw.(*tg.User); ok && u.ID == id {
					item.Title = displayName(u)
					item.Username, _ = u.GetUsername()
					if u.Bot {
						item.Type = "bot"
					}
					break
				}
			}
		case *tg.PeerChat:
			item.Type = "group"
			for _, raw := range chats {
				if ch, ok := raw.(*tg.Chat); ok && -ch.ID == id {
					item.Title = ch.Title
					break
				}
			}
		case *tg.PeerChannel:
			item.Type = "channel"
			for _, raw := range chats {
				if ch, ok := raw.(*tg.Channel); ok && -channelIDBase-ch.ID == id {
					item.Title = ch.Title
					item.Username, _ = ch.GetUsername()
					if ch.Megagroup {
						item.Type = "supergroup"
					}
					break
				}
			}
		}
		out.Peers = append(out.Peers, item)
	}
	if len(blocked) == 0 && offset < total {
		return out, nil, nil, fmt.Errorf("blocked page did not advance")
	}
	if len(blocked) > 0 && offset+len(blocked) < total {
		out.NextCursor = strconv.Itoa(offset + len(blocked))
	}
	return out, users, chats, nil
}
func (c *Client) Blocked(ctx context.Context, p rpc.BlockedParams) (rpc.BlockedResult, error) {
	out, users, chats, err := blockedPage(ctx, c.tg.API(), p)
	if err != nil {
		return out, err
	}
	m, err := c.manager(ctx)
	if err != nil {
		return out, err
	}
	if err = m.Apply(ctx, users, chats); err != nil {
		return out, mapError(err)
	}
	return out, nil
}
func notifyDefaultsAPI(ctx context.Context, api *tg.Client) (rpc.NotifyDefaultsResult, error) {
	out := rpc.NotifyDefaultsResult{Defaults: []rpc.NotifyDefault{}}
	for _, v := range []struct {
		scope string
		peer  tg.InputNotifyPeerClass
	}{{"users", &tg.InputNotifyUsers{}}, {"chats", &tg.InputNotifyChats{}}, {"broadcasts", &tg.InputNotifyBroadcasts{}}} {
		s, err := api.AccountGetNotifySettings(ctx, v.peer)
		if err != nil {
			return rpc.NotifyDefaultsResult{}, mapError(err)
		}
		out.Defaults = append(out.Defaults, rpc.NotifyDefault{Scope: v.scope, MuteUntil: optInt(s.GetMuteUntil()), Silent: optBool(s.GetSilent()), ShowPreviews: optBool(s.GetShowPreviews())})
	}
	return out, nil
}
func (c *Client) NotifyDefaults(ctx context.Context) (rpc.NotifyDefaultsResult, error) {
	return notifyDefaultsAPI(ctx, c.tg.API())
}
