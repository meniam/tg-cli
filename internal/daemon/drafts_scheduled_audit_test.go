package daemon

import (
	"context"
	"testing"

	"tg-cli/internal/rpc"
)

// TestAudit04_DraftWritesReachTelegram covers the real writes of drafts: the text
// and reply that reach Telegram, the clear that empties both, the chat id of the
// answer and the refusal of a negative reply id.
func TestAudit04_DraftWritesReachTelegram(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &draftsScheduledFake{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"allow":[42],"write":[42]}`)
	ctx := context.Background()

	out, err := d.draftSet(ctx, params(t, rpc.DraftParams{Chat: "42", Text: "new", ReplyTo: 9}))
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(rpc.DraftWriteResult); got.ChatID != 42 || got.DryRun {
		t.Errorf("set result = %+v", got)
	}
	if f.calls != 1 || f.lastSet.Text != "new" || f.lastSet.ReplyTo != 9 {
		t.Errorf("Telegram got %d calls, %+v; want one set with text and reply", f.calls, f.lastSet)
	}

	out, err = d.draftClear(ctx, params(t, rpc.DraftParams{Chat: "42", Text: "ignored", ReplyTo: 5}))
	if err != nil {
		t.Fatal(err)
	}
	if got := out.(rpc.DraftWriteResult); got.ChatID != 42 {
		t.Errorf("clear result = %+v", got)
	}
	if f.calls != 2 || f.lastSet.Text != "" || f.lastSet.ReplyTo != 0 {
		t.Errorf("clear sent %+v after %d calls; want an empty draft without a reply", f.lastSet, f.calls)
	}

	out, err = d.draftClear(ctx, params(t, rpc.DraftParams{Chat: "42", DryRun: true}))
	if got, _ := out.(rpc.DraftWriteResult); err != nil || !got.Cleared || !got.DryRun || got.Replaces || f.calls != 2 {
		t.Errorf("clear dry run = %+v %v calls=%d", out, err, f.calls)
	}

	_, err = d.draftSet(ctx, params(t, rpc.DraftParams{Chat: "42", Text: "new", ReplyTo: -1}))
	if code := rpcCode(t, err); code != rpc.CodeInvalidParams || f.calls != 2 {
		t.Errorf("negative reply: code %d, calls %d, error %v", code, f.calls, err)
	}
}

// TestAudit04_DraftGetAndScheduledListSetTheChat covers the two reads: both answer
// with the id of the chat the reference resolved to, and a closed chat is refused.
func TestAudit04_DraftGetAndScheduledListSetTheChat(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &draftsScheduledFake{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"allow":[42]}`)
	ctx := context.Background()

	draft, err := d.draftGet(ctx, params(t, rpc.DraftParams{Chat: "42"}))
	if got, _ := draft.(rpc.Draft); err != nil || got.ChatID != 42 || got.Text != "existing" {
		t.Errorf("draft = %+v %v", draft, err)
	}
	scheduled, err := d.scheduledList(ctx, params(t, rpc.ScheduledParams{Chat: "42"}))
	if got, _ := scheduled.(rpc.ScheduledResult); err != nil || got.ChatID != 42 || len(got.Messages) != 1 || got.Messages[0].ID != 5 {
		t.Errorf("scheduled = %+v %v", scheduled, err)
	}
	_, err = d.draftGet(ctx, params(t, rpc.DraftParams{Chat: "77"}))
	sameRPCError(t, err, rpc.ErrUnknownChatID())
	_, err = d.scheduledList(ctx, params(t, rpc.ScheduledParams{Chat: "77"}))
	sameRPCError(t, err, rpc.ErrUnknownChatID())
}
