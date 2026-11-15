package daemon

import (
	"context"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (*fakeTelegram) MessageButtons(context.Context, chatref.Ref, int) (rpc.ButtonsResult, error) {
	return rpc.ButtonsResult{}, nil
}
func (*fakeTelegram) PressButton(context.Context, chatref.Ref, rpc.PressParams) (rpc.PressResult, error) {
	return rpc.PressResult{}, nil
}
func (*fakeTelegram) SetAutoDelete(context.Context, chatref.Ref, int) error { return nil }
func (*fakeTelegram) InlineQuery(context.Context, chatref.Ref, chatref.Ref, rpc.InlineQueryParams) (rpc.InlineQueryResult, error) {
	return rpc.InlineQueryResult{}, nil
}
func (*fakeTelegram) SendInline(context.Context, chatref.Ref, rpc.SendInlineParams) (rpc.SendResult, *rpc.Message, error) {
	return rpc.SendResult{}, nil, nil
}
func (*fakeTelegram) EditMedia(context.Context, chatref.Ref, rpc.EditMediaParams, rpc.MediaFile) (rpc.SendResult, error) {
	return rpc.SendResult{}, nil
}
func (*fakeTelegram) StickerSet(context.Context, string) (rpc.StickerSetResult, error) {
	return rpc.StickerSetResult{}, nil
}
func (*fakeTelegram) SendSticker(context.Context, chatref.Ref, rpc.SendStickerParams) (rpc.SendStickerResult, error) {
	return rpc.SendStickerResult{}, nil
}
func (*fakeTelegram) SearchStickerSets(context.Context, rpc.StickerSearchParams) (rpc.StickerSetsResult, error) {
	return rpc.StickerSetsResult{Sets: []rpc.StickerSetSummary{}}, nil
}
func (*fakeTelegram) InstalledStickerSets(context.Context) (rpc.StickerSetsResult, error) {
	return rpc.StickerSetsResult{Sets: []rpc.StickerSetSummary{}}, nil
}
