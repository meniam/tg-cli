package tgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestTranslateRequest(t *testing.T) {
	for _, name := range []string{"", "TO_LANG_INVALID", "MSG_ID_INVALID", "INPUT_TEXT_EMPTY", "INPUT_TEXT_TOO_LONG", "TRANSLATIONS_DISABLED", "TRANSLATE_REQ_FAILED", "TRANSLATE_REQ_QUOTA_EXCEEDED", "TRANSLATION_TIMEOUT", "FLOOD_WAIT_40", "count"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				calls++
				req, ok := in.(*tg.MessagesTranslateTextRequest)
				if !ok || len(req.ID) != 2 || req.ID[0] != 9 || req.ID[1] != 3 || req.ToLang != "en" || req.Peer == nil || len(req.Text) != 0 || req.Tone != "" {
					t.Fatalf("request: %#v", in)
				}
				if name != "" && name != "count" {
					return tgerr.New(400, name)
				}
				result := []tg.TextWithEntities{{Text: "first"}, {Text: "second"}}
				if name == "count" {
					result = result[:1]
				}
				return respond(out, &tg.MessagesTranslateResult{Result: result})
			}))
			peer := testPeers(api).User(user(9, "A", "", "", false))
			got, err := translateResolved(context.Background(), api, peer, rpc.TranslateParams{IDs: []int{9, 3}, ToLang: "EN"})
			if calls != 1 {
				t.Fatalf("calls: %d", calls)
			}
			if name == "" {
				if err != nil || got.ToLang != "en" || got.Items[0].ID != 9 || got.Items[1].Text != "second" {
					t.Fatalf("result: %+v %v", got, err)
				}
				return
			}
			var e *rpc.Error
			if !errors.As(err, &e) {
				t.Fatalf("error: %v", err)
			}
			want := rpc.CodeTelegram
			switch name {
			case "TO_LANG_INVALID", "MSG_ID_INVALID", "INPUT_TEXT_EMPTY", "INPUT_TEXT_TOO_LONG":
				want = rpc.CodeInvalidParams
			case "FLOOD_WAIT_40":
				want = rpc.CodeFloodWait
			}
			if e.Code != want {
				t.Fatalf("error: %+v", e)
			}
			if name == "count" && e.Message != "Telegram returned an unexpected translation count" {
				t.Fatalf("count error message: %q", e.Message)
			}
			if name == "TRANSLATIONS_DISABLED" && audit10Reason(e) != "translations_disabled" {
				t.Fatal(e)
			}
			if name == "TRANSLATE_REQ_FAILED" || name == "TRANSLATE_REQ_QUOTA_EXCEEDED" || name == "TRANSLATION_TIMEOUT" {
				if audit10Reason(e) != "translation_unavailable" {
					t.Fatal(e)
				}
			}
		})
	}
}

// TestTranslateValidation checks that translateResolved refuses invalid
// parameters with invalid_params before it makes any Telegram request.
// rpc.ValidateTranslate itself belongs to internal/rpc (see the audit report).
func TestTranslateValidation(t *testing.T) {
	for _, p := range []rpc.TranslateParams{{ToLang: "en"}, {IDs: make([]int, 21), ToLang: "en"}, {IDs: []int{1, 1}, ToLang: "en"}, {IDs: []int{0}, ToLang: "en"}, {IDs: []int{-3}, ToLang: "en"}, {IDs: []int{1}, ToLang: "english"}, {IDs: []int{1}, ToLang: "e1"}} {
		err := rpc.ValidateTranslate(&p)
		if err == nil || rpcErrorCode(t, err) != rpc.CodeInvalidParams {
			t.Fatalf("ValidateTranslate(%+v) = %v, want invalid_params", p, err)
		}
		calls := 0
		api := tg.NewClient(writeInvoker(func(context.Context, bin.Encoder, bin.Decoder) error { calls++; return nil }))
		peer := testPeers(api).User(user(9, "A", "", "", false))
		if _, err := translateResolved(context.Background(), api, peer, p); err == nil || rpcErrorCode(t, err) != rpc.CodeInvalidParams || calls != 0 {
			t.Fatalf("translateResolved(%+v) = %v with %d requests, want invalid_params without a request", p, err, calls)
		}
	}
}
