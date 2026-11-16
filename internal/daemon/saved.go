package daemon

import (
	"context"
	"encoding/json"
	"strings"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (d *daemon) registerSaved() {
	d.handle(rpc.MethodSavedList, d.savedList)
	d.handle(rpc.MethodSavedTags, d.savedTags)
	d.handle(rpc.MethodSavedHistory, func(ctx context.Context, raw json.RawMessage) (any, error) { return d.savedMessages(ctx, raw, false) })
	d.handle(rpc.MethodSavedSearch, func(ctx context.Context, raw json.RawMessage) (any, error) { return d.savedMessages(ctx, raw, true) })
	d.handle(rpc.MethodSavedTagRename, d.savedTagRename)
}
func (d *daemon) savedOwner(ctx context.Context) (int64, error) {
	if err := d.requireAccount(ctx); err != nil {
		return 0, err
	}
	self, err := d.telegram(ctx).Self(ctx)
	if err != nil {
		return 0, err
	}
	if self == nil {
		return 0, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
	}
	if !d.policy().Allowed(self.ID) {
		return 0, rpc.ErrChatNotFound()
	}
	return self.ID, nil
}
func (d *daemon) savedTags(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SavedTagsParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	owner, err := d.savedOwner(ctx)
	if err != nil {
		return nil, err
	}
	var ref *chatref.Ref
	if p.Peer != "" {
		_, id, err := d.writeReference(ctx, p.Peer, false)
		if err != nil {
			return nil, err
		}
		r := chatref.Ref{Kind: chatref.ID, ID: id}
		ref = &r
	}
	result, err := d.telegram(ctx).SavedTags(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(owner) || (ref != nil && !d.policy().Allowed(ref.ID)) {
		return nil, rpc.ErrChatNotFound()
	}
	return result, nil
}
func (d *daemon) savedTagRename(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SavedTagRenameParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Emoji) == "" || (p.Title == nil) == !p.Clear || (p.Title != nil && strings.TrimSpace(*p.Title) == "") {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "provide an emoji and exactly one of title or clear")
	}
	id, err := d.savedOwner(ctx)
	if err != nil {
		return nil, err
	}
	if !d.policy().WriteAllowed(id) {
		return nil, rpc.ErrWriteDisabled()
	}
	result := rpc.SavedTagRenameResult{Emoji: p.Emoji, Title: p.Title, Cleared: p.Clear, DryRun: p.DryRun}
	if p.DryRun {
		return result, nil
	}
	if !d.policy().WriteAllowed(id) {
		return nil, rpc.ErrWriteDisabled()
	}
	if err := d.telegram(ctx).RenameSavedTag(ctx, p); err != nil {
		return nil, err
	}
	return result, nil
}

func (d *daemon) savedMessages(ctx context.Context, raw json.RawMessage, search bool) (any, error) {
	var p rpc.SavedMessagesParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Limit < 0 || p.Limit > 100 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "limit must be 1 to 100")
	}
	if search && strings.TrimSpace(p.Query) == "" && strings.TrimSpace(p.Tag) == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "search query or tag is required")
	}
	if !search && p.Peer == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "peer is required")
	}
	owner, err := d.savedOwner(ctx)
	if err != nil {
		return nil, err
	}
	var ref *chatref.Ref
	if p.Peer != "" {
		_, id, e := d.writeReference(ctx, p.Peer, false)
		if e != nil {
			return nil, e
		}
		r := chatref.Ref{Kind: chatref.ID, ID: id}
		ref = &r
	}
	result, err := d.telegram(ctx).SavedMessages(ctx, owner, ref, p, search)
	if err != nil {
		return nil, err
	}
	if !d.policy().Allowed(owner) {
		return nil, rpc.ErrChatNotFound()
	}
	if ref != nil && !d.policy().Allowed(ref.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	visible := make([]rpc.Message, 0, len(result.Messages))
	for _, message := range result.Messages {
		if !(ref == nil && message.SavedSourceUnknown && d.policy().Enabled()) && (message.SavedSourceID == 0 || d.policy().Allowed(message.SavedSourceID)) {
			visible = append(visible, message)
		}
	}
	result.Messages = visible
	d.saveMessages(ctx, rpc.MethodSavedHistory, result.Messages)
	return result, nil
}

func (d *daemon) savedList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.SavedListParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Limit < 0 || p.Limit > 100 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "limit must be 1 to 100")
	}
	owner, err := d.savedOwner(ctx)
	if err != nil {
		return nil, err
	}
	for scanned := 0; scanned < 100; scanned++ {
		out, err := d.telegram(ctx).SavedList(ctx, p)
		if err != nil {
			return nil, err
		}
		if !d.policy().Allowed(owner) {
			return nil, rpc.ErrChatNotFound()
		}
		items := make([]rpc.SavedDialog, 0, len(out.Dialogs))
		for _, v := range out.Dialogs {
			if d.policy().Allowed(v.PeerID) {
				items = append(items, v)
			}
		}
		out.Dialogs = items
		if out.CursorPeerID != 0 && !d.policy().Allowed(out.CursorPeerID) {
			if len(items) > 0 {
				out.NextCursor = items[len(items)-1].Cursor
				return out, nil
			}
			if out.NextCursor != "" && out.NextCursor != p.Cursor {
				p.Cursor = out.NextCursor
				continue
			}
			out.NextCursor = ""
		}
		return out, nil
	}
	return nil, rpc.NewError(rpc.CodeTelegram, "too many inaccessible saved dialogs")
}
