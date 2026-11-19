package tgclient

import (
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
	"tg-cli/internal/rpc"
)

func TestAudit08_ButtonsOfInlineWholeResult(t *testing.T) {
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{
		{Buttons: []tg.KeyboardInlineButton{
			{Text: "go", Type: &tg.InlineButtonTypeCallback{Data: []byte("secret")}},
			{Text: "locked", Type: &tg.InlineButtonTypeCallback{RequiresPassword: true}},
			{Text: "url", Type: &tg.InlineButtonTypeURL{URL: "https://example.test"}},
		}},
		{Buttons: []tg.KeyboardInlineButton{
			{Text: "copy", Type: &tg.InlineButtonTypeCopy{CopyText: "content"}},
			{Text: "profile", Type: &tg.InlineButtonTypeUserProfile{UserID: 77}},
			{Text: "gone", Type: nil},
		}},
	}}
	got := buttonsOf(1, 2, markup)
	want := rpc.ButtonsResult{ChatID: 1, MessageID: 2, Keyboard: "inline", Buttons: []rpc.Button{
		{Index: 0, Row: 0, Column: 0, Text: "go", Type: "callback", Pressable: true},
		{Index: 1, Row: 0, Column: 1, Text: "locked", Type: "callback_password"},
		{Index: 2, Row: 0, Column: 2, Text: "url", Type: "url", URL: "https://example.test"},
		{Index: 3, Row: 1, Column: 0, Text: "copy", Type: "copy", CopyText: "content"},
		{Index: 4, Row: 1, Column: 1, Text: "profile", Type: "user_profile", UserID: 77},
		{Index: 5, Row: 1, Column: 2, Text: "gone", Type: "disabled"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("inline result:\n got %+v\nwant %+v", got, want)
	}
}

func TestAudit08_ButtonsOfReplyWholeResult(t *testing.T) {
	got := buttonsOf(1, 2, &tg.ReplyKeyboardMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButton{
		{Text: "plain", Type: &tg.ButtonTypeDefault{}},
		{Text: "phone", Type: &tg.ButtonTypeRequestPhone{}},
		{Text: "gone", Type: nil},
	}}}})
	want := rpc.ButtonsResult{ChatID: 1, MessageID: 2, Keyboard: "reply", Buttons: []rpc.Button{
		{Index: 0, Row: 0, Column: 0, Text: "plain", Type: "text"},
		{Index: 1, Row: 0, Column: 1, Text: "phone", Type: "request_phone"},
		{Index: 2, Row: 0, Column: 2, Text: "gone", Type: "disabled"},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reply result:\n got %+v\nwant %+v", got, want)
	}
}

func TestAudit08_ButtonsOfWithoutKeyboard(t *testing.T) {
	want := rpc.ButtonsResult{ChatID: 1, MessageID: 2, Keyboard: "none", Buttons: []rpc.Button{}}
	for name, markup := range map[string]tg.ReplyMarkupClass{
		"nil markup":      nil,
		"hidden keyboard": &tg.ReplyKeyboardHide{},
	} {
		if got := buttonsOf(1, 2, markup); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}
}
