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
	folderRaw := json.RawMessage(`{"_":"dialogFilter","pinned_peers":[{"_":"inputPeerUser","user_id":1},{"_":"inputPeerUser","user_id":2}]}`)
	folders := []rpc.Folder{{Raw: folderRaw, ID: 1, Kind: "filter", Title: "x", Pinned: []rpc.FolderPeer{{ID: 1}, {ID: 2}}, Include: []rpc.FolderPeer{{ID: 1}}, Exclude: []rpc.FolderPeer{}}}
	if err = db.ReplaceFolders(ctx, folders, filter); err != nil {
		t.Fatal(err)
	}
	got, err := db.Folders(ctx, filter)
	if err != nil || len(got.Folders) != 1 || len(got.Folders[0].Pinned) != 1 || len(got.Folders[0].Include) != 1 {
		t.Fatalf("folders=%+v %v", got, err)
	}
	var raw, data string
	if err = db.read.QueryRow(`SELECT raw,data FROM folders`).Scan(&raw, &data); err != nil || raw != string(folderRaw) || strings.Contains(raw, "access_hash") || !strings.Contains(raw, `"user_id":2`) || strings.Contains(data, `"id":2`) {
		t.Fatalf("raw=%s data=%s err=%v", raw, data, err)
	}
	resultJSON, err := json.Marshal(got)
	if err != nil || strings.Contains(string(resultJSON), `"id":2`) || strings.Contains(string(resultJSON), `"user_id":2`) {
		t.Fatalf("folders output=%s err=%v", resultJSON, err)
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

func TestOwnerRawReplacementAndData(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	filter := ChatFilter{Block: []int64{2}}
	if err := db.UpsertChats(ctx, []Chat{{ID: 1, Type: "user", Title: "test"}}); err != nil {
		t.Fatal(err)
	}
	first := json.RawMessage(`{"_":"dialog","unread_count":4}`)
	next := json.RawMessage(`{"_":"dialog","unread_count":8}`)
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 1, Unread: 4, Raw: first}, {ChatID: 2, Raw: first}}, filter); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 1, Unread: 8, Raw: next}}, filter); err != nil {
		t.Fatal(err)
	}
	var raw, data string
	var count, unread int
	if err := db.read.QueryRow(`SELECT raw,data FROM dialog_state WHERE chat_id=1`).Scan(&raw, &data); err != nil || raw != string(next) || !strings.Contains(data, `"unread":8`) {
		t.Fatalf("dialog raw=%s data=%s err=%v", raw, data, err)
	}
	if err := db.read.QueryRow(`SELECT count(*) FROM dialog_state WHERE chat_id=2`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("closed count=%d err=%v", count, err)
	}
	if err := db.read.QueryRow(`SELECT unread FROM chats_state WHERE chat_id=1`).Scan(&unread); err != nil || unread != 8 {
		t.Fatalf("view unread=%d err=%v", unread, err)
	}
	session := rpc.Session{Hash: 123, DeviceModel: "one", Raw: json.RawMessage(`{"_":"authorization","ip":"127.0.0.1","country":"X"}`)}
	if err := db.ReplaceSessions(ctx, rpc.SessionsResult{Sessions: []rpc.Session{session}}); err != nil {
		t.Fatal(err)
	}
	session.Raw = json.RawMessage(`{"_":"authorization","ip":"127.0.0.2","country":"Y"}`)
	session.DeviceModel = "two"
	if err := db.ReplaceSessions(ctx, rpc.SessionsResult{Sessions: []rpc.Session{session}}); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRow(`SELECT raw,data FROM account_sessions WHERE hash=123`).Scan(&raw, &data); err != nil || raw != string(session.Raw) || strings.Contains(raw, `"hash"`) || !strings.Contains(data, `"device_model":"two"`) {
		t.Fatalf("session raw=%s data=%s err=%v", raw, data, err)
	}
	peer := rpc.BlockedPeer{PeerID: 1, Raw: json.RawMessage(`{"_":"peerBlocked","date":1}`)}
	if err := db.UpsertBlocked(ctx, []rpc.BlockedPeer{peer, {PeerID: 2, Raw: peer.Raw}}, filter); err != nil {
		t.Fatal(err)
	}
	peer.Raw = json.RawMessage(`{"_":"peerBlocked","date":2}`)
	if err := db.UpsertBlocked(ctx, []rpc.BlockedPeer{peer}, filter); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRow(`SELECT raw FROM blocked_peers WHERE peer_id=1`).Scan(&raw); err != nil || raw != string(peer.Raw) {
		t.Fatalf("blocked raw=%s err=%v", raw, err)
	}
	if err := db.read.QueryRow(`SELECT count(*) FROM blocked_peers WHERE peer_id=2`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("blocked closed=%d err=%v", count, err)
	}
	notify := rpc.NotifyDefault{Scope: "users", Raw: json.RawMessage(`{"_":"peerNotifySettings","mute_until":1}`)}
	if err := db.ReplaceNotifyDefaults(ctx, []rpc.NotifyDefault{notify}); err != nil {
		t.Fatal(err)
	}
	notify.Raw = json.RawMessage(`{"_":"peerNotifySettings","mute_until":2}`)
	if err := db.ReplaceNotifyDefaults(ctx, []rpc.NotifyDefault{notify}); err != nil {
		t.Fatal(err)
	}
	if err := db.read.QueryRow(`SELECT raw,data FROM notify_defaults WHERE scope='users'`).Scan(&raw, &data); err != nil || raw != string(notify.Raw) || !strings.Contains(data, `"scope":"users"`) {
		t.Fatalf("notify raw=%s data=%s err=%v", raw, data, err)
	}
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 1, Unread: 9}}, filter); err != nil {
		t.Fatal(err)
	}
	var nullable sql.NullString
	if err := db.read.QueryRow(`SELECT raw FROM dialog_state WHERE chat_id=1`).Scan(&nullable); err != nil || nullable.Valid {
		t.Fatalf("raw must be replaced with NULL: %+v err=%v", nullable, err)
	}
}
