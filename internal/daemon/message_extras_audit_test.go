package daemon

import (
	"bytes"
	"context"
	"strconv"
	"testing"
	"time"

	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

// audit05InlineDaemon is a daemon that may write to chat 42 and resolves any
// bot reference to chat 6 with the given type.
func audit05InlineDaemon(t *testing.T, botType string) (*daemon, *extrasTelegram) {
	t.Helper()
	d, base, _, path := accessTestDaemon(t)
	base.resolved = &rpc.Chat{ID: 6, Type: botType}
	f := &extrasTelegram{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"write":[42]}`)
	return d, f
}

// inline-query records the answer and send-inline accepts exactly what it
// recorded for the same chat.
func TestAudit05_InlineQueryThenSendInline(t *testing.T) {
	d, f := audit05InlineDaemon(t, "bot")
	ctx := context.Background()
	got, err := d.messageInlineQuery(ctx, params(t, rpc.InlineQueryParams{Chat: "42", Bot: "6"}))
	if err != nil {
		t.Fatal(err)
	}
	answer := got.(rpc.InlineQueryResult)
	if answer.QueryID != "123" || answer.ChatID != 42 || answer.BotID != 6 || f.queries != 1 {
		t.Fatalf("answer %+v queries=%d", answer, f.queries)
	}
	send := rpc.SendInlineParams{Chat: "42", QueryID: answer.QueryID, Result: "r"}
	sent, err := d.messageSendInline(ctx, params(t, send))
	if err != nil || sent.(rpc.SendResult).ID != 31 || f.inlineSent != 1 {
		t.Fatalf("send %+v err=%v sent=%d", sent, err, f.inlineSent)
	}
	send.Result = "not-in-the-answer"
	if _, err = d.messageSendInline(ctx, params(t, send)); audit05GuardCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("unknown result: %v", err)
	}
	if f.inlineSent != 1 {
		t.Fatalf("an unknown result was sent: %d", f.inlineSent)
	}
}

func TestAudit05_InlineQueryRequiresABot(t *testing.T) {
	d, f := audit05InlineDaemon(t, "user")
	_, err := d.messageInlineQuery(context.Background(), params(t, rpc.InlineQueryParams{Chat: "42", Bot: "6"}))
	if audit05GuardCode(t, err) != rpc.CodeInvalidParams || f.queries != 0 {
		t.Fatalf("non bot: %v queries=%d", err, f.queries)
	}
}

func TestAudit05_InlineQueryRejectsInvalidQueryID(t *testing.T) {
	for _, id := range []string{"abc", "0"} {
		d, f := audit05InlineDaemon(t, "bot")
		f.queryID = id
		_, err := d.messageInlineQuery(context.Background(), params(t, rpc.InlineQueryParams{Chat: "42", Bot: "6"}))
		if audit05GuardCode(t, err) != rpc.CodeTelegram {
			t.Errorf("query id %q: %v", id, err)
		}
		if len(d.inlineQueries) != 0 {
			t.Errorf("query id %q was stored", id)
		}
	}
}

// The answer lives between 60 and 600 seconds whatever cache time Telegram
// states.
func TestAudit05_InlineQueryTTLBounds(t *testing.T) {
	for _, tc := range []struct {
		cacheTime int
		want      time.Duration
	}{{1, 60 * time.Second}, {300, 300 * time.Second}, {9999, 600 * time.Second}} {
		d, f := audit05InlineDaemon(t, "bot")
		f.cacheTime = tc.cacheTime
		before := time.Now()
		if _, err := d.messageInlineQuery(context.Background(), params(t, rpc.InlineQueryParams{Chat: "42", Bot: "6"})); err != nil {
			t.Fatal(err)
		}
		after := time.Now()
		entry, ok := d.inlineQueries["123"]
		if !ok {
			t.Fatalf("cache time %d: answer not stored", tc.cacheTime)
		}
		if entry.expires.Before(before.Add(tc.want)) || entry.expires.After(after.Add(tc.want)) {
			t.Errorf("cache time %d: expires in %s, want %s", tc.cacheTime, entry.expires.Sub(before), tc.want)
		}
	}
}

// Expired answers are dropped and the store never grows past 512 entries.
func TestAudit05_InlineQueryEvictsExpiredAndCapsSize(t *testing.T) {
	d, _ := audit05InlineDaemon(t, "bot")
	d.inlineQueries = map[string]inlineQueryEntry{"stale": {chatID: 42, results: map[string]bool{"x": true}, expires: time.Now().Add(-time.Second)}}
	if _, err := d.messageInlineQuery(context.Background(), params(t, rpc.InlineQueryParams{Chat: "42", Bot: "6"})); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.inlineQueries["stale"]; ok {
		t.Fatal("an expired answer was kept")
	}
	d.inlineQueries = make(map[string]inlineQueryEntry)
	for i := 0; i < 512; i++ {
		d.inlineQueries["q"+strconv.Itoa(i)] = inlineQueryEntry{chatID: 42, results: map[string]bool{"x": true}, expires: time.Now().Add(time.Hour)}
	}
	if _, err := d.messageInlineQuery(context.Background(), params(t, rpc.InlineQueryParams{Chat: "42", Bot: "6"})); err != nil {
		t.Fatal(err)
	}
	if len(d.inlineQueries) != 512 {
		t.Fatalf("store holds %d answers, want 512", len(d.inlineQueries))
	}
	if _, ok := d.inlineQueries["123"]; !ok {
		t.Fatal("the new answer was evicted")
	}
}

// The default sticker search limit reaches Telegram; explicit limits pass as is.
func TestAudit05_StickerSearchPassesLimit(t *testing.T) {
	d, base, _, _ := accessTestDaemon(t)
	f := &extrasTelegram{fakeTelegram: base}
	d.opts.Telegram = f
	for _, limit := range []int{0, 1, 100} {
		if _, err := d.stickerSearch(context.Background(), params(t, rpc.StickerSearchParams{Query: "cats", Limit: limit})); err != nil {
			t.Fatal(err)
		}
	}
	want := []int{20, 1, 100}
	if len(f.stickerLimits) != len(want) {
		t.Fatalf("limits %v, want %v", f.stickerLimits, want)
	}
	for i := range want {
		if f.stickerLimits[i] != want[i] {
			t.Fatalf("limits %v, want %v", f.stickerLimits, want)
		}
	}
}

func TestAudit05_SendStickerDeliversStoresAndReplays(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &extrasTelegram{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"write":[42]}`)
	index := 0
	p := rpc.SendStickerParams{Chat: "42", Set: "fixture", Index: &index, IdempotencyKey: "sticker-1"}
	ctx := context.Background()
	got, err := d.messageSendSticker(ctx, params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	result := got.(rpc.SendStickerResult)
	if result.ID != 9 || result.ChatID != 42 || result.Replayed || f.stickers != 1 {
		t.Fatalf("sent %+v stickers=%d", result, f.stickers)
	}
	stored, err := db.GetMessage(ctx, 42, 9)
	if err != nil || stored.MediaType == nil || *stored.MediaType != "sticker" {
		t.Fatalf("stored %+v err=%v", stored, err)
	}
	again, err := d.messageSendSticker(ctx, params(t, p))
	if err != nil || !again.(rpc.SendStickerResult).Replayed || f.stickers != 1 {
		t.Fatalf("replay %+v err=%v stickers=%d", again, err, f.stickers)
	}
}

func TestAudit05_SendStickerRejectsBadSelection(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &extrasTelegram{fakeTelegram: base}
	d.opts.Telegram = f
	writeAccess(t, path, `{"write":[42]}`)
	zero, negative, emoji, empty := 0, -1, "👍", ""
	for name, p := range map[string]rpc.SendStickerParams{
		"index and emoji": {Chat: "42", Set: "fixture", Index: &zero, Emoji: &emoji},
		"neither":         {Chat: "42", Set: "fixture"},
		"negative index":  {Chat: "42", Set: "fixture", Index: &negative},
		"empty emoji":     {Chat: "42", Set: "fixture", Emoji: &empty},
	} {
		if _, err := d.messageSendSticker(context.Background(), params(t, p)); audit05GuardCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("%s: %v", name, err)
		}
	}
	if f.stickers != 0 {
		t.Fatalf("a bad selection sent %d stickers", f.stickers)
	}
}

type audit05EditMediaTelegram struct {
	*sendMediaTelegram
	response rpc.SendResult
	edits    int
}

func (f *audit05EditMediaTelegram) EditMedia(context.Context, chatref.Ref, rpc.EditMediaParams, rpc.MediaFile) (rpc.SendResult, error) {
	f.edits++
	return f.response, nil
}

// A real media edit reaches Telegram once and stores the message with the new
// media type and caption.
func TestAudit05_EditMediaDeliversAndStores(t *testing.T) {
	c := newMediaCase(t)
	c.tg.messages = []rpc.Message{{ID: 7, ChatID: 42, Text: "old caption", MediaType: "photo"}}
	f := &audit05EditMediaTelegram{sendMediaTelegram: c.tg, response: rpc.SendResult{ID: 7, Date: "2026-01-02T03:04:05Z"}}
	c.d.opts.Telegram = f
	caption := "new caption"
	p := rpc.EditMediaParams{Chat: "42", ID: 7, As: "document", File: c.document, Caption: &caption}
	got, err := c.d.messageEditMedia(context.Background(), params(t, p))
	if err != nil {
		t.Fatal(err)
	}
	result := got.(rpc.EditMediaResult)
	if result.DryRun || result.ID != 7 || result.MediaType != "document" || result.Date != "2026-01-02T03:04:05Z" || f.edits != 1 {
		t.Fatalf("result %+v edits=%d", result, f.edits)
	}
	stored, err := c.db.GetMessage(context.Background(), 42, 7)
	if err != nil || stored.Text != caption || stored.MediaType == nil || *stored.MediaType != "document" || !bytes.Contains(stored.Raw, []byte(`"edited":true`)) {
		t.Fatalf("stored %+v err=%v", stored, err)
	}
}
