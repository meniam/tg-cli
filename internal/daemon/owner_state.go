package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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
	d.handle(rpc.MethodAccountTerminate, d.terminateSession)
	d.handle(rpc.MethodFolderInviteInfo, d.folderInviteInfo)
	d.handle(rpc.MethodFolderOrder, d.folderOrder)
	for method, action := range map[string]string{rpc.MethodFolderCreate: "create", rpc.MethodFolderEdit: "edit", rpc.MethodFolderDelete: "delete"} {
		action := action
		d.handle(method, func(ctx context.Context, raw json.RawMessage) (any, error) { return d.folderWrite(ctx, raw, action) })
	}
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
	result, err := d.telegram(ctx).Folders(ctx)
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
	result, err := d.telegram(ctx).ChatStates(ctx, ref, limit)
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
		r, err := d.store.Sessions(ctx)
		if err != nil {
			return nil, err
		}
		setSessionIDs(&r)
		return r, nil
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	r, err := d.telegram(ctx).Sessions(ctx)
	if err != nil {
		return nil, err
	}
	if r.Sessions == nil {
		r.Sessions = []rpc.Session{}
	}
	setSessionIDs(&r)
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
	r, err := d.telegram(ctx).Blocked(ctx, p)
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
	r, err := d.telegram(ctx).NotifyDefaults(ctx)
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

func (d *daemon) folderInviteInfo(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.FolderInviteParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	slug, err := chatref.ParseChatlist(p.Link)
	if err != nil {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid chat folder invite link")
	}
	if !d.policy().InviteAllowed() {
		return nil, rpc.ErrChatNotFound()
	}
	if err = d.requireAccount(ctx); err != nil {
		return nil, err
	}
	result, err := d.telegram(ctx).FolderInviteInfo(ctx, slug)
	if err != nil {
		return nil, err
	}
	filtered := make([]rpc.FolderInviteChat, 0, len(result.Chats))
	for _, chat := range result.Chats {
		if d.policy().Allowed(chat.ID) {
			filtered = append(filtered, chat)
		}
	}
	result.Chats = filtered
	return result, nil
}

func (d *daemon) folderOrder(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.FolderOrderParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	if !d.policy().DialogsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("folder management requires dialogs: true in access.json"))
	}
	if err := d.telegram(ctx).OrderFolders(ctx, p.IDs, p.DryRun); err != nil {
		return nil, err
	}
	if !p.DryRun {
		if _, err := d.folderList(ctx, []byte(`{}`)); err != nil {
			d.log.Warn("folder refresh failed")
		}
	}
	return rpc.FolderOrderResult{IDs: p.IDs, DryRun: p.DryRun}, nil
}

func (d *daemon) folderWrite(ctx context.Context, raw json.RawMessage, action string) (any, error) {
	var p rpc.FolderWriteParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if action != "create" && p.ID < 2 {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid folder id")
	}
	if action == "delete" && !p.Yes && !p.DryRun {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "--yes is required")
	}
	if action == "create" && (p.Title == nil || strings.TrimSpace(*p.Title) == "") {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "folder title is required")
	}
	if action == "create" && len(p.Include) == 0 && len(p.Pin) == 0 {
		included := false
		if p.Rules != nil {
			for _, rule := range strings.Split(*p.Rules, ",") {
				switch rule {
				case "contacts", "non-contacts", "groups", "channels", "bots":
					included = true
				}
			}
		}
		if !included {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "folder needs an inclusion source")
		}
	}
	if p.Rules != nil {
		for _, rule := range strings.Split(*p.Rules, ",") {
			switch rule {
			case "", "contacts", "non-contacts", "groups", "channels", "bots", "exclude-muted", "exclude-read", "exclude-archived":
			default:
				return nil, rpc.NewError(rpc.CodeInvalidParams, "unknown folder rule")
			}
		}
	}
	for _, list := range []*[]string{&p.Include, &p.Add, &p.Remove, &p.Exclude, &p.Unexclude, &p.Pin, &p.Unpin} {
		for i, name := range *list {
			_, id, err := d.writeReference(ctx, name, false)
			if err != nil {
				return nil, err
			}
			(*list)[i] = strconv.FormatInt(id, 10)
		}
	}
	if err := d.requireAccount(ctx); err != nil {
		return nil, err
	}
	if !d.policy().DialogsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("folder management requires dialogs: true in access.json"))
	}
	var existing rpc.Folder
	hidden := 0
	if action == "delete" || action == "edit" {
		folders, err := d.telegram(ctx).Folders(ctx)
		if err != nil {
			return nil, err
		}
		found := false
		for _, f := range folders.Folders {
			if f.ID == p.ID && f.Kind == "filter" {
				existing = f
				found = true
				break
			}
		}
		if !found {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "folder does not exist or cannot be edited")
		}
		seen := map[int64]bool{}
		for _, peers := range [][]rpc.FolderPeer{existing.Pinned, existing.Include, existing.Exclude} {
			for _, peer := range peers {
				if !d.policy().Allowed(peer.ID) && !seen[peer.ID] {
					hidden++
					seen[peer.ID] = true
				}
			}
		}
	}
	for _, list := range [][]string{p.Include, p.Add, p.Remove, p.Exclude, p.Unexclude, p.Pin, p.Unpin} {
		for _, value := range list {
			id, _ := strconv.ParseInt(value, 10, 64)
			if !d.policy().Allowed(id) {
				return nil, rpc.ErrChatNotFound()
			}
		}
	}
	if !d.policy().DialogsAllowed() {
		return nil, rpc.NewError(rpc.CodeWriteDisabled, d.accessMessage("folder management requires dialogs: true in access.json"))
	}
	if action == "delete" && !p.DryRun {
		self, err := d.telegram(ctx).Self(ctx)
		if err != nil {
			return nil, err
		}
		if self == nil {
			return nil, rpc.NewError(rpc.CodeNotAuthorized, "not logged in")
		}
		record := map[string]any{"time": time.Now().UTC().Format(time.RFC3339), "account": self.ID, "tool": "tg", "phase": "intent", "action": "folder_delete", "folder": existing, "outcome": "pending"}
		if err = d.accountJournal(record); err != nil {
			return nil, err
		}
		out, err := d.telegram(ctx).WriteFolder(ctx, p, action)
		record["time"] = time.Now().UTC().Format(time.RFC3339)
		record["phase"] = "result"
		record["outcome"] = "success"
		if err != nil {
			record["outcome"] = "failed"
		}
		if e := d.accountJournal(record); e != nil {
			d.log.Error("account result journal failed")
		}
		if err != nil {
			return nil, err
		}
		if _, e := d.folderList(ctx, []byte(`{}`)); e != nil {
			d.log.Warn("folder refresh failed")
		}
		return rpc.FolderDeleteResult{ID: out.ID, Deleted: true}, nil
	}
	out, err := d.telegram(ctx).WriteFolder(ctx, p, action)
	if err != nil {
		return nil, err
	}
	out.Hidden = hidden
	if action == "delete" {
		return rpc.FolderDeleteResult{ID: out.ID, Deleted: true, DryRun: p.DryRun}, nil
	}
	if !p.DryRun {
		if _, e := d.folderList(ctx, []byte(`{}`)); e != nil {
			d.log.Warn("folder refresh failed")
		}
	}
	return out, nil
}
