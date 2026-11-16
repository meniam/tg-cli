package daemon

import (
	"context"
	"errors"
	"testing"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
)

type translationFake struct {
	*fakeTelegram
	calls int
}

func (f *translationFake) Translate(_ context.Context, _ chatref.Ref, p rpc.TranslateParams) (rpc.TranslateResult, error) {
	f.calls++
	return rpc.TranslateResult{ChatID: 77, ToLang: p.ToLang, Items: []rpc.TranslationItem{{ID: p.IDs[0], Text: "translated"}}}, nil
}
func TestTranslateAccess(t *testing.T) {
	d, base, db, path := accessTestDaemon(t)
	f := &translationFake{fakeTelegram: base}
	d.opts.Telegram = f
	p := rpc.TranslateParams{Chat: "77", IDs: []int{1}, ToLang: "EN"}
	v, err := d.messageTranslate(context.Background(), params(t, p))
	if err != nil || v.(rpc.TranslateResult).ToLang != "en" || f.calls != 1 {
		t.Fatalf("result: %+v %v calls=%d", v, err, f.calls)
	}
	if _, err := db.GetMessage(context.Background(), 77, 1); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unexpected stored message or error: %v", err)
	}
	writeAccess(t, path, `{"block":[77]}`)
	_, err = d.messageTranslate(context.Background(), params(t, p))
	if writeCode(t, err) != rpc.CodeNotFound || f.calls != 1 {
		t.Fatalf("closed: %v calls=%d", err, f.calls)
	}
}
