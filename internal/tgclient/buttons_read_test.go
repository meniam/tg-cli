package tgclient

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

func TestButtonsOf(t *testing.T) {
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{{Buttons: []tg.KeyboardInlineButton{
		{Text: "go", Type: &tg.InlineButtonTypeCallback{Data: []byte("secret")}},
		{Text: "locked", Type: &tg.InlineButtonTypeCallback{RequiresPassword: true}},
		{Text: "url", Type: &tg.InlineButtonTypeURL{URL: "https://example.test"}},
	}}, {Buttons: []tg.KeyboardInlineButton{{Text: "copy", Type: &tg.InlineButtonTypeCopy{CopyText: "content"}}}}}}
	got := buttonsOf(1, 2, markup)
	if got.Keyboard != "inline" || len(got.Buttons) != 4 || !got.Buttons[0].Pressable || got.Buttons[1].Pressable || got.Buttons[1].Type != "callback_password" || got.Buttons[3].Index != 3 || got.Buttons[3].Row != 1 || got.Buttons[3].Column != 0 {
		t.Fatalf("unexpected result: %+v", got)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatal("callback data leaked")
	}
	reply := buttonsOf(1, 2, &tg.ReplyKeyboardMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButton{{Text: "phone", Type: &tg.ButtonTypeRequestPhone{}}}}}})
	if reply.Keyboard != "reply" || len(reply.Buttons) != 1 || reply.Buttons[0].Type != "request_phone" || reply.Buttons[0].Pressable {
		t.Fatalf("unexpected reply: %+v", reply)
	}
}
