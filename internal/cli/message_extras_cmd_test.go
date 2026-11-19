package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
)

func TestMessageExtrasCLI(t *testing.T) {
	reply := func(method string, _ json.RawMessage) (any, error) {
		switch method {
		case rpc.MethodMessageButtons:
			return rpc.ButtonsResult{Buttons: []rpc.Button{}}, nil
		case rpc.MethodMessagePress:
			return rpc.PressResult{}, nil
		case rpc.MethodMessageInlineQuery:
			return rpc.InlineQueryResult{Results: []rpc.InlineResult{}}, nil
		case rpc.MethodMessageSendInline:
			return rpc.SendResult{}, nil
		case rpc.MethodMessageEditMedia:
			return rpc.EditMediaResult{}, nil
		case rpc.MethodMessageSendSticker:
			return rpc.SendStickerResult{}, nil
		case rpc.MethodStickerSet:
			return rpc.StickerSetResult{Stickers: []rpc.Sticker{}}, nil
		case rpc.MethodStickerSearch, rpc.MethodStickerInstalled:
			return rpc.StickerSetsResult{Sets: []rpc.StickerSetSummary{}}, nil
		default:
			return rpc.AutoDeleteResult{}, nil
		}
	}
	zero, text := 0, "go"
	for _, tc := range []struct {
		args   []string
		method string
		params func(*testing.T, json.RawMessage)
	}{
		{[]string{"message", "buttons", "42", "7"}, rpc.MethodMessageButtons, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.ButtonsParams{Chat: "42", ID: 7})
		}},
		// Button 0 must travel as a pointer to 0, not as an absent field.
		{[]string{"message", "press", "42", "7", "--button", "0", "--dry-run"}, rpc.MethodMessagePress, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.PressParams{Chat: "42", ID: 7, Button: &zero, DryRun: true})
		}},
		{[]string{"message", "press", "42", "7", "--text", "go", "--yes"}, rpc.MethodMessagePress, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.PressParams{Chat: "42", ID: 7, Text: &text})
		}},
		{[]string{"chat", "auto-delete", "42", "--period", "1d", "--yes"}, rpc.MethodChatAutoDelete, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.AutoDeleteParams{Chat: "42", Period: "1d"})
		}},
		{[]string{"message", "inline-query", "42", "--bot", "@fixturebot", "--query", "cats", "--offset", "9"}, rpc.MethodMessageInlineQuery, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.InlineQueryParams{Chat: "42", Bot: "@fixturebot", Query: "cats", Offset: "9"})
		}},
		{[]string{"message", "send-inline", "42", "--query-id", "123", "--result", "r", "--reply-to", "7", "--topic", "8", "--silent", "--idempotency-key", "k1", "--dry-run"}, rpc.MethodMessageSendInline, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.SendInlineParams{Chat: "42", QueryID: "123", Result: "r", ReplyTo: 7, Topic: 8, Silent: true, IdempotencyKey: "k1", DryRun: true})
		}},
		{[]string{"message", "edit-media", "42", "7", "--as", "document", "--file", "/tmp/a", "--caption", "cap", "--dry-run"}, rpc.MethodMessageEditMedia, func(t *testing.T, raw json.RawMessage) {
			caption := "cap"
			audit02Params(t, raw, rpc.EditMediaParams{Chat: "42", ID: 7, As: "document", File: "/tmp/a", Caption: &caption, DryRun: true})
		}},
		{[]string{"message", "send-sticker", "42", "--set", "cats", "--index", "0", "--dry-run"}, rpc.MethodMessageSendSticker, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.SendStickerParams{Chat: "42", Set: "cats", Index: &zero, DryRun: true})
		}},
		{[]string{"sticker", "search", "cats", "--limit", "5"}, rpc.MethodStickerSearch, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.StickerSearchParams{Query: "cats", Limit: 5})
		}},
		{[]string{"sticker", "set", "cats"}, rpc.MethodStickerSet, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, rpc.StickerSetParams{Name: "cats"})
		}},
		{[]string{"sticker", "installed"}, rpc.MethodStickerInstalled, func(t *testing.T, raw json.RawMessage) {
			audit02Params(t, raw, struct{}{})
		}},
	} {
		r := runWriteCLI(t, tc.args, "", reply, nil)
		if r.code != exitcode.OK || r.method != tc.method {
			t.Errorf("%v: %+v", tc.args, r)
			continue
		}
		tc.params(t, r.params)
		if ok, _ := audit02Envelope(t, r.stdout); !ok {
			t.Errorf("%v: envelope is not ok: %s", tc.args, r.stdout)
		}
	}
	// Each refusal must name its own reason: a refusal for another reason (an
	// unknown flag, a missing argument) must not pass for it.
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"message", "press", "42", "7", "--button", "0"}, "add --yes to confirm"},
		{[]string{"message", "press", "42", "7", "--yes"}, "choose exactly one of --button or --text"},
		{[]string{"message", "press", "42", "7", "--button", "0", "--text", "go", "--yes"}, "choose exactly one of --button or --text"},
		{[]string{"message", "press", "42", "7", "--button", "-1", "--yes"}, "button index must not be negative"},
		{[]string{"message", "edit-media", "42", "7", "--as", "document", "--file", "/tmp/a"}, "add --yes to confirm"},
		{[]string{"message", "edit-media", "42", "7", "--file", "/tmp/a", "--dry-run"}, "--as and --file are required"},
		{[]string{"chat", "auto-delete", "42", "--period", "off"}, "add --yes to confirm"},
		{[]string{"chat", "auto-delete", "42", "--period", "3", "--yes"}, "period must be off, 1d, 1w, 1m or seconds"},
		{[]string{"message", "send-sticker", "42", "--set", "cats", "--dry-run"}, "choose exactly one of --index or --emoji"},
		{[]string{"message", "send-sticker", "42", "--set", "cats", "--index", "-1", "--dry-run"}, "index must not be negative"},
		{[]string{"message", "inline-query", "42", "--bot", "@fixturebot"}, "--bot and --query are required"},
		{[]string{"message", "send-inline", "42", "--query-id", "123", "--dry-run"}, "query-id and result are required"},
	} {
		r := runWriteCLI(t, tc.args, "", reply, nil)
		if r.code != exitcode.Usage || r.method != "" || !strings.Contains(r.stdout, tc.want) {
			t.Errorf("%v: want a usage refusal with %q, got %+v", tc.args, tc.want, r)
		}
	}
}
