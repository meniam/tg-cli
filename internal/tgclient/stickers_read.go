package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func getStickerSet(ctx context.Context, api *tg.Client, name string) (*tg.MessagesStickerSet, error) {
	name, err := rpc.StickerShortName(name)
	if err != nil {
		return nil, err
	}
	raw, err := api.MessagesGetStickerSet(ctx, &tg.MessagesGetStickerSetRequest{Stickerset: &tg.InputStickerSetShortName{ShortName: name}})
	if tgerr.Is(err, "STICKERSET_INVALID") {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "invalid sticker set")
	}
	if err != nil {
		return nil, mapError(err)
	}
	set, ok := raw.(*tg.MessagesStickerSet)
	if !ok {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned an incomplete sticker set")
	}
	return set, nil
}
func stickerSetInfo(set tg.StickerSet, docs []tg.DocumentClass) rpc.StickerSetInfo {
	info := rpc.StickerSetInfo{ShortName: set.ShortName, Title: set.Title, Count: set.Count, Type: "regular", Official: set.Official}
	if set.Masks {
		info.Type = "masks"
	}
	if set.Emojis {
		info.Type = "emojis"
	}
	for _, raw := range docs {
		if d, ok := raw.(*tg.Document); ok {
			switch d.MimeType {
			case "application/x-tgsticker":
				info.Animated = true
			case "video/webm":
				info.Video = true
			}
		}
	}
	return info
}
func stickerOf(index int, d *tg.Document) rpc.Sticker {
	sticker := rpc.Sticker{Index: index, Type: "static"}
	switch d.MimeType {
	case "application/x-tgsticker":
		sticker.Type = "animated"
	case "video/webm":
		sticker.Type = "video"
	}
	for _, a := range d.Attributes {
		if v, ok := a.(*tg.DocumentAttributeSticker); ok {
			sticker.Emoji = v.Alt
			break
		}
	}
	for _, raw := range d.VideoThumbs {
		if v, ok := raw.(*tg.VideoSize); ok && v.Type == "f" {
			sticker.Premium = true
			break
		}
	}
	return sticker
}
func (c *Client) StickerSet(ctx context.Context, name string) (rpc.StickerSetResult, error) {
	raw, err := getStickerSet(ctx, c.tg.API(), name)
	if err != nil {
		return rpc.StickerSetResult{}, err
	}
	result := rpc.StickerSetResult{Set: stickerSetInfo(raw.Set, raw.Documents), Stickers: []rpc.Sticker{}}
	for i, v := range raw.Documents {
		if d, ok := v.(*tg.Document); ok {
			result.Stickers = append(result.Stickers, stickerOf(i, d))
		}
	}
	return result, nil
}

func searchStickerSets(ctx context.Context, api *tg.Client, query string, limit int) ([]tg.StickerSet, error) {
	raw, err := api.MessagesSearchStickerSets(ctx, &tg.MessagesSearchStickerSetsRequest{Q: query})
	if err != nil {
		return nil, mapError(err)
	}
	response, ok := raw.(*tg.MessagesFoundStickerSets)
	if !ok {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned incomplete sticker sets")
	}
	sets := make([]tg.StickerSet, 0, min(limit, len(response.Sets)))
	for _, raw := range response.Sets {
		var set tg.StickerSet
		switch v := raw.(type) {
		case *tg.StickerSetCovered:
			set = v.Set
		case *tg.StickerSetMultiCovered:
			set = v.Set
		case *tg.StickerSetFullCovered:
			set = v.Set
		case *tg.StickerSetNoCovered:
			set = v.Set
		default:
			continue
		}
		sets = append(sets, set)
		if len(sets) == limit {
			break
		}
	}
	return sets, nil
}
func installedStickerSets(ctx context.Context, api *tg.Client) ([]tg.StickerSet, error) {
	raw, err := api.MessagesGetAllStickers(ctx, 0)
	if err != nil {
		return nil, mapError(err)
	}
	response, ok := raw.(*tg.MessagesAllStickers)
	if !ok {
		return nil, rpc.NewError(rpc.CodeTelegram, "Telegram returned incomplete sticker sets")
	}
	return response.Sets, nil
}

func summarizeStickerSets(sets []tg.StickerSet) rpc.StickerSetsResult {
	out := rpc.StickerSetsResult{Sets: make([]rpc.StickerSetSummary, 0, len(sets))}
	for _, set := range sets {
		info := stickerSetInfo(set, nil)
		out.Sets = append(out.Sets, rpc.StickerSetSummary{ShortName: info.ShortName, Title: info.Title, Count: info.Count, Type: info.Type, Official: info.Official})
	}
	return out
}
func (c *Client) SearchStickerSets(ctx context.Context, p rpc.StickerSearchParams) (rpc.StickerSetsResult, error) {
	sets, err := searchStickerSets(ctx, c.tg.API(), p.Query, p.Limit)
	if err != nil {
		return rpc.StickerSetsResult{}, err
	}
	return summarizeStickerSets(sets), nil
}
func (c *Client) InstalledStickerSets(ctx context.Context) (rpc.StickerSetsResult, error) {
	sets, err := installedStickerSets(ctx, c.tg.API())
	if err != nil {
		return rpc.StickerSetsResult{}, err
	}
	return summarizeStickerSets(sets), nil
}
