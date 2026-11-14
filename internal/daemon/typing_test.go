package daemon

import (
	"context"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type typingTelegram struct {
	*fakeTelegram
	calls int
}

func (f *typingTelegram) Typing(_ context.Context, _ chatref.Ref, p rpc.TypingParams) (rpc.TypingResult, error) {
	f.calls++
	return rpc.TypingResult{Action: p.Action}, nil
}
func TestTypingAccessAndDryRun(t *testing.T) {
	d, f, _, path := accessTestDaemon(t)
	tg := &typingTelegram{fakeTelegram: f}
	d.opts.Telegram = tg
	p := rpc.TypingParams{Chat: "42", Action: "typing"}
	_, err := d.chatTyping(context.Background(), params(t, p))
	sameRPCError(t, err, rpc.ErrWriteDisabled())
	if tg.calls != 0 {
		t.Fatal("write reached Telegram")
	}
	writeAccess(t, path, `{"write":[42]}`)
	p.DryRun = true
	got, err := d.chatTyping(context.Background(), params(t, p))
	if err != nil || !got.(rpc.TypingResult).DryRun || tg.calls != 0 {
		t.Fatalf("dry run: %v %v", got, err)
	}
	p.DryRun = false
	got, err = d.chatTyping(context.Background(), params(t, p))
	if err != nil || got.(rpc.TypingResult).ChatID != 42 || tg.calls != 1 {
		t.Fatalf("typing: %v %v", got, err)
	}
	writeAccess(t, path, `{"allow":[77],"write":[42]}`)
	_, err = d.chatTyping(context.Background(), params(t, p))
	sameRPCError(t, err, rpc.ErrUnknownChatID())
	if tg.calls != 1 {
		t.Fatal("hidden chat reached Telegram")
	}
}
