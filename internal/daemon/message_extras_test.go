package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"time"
)

type extrasTelegram struct {
	*fakeTelegram
	presses, timers, queries, stickers int
	// queryID and cacheTime shape the inline query answer; empty and zero
	// keep the defaults below.
	queryID   string
	cacheTime int
	// stickerLimits records the limit of every sticker search that reached
	// Telegram.
	stickerLimits []int
	inlineSent    int
}

func (f *extrasTelegram) SearchStickerSets(_ context.Context, p rpc.StickerSearchParams) (rpc.StickerSetsResult, error) {
	f.stickerLimits = append(f.stickerLimits, p.Limit)
	return rpc.StickerSetsResult{Sets: []rpc.StickerSetSummary{}}, nil
}

func (f *extrasTelegram) SendInline(_ context.Context, _ chatref.Ref, _ rpc.SendInlineParams) (rpc.SendResult, *rpc.Message, error) {
	f.inlineSent++
	return rpc.SendResult{ID: 31}, nil, nil
}

func (f *extrasTelegram) PressButton(_ context.Context, _ chatref.Ref, p rpc.PressParams) (rpc.PressResult, error) {
	if !p.DryRun {
		f.presses++
	}
	return rpc.PressResult{DryRun: p.DryRun}, nil
}
func (f *extrasTelegram) SetAutoDelete(context.Context, chatref.Ref, int) error {
	f.timers++
	return nil
}
func TestMessageExtrasAccessAndDryRun(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &extrasTelegram{fakeTelegram: base}
	d.opts.Telegram = f
	ctx := context.Background()
	index := 0
	press := func(dry bool) (any, error) {
		return d.messagePress(ctx, params(t, rpc.PressParams{Chat: "42", ID: 7, Button: &index, DryRun: dry}))
	}
	timer := func(dry bool) (any, error) {
		return d.chatAutoDelete(ctx, params(t, rpc.AutoDeleteParams{Chat: "42", Period: "1d", DryRun: dry}))
	}
	for _, fn := range []func(bool) (any, error){press, timer} {
		_, err := fn(false)
		if writeCode(t, err) != rpc.CodeWriteDisabled {
			t.Fatalf("expected write disabled: %v", err)
		}
	}
	writeAccess(t, path, `{"allow":[77],"write":[42]}`)
	for _, fn := range []func(bool) (any, error){press, timer} {
		_, err := fn(false)
		if writeCode(t, err) != rpc.CodeNotFound {
			t.Fatalf("expected not found: %v", err)
		}
	}
	writeAccess(t, path, `{"write":[42]}`)
	for _, fn := range []func(bool) (any, error){press, timer} {
		if _, err := fn(true); err != nil {
			t.Fatal(err)
		}
	}
	if f.presses != 0 || f.timers != 0 {
		t.Fatalf("dry run wrote: %+v", f)
	}
	for _, fn := range []func(bool) (any, error){press, timer} {
		if _, err := fn(false); err != nil {
			t.Fatal(err)
		}
	}
	if f.presses != 1 || f.timers != 1 {
		t.Fatalf("writes not delivered: %+v", f)
	}
}

func TestSelfDestructRejectsAlbumsAndNonPhotoVideo(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    rpc.SendMediaParams
		ok   bool
	}{
		{"album", rpc.SendMediaParams{As: "photo", Files: []string{"/a", "/b"}, SelfDestruct: "1"}, false},
		{"document", rpc.SendMediaParams{As: "document", Files: []string{"/a"}, SelfDestruct: "1"}, false},
		{"photo", rpc.SendMediaParams{As: "photo", Files: []string{"/a"}, SelfDestruct: "1"}, true},
		{"video", rpc.SendMediaParams{As: "video", Files: []string{"/a"}, SelfDestruct: "view-once"}, true},
	} {
		err := validSendMediaParams(tc.p)
		if tc.ok && err != nil {
			t.Errorf("%s: rejected: %v", tc.name, err)
		}
		if !tc.ok && (err == nil || audit05GuardCode(t, err) != rpc.CodeInvalidParams) {
			t.Errorf("%s: err=%v", tc.name, err)
		}
	}
}

// The TTL itself is a range the parser owns: out of range is refused by code.
func TestSelfDestructRejectsOutOfRangeTTL(t *testing.T) {
	err := validSendMediaParams(rpc.SendMediaParams{As: "video", Files: []string{"/a"}, SelfDestruct: "61"})
	if err == nil || audit05GuardCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("out of range TTL: %v", err)
	}
}

func TestSendInlineRejectsForeignAndExpiredQueries(t *testing.T) {
	d, _, _, path := accessTestDaemon(t)
	writeAccess(t, path, `{"write":[42]}`)
	ctx := context.Background()
	d.inlineQueries = map[string]inlineQueryEntry{"other": {chatID: 43, results: map[string]bool{"x": true}, expires: time.Now().Add(time.Minute)}, "expired": {chatID: 42, results: map[string]bool{"x": true}, expires: time.Now().Add(-time.Second)}, "valid": {chatID: 42, results: map[string]bool{"x": true}, expires: time.Now().Add(time.Minute)}}
	for _, query := range []string{"other", "expired", "missing"} {
		_, err := d.messageSendInline(ctx, params(t, rpc.SendInlineParams{Chat: "42", QueryID: query, Result: "x", DryRun: true}))
		if writeCode(t, err) != rpc.CodeInvalidParams {
			t.Errorf("%s: %v", query, err)
		}
	}
	if _, err := d.messageSendInline(ctx, params(t, rpc.SendInlineParams{Chat: "42", QueryID: "valid", Result: "x", DryRun: true})); err != nil {
		t.Fatal(err)
	}
}

func TestEditMediaChecksBeforeUploading(t *testing.T) {
	c := newMediaCase(t)
	c.tg.messages = []rpc.Message{{ID: 7, ChatID: 42, Text: "text"}}
	p := rpc.EditMediaParams{Chat: "42", ID: 7, As: "document", File: c.document, DryRun: true}
	if _, err := c.d.messageEditMedia(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeInvalidParams || len(c.opened) != 0 {
		t.Fatalf("no media: %v opened=%d", err, len(c.opened))
	}
	c.tg.messages[0].MediaType = "document"
	p.File = filepath.Join(c.base, "outside")
	if err := os.WriteFile(p.File, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.d.messageEditMedia(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeInvalidParams {
		t.Fatalf("outside: %v", err)
	}
	p.File = c.document
	got, err := c.d.messageEditMedia(context.Background(), params(t, p))
	if err != nil || !got.(rpc.EditMediaResult).DryRun {
		t.Fatalf("dry run: %+v %v", got, err)
	}
}

func (f *extrasTelegram) InlineQuery(_ context.Context, _ chatref.Ref, _ chatref.Ref, _ rpc.InlineQueryParams) (rpc.InlineQueryResult, error) {
	f.queries++
	id, cache := "123", 1
	if f.queryID != "" {
		id = f.queryID
	}
	if f.cacheTime != 0 {
		cache = f.cacheTime
	}
	return rpc.InlineQueryResult{QueryID: id, CacheTime: cache, Results: []rpc.InlineResult{{ID: "r"}}}, nil
}
func TestInlineQueryRequiresWriteAndVisibleBot(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &extrasTelegram{fakeTelegram: base}
	d.opts.Telegram = f
	ctx := context.Background()
	p := rpc.InlineQueryParams{Chat: "42", Bot: "6"}
	if _, err := d.messageInlineQuery(ctx, params(t, p)); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatalf("write: %v", err)
	}
	if f.queries != 0 {
		t.Fatal("queried without write")
	}
	writeAccess(t, path, `{"allow":[42],"write":[42]}`)
	if _, err := d.messageInlineQuery(ctx, params(t, p)); writeCode(t, err) != rpc.CodeNotFound {
		t.Fatalf("bot visibility: %v", err)
	}
	if f.queries != 0 {
		t.Fatal("queried hidden bot")
	}
}

func (f *extrasTelegram) SendSticker(_ context.Context, _ chatref.Ref, p rpc.SendStickerParams) (rpc.SendStickerResult, error) {
	if !p.DryRun {
		f.stickers++
	}
	return rpc.SendStickerResult{SendResult: rpc.SendResult{DryRun: p.DryRun, ID: 9, Date: "2026-01-01T00:00:00Z"}}, nil
}
func TestSendStickerAccessAndPreview(t *testing.T) {
	d, base, _, path := accessTestDaemon(t)
	f := &extrasTelegram{fakeTelegram: base}
	d.opts.Telegram = f
	index := 0
	p := rpc.SendStickerParams{Chat: "42", Set: "fixture", Index: &index}
	if _, err := d.messageSendSticker(context.Background(), params(t, p)); writeCode(t, err) != rpc.CodeWriteDisabled {
		t.Fatal(err)
	}
	writeAccess(t, path, `{"write":[42]}`)
	p.DryRun = true
	if _, err := d.messageSendSticker(context.Background(), params(t, p)); err != nil {
		t.Fatal(err)
	}
	if f.stickers != 0 {
		t.Fatal("preview sent sticker")
	}
}

func TestStickerSearchLimitsAndInstalled(t *testing.T) {
	d, _, _, _ := accessTestDaemon(t)
	ctx := context.Background()
	for _, limit := range []int{-1, 101} {
		_, err := d.stickerSearch(ctx, params(t, rpc.StickerSearchParams{Query: "cats", Limit: limit}))
		if writeCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	for _, limit := range []int{0, 1, 100} {
		if _, err := d.stickerSearch(ctx, params(t, rpc.StickerSearchParams{Query: "cats", Limit: limit})); err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	if _, err := d.stickerInstalled(ctx, nil); err != nil {
		t.Fatal(err)
	}
}
