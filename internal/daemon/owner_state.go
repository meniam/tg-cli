package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type ownerStateTelegram interface {
	Folders(context.Context) (rpc.FoldersResult, error)
	ChatStates(context.Context, *chatref.Ref, int) (rpc.ChatStatesResult, error)
	Sessions(context.Context) (rpc.SessionsResult, error)
	Blocked(context.Context, rpc.BlockedParams) (rpc.BlockedResult, error)
	NotifyDefaults(context.Context) (rpc.NotifyDefaultsResult, error)
}

func (d *daemon) registerOwnerState() {
	d.handle(rpc.MethodFolderList, d.folderList)
	d.handle(rpc.MethodChatState, d.chatState)
	d.handle(rpc.MethodAccountSessions, d.accountSessions)
	d.handle(rpc.MethodAccountBlocked, d.accountBlocked)
	d.handle(rpc.MethodAccountNotify, d.accountNotify)
}
func filterFolderPeers(peers []rpc.FolderPeer, p access.Policy) []rpc.FolderPeer {
	out := make([]rpc.FolderPeer, 0, len(peers))
	for _, peer := range peers {
		if p.Allowed(peer.ID) {
			out = append(out, peer)
		}
	}
	return out
}
func filterFolders(folders []rpc.Folder, p access.Policy) []rpc.Folder {
	out := make([]rpc.Folder, 0, len(folders))
	for _, f := range folders {
		f.Pinned = filterFolderPeers(f.Pinned, p)
		f.Include = filterFolderPeers(f.Include, p)
		f.Exclude = filterFolderPeers(f.Exclude, p)
		out = append(out, f)
	}
	return out
}
func (d *daemon) folderList(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.LocalParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Local {
		return d.store.Folders(ctx, chatFilter(d.policy()))
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	result, err := d.opts.Telegram.Folders(ctx)
	if err != nil {
		return nil, err
	}
	result.Folders = filterFolders(result.Folders, d.policy())
	for i := range result.Folders {
		f := &result.Folders[i]
		for _, peers := range [][]rpc.FolderPeer{f.Pinned, f.Include, f.Exclude} {
			for j := range peers {
				if chat, err := d.store.GetChat(ctx, peers[j].ID); err == nil {
					peers[j].Title = chat.Title
				}
			}
		}
	}
	now := time.Now().Unix()
	result.SyncedAt = &now
	for i := range result.Folders {
		result.Folders[i].SyncedAt = &now
	}
	d.save(ctx, rpc.MethodFolderList, func(ctx context.Context) error {
		return d.store.ReplaceFolders(ctx, result.Folders, chatFilter(d.policy()))
	})
	result.Folders = filterFolders(result.Folders, d.policy())
	return result, nil
}
func (d *daemon) chatState(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.ChatStateParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidateChatStateParams(p); err != nil {
		return nil, err
	}
	limit := p.Limit
	if limit == 0 {
		limit = 100
	}
	if p.Local {
		if p.Chat == "" {
			return d.store.DialogStates(ctx, limit, chatFilter(d.policy()))
		}
		id, _, err := d.localChatID(ctx, p.Chat)
		if err != nil {
			return nil, err
		}
		s, err := d.store.DialogState(ctx, id, chatFilter(d.policy()))
		if errors.Is(err, store.ErrNotFound) {
			return rpc.ChatStatesResult{States: []rpc.ChatState{}}, nil
		}
		if err != nil {
			return nil, err
		}
		return rpc.ChatStatesResult{States: []rpc.ChatState{s}, SyncedAt: s.SyncedAt}, nil
	}
	var ref *chatref.Ref
	var chat rpc.Chat
	if p.Chat != "" {
		r, c, err := d.listChat(ctx, p.Chat)
		if err != nil {
			return nil, err
		}
		ref = &r
		chat = c
	} else if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	result, err := d.opts.Telegram.ChatStates(ctx, ref, limit)
	if err != nil {
		return nil, err
	}
	if ref != nil && !d.policy().Allowed(chat.ID) {
		return nil, listChatNotFound(*ref)
	}
	visible := make([]rpc.ChatState, 0, len(result.States))
	policy := d.policy()
	for _, s := range result.States {
		if policy.Allowed(s.ChatID) && (ref == nil || s.ChatID == chat.ID) {
			visible = append(visible, s)
		}
	}
	if ref != nil && len(visible) == 0 {
		return nil, listChatNotFound(*ref)
	}
	now := time.Now().Unix()
	result.SyncedAt = &now
	for i := range visible {
		visible[i].SyncedAt = &now
	}
	result.States = visible
	d.save(ctx, rpc.MethodChatState, func(ctx context.Context) error {
		return d.store.UpsertDialogStates(ctx, result.States, chatFilter(d.policy()))
	})
	policy = d.policy()
	visible = make([]rpc.ChatState, 0, len(result.States))
	for _, s := range result.States {
		if policy.Allowed(s.ChatID) {
			visible = append(visible, s)
		}
	}
	if ref != nil && len(visible) == 0 {
		return nil, listChatNotFound(*ref)
	}
	result.States = visible
	return result, nil
}
func (d *daemon) accountSessions(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.LocalParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Local {
		return d.store.Sessions(ctx)
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	r, err := d.opts.Telegram.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	if r.Sessions == nil {
		r.Sessions = []rpc.Session{}
	}
	now := time.Now().Unix()
	r.SyncedAt = &now
	d.save(ctx, rpc.MethodAccountSessions, func(ctx context.Context) error { return d.store.ReplaceSessions(ctx, r) })
	return r, nil
}
func (d *daemon) accountBlocked(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.BlockedParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if _, err := rpc.BlockedOffset(p); err != nil {
		return nil, err
	}
	if p.Limit == 0 {
		p.Limit = 100
	}
	if p.Local {
		return d.store.Blocked(ctx, p.Limit, chatFilter(d.policy()))
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	r, err := d.opts.Telegram.Blocked(ctx, p)
	if err != nil {
		return nil, err
	}
	visible := make([]rpc.BlockedPeer, 0, len(r.Peers))
	for _, peer := range r.Peers {
		if d.policy().Allowed(peer.PeerID) {
			visible = append(visible, peer)
		}
	}
	r.Peers = visible
	now := time.Now().Unix()
	r.SyncedAt = &now
	d.save(ctx, rpc.MethodAccountBlocked, func(ctx context.Context) error {
		if p.Cursor == "" && r.NextCursor == "" {
			return d.store.ReplaceBlocked(ctx, r.Peers, chatFilter(d.policy()))
		}
		return d.store.UpsertBlocked(ctx, r.Peers, chatFilter(d.policy()))
	})
	visible = make([]rpc.BlockedPeer, 0, len(r.Peers))
	for _, peer := range r.Peers {
		if d.policy().Allowed(peer.PeerID) {
			visible = append(visible, peer)
		}
	}
	r.Peers = visible
	return r, nil
}
func (d *daemon) accountNotify(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.LocalParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if p.Local {
		return d.store.NotifyDefaults(ctx)
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	r, err := d.opts.Telegram.NotifyDefaults(ctx)
	if err != nil {
		return nil, err
	}
	if r.Defaults == nil {
		r.Defaults = []rpc.NotifyDefault{}
	}
	now := time.Now().Unix()
	r.SyncedAt = &now
	for i := range r.Defaults {
		r.Defaults[i].SyncedAt = &now
	}
	d.save(ctx, rpc.MethodAccountNotify, func(ctx context.Context) error { return d.store.ReplaceNotifyDefaults(ctx, r.Defaults) })
	return r, nil
}
