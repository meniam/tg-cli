package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

// chatLimitEvery is the wait between two cleanups of limited chats. A cleanup
// also runs once at start and right after a limit is set.
const chatLimitEvery = time.Hour

func (d *daemon) registerChatLimit() {
	d.handle(rpc.MethodChatLimitSet, d.chatLimitSet)
	d.handle(rpc.MethodChatLimitOff, d.chatLimitOff)
	d.handle(rpc.MethodChatLimitList, d.chatLimitList)
}

// kickChatLimits asks the cleanup loop for a pass now. It never blocks.
func (d *daemon) kickChatLimits() {
	select {
	case d.chatLimitKick <- struct{}{}:
	default:
	}
}

// runChatLimits trims the limited chats at start, every interval and whenever a
// limit is set, until ctx ends.
func (d *daemon) runChatLimits(ctx context.Context, every time.Duration) {
	d.enforceChatLimits(ctx)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-d.chatLimitKick:
		}
		d.enforceChatLimits(ctx)
	}
}

// enforceChatLimits cuts every limited chat that holds more messages than its
// limit down to 95% of it and returns how many messages it removed. A chat that
// the access lists close is left alone. A trimmed chat counts as completely
// loaded, so a later history load does not fetch the deleted messages again.
func (d *daemon) enforceChatLimits(ctx context.Context) int {
	if d.store == nil {
		return 0
	}
	limits, err := d.store.ListChatMessageLimits(ctx)
	if err != nil {
		if ctx.Err() == nil {
			d.log.Warn("chat limit cleanup could not read the limits")
		}
		return 0
	}
	policy := d.policy()
	total := 0
	for _, entry := range limits {
		if ctx.Err() != nil {
			break
		}
		if !policy.Allowed(entry.ChatID) {
			continue
		}
		removed, err := d.store.TrimChatMessages(ctx, entry.ChatID, entry.Limit)
		total += removed
		if removed > 0 {
			if _, markErr := d.store.MarkHistoryLimitReached(ctx, entry.ChatID); markErr != nil && ctx.Err() == nil {
				d.log.Warn("chat limit cleanup could not mark the chat loaded", "chat", entry.ChatID)
			}
			d.log.Info("chat limit cleanup", "chat", entry.ChatID, "removed", removed)
		}
		if err != nil && ctx.Err() == nil {
			d.log.Warn("chat limit cleanup failed", "chat", entry.ChatID)
		}
	}
	if total > 0 {
		// The index drops the fragments of messages that are gone.
		d.triggerSemanticIndex()
	}
	return total
}

func (d *daemon) chatLimitSet(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatLimitSetParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "give a chat")
	}
	chatID, err := d.voiceTarget(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	// The chat comes first: a closed chat answers the same whatever the number is.
	if p.Messages < store.MinChatMessageLimit || p.Messages > store.MaxChatMessageLimit {
		return nil, rpc.NewError(rpc.CodeInvalidParams, fmt.Sprintf("messages must be between %d and %d", store.MinChatMessageLimit, store.MaxChatMessageLimit))
	}
	saved, err := d.store.CountChatMessages(ctx, chatID)
	if err != nil {
		return nil, err
	}
	res := rpc.ChatLimitSetResult{ChatID: chatID, Messages: p.Messages, Keep: store.ChatLimitKeep(p.Messages), Saved: saved}
	if saved > p.Messages {
		res.WillDelete = saved - res.Keep
	}
	if res.WillDelete > 0 && !p.Confirm {
		return nil, &rpc.Error{
			Code:    rpc.CodeInvalidParams,
			Message: fmt.Sprintf("the chat holds %d messages: the cleanup would delete the %d oldest of them for good; repeat with --yes to confirm", saved, res.WillDelete),
			Data:    map[string]any{"reason": "confirm_required", "saved": saved, "will_delete": res.WillDelete},
		}
	}
	if err := d.store.SetChatMessageLimit(ctx, chatID, p.Messages); err != nil {
		return nil, err
	}
	d.kickChatLimits()
	return res, nil
}

func (d *daemon) chatLimitOff(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatLimitOffParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if p.Chat == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "give a chat")
	}
	chatID, err := d.voiceTarget(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	removed, err := d.store.ClearChatMessageLimit(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return rpc.ChatLimitOffResult{ChatID: chatID, Removed: removed}, nil
}

func (d *daemon) chatLimitList(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.ChatLimitListParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	limits, err := d.store.ListChatMessageLimits(ctx)
	if err != nil {
		return nil, err
	}
	policy := d.policy()
	res := rpc.ChatLimitListResult{Chats: []rpc.ChatLimitEntry{}}
	for _, entry := range limits {
		if !policy.Allowed(entry.ChatID) {
			continue
		}
		saved, err := d.store.CountChatMessages(ctx, entry.ChatID)
		if err != nil {
			return nil, err
		}
		item := rpc.ChatLimitEntry{ChatID: entry.ChatID, Messages: entry.Limit, Saved: saved}
		chat, err := d.store.GetChat(ctx, entry.ChatID)
		switch {
		case err == nil:
			item.Title = chat.Title
			if chat.Username != nil {
				item.Username = *chat.Username
			}
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
		res.Chats = append(res.Chats, item)
	}
	return res, nil
}

// historyLimitFor is historyLimit with the message limit of the chat in place of
// the global one when the chat has its own.
func (d *daemon) historyLimitFor(ctx context.Context, chatID int64) historyLimit {
	limit := d.historyLimit()
	if own, found, err := d.store.ChatMessageLimit(ctx, chatID); err == nil && found {
		limit.Messages = own
	}
	return limit
}
