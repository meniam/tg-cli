package tgclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/tg"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

const discoveryDefaultLimit = 20
const discoveryMaxLimit = 100

func discoveryLimit(limit int) (int, error) {
	if limit == 0 {
		return discoveryDefaultLimit, nil
	}
	if limit < 0 || limit > discoveryMaxLimit {
		return 0, rpc.NewError(rpc.CodeInvalidParams, "limit must be between 1 and 100")
	}
	return limit, nil
}
func searchMediaFilter(media string) (tg.MessagesFilterClass, error) {
	switch media {
	case "":
		return &tg.InputMessagesFilterEmpty{}, nil
	case "photo":
		return &tg.InputMessagesFilterPhotos{}, nil
	case "video":
		return &tg.InputMessagesFilterVideo{}, nil
	case "document":
		return &tg.InputMessagesFilterDocument{}, nil
	case "voice":
		return &tg.InputMessagesFilterVoice{}, nil
	case "audio":
		return &tg.InputMessagesFilterMusic{}, nil
	case "link":
		return &tg.InputMessagesFilterURL{}, nil
	case "gif":
		return &tg.InputMessagesFilterGif{}, nil
	case "pinned":
		return &tg.InputMessagesFilterPinned{}, nil
	default:
		return nil, rpc.NewError(rpc.CodeInvalidParams, "media must be photo, video, document, voice, audio, link, gif, or pinned")
	}
}

func (c *Client) SearchMessages(ctx context.Context, ref chatref.Ref, p rpc.SearchMessagesParams) (rpc.MessagesResult, error) {
	if strings.TrimSpace(p.Query) == "" && p.Media == "" {
		return rpc.MessagesResult{}, rpc.NewError(rpc.CodeInvalidParams, "search query or media filter is required")
	}
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	filter, err := searchMediaFilter(p.Media)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	chat := toChatOf(resolved)
	offset, err := decodeCursor(p.Cursor, chat.ID)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	return searchMessagesAPI(ctx, c.tg.API(), resolved.InputPeer(), chat, p.Query, filter, limit, offset)
}
func searchMessagesAPI(ctx context.Context, api *tg.Client, input tg.InputPeerClass, chat rpc.Chat, query string, filter tg.MessagesFilterClass, limit, offset int) (rpc.MessagesResult, error) {
	response, err := api.MessagesSearch(ctx, &tg.MessagesSearchRequest{Peer: input, Q: query, Filter: filter, OffsetID: offset, Limit: min(limit+1, discoveryMaxLimit)})
	if err != nil {
		return rpc.MessagesResult{}, mapError(err)
	}
	modified, err := modifiedMessages(response)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	messages := convertMessages(modified, chat.ID, &rpc.Sender{ID: chat.ID, Name: chat.Title, Username: chat.Username})
	result := rpc.MessagesResult{Messages: []rpc.Message{}}
	for _, message := range messages {
		if len(result.Messages) == limit {
			break
		}
		result.Messages = append(result.Messages, message)
	}
	if len(messages) > limit || len(messages) >= limit && len(messages) == discoveryMaxLimit {
		result.NextCursor = encodeCursor(chat.ID, result.Messages[len(result.Messages)-1].ID)
	}
	return result, nil
}

func modifiedMessages(response tg.MessagesMessagesClass) (tg.ModifiedMessagesMessages, error) {
	modified, ok := response.AsModified()
	if !ok {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned no message list")
	}
	return modified, nil
}
func messageEntities(response tg.ModifiedMessagesMessages) peer.Entities {
	return peer.EntitiesFromUpdate(tg.EntitiesFrom(response.GetUsers(), response.GetChats()))
}
func convertMessages(response tg.ModifiedMessagesMessages, chatID int64, fallback *rpc.Sender) []rpc.Message {
	entities := messageEntities(response)
	out := make([]rpc.Message, 0, len(response.GetMessages()))
	for _, raw := range response.GetMessages() {
		msg, ok := raw.(tg.NotEmptyMessage)
		if !ok {
			continue
		}
		actualChatID := chatID
		if actualChatID == 0 {
			actualChatID = botPeerID(msg.GetPeerID())
		}
		if actualChatID == 0 {
			continue
		}
		converted, ok := toMessage(actualChatID, msg, entities, fallback)
		if ok {
			out = append(out, converted)
		}
	}
	return out
}

type publicCursor struct {
	V      int    `json:"v"`
	Query  string `json:"q"`
	ID     int    `json:"i"`
	Rate   int    `json:"r"`
	Kind   string `json:"k"`
	PeerID int64  `json:"p"`
	Hash   int64  `json:"h,omitempty"`
}

func encodePublicCursor(c publicCursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodePublicCursor(value, query string) (publicCursor, error) {
	if value == "" {
		return publicCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return publicCursor{}, invalidCursor()
	}
	var c publicCursor
	if json.Unmarshal(raw, &c) != nil || c.V != 1 || c.Query != query || c.ID <= 0 || c.PeerID <= 0 {
		return publicCursor{}, invalidCursor()
	}
	if c.Kind != "user" && c.Kind != "chat" && c.Kind != "channel" {
		return publicCursor{}, invalidCursor()
	}
	return c, nil
}
func inputPeerFromPublicCursor(c publicCursor) tg.InputPeerClass {
	switch c.Kind {
	case "user":
		return &tg.InputPeerUser{UserID: c.PeerID, AccessHash: c.Hash}
	case "chat":
		return &tg.InputPeerChat{ChatID: c.PeerID}
	case "channel":
		return &tg.InputPeerChannel{ChannelID: c.PeerID, AccessHash: c.Hash}
	}
	return &tg.InputPeerEmpty{}
}
func publicCursorPeer(id tg.PeerClass, entities peer.Entities) (publicCursor, bool) {
	switch p := id.(type) {
	case *tg.PeerUser:
		user := entities.Users()[p.UserID]
		if user == nil {
			return publicCursor{}, false
		}
		hash, _ := user.GetAccessHash()
		return publicCursor{Kind: "user", PeerID: p.UserID, Hash: hash}, true
	case *tg.PeerChat:
		return publicCursor{Kind: "chat", PeerID: p.ChatID}, true
	case *tg.PeerChannel:
		channel := entities.Channels()[p.ChannelID]
		if channel == nil {
			return publicCursor{}, false
		}
		hash, _ := channel.GetAccessHash()
		return publicCursor{Kind: "channel", PeerID: p.ChannelID, Hash: hash}, true
	}
	return publicCursor{}, false
}
func publicChat(id tg.PeerClass, entities peer.Entities) *rpc.Chat {
	chatID := botPeerID(id)
	switch p := id.(type) {
	case *tg.PeerUser:
		if user := entities.Users()[p.UserID]; user != nil {
			username, _ := user.GetUsername()
			kind := "user"
			if user.Bot {
				kind = "bot"
			}
			return &rpc.Chat{ID: chatID, Type: kind, Title: displayName(user), Username: username}
		}
	case *tg.PeerChat:
		if chat := entities.Chats()[p.ChatID]; chat != nil {
			return &rpc.Chat{ID: chatID, Type: "group", Title: chat.Title}
		}
	case *tg.PeerChannel:
		if channel := entities.Channels()[p.ChannelID]; channel != nil {
			username, _ := channel.GetUsername()
			kind := "channel"
			if channel.Megagroup {
				kind = "supergroup"
			}
			return &rpc.Chat{ID: chatID, Type: kind, Title: channel.Title, Username: username}
		}
	}
	return nil
}
func (c *Client) SearchPublic(ctx context.Context, p rpc.PublicSearchParams) (rpc.PublicSearchResult, error) {
	if strings.TrimSpace(p.Query) == "" {
		return rpc.PublicSearchResult{}, rpc.NewError(rpc.CodeInvalidParams, "search query must not be empty")
	}
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return rpc.PublicSearchResult{}, err
	}
	cursor, err := decodePublicCursor(p.Cursor, p.Query)
	if err != nil {
		return rpc.PublicSearchResult{}, err
	}
	return searchPublicAPI(ctx, c.tg.API(), p.Query, limit, cursor)
}
func searchPublicAPI(ctx context.Context, api *tg.Client, query string, limit int, cursor publicCursor) (rpc.PublicSearchResult, error) {
	request := &tg.MessagesSearchGlobalRequest{Q: query, Filter: &tg.InputMessagesFilterEmpty{}, Limit: limit, OffsetPeer: &tg.InputPeerEmpty{}}
	if cursor.ID > 0 {
		request.OffsetID = cursor.ID
		request.OffsetRate = cursor.Rate
		request.OffsetPeer = inputPeerFromPublicCursor(cursor)
	}
	response, err := api.MessagesSearchGlobal(ctx, request)
	if err != nil {
		return rpc.PublicSearchResult{}, mapError(err)
	}
	modified, err := modifiedMessages(response)
	if err != nil {
		return rpc.PublicSearchResult{}, err
	}
	entities := messageEntities(modified)
	result := rpc.PublicSearchResult{Hits: []rpc.PublicHit{}}
	var last tg.NotEmptyMessage
	for _, raw := range modified.GetMessages() {
		msg, ok := raw.(tg.NotEmptyMessage)
		if !ok {
			continue
		}
		if len(result.Hits) >= limit {
			continue
		}
		chatID := botPeerID(msg.GetPeerID())
		if chatID == 0 {
			continue
		}
		chat := publicChat(msg.GetPeerID(), entities)
		converted, ok := toMessage(chatID, msg, entities, nil)
		if !ok {
			continue
		}
		result.Hits = append(result.Hits, rpc.PublicHit{ChatID: chatID, Chat: chat, Message: converted})
		last = msg
	}
	if len(result.Hits) == limit && last != nil {
		next, ok := publicCursorPeer(last.GetPeerID(), entities)
		if !ok {
			return rpc.PublicSearchResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram did not return the peer for the search cursor")
		}
		next.V, next.Query, next.ID = 1, query, last.GetID()
		next.Rate = last.GetDate()
		if slice, ok := response.(*tg.MessagesMessagesSlice); ok {
			if rate, has := slice.GetNextRate(); has {
				next.Rate = rate
			}
		}
		result.NextCursor = encodePublicCursor(next)
	}
	return result, nil
}

func (c *Client) MessageContext(ctx context.Context, ref chatref.Ref, p rpc.ContextParams) (rpc.MessagesResult, error) {
	if p.ID <= 0 || p.Before < 0 || p.Before > 50 || p.After < 0 || p.After > 50 {
		return rpc.MessagesResult{}, rpc.NewError(rpc.CodeInvalidParams, "id must be positive and before/after must be between 0 and 50")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	return contextAPI(ctx, c.tg.API(), resolved.InputPeer(), toChatOf(resolved), p)
}
func contextAPI(ctx context.Context, api *tg.Client, input tg.InputPeerClass, chat rpc.Chat, p rpc.ContextParams) (rpc.MessagesResult, error) {
	response, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: input, OffsetID: p.ID, AddOffset: -p.After - 1, Limit: p.Before + p.After + 1})
	if err != nil {
		return rpc.MessagesResult{}, mapError(err)
	}
	modified, err := modifiedMessages(response)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	messages := convertMessages(modified, chat.ID, &rpc.Sender{ID: chat.ID, Name: chat.Title, Username: chat.Username})
	sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
	before := make([]rpc.Message, 0)
	after := make([]rpc.Message, 0)
	var anchor *rpc.Message
	for i := range messages {
		switch {
		case messages[i].ID < p.ID:
			before = append(before, messages[i])
		case messages[i].ID == p.ID:
			anchor = &messages[i]
		case messages[i].ID > p.ID:
			after = append(after, messages[i])
		}
	}
	if anchor == nil {
		return rpc.MessagesResult{}, messageNotFound()
	}
	if len(before) > p.Before {
		before = before[len(before)-p.Before:]
	}
	if len(after) > p.After {
		after = after[:p.After]
	}
	out := append(before, *anchor)
	out = append(out, after...)
	return rpc.MessagesResult{Messages: out}, nil
}

func (c *Client) Comments(ctx context.Context, ref chatref.Ref, p rpc.CommentsParams) (rpc.MessagesResult, error) {
	if p.ID <= 0 {
		return rpc.MessagesResult{}, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	chat := toChatOf(resolved)
	if !chatref.IsChannel(chat.ID) {
		return rpc.MessagesResult{}, rpc.NewError(rpc.CodeInvalidParams, "comments require a channel post")
	}
	offset, err := decodeCursor(p.Cursor, chat.ID)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	post, err := query.Messages(c.tg.API()).GetMessagesWithEntities(ctx, resolved.InputPeer(), p.ID)
	if err != nil {
		return rpc.MessagesResult{}, mapError(err)
	}
	if len(post) == 0 || !postHasDiscussion(post[0].Msg) {
		return rpc.MessagesResult{}, rpc.NewError(rpc.CodeNotFound, "comments not found: this post has no discussion")
	}
	return commentsAPI(ctx, c.tg.API(), resolved.InputPeer(), chat, p.ID, limit, offset)
}
func postHasDiscussion(value tg.NotEmptyMessage) bool {
	post, ok := value.(*tg.Message)
	if !ok {
		return false
	}
	replies, ok := post.GetReplies()
	return ok && replies.Comments
}
func commentsAPI(ctx context.Context, api *tg.Client, input tg.InputPeerClass, chat rpc.Chat, id, limit, offset int) (rpc.MessagesResult, error) {
	response, err := api.MessagesGetReplies(ctx, &tg.MessagesGetRepliesRequest{Peer: input, MsgID: id, OffsetID: offset, Limit: min(limit+1, discoveryMaxLimit)})
	if err != nil {
		if isNotFound(err) {
			return rpc.MessagesResult{}, rpc.NewError(rpc.CodeNotFound, "comments not found: this post has no discussion")
		}
		return rpc.MessagesResult{}, mapError(err)
	}
	modified, err := modifiedMessages(response)
	if err != nil {
		return rpc.MessagesResult{}, err
	}
	messages := convertMessages(modified, 0, nil)
	result := rpc.MessagesResult{Messages: []rpc.Message{}}
	for _, message := range messages {
		if len(result.Messages) >= limit {
			break
		}
		result.Messages = append(result.Messages, message)
	}
	if len(messages) > limit || len(messages) >= limit && len(messages) == discoveryMaxLimit {
		result.NextCursor = encodeCursor(chat.ID, result.Messages[len(result.Messages)-1].ID)
	}
	return result, nil
}

func (c *Client) MessageLink(ctx context.Context, ref chatref.Ref, p rpc.LinkParams) (rpc.LinkResult, error) {
	if p.ID <= 0 || p.Topic < 0 {
		return rpc.LinkResult{}, rpc.NewError(rpc.CodeInvalidParams, "id must be positive and topic must not be negative")
	}
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.LinkResult{}, err
	}
	chat := toChatOf(resolved)
	return linkForChat(chat, p.ID, p.Topic)
}
func linkForChat(chat rpc.Chat, id, topic int) (rpc.LinkResult, error) {
	if chat.Type != "channel" && chat.Type != "supergroup" {
		return rpc.LinkResult{}, rpc.NewError(rpc.CodeInvalidParams, "message links require a channel or supergroup")
	}
	link, err := chatref.FormatMessageLink(chatref.MessageRef{Chat: chatref.Ref{Kind: chatref.ID, ID: chat.ID}, ID: id, Topic: topic}, chat.Username)
	if err != nil {
		return rpc.LinkResult{}, rpc.NewError(rpc.CodeInvalidParams, err.Error())
	}
	return rpc.LinkResult{Link: link}, nil
}

func (c *Client) MarkRead(ctx context.Context, ref chatref.Ref) (rpc.MarkReadResult, error) {
	resolved, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.MarkReadResult{}, err
	}
	id := int64(resolved.TDLibPeerID())
	if err := markReadAPI(ctx, c.tg.API(), resolved.InputPeer()); err != nil {
		return rpc.MarkReadResult{}, err
	}
	return rpc.MarkReadResult{ChatID: id}, nil
}
func markReadAPI(ctx context.Context, api *tg.Client, input tg.InputPeerClass) error {
	const allMessages = math.MaxInt32
	if channel, ok := peer.ToInputChannel(input); ok {
		_, err := api.ChannelsReadHistory(ctx, &tg.ChannelsReadHistoryRequest{Channel: channel, MaxID: allMessages})
		return mapError(err)
	}
	_, err := api.MessagesReadHistory(ctx, &tg.MessagesReadHistoryRequest{Peer: input, MaxID: allMessages})
	return mapError(err)
}

var _ = fmt.Sprint
