package daemon

import (
	"context"
	"errors"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"time"
)

type draftsScheduledFake struct {
	*fakeTelegram
	calls int
	// emptyDraft makes DraftGet answer a chat without a draft.
	emptyDraft bool
	// lastSet is what the last DraftSet received.
	lastSet rpc.DraftParams
}

func (f *draftsScheduledFake) DraftGet(context.Context, chatref.Ref) (rpc.Draft, error) {
	if f.emptyDraft {
		return rpc.Draft{Empty: true}, nil
	}
	return rpc.Draft{Text: "existing"}, nil
}
func (f *draftsScheduledFake) DraftList(context.Context) (rpc.DraftList, error) {
	return rpc.DraftList{Drafts: []rpc.Draft{{ChatID: 42}, {ChatID: 77}}}, nil
}
func (f *draftsScheduledFake) DraftSet(_ context.Context, _ chatref.Ref, p rpc.DraftParams) (rpc.DraftWriteResult, error) {
	f.calls++
	f.lastSet = p
	return rpc.DraftWriteResult{}, nil
}
func (f *draftsScheduledFake) ScheduledList(context.Context, chatref.Ref) (rpc.ScheduledResult, error) {
	return rpc.ScheduledResult{Messages: []rpc.Message{{ID: 5, Text: "later"}}}, nil
}
func (f *draftsScheduledFake) ScheduledDelete(context.Context, chatref.Ref, []int) (rpc.ScheduledResult, error) {
	f.calls++
	return rpc.ScheduledResult{}, nil
}
func TestDraftAndScheduledAccess(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &draftsScheduledFake{fakeTelegram: base}
	d.opts.Telegram = f
	ctx := context.Background()
	for _, run := range []func() (any, error){func() (any, error) { return d.draftSet(ctx, params(t, rpc.DraftParams{Chat: "42", Text: "new"})) }, func() (any, error) {
		return d.scheduledDelete(ctx, params(t, rpc.ScheduledParams{Chat: "42", IDs: []int{1}}))
	}} {
		_, err := run()
		sameRPCError(t, err, rpc.ErrWriteDisabled())
	}
	if f.calls != 0 {
		t.Fatal("unauthorized write")
	}
	writeAccess(t, path, `{"allow":[42],"write":[42]}`)
	out, err := d.draftSet(ctx, params(t, rpc.DraftParams{Chat: "42", Text: "new", DryRun: true}))
	if res, _ := out.(rpc.DraftWriteResult); err != nil || !res.Replaces || !res.DryRun || res.TextLength != 3 || res.ChatID != 42 || f.calls != 0 {
		t.Fatalf("dry-run %v %v", out, err)
	}
	// A chat without a draft is not replaced.
	f.emptyDraft = true
	out, err = d.draftSet(ctx, params(t, rpc.DraftParams{Chat: "42", Text: "new", DryRun: true}))
	if res, _ := out.(rpc.DraftWriteResult); err != nil || res.Replaces || !res.DryRun || f.calls != 0 {
		t.Fatalf("dry-run over an empty draft: %v %v", out, err)
	}
	f.emptyDraft = false
	out, err = d.draftList(ctx, []byte(`{}`))
	if err != nil || len(out.(rpc.DraftList).Drafts) != 1 || out.(rpc.DraftList).Drafts[0].ChatID != 42 {
		t.Fatalf("list %v %v", out, err)
	}
	_, err = d.scheduledDelete(ctx, params(t, rpc.ScheduledParams{Chat: "42", IDs: []int{1}, DryRun: true}))
	if err != nil || f.calls != 0 {
		t.Fatalf("dry-run delete %v", err)
	}
}

func TestScheduledSendDoesNotStoreScheduledID(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &scheduledSendFake{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"write":[42]}`)
	ctx := context.Background()
	at := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	out, err := d.messageSend(ctx, params(t, rpc.SendParams{Chat: "42", Text: "hello", At: at}))
	if err != nil || !out.(rpc.SendResult).Scheduled {
		t.Fatalf("send %v %v", out, err)
	}
	if _, err := db.GetMessage(ctx, 42, 11); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("scheduled message stored: %v", err)
	}
}

type scheduledSendFake struct{ *fakeTelegram }

func (f *scheduledSendFake) Send(context.Context, chatref.Ref, rpc.SendParams) (rpc.SendResult, error) {
	return rpc.SendResult{ID: 11, Date: "2026-11-15T10:00:00Z", Scheduled: true}, nil
}

func (f *draftsScheduledFake) ScheduledSendNow(context.Context, chatref.Ref, []int) (rpc.ScheduledResult, error) {
	f.calls++
	return rpc.ScheduledResult{Messages: []rpc.Message{{ChatID: 42, ID: 101, Date: "2026-11-15T10:00:00Z", Text: "delivered", Out: true}}}, nil
}
func TestScheduledSendNowPersistsDelivered(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &draftsScheduledFake{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"write":[42]}`)
	ctx := context.Background()
	_, err := d.scheduledSendNow(ctx, params(t, rpc.ScheduledParams{Chat: "42", IDs: []int{5}}))
	if err != nil {
		t.Fatal(err)
	}
	message, err := db.GetMessage(ctx, 42, 101)
	if err != nil || (message.Out == nil || !*message.Out) || message.Text != "delivered" {
		t.Fatalf("message %+v err=%v", message, err)
	}
}
