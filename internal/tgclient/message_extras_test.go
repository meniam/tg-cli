package tgclient

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func TestPressMarkup(t *testing.T) {
	markup := &tg.ReplyInlineMarkup{Rows: []tg.KeyboardInlineButtonRow{{Buttons: []tg.KeyboardInlineButton{{Text: "go", Type: &tg.InlineButtonTypeCallback{Data: []byte("payload")}}}}}}
	index := 0
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		req, ok := in.(*tg.MessagesGetBotCallbackAnswerRequest)
		if !ok || req.MsgID != 7 || string(req.Data) != "payload" {
			t.Fatalf("unexpected request %T", in)
		}
		return tgerr.New(400, "BOT_RESPONSE_TIMEOUT")
	}))
	p := rpc.PressParams{ID: 7, Button: &index, DryRun: true}
	result, err := pressMarkup(context.Background(), api, &tg.InputPeerSelf{}, 1, p, markup)
	if err != nil || !result.DryRun || calls != 0 {
		t.Fatalf("dry run: %+v %v calls=%d", result, err, calls)
	}
	p.DryRun = false
	result, err = pressMarkup(context.Background(), api, &tg.InputPeerSelf{}, 1, p, markup)
	if err != nil || result.Answered || calls != 1 {
		t.Fatalf("timeout: %+v %v calls=%d", result, err, calls)
	}
}
func TestSetAutoDeleteRequest(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesSetHistoryTTLRequest)
		if !ok || req.Period != 86400 {
			t.Fatalf("unexpected request %T", in)
		}
		return respond(out, &tg.Updates{})
	}))
	if err := setAutoDelete(context.Background(), api, &tg.InputPeerSelf{}, 86400); err != nil {
		t.Fatal(err)
	}
}

func TestSelfDestructMediaSpec(t *testing.T) {
	for _, kind := range []string{"photo", "video"} {
		for _, ttl := range []string{"", "12", "view-once"} {
			spec, err := makeMediaSpec(&tg.InputFile{ID: 1, Parts: 1, Name: "x"}, rpc.SendMediaParams{As: kind, SelfDestruct: ttl}, rpc.MediaFile{Name: "x", Duration: 1, Width: 1, Height: 1}, "")
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if ttl != "" {
				want, err = rpc.ParseSelfDestruct(ttl)
				if err != nil {
					t.Fatal(err)
				}
			}
			var got int
			switch raw := spec.raw.(type) {
			case *tg.InputMediaUploadedPhoto:
				got = raw.TTLSeconds
			case *tg.InputMediaUploadedDocument:
				got = raw.TTLSeconds
			default:
				t.Fatalf("unexpected media %T", raw)
			}
			if got != want {
				t.Errorf("%s %q: got %d want %d", kind, ttl, got, want)
			}
		}
	}
}

func TestSendInlineRequest(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesSendInlineBotResultRequest)
		if !ok || req.QueryID != 123 || req.ID != "r" || req.HideVia || req.RandomID != deterministicID(5, 42, "key", 0) {
			t.Fatalf("unexpected request: %#v", in)
		}
		return respond(out, &tg.UpdateShortSentMessage{ID: 9, Date: 1700000000})
	}))
	res, msg, err := sendInline(context.Background(), api, &tg.InputPeerUser{UserID: 42}, 42, 5, rpc.SendInlineParams{QueryID: "123", Result: "r", IdempotencyKey: "key"})
	if err != nil || res.ID != 9 || msg == nil || msg.ID != 9 {
		t.Fatalf("%+v %+v %v", res, msg, err)
	}
}

func TestStickerMetadata(t *testing.T) {
	doc := &tg.Document{MimeType: "application/x-tgsticker", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeSticker{Alt: "🙂"}}, VideoThumbs: []tg.VideoSizeClass{&tg.VideoSize{Type: "f"}}}
	sticker := stickerOf(2, doc)
	if sticker.Index != 2 || sticker.Type != "animated" || !sticker.Premium || sticker.Emoji != "🙂" {
		t.Fatalf("%+v", sticker)
	}
	set := stickerSetInfo(tg.StickerSet{Title: "X", ShortName: "x"}, []tg.DocumentClass{doc, &tg.Document{MimeType: "video/webm"}})
	if !set.Animated || !set.Video {
		t.Fatalf("%+v", set)
	}
}

func TestSearchStickerSetsLimit(t *testing.T) {
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesSearchStickerSetsRequest)
		if !ok || req.Q != "cat" || req.ExcludeFeatured || req.Hash != 0 {
			t.Fatalf("request %#v", in)
		}
		return respond(out, &tg.MessagesFoundStickerSets{Sets: []tg.StickerSetCoveredClass{&tg.StickerSetNoCovered{Set: tg.StickerSet{ShortName: "one"}}, &tg.StickerSetNoCovered{Set: tg.StickerSet{ShortName: "two"}}}})
	}))
	sets, err := searchStickerSets(context.Background(), api, "cat", 1)
	if err != nil || len(sets) != 1 || sets[0].ShortName != "one" {
		t.Fatalf("%+v %v", sets, err)
	}
}

func TestEditMediaCaptionPresence(t *testing.T) {
	media := &tg.InputMediaUploadedPhoto{File: &tg.InputFile{ID: 1, Parts: 1, Name: "x"}}
	req := editMediaRequest(&tg.InputPeerSelf{}, 7, media, nil)
	if _, ok := req.GetMessage(); ok {
		t.Fatal("absent caption was sent")
	}
	empty := ""
	req = editMediaRequest(&tg.InputPeerSelf{}, 7, media, &empty)
	if value, ok := req.GetMessage(); !ok || value != "" {
		t.Fatal("empty caption was omitted")
	}
}

func TestInstalledStickerSetsOneRequestAndSummary(t *testing.T) {
	calls := 0
	api := tg.NewClient(writeInvoker(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		calls++
		if _, ok := in.(*tg.MessagesGetAllStickersRequest); !ok {
			t.Fatalf("unexpected %T", in)
		}
		return respond(out, &tg.MessagesAllStickers{Sets: []tg.StickerSet{{ShortName: "example", Title: "Example", Count: 3}}})
	}))
	sets, err := installedStickerSets(context.Background(), api)
	if err != nil || calls != 1 || len(sets) != 1 {
		t.Fatalf("sets=%v calls=%d err=%v", sets, calls, err)
	}
	encoded, err := json.Marshal(summarizeStickerSets(sets))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "animated") || strings.Contains(string(encoded), "video") {
		t.Fatalf("unexpected properties: %s", encoded)
	}
	summary := summarizeStickerSets(sets)
	if len(summary.Sets) != 1 || summary.Sets[0].ShortName != "example" || summary.Sets[0].Title != "Example" || summary.Sets[0].Count != 3 || summary.Sets[0].Type != "regular" {
		t.Fatalf("summary: %+v", summary)
	}
}

func TestSelectStickerByEmojiAndIndex(t *testing.T) {
	set := &tg.MessagesStickerSet{Documents: []tg.DocumentClass{&tg.Document{ID: 1}, &tg.Document{ID: 2}}, Packs: []tg.StickerPack{{Emoticon: "🙂", Documents: []int64{2}}}}
	emoji := "🙂"
	doc, index, err := selectSticker(set, rpc.SendStickerParams{Emoji: &emoji})
	if err != nil || index != 1 || doc.ID != 2 {
		t.Fatalf("doc=%v index=%d err=%v", doc, index, err)
	}
	n := 0
	doc, index, err = selectSticker(set, rpc.SendStickerParams{Index: &n})
	if err != nil || index != 0 || doc.ID != 1 {
		t.Fatalf("doc=%v index=%d err=%v", doc, index, err)
	}
}

func TestAudit09_SelectStickerErrors(t *testing.T) {
	set := &tg.MessagesStickerSet{Documents: []tg.DocumentClass{&tg.Document{ID: 1}, &tg.DocumentEmpty{ID: 2}}}
	zero, one, outOfRange, negative := 0, 1, 2, -1
	missing, present := "🙃", "🙂"
	for _, tc := range []struct {
		name string
		p    rpc.SendStickerParams
		code int
		msg  string
	}{
		{"neither", rpc.SendStickerParams{}, rpc.CodeInvalidParams, "choose exactly one of index or emoji"},
		{"both", rpc.SendStickerParams{Index: &zero, Emoji: &present}, rpc.CodeInvalidParams, "choose exactly one of index or emoji"},
		{"index out of range", rpc.SendStickerParams{Index: &outOfRange}, rpc.CodeInvalidParams, "sticker was not found in set"},
		{"negative index", rpc.SendStickerParams{Index: &negative}, rpc.CodeInvalidParams, "sticker was not found in set"},
		{"emoji not found", rpc.SendStickerParams{Emoji: &missing}, rpc.CodeInvalidParams, "sticker was not found in set"},
		{"invalid sticker", rpc.SendStickerParams{Index: &one}, rpc.CodeTelegram, "Telegram returned an invalid sticker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := selectSticker(set, tc.p)
			var re *rpc.Error
			if !errors.As(err, &re) || re.Code != tc.code || re.Message != tc.msg {
				t.Fatalf("error %v, want code %d %q", err, tc.code, tc.msg)
			}
		})
	}
}
