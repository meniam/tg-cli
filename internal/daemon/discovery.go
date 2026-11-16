package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func (d *daemon) discoveryChat(ctx context.Context, value string) (chatref.Ref, error) {
	ref, err := parseChatNoInvite(value)
	if err != nil {
		return ref, err
	}
	if _, err := d.requireAllowedChat(ctx, ref); err != nil {
		return ref, err
	}
	return ref, nil
}
func discoveryLimit(limit int) (int, error) { return limitOrDefault(limit, 20, 100) }

func (d *daemon) messageSearch(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SearchMessagesParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Query) == "" && p.Media == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "search query or media filter is required")
	}
	if p.Media != "" && !map[string]bool{"photo": true, "video": true, "document": true, "voice": true, "audio": true, "link": true, "gif": true, "pinned": true}[p.Media] {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "unknown media filter")
	}
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return nil, err
	}
	p.Limit = limit
	ref, err := d.discoveryChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	result, err := d.telegram(ctx).SearchMessages(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	result.Messages = d.visibleMessages(result.Messages)
	d.saveMessages(ctx, rpc.MethodMessageSearch, result.Messages)
	return result, nil
}
func (d *daemon) messageSearchPublic(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.PublicSearchParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Query) == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "search query must not be empty")
	}
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return nil, err
	}
	p.Limit = limit
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	result, err := d.telegram(ctx).SearchPublic(ctx, p)
	if err != nil {
		return nil, err
	}
	policy := d.policy()
	hits := make([]rpc.PublicHit, 0, len(result.Hits))
	messages := make([]rpc.Message, 0, len(result.Hits))
	for _, hit := range result.Hits {
		if hit.ChatID == 0 || !policy.Allowed(hit.ChatID) || hit.Message.ChatID != hit.ChatID {
			continue
		}
		hits = append(hits, hit)
		messages = append(messages, hit.Message)
	}
	result.Hits = hits
	d.saveMessages(ctx, rpc.MethodMessageSearchPublic, messages)
	return result, nil
}
func (d *daemon) visibleMessages(messages []rpc.Message) []rpc.Message {
	policy := d.policy()
	visible := make([]rpc.Message, 0, len(messages))
	for _, message := range messages {
		if message.ChatID != 0 && policy.Allowed(message.ChatID) {
			visible = append(visible, message)
		}
	}
	return visible
}
func contextWindow(raw json.RawMessage, p *rpc.ContextParams) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return rpc.NewError(rpc.CodeInvalidParams, "invalid params")
	}
	if _, ok := fields["before"]; !ok {
		p.Before = 5
	}
	if _, ok := fields["after"]; !ok {
		p.After = 5
	}
	if p.ID <= 0 || p.Before < 0 || p.Before > 50 || p.After < 0 || p.After > 50 {
		return rpc.NewError(rpc.CodeInvalidParams, "id must be positive and before/after must be between 0 and 50")
	}
	return nil
}
func (d *daemon) messageContext(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ContextParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := contextWindow(raw, &p); err != nil {
		return nil, err
	}
	if p.Local {
		return d.localContext(ctx, p)
	}
	ref, err := d.discoveryChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	result, err := d.telegram(ctx).MessageContext(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	result.Messages = d.visibleMessages(result.Messages)
	d.saveMessages(ctx, rpc.MethodMessageContext, result.Messages)
	return result, nil
}
func (d *daemon) localContext(ctx context.Context, p rpc.ContextParams) (any, error) {
	id, _, err := d.localChatID(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	stored, err := d.store.MessagesAround(ctx, id, p.ID, p.Before, p.After)
	if errors.Is(err, store.ErrNotFound) {
		return nil, rpc.ErrChatNotFound()
	}
	if err != nil {
		return nil, err
	}
	result := rpc.MessagesResult{Messages: make([]rpc.Message, 0, len(stored))}
	for _, message := range stored {
		result.Messages = append(result.Messages, rpcStoredMessage(message))
	}
	return result, nil
}

// localChatID resolves a chat and checks access without a Telegram call.
func (d *daemon) localChatID(ctx context.Context, value string) (int64, chatref.Kind, error) {
	ref, err := parseChatNoInvite(value)
	if err != nil {
		return 0, 0, err
	}
	var id int64
	if ref.Kind == chatref.ID {
		id = ref.ID
	} else {
		var found bool
		id, found, err = d.resolveStoredReference(ctx, value, false, false)
		if err != nil {
			return 0, 0, err
		}
		if !found {
			return 0, 0, rpc.ErrChatNotFound()
		}
	}
	if !d.policy().Allowed(id) {
		if ref.Kind == chatref.ID {
			return 0, 0, rpc.ErrUnknownChatID()
		}
		return 0, 0, rpc.ErrChatNotFound()
	}
	return id, ref.Kind, nil
}
func rpcStoredMessage(m store.Message) rpc.Message {
	var out rpc.Message
	_ = json.Unmarshal(m.Raw, &out)
	out.ID, out.ChatID, out.Date, out.Text = m.ID, m.ChatID, time.Unix(m.Date, 0).UTC().Format(time.RFC3339), m.Text
	if m.Out != nil {
		out.Out = *m.Out
	}
	if m.Pinned != nil {
		out.Pinned = *m.Pinned
	}
	if m.ReplyTo != nil {
		out.ReplyTo = int(*m.ReplyTo)
	}
	if m.TopicID != nil {
		out.TopicID = int(*m.TopicID)
	}
	if m.MediaType != nil {
		out.MediaType = *m.MediaType
	}
	if m.Service != nil {
		out.Service = *m.Service
	}
	if m.EditedAt != nil {
		out.Edited = true
	}
	if m.SenderID != nil || m.SenderName != nil {
		out.Sender = &rpc.Sender{}
		if m.SenderID != nil {
			out.Sender.ID = *m.SenderID
		}
		if m.SenderName != nil {
			out.Sender.Name = *m.SenderName
		}
	}
	return out
}
func (d *daemon) messageComments(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.CommentsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "message id must be positive")
	}
	limit, err := discoveryLimit(p.Limit)
	if err != nil {
		return nil, err
	}
	p.Limit = limit
	ref, err := d.discoveryChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	result, err := d.telegram(ctx).Comments(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	result.Messages = d.visibleMessages(result.Messages)
	d.saveMessages(ctx, rpc.MethodMessageComments, result.Messages)
	return result, nil
}
func (d *daemon) messageLink(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.LinkParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.ID <= 0 || p.Topic < 0 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "id must be positive and topic must not be negative")
	}
	ref, err := d.discoveryChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	return d.telegram(ctx).MessageLink(ctx, ref, p)
}
func (d *daemon) chatMarkRead(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.MarkReadParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.MarkReadResult{ChatID: id, DryRun: true}, nil
	}
	result, err := d.telegram(ctx).MarkRead(ctx, ref)
	if err != nil {
		return nil, err
	}
	result.ChatID = id
	return result, nil
}
