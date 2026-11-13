package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

const (
	defaultHistoryLimit = 20
	maxHistoryLimit     = 100
	defaultPinnedLimit  = 20
	maxPinnedLimit      = 100
	// maxPageBytes cuts a page of messages well below the frame limit of the
	// protocol (1 MiB), leaving room for the envelope and for one more message.
	maxPageBytes = 768 << 10
)

var chatTypes = map[string]bool{"user": true, "bot": true, "group": true, "supergroup": true, "channel": true}

// parseChat turns the chat reference of a request into a chatref.Ref.
func parseChat(chat string) (chatref.Ref, error) {
	ref, err := chatref.Parse(chat)
	if err != nil {
		return chatref.Ref{}, rpc.NewError(rpc.CodeInvalidParams, err.Error())
	}
	return ref, nil
}

// parseChatNoInvite is parseChat for requests that need an existing chat: an
// invite link points at a chat the account has not joined.
func parseChatNoInvite(chat string) (chatref.Ref, error) {
	ref, err := parseChat(chat)
	if err != nil {
		return ref, err
	}
	if ref.Kind == chatref.Invite {
		return ref, rpc.NewError(rpc.CodeInvalidParams, "an invite link points at a chat you have not joined: use chat invite-info")
	}
	return ref, nil
}

func (d *daemon) chatResolve(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatRefParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	chat, err := d.opts.Telegram.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	d.saveChats(ctx, rpc.MethodChatResolve, []rpc.Chat{chat}, false)
	return chat, nil
}

func (d *daemon) chatInfo(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatRefParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	info, err := d.opts.Telegram.ChatInfo(ctx, ref)
	if err != nil {
		return nil, err
	}
	d.saveChatInfo(ctx, info)
	return info, nil
}

func (d *daemon) chatInviteInfo(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatRefParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ref, err := parseChat(p.Chat)
	if err != nil {
		return nil, err
	}
	if ref.Kind != chatref.Invite {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "this is not an invite link: use chat info")
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	return d.opts.Telegram.InviteInfo(ctx, ref)
}

// parseTime reads an RFC 3339 time; an empty string is the zero time.
func parseTime(name, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, rpc.NewError(rpc.CodeInvalidParams, fmt.Sprintf("%s must be an RFC 3339 time, like 2026-01-31T10:00:00Z", name))
	}
	return t, nil
}

func (d *daemon) messageHistory(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.HistoryParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, defaultHistoryLimit, maxHistoryLimit)
	if err != nil {
		return nil, err
	}
	since, err := parseTime("since", p.Since)
	if err != nil {
		return nil, err
	}
	until, err := parseTime("until", p.Until)
	if err != nil {
		return nil, err
	}
	if !since.IsZero() && !until.IsZero() && since.After(until) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "since must not be later than until")
	}
	if p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "topic must be a positive topic id")
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	res, err := d.opts.Telegram.History(ctx, ref, rpc.HistoryQuery{
		Limit: limit, Since: since, Until: until, Topic: p.Topic, Cursor: p.Cursor, MaxBytes: maxPageBytes,
	})
	if err != nil {
		return nil, err
	}
	if res.Messages == nil {
		res.Messages = []rpc.Message{}
	}
	d.saveMessages(ctx, rpc.MethodMessageHistory, res.Messages)
	return res, nil
}

func (d *daemon) messageGet(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.MessageGetParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "id must be a positive message id")
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	message, err := d.opts.Telegram.Message(ctx, ref, p.ID)
	if err != nil {
		return nil, err
	}
	d.saveMessages(ctx, rpc.MethodMessageGet, []rpc.Message{message})
	return message, nil
}

func (d *daemon) messagePinned(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.PinnedParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	ref, err := parseChatNoInvite(p.Chat)
	if err != nil {
		return nil, err
	}
	limit, err := limitOrDefault(p.Limit, defaultPinnedLimit, maxPinnedLimit)
	if err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	res, err := d.opts.Telegram.Pinned(ctx, ref, limit)
	if err != nil {
		return nil, err
	}
	if res.Messages == nil {
		res.Messages = []rpc.Message{}
	}
	d.saveMessages(ctx, rpc.MethodMessagePinned, res.Messages)
	return res, nil
}

// limitOrDefault applies the default to an absent limit and rejects values
// outside 1..max.
func limitOrDefault(limit, def, max int) (int, error) {
	switch {
	case limit == 0:
		return def, nil
	case limit < 0 || limit > max:
		return 0, rpc.NewError(rpc.CodeInvalidParams, fmt.Sprintf("limit must be between 1 and %d", max))
	}
	return limit, nil
}
