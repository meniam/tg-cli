package cli

import (
	"context"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (*fakeTelegram) MessageButtons(context.Context, chatref.Ref, int) (rpc.ButtonsResult, error) {
	return rpc.ButtonsResult{ChatID: 5, MessageID: 30, Keyboard: "inline", Buttons: []rpc.Button{{Index: 0, Text: "go", Type: "callback", Pressable: true}}}, nil
}
func (*fakeTelegram) PressButton(_ context.Context, _ chatref.Ref, p rpc.PressParams) (rpc.PressResult, error) {
	return rpc.PressResult{ChatID: 5, MessageID: p.ID, Button: rpc.PressButton{Text: "go", Type: "callback"}, DryRun: p.DryRun}, nil
}
func (*fakeTelegram) SetAutoDelete(context.Context, chatref.Ref, int) error { return nil }
func (*fakeTelegram) InlineQuery(context.Context, chatref.Ref, chatref.Ref, rpc.InlineQueryParams) (rpc.InlineQueryResult, error) {
	return rpc.InlineQueryResult{QueryID: "123", CacheTime: 60, Results: []rpc.InlineResult{{ID: "result", Type: "article", Title: "Title"}}}, nil
}
func (*fakeTelegram) SendInline(context.Context, chatref.Ref, rpc.SendInlineParams) (rpc.SendResult, *rpc.Message, error) {
	return rpc.SendResult{}, nil, nil
}
func (*fakeTelegram) EditMedia(context.Context, chatref.Ref, rpc.EditMediaParams, rpc.MediaFile) (rpc.SendResult, error) {
	return rpc.SendResult{}, nil
}
func (*fakeTelegram) StickerSet(context.Context, string) (rpc.StickerSetResult, error) {
	return rpc.StickerSetResult{Set: rpc.StickerSetInfo{ShortName: "fixture", Title: "Fixture", Type: "regular"}, Stickers: []rpc.Sticker{{Index: 0, Emoji: "🙂", Type: "static"}}}, nil
}
func (*fakeTelegram) SendSticker(_ context.Context, _ chatref.Ref, p rpc.SendStickerParams) (rpc.SendStickerResult, error) {
	return rpc.SendStickerResult{SendResult: rpc.SendResult{ChatID: 5, DryRun: p.DryRun}, Sticker: &rpc.Sticker{Index: 0, Emoji: "🙂", Type: "static"}}, nil
}
func (*fakeTelegram) SearchStickerSets(context.Context, rpc.StickerSearchParams) (rpc.StickerSetsResult, error) {
	return rpc.StickerSetsResult{Sets: []rpc.StickerSetSummary{{ShortName: "fixture", Title: "Fixture", Type: "regular"}}}, nil
}
func (*fakeTelegram) InstalledStickerSets(context.Context) (rpc.StickerSetsResult, error) {
	return rpc.StickerSetsResult{Sets: []rpc.StickerSetSummary{{ShortName: "fixture", Title: "Fixture", Type: "regular"}}}, nil
}
