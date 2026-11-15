package daemon

import (
	"context"
	"errors"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

func TestSavedOwnerVisibilityAndRenamePermission(t *testing.T) {
	d, _, _, path := accessTestDaemon(t)
	ctx := context.Background()
	title := "tag"
	writeAccess(t, path, `{"block":[42]}`)
	_, err := d.savedTags(ctx, params(t, rpc.SavedTagsParams{}))
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeNotFound {
		t.Fatalf("hidden owner: %v", err)
	}
	writeAccess(t, path, `{}`)
	_, err = d.savedTagRename(ctx, params(t, rpc.SavedTagRenameParams{Emoji: "👍", Title: &title, DryRun: true}))
	if !errors.As(err, &re) || re.Code != rpc.CodeWriteDisabled {
		t.Fatalf("without write: %v", err)
	}
	writeAccess(t, path, `{"write":[42]}`)
	result, err := d.savedTagRename(ctx, params(t, rpc.SavedTagRenameParams{Emoji: "👍", Title: &title, DryRun: true}))
	if err != nil || !result.(rpc.SavedTagRenameResult).DryRun {
		t.Fatalf("preview %v %v", result, err)
	}
}

type savedFake struct{ *fakeTelegram }

func (f *savedFake) SavedList(context.Context, rpc.SavedListParams) (rpc.SavedListResult, error) {
	return rpc.SavedListResult{Dialogs: []rpc.SavedDialog{{PeerID: 77}, {PeerID: 88}}}, nil
}
func (f *savedFake) SavedMessages(context.Context, int64, *chatref.Ref, rpc.SavedMessagesParams, bool) (rpc.MessagesResult, error) {
	return rpc.MessagesResult{Messages: []rpc.Message{{ID: 1, ChatID: 42, Date: "2026-01-01T00:00:00Z", Text: "visible", SavedSourceID: 77}, {ID: 2, ChatID: 42, Date: "2026-01-01T00:00:00Z", Text: "hidden", SavedSourceID: 88}}}, nil
}
func TestSavedFilteringAndStorage(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	d.opts.Telegram = &savedFake{base}
	writeAccess(t, path, `{"block":[88]}`)
	ctx := context.Background()
	got, err := d.savedList(ctx, params(t, rpc.SavedListParams{}))
	if err != nil || len(got.(rpc.SavedListResult).Dialogs) != 1 {
		t.Fatalf("list %+v %v", got, err)
	}
	got, err = d.savedMessages(ctx, params(t, rpc.SavedMessagesParams{Query: "test"}), true)
	if err != nil || len(got.(rpc.MessagesResult).Messages) != 1 {
		t.Fatalf("search %+v %v", got, err)
	}
	_, err = db.GetMessage(ctx, 42, 2)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("hidden message saved: %v", err)
	}
	got, err = d.savedMessages(ctx, params(t, rpc.SavedMessagesParams{Peer: "77"}), false)
	if err != nil || len(got.(rpc.MessagesResult).Messages) != 1 {
		t.Fatalf("history %+v %v", got, err)
	}
	if _, err := db.GetMessage(ctx, 42, 1); err != nil {
		t.Fatalf("history not saved: %v", err)
	}
	_, err = d.savedMessages(ctx, params(t, rpc.SavedMessagesParams{}), true)
	var re *rpc.Error
	if !errors.As(err, &re) || re.Code != rpc.CodeInvalidParams {
		t.Fatalf("empty search: %v", err)
	}
}

type pagedSavedFake struct {
	*fakeTelegram
	calls int
}

func (f *pagedSavedFake) SavedList(_ context.Context, p rpc.SavedListParams) (rpc.SavedListResult, error) {
	f.calls++
	if p.Cursor == "" {
		return rpc.SavedListResult{Dialogs: []rpc.SavedDialog{{PeerID: 88, Cursor: "hidden"}}, NextCursor: "hidden", CursorPeerID: 88}, nil
	}
	return rpc.SavedListResult{Dialogs: []rpc.SavedDialog{{PeerID: 77, Cursor: "visible"}}}, nil
}
func TestSavedListSkipsClosedPage(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	fake := &pagedSavedFake{fakeTelegram: base}
	d.opts.Telegram = fake
	writeAccess(t, path, `{"block":[88]}`)
	got, err := d.savedList(context.Background(), params(t, rpc.SavedListParams{}))
	if err != nil || fake.calls != 2 {
		t.Fatalf("result %+v calls=%d err=%v", got, fake.calls, err)
	}
	result := got.(rpc.SavedListResult)
	if len(result.Dialogs) != 1 || result.Dialogs[0].PeerID != 77 || result.NextCursor == "hidden" {
		t.Fatalf("result %+v", result)
	}
}
