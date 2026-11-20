package tgclient

import (
	"context"

	"github.com/gotd/td/tdp"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

const forumFetchLimit = 100
const forumMaxRequests = 5

func (c *Client) Topics(ctx context.Context, ref chatref.Ref, p rpc.TopicsParams) (rpc.TopicsResult, error) {
	limit, err := listLimit(p.Limit, 100)
	if err != nil {
		return rpc.TopicsResult{}, err
	}
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.TopicsResult{}, err
	}
	return topicsAPI(ctx, c.tg.API(), peer, p.Cursor, limit)
}

func topicsAPI(ctx context.Context, api *tg.Client, peer peers.Peer, token string, limit int) (rpc.TopicsResult, error) {
	channel, ok := peer.(peers.Channel)
	if !ok || !channel.Raw().Forum {
		return rpc.TopicsResult{}, rpc.NewError(rpc.CodeInvalidParams, "topics require a forum chat")
	}
	chatID := int64(peer.TDLibPeerID())
	cursor, err := decodeTopicsCursor(token, chatID)
	if err != nil {
		return rpc.TopicsResult{}, err
	}
	pinned := append([]int(nil), cursor.Pinned...)
	seenPinned := make(map[int]struct{}, len(pinned))
	for _, id := range pinned {
		seenPinned[id] = struct{}{}
	}
	result := rpc.TopicsResult{Topics: []rpc.Topic{}}
	for range forumMaxRequests {
		previous := cursor
		// Telegram can return pinned topics beyond a smaller requested limit.
		response, err := api.MessagesGetForumTopics(ctx, &tg.MessagesGetForumTopicsRequest{
			Peer: peer.InputPeer(), OffsetDate: cursor.Date, OffsetID: cursor.ID,
			OffsetTopic: cursor.Topic, Limit: forumFetchLimit,
		})
		if err != nil {
			return rpc.TopicsResult{}, mapError(err)
		}
		dates := make(map[int]int, len(response.Messages))
		for _, msg := range response.Messages {
			if message, ok := msg.(tg.NotEmptyMessage); ok {
				dates[message.GetID()] = message.GetDate()
			}
		}
		var last *tg.ForumTopic
		processed := 0
		for _, raw := range response.Topics {
			topic, ok := raw.(*tg.ForumTopic)
			if !ok {
				continue
			}
			last = topic // Advance through every examined topic, including a skipped repeat.
			processed++
			if topic.Pinned {
				if _, seen := seenPinned[topic.ID]; seen {
					continue
				}
			}
			result.Topics = append(result.Topics, rpc.Topic{
				ID: topic.ID, Title: topic.Title, Closed: topic.Closed, Pinned: topic.Pinned,
				Hidden: topic.Hidden, Unread: topic.UnreadCount, TopMessage: topic.TopMessage,
				Created: joinedDate(topic.Date),
			})
			if topic.Pinned {
				seenPinned[topic.ID] = struct{}{}
				if len(pinned) < maxPinnedTopics {
					pinned = append(pinned, topic.ID)
				}
			}
			if len(result.Topics) == limit {
				break
			}
		}
		unprocessed := processed < len(response.Topics)
		fullWithMore := len(response.Topics) >= forumFetchLimit && response.Count > processed
		resetWithMore := last != nil && last.Pinned && response.Count > processed
		more := last != nil && (unprocessed || fullWithMore || resetWithMore)
		if more {
			next := topicsCursor{Chat: chatID, Pinned: pinned}
			if !last.Pinned {
				next.Date = dates[last.TopMessage]
				if response.OrderByCreateDate {
					next.Date = last.Date
				}
				if next.Date <= 0 {
					return rpc.TopicsResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram did not return the message date needed for the topic cursor")
				}
				next.ID, next.Topic = last.TopMessage, last.ID
			}
			if len(result.Topics) == 0 && next.Date == previous.Date && next.ID == previous.ID && next.Topic == previous.Topic {
				return rpc.TopicsResult{Topics: []rpc.Topic{}}, nil
			}
			cursor = next
			result.NextCursor = encodeTopicsCursor(cursor)
		} else {
			result.NextCursor = ""
		}
		if len(result.Topics) > 0 || !more {
			return result, nil
		}
	}
	return result, nil
}

func (c *Client) CommonChats(ctx context.Context, ref chatref.Ref, p rpc.CommonChatsParams) (rpc.CommonChatsResult, error) {
	limit, err := listLimit(p.Limit, 100)
	if err != nil {
		return rpc.CommonChatsResult{}, err
	}
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.CommonChatsResult{}, err
	}
	return commonChatsAPI(ctx, c.tg.API(), peer, p.Cursor, limit)
}

func commonChatsAPI(ctx context.Context, api *tg.Client, peer peers.Peer, token string, limit int) (rpc.CommonChatsResult, error) {
	user, ok := peer.(peers.User)
	if !ok {
		return rpc.CommonChatsResult{}, rpc.NewError(rpc.CodeInvalidParams, "common chats require a user or bot")
	}
	cursor, err := decodeCommonChatsCursor(token, user.ID())
	if err != nil {
		return rpc.CommonChatsResult{}, err
	}
	response, err := api.MessagesGetCommonChats(ctx, &tg.MessagesGetCommonChatsRequest{UserID: user.InputUser(), MaxID: cursor.MaxID, Limit: limit})
	if err != nil {
		return rpc.CommonChatsResult{}, mapError(err)
	}
	result := rpc.CommonChatsResult{Chats: []rpc.CommonChat{}}
	chats := response.GetChats()
	var lastID int64
	for _, raw := range chats {
		if len(result.Chats) >= limit {
			break
		}
		if id := commonRawID(raw); id > 0 {
			lastID = id
		}
		chat, id, ok := commonChatOf(raw)
		if !ok {
			continue
		}
		if obj, ok := raw.(tdp.Object); ok {
			chat.Raw = methodRaw(rawCommonChats, obj)
		}
		result.Chats = append(result.Chats, chat)
		lastID = id
	}
	more := len(chats) > limit
	if slice, ok := response.(*tg.MessagesChatsSlice); ok && slice.Count > len(chats) {
		more = true
	}
	if more && lastID > 0 {
		result.NextCursor = encodeCommonChatsCursor(commonChatsCursor{User: user.ID(), MaxID: lastID})
	}
	return result, nil
}

func commonRawID(raw tg.ChatClass) int64 {
	switch chat := raw.(type) {
	case *tg.Chat:
		return chat.ID
	case *tg.Channel:
		return chat.ID
	case *tg.ChatForbidden:
		return chat.ID
	case *tg.ChannelForbidden:
		return chat.ID
	case *tg.ChatEmpty:
		return chat.ID
	default:
		return 0
	}
}

// MaxID is the raw Chat.ID/Channel.ID accepted by messages.getCommonChats,
// not the negative Bot API ID returned to callers.
func commonChatOf(raw tg.ChatClass) (rpc.CommonChat, int64, bool) {
	switch chat := raw.(type) {
	case *tg.Chat:
		return rpc.CommonChat{ID: -chat.ID, Type: "group", Title: chat.Title}, chat.ID, true
	case *tg.Channel:
		kind := "channel"
		if chat.Megagroup {
			kind = "supergroup"
		}
		username, _ := chat.GetUsername()
		return rpc.CommonChat{ID: -channelIDBase - chat.ID, Type: kind, Title: chat.Title, Username: username}, chat.ID, true
	default:
		return rpc.CommonChat{}, 0, false
	}
}
