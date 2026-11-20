package store

import (
	"context"
	"reflect"
	"testing"

	"tg-cli/internal/rpc"
)

// audit08RowCount counts rows straight in the table, bypassing the read-side filter.
func audit08RowCount(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	return schemaCount(t, db.read, query, args...)
}

func TestAudit08_HiddenChatsAreNotWrittenToOwnerState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	filter := ChatFilter{Block: []int64{2}}
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 1, Unread: 4}, {ChatID: 2, Unread: 9}}, filter); err != nil {
		t.Fatal(err)
	}
	if n := audit08RowCount(t, db, `SELECT count(*) FROM dialog_state WHERE chat_id=2`); n != 0 {
		t.Errorf("hidden chat written to dialog_state: %d rows", n)
	}
	if n := audit08RowCount(t, db, `SELECT count(*) FROM dialog_state WHERE chat_id=1`); n != 1 {
		t.Errorf("visible chat rows in dialog_state: %d, want 1", n)
	}
	for name, save := range map[string]func(context.Context, []rpc.BlockedPeer, ChatFilter) error{
		"replace": db.ReplaceBlocked,
		"upsert":  db.UpsertBlocked,
	} {
		if err := save(ctx, []rpc.BlockedPeer{{PeerID: 1}, {PeerID: 2}}, filter); err != nil {
			t.Fatal(err)
		}
		if n := audit08RowCount(t, db, `SELECT count(*) FROM blocked_peers WHERE peer_id=2`); n != 0 {
			t.Errorf("%s: hidden peer written to blocked_peers: %d rows", name, n)
		}
		if n := audit08RowCount(t, db, `SELECT count(*) FROM blocked_peers WHERE peer_id=1`); n != 1 {
			t.Errorf("%s: visible peer rows in blocked_peers: %d, want 1", name, n)
		}
	}
	// Hide closes every chat-owned row on write as well.
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 5}}, ChatFilter{Hide: true}); err != nil {
		t.Fatal(err)
	}
	if n := audit08RowCount(t, db, `SELECT count(*) FROM dialog_state WHERE chat_id=5`); n != 0 {
		t.Errorf("Hide filter still wrote dialog_state: %d rows", n)
	}
}

func TestAudit08_BlockedKeepsContentAndOrder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	setClock(db, 700)
	if err := db.ReplaceBlocked(ctx, []rpc.BlockedPeer{{PeerID: 3, Title: "c"}, {PeerID: 1, Title: "a"}}, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertBlocked(ctx, []rpc.BlockedPeer{{PeerID: 2, Title: "b"}}, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Blocked(ctx, 10, ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	want := []rpc.BlockedPeer{{PeerID: 1, Title: "a"}, {PeerID: 2, Title: "b"}, {PeerID: 3, Title: "c"}}
	if !reflect.DeepEqual(got.Peers, want) {
		t.Errorf("peers=%+v, want %+v", got.Peers, want)
	}
	if got.SyncedAt == nil || *got.SyncedAt != 700 {
		t.Errorf("synced_at=%v, want 700", got.SyncedAt)
	}
	limited, err := db.Blocked(ctx, 2, ChatFilter{})
	if err != nil || len(limited.Peers) != 2 || limited.Peers[0].PeerID != 1 || limited.Peers[1].PeerID != 2 {
		t.Errorf("limited=%+v %v", limited.Peers, err)
	}
}

func TestAudit08_NotifyDefaultsRoundTripAndReplace(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	silent := true
	setClock(db, 800)
	if err := db.ReplaceNotifyDefaults(ctx, []rpc.NotifyDefault{{Scope: "users", Silent: &silent}, {Scope: "chats"}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.NotifyDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := int64(800)
	want := []rpc.NotifyDefault{{Scope: "chats", SyncedAt: &at}, {Scope: "users", Silent: &silent, SyncedAt: &at}}
	if !reflect.DeepEqual(got.Defaults, want) {
		t.Errorf("defaults=%+v, want %+v", got.Defaults, want)
	}
	if got.SyncedAt == nil || *got.SyncedAt != 800 {
		t.Errorf("snapshot synced_at=%v, want 800", got.SyncedAt)
	}
	setClock(db, 900)
	if err = db.ReplaceNotifyDefaults(ctx, []rpc.NotifyDefault{{Scope: "channels"}}); err != nil {
		t.Fatal(err)
	}
	got, err = db.NotifyDefaults(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at = 900
	want = []rpc.NotifyDefault{{Scope: "channels", SyncedAt: &at}}
	if !reflect.DeepEqual(got.Defaults, want) {
		t.Errorf("after replace defaults=%+v, want %+v", got.Defaults, want)
	}
}

func TestAudit08_DialogStatesOrderLimitFilter(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	setClock(db, 100)
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 3, Unread: 3}, {ChatID: 1, Unread: 1}, {ChatID: 4, Unread: 4}}, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	// A later sync makes chat 2 the freshest row.
	setClock(db, 200)
	if err := db.UpsertDialogStates(ctx, []rpc.ChatState{{ChatID: 2, Unread: 2}}, ChatFilter{}); err != nil {
		t.Fatal(err)
	}
	ids := func(res rpc.ChatStatesResult) []int64 {
		out := []int64{}
		for _, s := range res.States {
			out = append(out, s.ChatID)
		}
		return out
	}
	all, err := db.DialogStates(ctx, 10, ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(all), []int64{2, 1, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("order=%v, want %v (newest sync first, then chat_id)", got, want)
	}
	if all.SyncedAt == nil || *all.SyncedAt != 200 {
		t.Errorf("snapshot synced_at=%v, want 200", all.SyncedAt)
	}
	if s := all.States[0]; s.SyncedAt == nil || *s.SyncedAt != 200 || s.Unread != 2 {
		t.Errorf("first state=%+v", s)
	}
	limited, err := db.DialogStates(ctx, 2, ChatFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(limited), []int64{2, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("limited=%v, want %v", got, want)
	}
	filtered, err := db.DialogStates(ctx, 10, ChatFilter{Block: []int64{2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(filtered), []int64{1, 4}; !reflect.DeepEqual(got, want) {
		t.Errorf("filtered=%v, want %v", got, want)
	}
	allowed, err := db.DialogStates(ctx, 10, ChatFilter{Allow: []int64{4}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(allowed), []int64{4}; !reflect.DeepEqual(got, want) {
		t.Errorf("allowed=%v, want %v", got, want)
	}
	empty, err := db.DialogStates(ctx, 10, ChatFilter{Hide: true})
	if err != nil || len(empty.States) != 0 || empty.States == nil {
		t.Errorf("hidden=%+v %v, want empty non-nil slice", empty.States, err)
	}
}
