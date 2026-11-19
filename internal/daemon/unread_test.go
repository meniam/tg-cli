package daemon

import (
	"context"
	"errors"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func (*fakeTelegram) UnreadChats(context.Context, int, bool, func(int64) bool) (rpc.UnreadChatsResult, []rpc.ChatState, error) {
	return rpc.UnreadChatsResult{Chats: []rpc.UnreadChat{}}, nil, nil
}
func (*fakeTelegram) UnreadMessages(context.Context, chatref.Ref, rpc.MessageUnreadParams) (rpc.UnreadMessagesResult, *rpc.ChatState, error) {
	return rpc.UnreadMessagesResult{Messages: []rpc.Message{}}, nil, nil
}

type unreadFake struct {
	*fakeTelegram
	calls int
}

func (f *unreadFake) UnreadMessages(_ context.Context, _ chatref.Ref, _ rpc.MessageUnreadParams) (rpc.UnreadMessagesResult, *rpc.ChatState, error) {
	f.calls++
	return rpc.UnreadMessagesResult{ChatID: 42, Messages: []rpc.Message{}}, nil, nil
}
func TestUnreadLocalAndClosedAccess(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &unreadFake{fakeTelegram: base}
	d.opts.Telegram = f
	ctx := context.Background()
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "group", Title: "before"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertChats(ctx, []store.Chat{{ID: 42, Type: "group", Title: "after"}}); err != nil {
		t.Fatal(err)
	}
	base.user = nil
	v, err := d.chatChanges(ctx, params(t, rpc.ChatChangesParams{Chat: "42"}))
	if err != nil || len(v.(rpc.ChatChangesResult).Versions) != 1 {
		t.Fatalf("local changes: %+v %v", v, err)
	}
	writeAccess(t, path, `{"block":[42]}`)
	_, err = d.chatChanges(ctx, params(t, rpc.ChatChangesParams{Chat: "42"}))
	if err == nil || writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("closed chat changes: %v", err)
	}
	_, err = d.messageUnread(ctx, params(t, rpc.MessageUnreadParams{Chat: "42"}))
	if err == nil || writeCode(t, err) != rpc.CodeNotFound || f.calls != 0 {
		t.Fatalf("closed unread: %v calls %d", err, f.calls)
	}
}

type unreadListFake struct{ *fakeTelegram }

func (f *unreadListFake) UnreadChats(_ context.Context, _ int, _ bool, allowed func(int64) bool) (rpc.UnreadChatsResult, []rpc.ChatState, error) {
	all := []rpc.UnreadChat{{ID: 42, Type: "user", Title: "visible", Unread: 1}, {ID: 77, Type: "user", Title: "hidden", Unread: 1}}
	out := rpc.UnreadChatsResult{Chats: []rpc.UnreadChat{}, Scanned: 2, Complete: true}
	states := []rpc.ChatState{}
	for _, c := range all {
		if allowed(c.ID) {
			out.Chats = append(out.Chats, c)
			states = append(states, rpc.ChatState{ChatID: c.ID, Unread: 1})
		}
	}
	return out, states, nil
}
func TestUnreadListDoesNotPersistClosedChats(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	d.opts.Telegram = &unreadListFake{base}
	writeAccess(t, path, `{"block":[77]}`)
	ctx := context.Background()
	v, err := d.chatUnread(ctx, params(t, rpc.ChatUnreadParams{}))
	if err != nil {
		t.Fatal(err)
	}
	got := v.(rpc.UnreadChatsResult)
	if len(got.Chats) != 1 || got.Chats[0].ID != 42 || got.Scanned != 2 {
		t.Fatalf("result %+v", got)
	}
	if _, err = db.GetChat(ctx, 77); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed chat saved: %v", err)
	}
	if _, err = db.DialogState(ctx, 77, store.ChatFilter{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed state saved: %v", err)
	}
}

// audit06UnreadLeakyFake ignores the allowed filter and returns every chat and
// state, so the daemon's own filters are the only barrier for a closed chat.
type audit06UnreadLeakyFake struct{ *fakeTelegram }

func (f *audit06UnreadLeakyFake) UnreadChats(context.Context, int, bool, func(int64) bool) (rpc.UnreadChatsResult, []rpc.ChatState, error) {
	out := rpc.UnreadChatsResult{
		Chats:    []rpc.UnreadChat{{ID: 42, Type: "user", Title: "visible", Unread: 1}, {ID: 77, Type: "user", Title: "hidden", Unread: 1}},
		Scanned:  2,
		Complete: true,
	}
	return out, []rpc.ChatState{{ChatID: 42, Unread: 1}, {ChatID: 77, Unread: 1}}, nil
}

func TestAudit06_UnreadListFiltersWhatTelegramReturns(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	d.opts.Telegram = &audit06UnreadLeakyFake{base}
	writeAccess(t, path, `{"block":[77]}`)
	ctx := context.Background()
	v, err := d.chatUnread(ctx, params(t, rpc.ChatUnreadParams{}))
	if err != nil {
		t.Fatal(err)
	}
	got := v.(rpc.UnreadChatsResult)
	if len(got.Chats) != 1 || got.Chats[0].ID != 42 {
		t.Fatalf("closed chat in the answer: %+v", got)
	}
	if _, err = db.GetChat(ctx, 77); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed chat saved: %v", err)
	}
	if _, err = db.DialogState(ctx, 77, store.ChatFilter{}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("closed state saved: %v", err)
	}
	chat, err := db.GetChat(ctx, 42)
	if err != nil || chat.Title != "visible" {
		t.Fatalf("open chat not saved: %+v %v", chat, err)
	}
	if _, err = db.DialogState(ctx, 42, store.ChatFilter{}); err != nil {
		t.Fatalf("open state not saved: %v", err)
	}
}
