package daemon

import (
	"context"
	"errors"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func (f *fakeTelegram) Folders(context.Context) (rpc.FoldersResult, error) {
	return rpc.FoldersResult{Folders: []rpc.Folder{}}, nil
}
func (f *fakeTelegram) ChatStates(context.Context, *chatref.Ref, int) (rpc.ChatStatesResult, error) {
	return rpc.ChatStatesResult{States: []rpc.ChatState{}}, nil
}
func (f *fakeTelegram) Sessions(context.Context) (rpc.SessionsResult, error) {
	return rpc.SessionsResult{Sessions: []rpc.Session{}}, nil
}
func (f *fakeTelegram) Blocked(context.Context, rpc.BlockedParams) (rpc.BlockedResult, error) {
	return rpc.BlockedResult{Peers: []rpc.BlockedPeer{}}, nil
}
func (f *fakeTelegram) NotifyDefaults(context.Context) (rpc.NotifyDefaultsResult, error) {
	return rpc.NotifyDefaultsResult{Defaults: []rpc.NotifyDefault{}}, nil
}

type ownerFake struct {
	*fakeTelegram
	folders rpc.FoldersResult
	states  rpc.ChatStatesResult
	blocked rpc.BlockedResult
	calls   int
}

func (f *ownerFake) Folders(context.Context) (rpc.FoldersResult, error) {
	f.calls++
	return f.folders, nil
}
func (f *ownerFake) ChatStates(context.Context, *chatref.Ref, int) (rpc.ChatStatesResult, error) {
	f.calls++
	return f.states, nil
}
func (f *ownerFake) Blocked(context.Context, rpc.BlockedParams) (rpc.BlockedResult, error) {
	f.calls++
	return f.blocked, nil
}
func TestOwnerStateAccessAndLocal(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	ctx := context.Background()
	f := &ownerFake{fakeTelegram: base, folders: rpc.FoldersResult{Folders: []rpc.Folder{{ID: 1, Kind: "filter", Pinned: []rpc.FolderPeer{{ID: 42}, {ID: 77}}}}}, states: rpc.ChatStatesResult{States: []rpc.ChatState{{ChatID: 42}, {ChatID: 77}}}, blocked: rpc.BlockedResult{Peers: []rpc.BlockedPeer{{PeerID: 42}, {PeerID: 77}}}}
	d.opts.Telegram = f
	writeAccess(t, path, `{"allow":[42],"block":[77]}`)
	if _, err := d.chatState(ctx, params(t, rpc.ChatStateParams{Chat: "77"})); writeCode(t, err) != rpc.CodeNotFound || f.calls != 0 {
		t.Fatalf("closed request: %v, calls %d", err, f.calls)
	}
	v, err := d.folderList(ctx, params(t, rpc.LocalParams{}))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(rpc.FoldersResult); len(got.Folders) != 1 || len(got.Folders[0].Pinned) != 1 || got.Folders[0].Pinned[0].ID != 42 {
		t.Fatalf("folders %+v", got)
	}
	stored, err := db.Folders(ctx, store.ChatFilter{})
	if err != nil || len(stored.Folders) != 1 || len(stored.Folders[0].Pinned) != 1 {
		t.Fatalf("stored %+v %v", stored, err)
	}
	v, err = d.chatState(ctx, params(t, rpc.ChatStateParams{}))
	if err != nil || len(v.(rpc.ChatStatesResult).States) != 1 {
		t.Fatalf("states %+v %v", v, err)
	}
	if _, err = db.DialogState(ctx, 77, store.ChatFilter{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed state in database: %v", err)
	}
	v, err = d.accountBlocked(ctx, params(t, rpc.BlockedParams{}))
	if err != nil || len(v.(rpc.BlockedResult).Peers) != 1 {
		t.Fatalf("blocked %+v %v", v, err)
	}
	saved, err := db.Blocked(ctx, 100, store.ChatFilter{})
	if err != nil || len(saved.Peers) != 1 {
		t.Fatalf("saved %+v %v", saved, err)
	}
	f.folders = rpc.FoldersResult{Folders: []rpc.Folder{}}
	if _, err = d.folderList(ctx, params(t, rpc.LocalParams{})); err != nil {
		t.Fatal(err)
	}
	stored, err = db.Folders(ctx, store.ChatFilter{})
	if err != nil || len(stored.Folders) != 0 || stored.SyncedAt == nil {
		t.Fatalf("replacement %+v %v", stored, err)
	}
	f.folders = rpc.FoldersResult{Folders: []rpc.Folder{{ID: 1, Kind: "filter"}}}
	if _, err = d.folderList(ctx, params(t, rpc.LocalParams{})); err != nil {
		t.Fatal(err)
	}
	base.user = nil
	v, err = d.folderList(ctx, params(t, rpc.LocalParams{Local: true}))
	if err != nil || len(v.(rpc.FoldersResult).Folders) != 1 {
		t.Fatalf("offline %+v %v", v, err)
	}
	v, err = d.accountNotify(ctx, params(t, rpc.LocalParams{Local: true}))
	if err != nil || len(v.(rpc.NotifyDefaultsResult).Defaults) != 0 || v.(rpc.NotifyDefaultsResult).SyncedAt != nil {
		t.Fatalf("empty %+v %v", v, err)
	}
}
