package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
)

func TestOwnerStateSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	filter := ChatFilter{Block: []int64{2}}
	folders := []rpc.Folder{{ID: 1, Kind: "filter", Title: "x", Pinned: []rpc.FolderPeer{{ID: 1}, {ID: 2}}, Include: []rpc.FolderPeer{{ID: 1}}, Exclude: []rpc.FolderPeer{}}}
	if err = db.ReplaceFolders(ctx, folders, filter); err != nil {
		t.Fatal(err)
	}
	got, err := db.Folders(ctx, filter)
	if err != nil || len(got.Folders) != 1 || len(got.Folders[0].Pinned) != 1 || len(got.Folders[0].Include) != 1 {
		t.Fatalf("folders=%+v %v", got, err)
	}
	var raw string
	if err = db.read.QueryRow(`SELECT data FROM folders`).Scan(&raw); err != nil || strings.Contains(raw, "access_hash") || strings.Contains(raw, `"id":2`) {
		t.Fatalf("raw=%s %v", raw, err)
	}
	if err = db.ReplaceFolders(ctx, nil, filter); err != nil {
		t.Fatal(err)
	}
	got, err = db.Folders(ctx, filter)
	if err != nil || len(got.Folders) != 0 || got.SyncedAt == nil {
		t.Fatalf("replacement=%+v %v", got, err)
	}
	if err = db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 1, Unread: 4}, {ChatID: 2}}, filter); err != nil {
		t.Fatal(err)
	}
	if err = db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 1, Unread: 8}}, filter); err != nil {
		t.Fatal(err)
	}
	s, err := db.DialogState(ctx, 1, filter)
	if err != nil || s.Unread != 8 {
		t.Fatalf("state=%+v %v", s, err)
	}
	if _, err = db.DialogState(ctx, 2, filter); err != ErrNotFound {
		t.Fatal(err)
	}
	if err = db.ReplaceSessions(ctx, rpc.SessionsResult{AuthorizationTTLDays: 30, Sessions: []rpc.Session{{Hash: 123, DeviceModel: "test"}}}); err != nil {
		t.Fatal(err)
	}
	ss, err := db.Sessions(ctx)
	if err != nil || len(ss.Sessions) != 1 || ss.Sessions[0].Hash != 123 || ss.AuthorizationTTLDays != 30 {
		t.Fatalf("sessions=%+v %v", ss, err)
	}
	b, _ := json.Marshal(ss)
	if strings.Contains(string(b), "123") {
		t.Fatalf("hash leaked: %s", b)
	}
	if err = db.ReplaceSessions(ctx, rpc.SessionsResult{AuthorizationTTLDays: 7}); err != nil {
		t.Fatal(err)
	}
	ss, err = db.Sessions(ctx)
	if err != nil || len(ss.Sessions) != 0 || ss.SyncedAt == nil || ss.AuthorizationTTLDays != 7 {
		t.Fatalf("empty=%+v %v", ss, err)
	}
	if err = db.ReplaceBlocked(ctx, []rpc.BlockedPeer{{PeerID: 1}, {PeerID: 2}}, filter); err != nil {
		t.Fatal(err)
	}
	if err = db.UpsertBlocked(ctx, []rpc.BlockedPeer{{PeerID: 3}}, filter); err != nil {
		t.Fatal(err)
	}
	bs, err := db.Blocked(ctx, 10, filter)
	if err != nil || len(bs.Peers) != 2 {
		t.Fatalf("blocked=%+v %v", bs, err)
	}
	if err = db.ReplaceBlocked(ctx, nil, filter); err != nil {
		t.Fatal(err)
	}
	bs, err = db.Blocked(ctx, 10, filter)
	if err != nil || len(bs.Peers) != 0 {
		t.Fatalf("blocked=%+v %v", bs, err)
	}
	if err = db.ReplaceNotifyDefaults(ctx, []rpc.NotifyDefault{{Scope: "users"}}); err != nil {
		t.Fatal(err)
	}
	if err = db.ReplaceNotifyDefaults(ctx, nil); err != nil {
		t.Fatal(err)
	}
	ns, err := db.NotifyDefaults(ctx)
	if err != nil || len(ns.Defaults) != 0 || ns.SyncedAt == nil {
		t.Fatalf("notify=%+v %v", ns, err)
	}
}
func TestOwnerStateUpgradeFromPreviousVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := listMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = applyMigrations(raw, ms[:len(ms)-1]); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.read.QueryRow(`SELECT count(*) FROM account_state`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("account_state rows %d %v", n, err)
	}
	if v, err := db.Version(); err != nil || v != len(ms) {
		t.Fatalf("version %d %v", v, err)
	}
}
