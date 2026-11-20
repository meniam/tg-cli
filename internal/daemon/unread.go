package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type unreadTelegram interface {
	UnreadChats(context.Context, int, bool, func(int64) bool) (rpc.UnreadChatsResult, []rpc.ChatState, error)
	UnreadMessages(context.Context, chatref.Ref, rpc.MessageUnreadParams) (rpc.UnreadMessagesResult, *rpc.ChatState, error)
}

func (d *daemon) registerUnread() {
	d.handle(rpc.MethodChatUnread, d.chatUnread)
	d.handle(rpc.MethodMessageUnread, d.messageUnread)
	d.handle(rpc.MethodChatChanges, d.chatChanges)
}
func (d *daemon) chatChanges(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatChangesParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 20, 100)
	if err != nil {
		return nil, err
	}
	id, _, err := d.localChatID(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	out, err := d.store.ChatVersions(ctx, id, limit, chatFilter(d.policy()))
	if errors.Is(err, store.ErrNotFound) {
		return nil, rpc.ErrChatNotFound()
	}
	return out, err
}
func (d *daemon) chatUnread(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatUnreadParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 100, 500)
	if err != nil {
		return nil, err
	}
	if err = d.requireAccount(ctx); err != nil {
		return nil, err
	}
	policy := d.policy()
	out, states, err := d.telegram(ctx).UnreadChats(ctx, limit, p.Archived, policy.Allowed)
	if err != nil {
		return nil, err
	}
	visible := make([]rpc.UnreadChat, 0, len(out.Chats))
	chats := []rpc.Chat{}
	for _, v := range out.Chats {
		if d.policy().Allowed(v.ID) {
			visible = append(visible, v)
			chats = append(chats, rpc.Chat{Raw: v.Raw, ID: v.ID, Type: v.Type, Title: v.Title, Username: v.Username, Unread: v.Unread})
		}
	}
	out.Chats = visible
	d.saveChats(ctx, rpc.MethodChatUnread, chats, true)
	d.save(ctx, rpc.MethodChatUnread, func(ctx context.Context) error {
		return d.store.UpsertDialogStates(ctx, states, chatFilter(d.policy()))
	})
	return out, nil
}
func (d *daemon) messageUnread(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.MessageUnreadParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, 20, 100)
	if err != nil {
		return nil, err
	}
	p.Limit = limit
	if p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "topic must be a positive topic id")
	}
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	if ref.Kind == chatref.Username {
		id, found, lookupErr := d.resolveStoredReference(ctx, p.Chat, false, false)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if found && !d.policy().Allowed(id) {
			return nil, rpc.ErrChatNotFound()
		}
	}
	if _, err = d.requireAllowedChat(ctx, ref); err != nil {
		return nil, err
	}
	out, state, err := d.telegram(ctx).UnreadMessages(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(out.ChatID) {
		return nil, rpc.ErrChatNotFound()
	}
	d.saveMessages(ctx, rpc.MethodMessageUnread, out.Messages)
	if state != nil {
		d.save(ctx, rpc.MethodMessageUnread, func(ctx context.Context) error {
			return d.store.UpsertDialogStates(ctx, []rpc.ChatState{*state}, chatFilter(d.policy()))
		})
	}
	return out, nil
}
