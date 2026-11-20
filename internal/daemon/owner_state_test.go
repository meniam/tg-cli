package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"time"
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
func TestRefreshFoldersSavesSnapshot(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "user", Title: "Kept"}}); err != nil {
		t.Fatal(err)
	}
	f := &ownerFake{fakeTelegram: base, folders: rpc.FoldersResult{Folders: []rpc.Folder{{
		ID: 1, Kind: "filter", Title: "Work",
		Pinned:  []rpc.FolderPeer{{ID: 42}, {ID: 77}},
		Include: []rpc.FolderPeer{{ID: 43}},
		Exclude: []rpc.FolderPeer{{ID: 77}},
	}}}}
	d.opts.Telegram = f
	writeAccess(t, path, `{"block":[77]}`)
	if at, err := db.FoldersSyncedAt(ctx); err != nil || at != nil {
		t.Fatalf("snapshot before the refresh: %v %v", at, err)
	}
	before := time.Now().Unix()
	result, err := d.refreshFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Folders) != 1 || len(result.Folders[0].Pinned) != 1 || result.Folders[0].Pinned[0].Title != "Kept" || result.SyncedAt == nil {
		t.Fatalf("result %+v", result)
	}
	at, err := db.FoldersSyncedAt(ctx)
	if err != nil || at == nil || *at < before || *at > time.Now().Unix() {
		t.Fatalf("snapshot after the refresh: %v %v", at, err)
	}
	connection, err := sql.Open("sqlite", filepath.Join(filepath.Dir(path), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	var folders, links, closed int
	for query, target := range map[string]*int{
		`SELECT COUNT(*) FROM folders`:                         &folders,
		`SELECT COUNT(*) FROM folder_chats`:                    &links,
		`SELECT COUNT(*) FROM folder_chats WHERE chat_id = 77`: &closed,
	} {
		if err := connection.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if folders != 1 || links != 2 || closed != 0 {
		t.Fatalf("folders=%d folder_chats=%d closed chat rows=%d", folders, links, closed)
	}
}

func TestRefreshFoldersReportsStorageFailure(t *testing.T) {
	d, base, db, _ := accessTestDaemon(t)
	d.opts.Telegram = &ownerFake{fakeTelegram: base, folders: rpc.FoldersResult{Folders: []rpc.Folder{{ID: 1, Kind: "filter"}}}}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := d.refreshFolders(context.Background())
	if !errors.Is(err, errFoldersNotSaved) || len(result.Folders) != 1 {
		t.Fatalf("refresh with a closed database: %+v %v", result, err)
	}
	// The handler keeps its former behavior: the Telegram answer survives a storage failure.
	v, err := d.folderList(context.Background(), params(t, rpc.LocalParams{}))
	if err != nil || len(v.(rpc.FoldersResult).Folders) != 1 {
		t.Fatalf("folder.list with a closed database: %+v %v", v, err)
	}
}

func TestOwnerStateAccessAndLocal(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	ctx := context.Background()
	f := &ownerFake{fakeTelegram: base, folders: rpc.FoldersResult{Folders: []rpc.Folder{{Raw: json.RawMessage(`{"_":"dialogFilter","pinned_peers":[{"_":"inputPeerUser","user_id":77}]}`), ID: 1, Kind: "filter", Pinned: []rpc.FolderPeer{{ID: 42}, {ID: 77}}}}}, states: rpc.ChatStatesResult{States: []rpc.ChatState{{ChatID: 42}, {ChatID: 77}}}, blocked: rpc.BlockedResult{Peers: []rpc.BlockedPeer{{PeerID: 42}, {PeerID: 77}}}}
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
	encoded, err := json.Marshal(v)
	if err != nil || strings.Contains(string(encoded), `"user_id":77`) || strings.Contains(string(encoded), `"id":77`) {
		t.Fatalf("folder response=%s err=%v", encoded, err)
	}
	connection, err := sql.Open("sqlite", filepath.Join(filepath.Dir(path), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	var folderRaw, folderData string
	if err := connection.QueryRowContext(ctx, `SELECT raw,data FROM folders WHERE id=1`).Scan(&folderRaw, &folderData); err != nil || !strings.Contains(folderRaw, `"user_id":77`) || strings.Contains(folderData, `"id":77`) {
		t.Fatalf("folder raw=%s data=%s err=%v", folderRaw, folderData, err)
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
