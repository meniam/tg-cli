package tgclient

import (
	"context"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

const hiddenSavedAuthorID int64 = 2666000

func (c *Client) SavedTags(ctx context.Context, ref *chatref.Ref) (rpc.SavedTagsResult, error) {
	request := &tg.MessagesGetSavedReactionTagsRequest{}
	if ref != nil {
		p, err := c.resolve(ctx, *ref)
		if err != nil {
			return rpc.SavedTagsResult{}, err
		}
		request.Peer = p.InputPeer()
		request.Flags |= 1 << 0
	}
	raw, err := c.tg.API().MessagesGetSavedReactionTags(ctx, request)
	if err != nil {
		return rpc.SavedTagsResult{}, mapError(err)
	}
	data, ok := raw.(*tg.MessagesSavedReactionTags)
	if !ok {
		return rpc.SavedTagsResult{}, rpc.NewError(rpc.CodeTelegram, "unexpected saved tags response")
	}
	out := rpc.SavedTagsResult{Tags: make([]rpc.SavedTag, 0, len(data.Tags))}
	for _, tag := range data.Tags {
		item := rpc.SavedTag{Title: tag.Title, Count: tag.Count}
		switch r := tag.Reaction.(type) {
		case *tg.ReactionEmoji:
			item.Emoji = r.Emoticon
		case *tg.ReactionCustomEmoji:
			item.CustomEmojiID = r.DocumentID
		default:
			continue
		}
		out.Tags = append(out.Tags, item)
	}
	return out, nil
}

func (c *Client) SavedMessages(ctx context.Context, owner int64, ref *chatref.Ref, p rpc.SavedMessagesParams, search bool) (rpc.MessagesResult, error) {
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	cursorID := owner
	var peer tg.InputPeerClass
	if ref != nil {
		r, e := c.resolve(ctx, *ref)
		if e != nil {
			return rpc.MessagesResult{}, e
		}
		peer = r.InputPeer()
		cursorID = int64(r.TDLibPeerID())
	}
	offset, err := decodeCursor(p.Cursor, cursorID)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	var response tg.MessagesMessagesClass
	if search {
		req := &tg.MessagesSearchRequest{Peer: &tg.InputPeerSelf{}, Q: p.Query, Filter: &tg.InputMessagesFilterEmpty{}, OffsetID: offset, Limit: min(limit+1, discoveryMaxLimit)}
		if peer != nil {
			req.SavedPeerID = peer
			req.Flags |= 1 << 2
		}
		if p.Tag != "" {
			req.SavedReaction = []tg.ReactionClass{&tg.ReactionEmoji{Emoticon: p.Tag}}
			req.Flags |= 1 << 3
		}
		response, err = c.tg.API().MessagesSearch(ctx, req)
	} else {
		if peer == nil {
			return rpc.MessagesResult{}, rpc.NewError(rpc.CodeInvalidParams, "saved history requires a peer")
		}
		response, err = c.tg.API().MessagesGetSavedHistory(ctx, &tg.MessagesGetSavedHistoryRequest{Peer: peer, OffsetID: offset, Limit: min(limit+1, discoveryMaxLimit)})
	}
	if err != nil {
		return rpc.MessagesResult{}, mapError(err)
	}
	modified, err := modifiedMessages(response)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	messages := convertMessages(modified, owner, nil)
	sources := make(map[int]int64)
	unknown := make(map[int]bool)
	for _, raw := range modified.GetMessages() {
		if m, ok := raw.(*tg.Message); ok {
			if source, present := m.GetSavedPeerID(); present {
				sources[m.ID], _ = peerID(source)
			} else if fwd, present := m.GetFwdFrom(); present {
				if source, ok := fwd.GetSavedFromPeer(); ok {
					sources[m.ID], _ = peerID(source)
				} else if source, ok := fwd.GetFromID(); ok {
					sources[m.ID], _ = peerID(source)
				} else {
					unknown[m.ID] = true
				}
			}
		}
	}
	for i := range messages {
		messages[i].SavedSourceID = sources[messages[i].ID]
		messages[i].SavedSourceUnknown = unknown[messages[i].ID]
	}
	result := rpc.MessagesResult{Messages: []rpc.Message{}}
	for _, msg := range messages {
		if len(result.Messages) == limit {
			break
		}
		result.Messages = append(result.Messages, msg)
	}
	if len(messages) > limit || (len(messages) == limit && limit == discoveryMaxLimit) {
		result.NextCursor = encodeCursor(cursorID, result.Messages[len(result.Messages)-1].ID)
	}
	return result, nil
}

type savedListCursor struct {
	V    int   `json:"v"`
	ID   int   `json:"id"`
	Date int   `json:"date"`
	Peer int64 `json:"peer"`
}

func (c *Client) SavedList(ctx context.Context, p rpc.SavedListParams) (rpc.SavedListResult, error) {
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return rpc.SavedListResult{}, err
	}
	req := &tg.MessagesGetSavedDialogsRequest{OffsetPeer: &tg.InputPeerEmpty{}, Limit: min(limit+1, discoveryMaxLimit)}
	if p.Cursor != "" {
		var cur savedListCursor
		if decodeOpaqueCursor(p.Cursor, &cur) != nil || cur.V != 1 || cur.ID <= 0 || cur.Date <= 0 || cur.Peer == 0 {
			return rpc.SavedListResult{}, invalidCursor()
		}
		peer, err := c.resolve(ctx, chatref.Ref{Kind: chatref.ID, ID: cur.Peer})
		if err != nil {
			return rpc.SavedListResult{}, err
		}
		req.OffsetPeer = peer.InputPeer()
		req.OffsetID = cur.ID
		req.OffsetDate = cur.Date
	}
	raw, err := c.tg.API().MessagesGetSavedDialogs(ctx, req)
	if err != nil {
		return rpc.SavedListResult{}, mapError(err)
	}
	data, ok := raw.AsModified()
	if !ok {
		return rpc.SavedListResult{}, rpc.NewError(rpc.CodeTelegram, "unexpected saved dialogs response")
	}
	mgr, err := c.manager(ctx)
	if err != nil {
		return rpc.SavedListResult{}, err
	}
	if err = mgr.Apply(ctx, data.GetUsers(), data.GetChats()); err != nil {
		return rpc.SavedListResult{}, mapError(err)
	}
	entities := peer.EntitiesFromUpdate(tg.EntitiesFrom(data.GetUsers(), data.GetChats()))
	dates := map[int]int{}
	for _, raw := range data.GetMessages() {
		if msg, ok := raw.(tg.NotEmptyMessage); ok {
			dates[msg.GetID()] = msg.GetDate()
		}
	}
	out := rpc.SavedListResult{Dialogs: []rpc.SavedDialog{}}
	for _, raw := range data.GetDialogs() {
		d, ok := raw.(*tg.SavedDialog)
		if !ok {
			continue
		}
		chat := publicChat(d.Peer, entities)
		if chat == nil && botPeerID(d.Peer) == hiddenSavedAuthorID {
			chat = &rpc.Chat{ID: hiddenSavedAuthorID, Type: "user", Title: "Hidden author"}
		}
		if chat == nil {
			continue
		}
		if len(out.Dialogs) == limit {
			break
		}
		cursor := encodeOpaqueCursor(savedListCursor{V: 1, ID: d.TopMessage, Date: dates[d.TopMessage], Peer: chat.ID})
		out.Dialogs = append(out.Dialogs, rpc.SavedDialog{PeerID: chat.ID, Type: chat.Type, Title: chat.Title, TopMessage: d.TopMessage, Date: formatDate(dates[d.TopMessage]), Pinned: d.Pinned, Cursor: cursor})
	}
	if (len(data.GetDialogs()) > len(out.Dialogs) || len(data.GetDialogs()) == discoveryMaxLimit) && len(out.Dialogs) > 0 {
		last := out.Dialogs[len(out.Dialogs)-1]
		out.NextCursor = encodeOpaqueCursor(savedListCursor{V: 1, ID: last.TopMessage, Date: dates[last.TopMessage], Peer: last.PeerID})
		out.CursorPeerID = last.PeerID
	}
	return out, nil
}
