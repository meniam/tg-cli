package cli

import (
	"encoding/json"
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
	for _, tc := range []struct {
		args   []string
		method string
	}{{[]string{"message", "buttons", "42", "7"}, rpc.MethodMessageButtons}, {[]string{"message", "press", "42", "7", "--button", "0", "--dry-run"}, rpc.MethodMessagePress}, {[]string{"chat", "auto-delete", "42", "--period", "1d", "--yes"}, rpc.MethodChatAutoDelete},
		{[]string{"message", "inline-query", "42", "--bot", "@fixturebot", "--query", "cats"}, rpc.MethodMessageInlineQuery},
		{[]string{"message", "send-inline", "42", "--query-id", "123", "--result", "r", "--dry-run"}, rpc.MethodMessageSendInline},
		{[]string{"message", "edit-media", "42", "7", "--as", "document", "--file", "/tmp/a", "--dry-run"}, rpc.MethodMessageEditMedia},
		{[]string{"message", "send-sticker", "42", "--set", "cats", "--index", "0", "--dry-run"}, rpc.MethodMessageSendSticker},
		{[]string{"sticker", "search", "cats"}, rpc.MethodStickerSearch}, {[]string{"sticker", "set", "cats"}, rpc.MethodStickerSet}, {[]string{"sticker", "installed"}, rpc.MethodStickerInstalled}} {
		r := runWriteCLI(t, tc.args, "", reply, nil)
		if r.code != exitcode.OK || r.method != tc.method {
			t.Errorf("%v: %+v", tc.args, r)
		}
	}
	for _, args := range [][]string{{"message", "press", "42", "7", "--button", "0"}, {"message", "press", "42", "7", "--button", "0", "--text", "go", "--yes"}, {"message", "edit-media", "42", "7", "--as", "document", "--file", "/tmp/a"}, {"chat", "auto-delete", "42", "--period", "off"}, {"chat", "auto-delete", "42", "--period", "3", "--yes"}} {
		r := runWriteCLI(t, args, "", reply, nil)
		if r.code != exitcode.Usage || r.method != "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
}
