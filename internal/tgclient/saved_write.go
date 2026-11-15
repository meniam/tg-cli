package tgclient

import (
	"context"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/rpc"
)

func (c *Client) RenameSavedTag(ctx context.Context, p rpc.SavedTagRenameParams) error {
	req := &tg.MessagesUpdateSavedReactionTagRequest{Reaction: &tg.ReactionEmoji{Emoticon: p.Emoji}}
	if p.Title != nil {
		req.SetTitle(*p.Title)
	}
	ok, err := c.tg.API().MessagesUpdateSavedReactionTag(ctx, req)
	if tgerr.Is(err, "PREMIUM_ACCOUNT_REQUIRED") {
		return rpc.NewError(rpc.CodeTelegram, "Telegram Premium is required for saved message tags")
	}
	if err != nil {
		return mapError(err)
	}
	if !ok {
		return rpc.NewError(rpc.CodeTelegram, "Telegram rejected the tag update")
	}
	return nil
}
