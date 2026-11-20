package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"strconv"
	"tg-cli/internal/store"

	"tg-cli/internal/rpc"
)

func (d *daemon) registerWeb() {
	d.handle(rpc.MethodMediaThumb, d.webThumb)
	d.handle(rpc.MethodWebThumbEnabled, d.webThumbEnabled)
	d.handle(rpc.MethodWebFolders, d.webFolders)
	d.handle(rpc.MethodWebHistory, d.webHistory)
	d.handle(rpc.MethodWebContext, d.webContext)
	d.handle(rpc.MethodWebChat, d.webChat)
	d.handle(rpc.MethodWebAccess, d.webAccess)
	d.handle(rpc.MethodWebChatSettings, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return d.webChatSettings(ctx, raw, false)
	})
	d.handle(rpc.MethodWebSetChatSettings, func(ctx context.Context, raw json.RawMessage) (any, error) {
		return d.webChatSettings(ctx, raw, true)
	})

}

func (d *daemon) webFolders(ctx context.Context, _ json.RawMessage) (any, error) {
	return d.store.Folders(ctx, chatFilter(d.policy()))
}

func (d *daemon) webContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ContextParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := contextWindow(raw, &p); err != nil {
		return nil, err
	}
	chat, err := d.webStoredChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	rows, err := d.store.MessagesAround(ctx, chat.ID, p.ID, p.Before, p.After)
	if errors.Is(err, store.ErrNotFound) {
		return nil, rpc.ErrChatNotFound()
	}
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	out := rpc.WebMessages{Messages: make([]rpc.WebMessage, 0, len(rows))}
	for _, row := range rows {
		out.Messages = append(out.Messages, webMessage(row, chat))
	}
	return out, nil
}

func (d *daemon) webHistory(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.HistoryParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 40, 100)
	if err != nil {
		return nil, err
	}
	chat, err := d.webStoredChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	var before int
	if p.Cursor != "" {
		before, err = strconv.Atoi(p.Cursor)
		if err != nil || before < 1 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid history cursor")
		}
	}
	if p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "topic must not be negative")
	}
	messages, err := d.store.WebHistory(ctx, chat.ID, before, p.Topic, limit+1)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	result := rpc.WebMessages{Messages: make([]rpc.WebMessage, 0, min(limit, len(messages)))}
	size := len(`{"messages":[],"next_cursor":"9223372036854775807"}`)
	for i, message := range messages[:min(limit, len(messages))] {
		item := webMessage(message, chat)
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		if size+len(encoded)+1 > maxPageBytes {
			if len(result.Messages) == 0 {
				return nil, rpc.NewError(rpc.CodeInternal, "message exceeds response size")
			}
			break
		}
		size += len(encoded) + 1
		result.Messages = append(result.Messages, item)
		if i+1 < len(messages) {
			result.NextCursor = strconv.Itoa(message.ID)
		} else {
			result.NextCursor = ""
		}
	}
	return result, nil
}

func (d *daemon) webStoredChat(ctx context.Context, ref string) (store.Chat, error) {
	id, _, err := d.localChatID(ctx, ref)
	if err != nil {
		return store.Chat{}, err
	}
	chat, err := d.store.GetChat(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Chat{}, rpc.ErrChatNotFound()
	}
	return chat, err
}

func (d *daemon) webChat(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.WebChatSettings
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	chat, err := d.webStoredChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	out := rpc.WebChat{Title: chat.Title, Username: chat.Username, About: chat.About, Members: chat.Members}
	pinned, err := d.store.WebPinned(ctx, chat.ID)
	if err != nil {
		return nil, err
	}
	for _, row := range pinned {
		out.Pinned = append(out.Pinned, rpcStoredMessage(row))
	}
	if chat.Forum != nil && *chat.Forum {
		out.Topics, err = d.store.WebTopics(ctx, chat.ID)
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	return out, err
}

func (d *daemon) webChatSettings(ctx context.Context, raw json.RawMessage, write bool) (any, error) {
	var p rpc.WebChatSettings
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	chat, err := d.webStoredChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	if write {
		switch p.HistoryMode {
		case "auto", "skip", "first":
		default:
			return nil, rpc.NewError(rpc.CodeInvalidParams, "history mode must be auto, skip or first")
		}
		if err := d.store.SetHistoryMode(ctx, chat.ID, p.HistoryMode); err != nil {
			return nil, err
		}
	}
	mode, err := d.store.HistoryMode(ctx, chat.ID)
	return rpc.WebChatSettings{Chat: strconv.FormatInt(chat.ID, 10), HistoryMode: mode}, err
}

type webRawMessage struct {
	NoForwards bool `json:"noforwards"`
	Entities   []struct {
		Kind   string `json:"_"`
		Offset int
		Length int
		URL    string
	}
	Forward   json.RawMessage `json:"fwd_from"`
	Reactions struct {
		Results []struct {
			Count    int
			Reaction struct {
				Emoticon string
			}
		}
	}
	Media struct {
		TTLSeconds int `json:"ttl_seconds"`
		Photo      struct {
			Sizes []webPhotoSize
		}
		Document struct {
			Size       int64
			Attributes []struct {
				Name string `json:"file_name"`
			}
			Thumbs []webPhotoSize
		}
	}
}

type webPhotoSize struct {
	Kind  string `json:"_"`
	Bytes []byte
}

func webMessage(row store.Message, chat store.Chat) rpc.WebMessage {
	out := rpc.WebMessage{Message: rpcStoredMessage(row)}
	if chat.ID != 0 {
		username := ""
		if chat.Username != nil {
			username = *chat.Username
		}
		out.Link, _ = store.MessageLink(chat.ID, int64(row.ID), chat.Type, username, row.TopicID)
	}
	var raw webRawMessage
	_ = json.Unmarshal(row.Raw, &raw)
	for _, e := range raw.Entities {
		kind := strings.ToLower(strings.TrimPrefix(e.Kind, "messageEntity"))
		switch kind {
		case "bold", "italic", "code", "pre", "blockquote", "underline", "strike", "url", "texturl":
			out.Entities = append(out.Entities, rpc.WebEntity{
				Type: strings.Replace(kind, "texturl", "text_url", 1), URL: e.URL, Offset: e.Offset, Length: e.Length,
			})
		}
	}
	if len(raw.Forward) > 0 && string(raw.Forward) != "null" {
		out.Forwarded = true
	}
	for _, reaction := range raw.Reactions.Results {
		if reaction.Reaction.Emoticon != "" {
			out.Reactions = append(out.Reactions, rpc.ReactionCount{Emoji: reaction.Reaction.Emoticon, Count: reaction.Count})
		}
	}
	for _, item := range raw.Media.Document.Attributes {
		if item.Name != "" {
			out.MediaName = item.Name
			break
		}
	}
	out.MediaSize = raw.Media.Document.Size
	return out
}

func (d *daemon) webAccess(_ context.Context, _ json.RawMessage) (any, error) {
	p := d.policy()
	return rpc.WebAccessResult{
		Closed: p.Closed(), Allow: p.AllowCount(), Block: p.BlockCount(),
		Write: p.WriteCount(), Admin: p.AdminCount(), Dialogs: p.DialogsAllowed(),
		Sessions: p.SessionsAllowed(), Create: p.CreateAllowed(), Profile: p.ProfileAllowed(),
		Files: p.FileRoots(), Downloads: p.DownloadRoots(),
	}, nil
}
