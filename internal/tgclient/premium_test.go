package tgclient

import (
	"context"
	"errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"testing"
	"tg-cli/internal/rpc"
)

func TestPremiumErrors(t *testing.T) {
	for _, tc := range []struct{ code, reason string }{{"PREMIUM_ACCOUNT_REQUIRED", "premium_required"}, {"PRIVACY_PREMIUM_REQUIRED", "recipient_requires_premium"}} {
		for _, rich := range []bool{false, true} {
			err := writeFormatError(tgerr.New(403, tc.code), rich)
			var e *rpc.Error
			if !errors.As(err, &e) || e.Code != rpc.CodeTelegram || e.Data.(map[string]any)["reason"] != tc.reason {
				t.Fatalf("%s rich=%v: %v", tc.code, rich, err)
			}
			want := tc.code
			if rich {
				want = "Telegram rejected rich message"
			}
			if e.Message != want {
				t.Fatal(e.Message)
			}
		}
	}
}

func TestPremiumDropped(t *testing.T) {
	request := []tg.MessageEntityClass{&tg.MessageEntityCustomEmoji{DocumentID: 1}}
	absent := &tg.UpdateShortSentMessage{ID: 7}
	present := &tg.UpdateShortSentMessage{ID: 7}
	present.SetEntities([]tg.MessageEntityClass{&tg.MessageEntityBold{}})
	retained := &tg.UpdateShortSentMessage{ID: 7}
	retained.SetEntities(request)
	if len(premiumDropped(absent, 7, request)) != 0 || len(premiumDropped(retained, 7, request)) != 0 || len(premiumDropped(present, 7, nil)) != 0 || len(premiumDropped(present, 7, request)) != 1 {
		t.Fatal("incorrect custom emoji detection")
	}
}

func TestPremiumFeatures(t *testing.T) {
	c := New(Config{AppID: 1, AppHash: "test"})
	for _, p := range []rpc.SendParams{
		{Text: `<tg-emoji emoji-id="123">👍</tg-emoji>`, Parse: "html"},
		{Text: `![👍](tg://emoji?id=123)`, Parse: "markdown"},
		{Text: "👍", Entities: []rpc.Entity{{Type: "custom_emoji", Offset: 0, Length: 2, CustomEmojiID: 123}}},
	} {
		got, err := c.CheckText(context.Background(), p)
		if err != nil || len(got.PremiumFeatures) != 1 || got.PremiumFeatures[0] != "custom_emoji" {
			t.Fatalf("%+v: %+v %v", p, got, err)
		}
	}
}

func TestPremiumDroppedSendResponse(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesSendMessageRequest)
		if !ok || customEmojiCount(req.Entities) != 1 {
			t.Fatalf("request: %+v", in)
		}
		result := &tg.UpdateShortSentMessage{ID: 7, Date: 1700000000}
		result.SetEntities([]tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 2}})
		return respond(out, result)
	}))
	p := rpc.SendParams{Text: "👍", Entities: []rpc.Entity{{Type: "custom_emoji", Offset: 0, Length: 2, CustomEmojiID: 123}}}
	got, err := sendText(context.Background(), api, &tg.InputPeerUser{UserID: 1, AccessHash: 2}, 1, p)
	if err != nil || len(got.PremiumDropped) != 1 || got.PremiumDropped[0] != "custom_emoji" {
		t.Fatalf("result: %+v %v", got, err)
	}
}

func TestPremiumWriteErrors(t *testing.T) {
	peer := &tg.InputPeerUser{UserID: 1, AccessHash: 2}
	for _, action := range []string{"send", "edit", "rich", "media"} {
		t.Run(action, func(t *testing.T) {
			api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
				if _, ok := in.(*tg.UploadSaveFilePartRequest); ok {
					return uploadOK(out)
				}
				return tgerr.New(403, "PREMIUM_ACCOUNT_REQUIRED")
			}))
			var err error
			switch action {
			case "send":
				_, err = sendText(context.Background(), api, peer, 1, rpc.SendParams{Text: "hello"})
			case "edit":
				_, err = editText(context.Background(), api, peer, 1, rpc.EditParams{ID: 1, Text: "hello"})
			case "rich":
				_, err = sendText(context.Background(), api, peer, 1, rpc.SendParams{Text: "hello", Parse: "rich-html"})
			case "media":
				file := mediaFixture(t, "x.bin", 10)
				_, err = sendMedia(context.Background(), api, peer, 1, rpc.SendMediaParams{As: "document"}, []rpc.MediaFile{file})
			}
			var e *rpc.Error
			if !errors.As(err, &e) || e.Code != rpc.CodeTelegram || e.Data.(map[string]any)["reason"] != "premium_required" {
				t.Fatalf("error: %v", err)
			}
		})
	}
}
