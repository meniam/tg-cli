package tgclient

import (
	"context"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"time"
)

func (c *Client) UnreadChats(ctx context.Context, limit int, archived bool, allowed func(int64) bool) (rpc.UnreadChatsResult, []rpc.ChatState, error) {
	return unreadChatsAPI(ctx, c.tg.API(), limit, archived, allowed)
}

func unreadChatsAPI(ctx context.Context, api *tg.Client, limit int, archived bool, allowed func(int64) bool) (rpc.UnreadChatsResult, []rpc.ChatState, error) {
	out := rpc.UnreadChatsResult{Chats: []rpc.UnreadChat{}}
	states := []rpc.ChatState{}
	builder := query.GetDialogs(api).BatchSize(100)
	if archived {
		builder = builder.FolderID(1)
	}
	it := builder.Iter()
	for out.Scanned < 1000 && len(out.Chats) < limit && it.Next(ctx) {
		out.Scanned++
		e := it.Value()
		d, ok := e.Dialog.(*tg.Dialog)
		if !ok {
			continue
		}
		chat, ok := toChat(e)
		if !ok {
			continue
		}
		state, ok := dialogState(d)
		if !ok || !allowed(chat.ID) {
			continue
		}
		if state.Archived != archived {
			continue
		}
		if state.Unread == 0 && !state.UnreadMark {
			continue
		}
		forum := false
		if p, ok := d.Peer.(*tg.PeerChannel); ok {
			if ch, ok := e.Entities.Channel(p.ChannelID); ok {
				forum = ch.Forum
			}
		}
		out.Chats = append(out.Chats, rpc.UnreadChat{ID: chat.ID, Type: chat.Type, Title: chat.Title, Username: chat.Username, Forum: forum, Unread: state.Unread, UnreadMentions: state.UnreadMentions, UnreadReactions: state.UnreadReactions, UnreadMark: state.UnreadMark, Muted: state.MuteUntil != nil && int64(*state.MuteUntil) > time.Now().Unix(), Archived: state.Archived, ReadInboxMaxID: state.ReadInboxMaxID, TopMessage: state.TopMessage})
		states = append(states, state)
	}
	if err := it.Err(); err != nil {
		return out, nil, mapError(err)
	}
	if out.Scanned < 1000 && len(out.Chats) < limit {
		out.Complete = true
	}
	return out, states, nil
}
func (c *Client) UnreadMessages(ctx context.Context, ref chatref.Ref, p rpc.MessageUnreadParams) (rpc.UnreadMessagesResult, *rpc.ChatState, error) {
	out := rpc.UnreadMessagesResult{Messages: []rpc.Message{}, Topic: p.Topic}
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return out, nil, err
	}
	return unreadMessagesAPI(ctx, c.tg.API(), peer, p)
}

func unreadMessagesAPI(ctx context.Context, api *tg.Client, peer peers.Peer, p rpc.MessageUnreadParams) (rpc.UnreadMessagesResult, *rpc.ChatState, error) {
	out := rpc.UnreadMessagesResult{Messages: []rpc.Message{}, Topic: p.Topic}
	out.ChatID = int64(peer.TDLibPeerID())
	channel, isChannel := peer.(peers.Channel)
	forum := isChannel && channel.Raw().Forum
	if forum && p.Topic == 0 {
		return out, nil, rpc.NewError(rpc.CodeInvalidParams, "forum requires --topic; use chat topics to list topics")
	}
	if !forum && p.Topic != 0 {
		return out, nil, rpc.NewError(rpc.CodeInvalidParams, "topic requires a forum chat")
	}
	var state *rpc.ChatState
	if p.Topic != 0 {
		response, e := api.MessagesGetForumTopicsByID(ctx, &tg.MessagesGetForumTopicsByIDRequest{Peer: peer.InputPeer(), Topics: []int{p.Topic}})
		if e != nil {
			return out, nil, mapError(e)
		}
		found := false
		for _, raw := range response.Topics {
			if t, ok := raw.(*tg.ForumTopic); ok && t.ID == p.Topic {
				out.ReadInboxMaxID = t.ReadInboxMaxID
				out.Unread = t.UnreadCount
				found = true
				break
			}
		}
		if !found {
			return out, nil, rpc.NewError(rpc.CodeNotFound, "topic not found")
		}
	} else {
		response, e := peerDialogsAPI(ctx, api, peer.InputPeer())
		if e != nil {
			return out, nil, e
		}
		for _, raw := range response.Dialogs {
			if d, ok := raw.(*tg.Dialog); ok {
				s, ok := dialogState(d)
				if ok && s.ChatID == out.ChatID {
					state = &s
					out.ReadInboxMaxID = s.ReadInboxMaxID
					out.Unread = s.Unread
					break
				}
			}
		}
		if state == nil {
			return out, nil, rpc.ErrChatNotFound()
		}
	}
	history, err := historyResolved(ctx, api, peer, rpc.HistoryQuery{Limit: p.Limit, Topic: p.Topic, Cursor: p.Cursor, MinID: out.ReadInboxMaxID, MaxBytes: 768 << 10})
	if err != nil {
		return out, nil, err
	}
	out.NextCursor = history.NextCursor
	for _, m := range history.Messages {
		if !m.Out {
			out.Messages = append(out.Messages, m)
		}
	}
	return out, state, nil
}
