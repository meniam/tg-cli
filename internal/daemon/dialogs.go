package daemon

import (
	"context"
	"encoding/json"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"time"
)

func (d *daemon) registerDialogs() {
	for _, method := range []string{rpc.MethodChatMute, rpc.MethodChatUnmute, rpc.MethodChatNotify} {
		d.handle(method, d.changeNotify)
	}
	for method, action := range map[string]string{rpc.MethodChatArchive: "archive", rpc.MethodChatUnarchive: "unarchive", rpc.MethodChatPinDialog: "pin", rpc.MethodChatUnpinDialog: "unpin"} {
		action := action
		d.handle(method, func(ctx context.Context, raw json.RawMessage) (any, error) { return d.changeDialog(ctx, raw, action) })
	}
}
func (d *daemon) changeDialog(ctx context.Context, raw json.RawMessage, action string) (any, error) {
	var p rpc.DialogParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	archive := action == "archive" || action == "unarchive"
	if len(p.Chats) < 1 || len(p.Chats) > 100 || (!archive && len(p.Chats) != 1) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid number of chats")
	}
	refs := make([]chatref.Ref, 0, len(p.Chats))
	ids := make([]int64, 0, len(p.Chats))
	for _, name := range p.Chats {
		_, id, err := d.writeReference(ctx, name, false)
		if err != nil {
			return nil, err
		}
		chat, err := d.telegram(ctx).Resolve(ctx, chatref.Ref{Kind: chatref.ID, ID: id})
		if err != nil {
			return nil, err
		}
		if chat.ID != id || !d.policy().Allowed(id) {
			return nil, rpc.ErrChatNotFound()
		}
		refs = append(refs, chatref.Ref{Kind: chatref.ID, ID: id})
		ids = append(ids, id)
	}
	if !d.policy().DialogsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("dialog management requires dialogs: true in access.json"))
	}
	result := rpc.DialogArchiveResult{Chats: make([]rpc.DialogItem, 0, len(ids)), DryRun: p.DryRun}
	for _, id := range ids {
		result.Chats = append(result.Chats, rpc.DialogItem{ChatID: id, Archived: action == "archive"})
	}
	pinResult := rpc.DialogPinResult{ChatID: ids[0], Pinned: action == "pin", DryRun: p.DryRun}
	if p.DryRun {
		if archive {
			return result, nil
		}
		return pinResult, nil
	}
	for _, id := range ids {
		if !d.policy().Allowed(id) {
			return nil, rpc.ErrChatNotFound()
		}
	}
	if !d.policy().DialogsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("dialog management requires dialogs: true in access.json"))
	}
	var err error
	if archive {
		err = d.telegram(ctx).ArchiveDialogs(ctx, refs, action == "archive")
	} else {
		err = d.telegram(ctx).PinDialog(ctx, refs[0], action == "pin")
	}
	if err != nil {
		return nil, err
	}
	states, e := d.telegram(ctx).RefreshDialogStates(ctx, refs)
	if e != nil {
		d.log.Warn("dialog state refresh failed")
	} else {
		d.save(ctx, rpc.MethodChatState, func(ctx context.Context) error {
			return d.store.UpsertDialogStates(ctx, states.States, chatFilter(d.policy()))
		})
	}
	if archive {
		return result, nil
	}
	return pinResult, nil
}

func (d *daemon) changeNotify(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.NotifyParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.MuteUntil == nil && p.Silent == nil && p.Previews == nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "a notification setting is required")
	}
	if p.MuteUntil != nil && (*p.MuteUntil < 0 || int64(*p.MuteUntil) > 2147483647 || (*p.MuteUntil != 0 && int64(*p.MuteUntil) <= time.Now().Unix())) {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "mute time must be in the future")
	}
	_, id, err := d.writeReference(ctx, p.Chat, false)
	if err != nil {
		return nil, err
	}
	if !d.policy().DialogsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("dialog management requires dialogs: true in access.json"))
	}
	ref := chatref.Ref{Kind: chatref.ID, ID: id}
	out, err := d.telegram(ctx).ChangeNotify(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	if !p.DryRun {
		states, e := d.telegram(ctx).RefreshDialogStates(ctx, []chatref.Ref{ref})
		if e != nil {
			d.log.Warn("dialog state refresh failed")
		} else {
			d.save(ctx, rpc.MethodChatState, func(ctx context.Context) error {
				return d.store.UpsertDialogStates(ctx, states.States, chatFilter(d.policy()))
			})
		}
	}
	return out, nil
}
