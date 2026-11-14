package daemon

import (
	"context"
	"encoding/json"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// listChat checks access before a list request and keeps the resolved ID for
// another policy check after Telegram has answered.
func (d *daemon) listChat(ctx context.Context, value string) (chatref.Ref, rpc.Chat, error) {
	ref, err := parseChatNoInvite(value)
	if err != nil {
		return chatref.Ref{}, rpc.Chat{}, err
	}
	resolved, err := d.requireAllowedChat(ctx, ref)
	if err != nil {
		return chatref.Ref{}, rpc.Chat{}, err
	}
	var chat rpc.Chat
	if resolved != nil {
		chat = *resolved
	} else {
		chat, err = d.opts.Telegram.Resolve(ctx, ref)
		if err != nil {
			return chatref.Ref{}, rpc.Chat{}, err
		}
	}
	if !d.policy().Allowed(chat.ID) {
		return chatref.Ref{}, rpc.Chat{}, listChatNotFound(ref)
	}
	return ref, chat, nil
}

func listChatNotFound(ref chatref.Ref) error {
	if ref.Kind == chatref.ID {
		return rpc.ErrUnknownChatID()
	}
	return rpc.ErrChatNotFound()
}

func (d *daemon) chatMembers(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.MembersParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 50, 200)
	if err != nil {
		return nil, err
	}
	if p.Offset < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "offset must not be negative")
	}
	if p.Filter != "" && p.Filter != "recent" && p.Filter != "admins" && p.Filter != "bots" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "filter must be recent, admins, or bots")
	}
	p.Limit = limit
	ref, chat, err := d.listChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	if chat.Type == "user" || chat.Type == "bot" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "members require a group, supergroup, or channel")
	}
	result, err := d.opts.Telegram.Members(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, listChatNotFound(ref)
	}
	if result.Members == nil {
		result.Members = []rpc.Member{}
	}
	d.saveMembers(ctx, result.Members)
	return result, nil
}

func (d *daemon) chatTopics(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.TopicsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 50, 100)
	if err != nil {
		return nil, err
	}
	p.Limit = limit
	ref, chat, err := d.listChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	if chat.Type == "user" || chat.Type == "bot" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "topics require a forum chat")
	}
	result, err := d.opts.Telegram.Topics(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(chat.ID) {
		return nil, listChatNotFound(ref)
	}
	if result.Topics == nil {
		result.Topics = []rpc.Topic{}
	}
	return result, nil
}

func (d *daemon) chatCommon(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.CommonChatsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 50, 100)
	if err != nil {
		return nil, err
	}
	p.Limit = limit
	ref, err := parseChatNoInvite(p.User)
	if err != nil {
		return nil, err
	}
	if ref.Kind != chatref.Username && ref.Kind != chatref.ID {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "common chats require a username or positive user id")
	}
	if ref.Kind == chatref.ID && ref.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "a person needs a positive user id")
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	if ref.Kind == chatref.ID && !d.policy().Allowed(ref.ID) {
		return nil, rpc.ErrUnknownChatID()
	}
	id, found, err := d.resolveStoredReference(ctx, p.User, true, true)
	if err != nil {
		return nil, err
	}
	if !found || id <= 0 {
		return nil, rpc.NewError(rpc.CodeNotFound, "not found")
	}
	// Resolve also supplies the person fields that CommonChatsResult does not carry.
	user, err := d.opts.Telegram.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(user.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	if user.ID != id || (user.Type != "user" && user.Type != "bot") {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "common chats require a user or bot")
	}
	result := rpc.CommonChatsResult{Chats: []rpc.CommonChat{}}
	for range 10 {
		page, err := d.opts.Telegram.CommonChats(ctx, ref, p)
		if err != nil {
			return nil, err
		}
		p.Cursor = page.NextCursor
		result.NextCursor = page.NextCursor
		policy := d.policy()
		for _, chat := range page.Chats {
			if policy.Allowed(chat.ID) {
				result.Chats = append(result.Chats, chat)
			}
		}
		if len(result.Chats) > 0 || page.NextCursor == "" {
			break
		}
	}
	if !d.policy().Allowed(user.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	visible := result.Chats[:0]
	policy := d.policy()
	for _, chat := range result.Chats {
		if policy.Allowed(chat.ID) {
			visible = append(visible, chat)
		}
	}
	result.Chats = visible
	// A remaining cursor signals that a capped empty page may be continued.
	if len(result.Chats) > 0 {
		chats := make([]rpc.Chat, 0, len(result.Chats))
		for _, chat := range result.Chats {
			chats = append(chats, rpc.Chat{ID: chat.ID, Type: chat.Type, Title: chat.Title, Username: chat.Username})
		}
		d.saveChats(ctx, rpc.MethodChatCommon, chats, false)
	}
	if person, ok := chatPerson(user); ok {
		d.savePerson(ctx, rpc.MethodChatCommon, person)
	}
	return result, nil
}
