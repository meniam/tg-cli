package tgclient

import (
	"context"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

func (c *Client) Translate(ctx context.Context, ref chatref.Ref, p rpc.TranslateParams) (rpc.TranslateResult, error) {
	if err := rpc.ValidateTranslate(&p); err != nil {
		return rpc.TranslateResult{}, err
	}
	peer, err := c.resolve(ctx, ref)
	if err != nil {
		return rpc.TranslateResult{}, err
	}
	return translateResolved(ctx, c.tg.API(), peer, p)
}

func translateResolved(ctx context.Context, api *tg.Client, peer peers.Peer, p rpc.TranslateParams) (rpc.TranslateResult, error) {
	if err := rpc.ValidateTranslate(&p); err != nil {
		return rpc.TranslateResult{}, err
	}
	v, err := api.MessagesTranslateText(ctx, &tg.MessagesTranslateTextRequest{Peer: peer.InputPeer(), ID: p.IDs, ToLang: p.ToLang})
	if err != nil {
		if e, ok := tgerr.As(err); ok {
			switch e.Type {
			case "TO_LANG_INVALID":
				return rpc.TranslateResult{}, rpc.NewError(rpc.CodeInvalidParams, "target language is not supported by Telegram")
			case "MSG_ID_INVALID":
				return rpc.TranslateResult{}, rpc.NewError(rpc.CodeInvalidParams, "message id is invalid")
			case "INPUT_TEXT_EMPTY":
				return rpc.TranslateResult{}, rpc.NewError(rpc.CodeInvalidParams, "message has no text to translate")
			case "INPUT_TEXT_TOO_LONG":
				return rpc.TranslateResult{}, rpc.NewError(rpc.CodeInvalidParams, "message text is too long to translate")
			case "TRANSLATIONS_DISABLED":
				return rpc.TranslateResult{}, &rpc.Error{Code: rpc.CodeTelegram, Message: e.Type, Data: map[string]any{"reason": "translations_disabled"}}
			case "TRANSLATE_REQ_QUOTA_EXCEEDED", "TRANSLATE_REQ_FAILED", "TRANSLATION_TIMEOUT":
				return rpc.TranslateResult{}, &rpc.Error{Code: rpc.CodeTelegram, Message: e.Type, Data: map[string]any{"reason": "translation_unavailable"}}
			}
		}
		return rpc.TranslateResult{}, mapError(err)
	}
	if len(v.Result) != len(p.IDs) {
		return rpc.TranslateResult{}, rpc.NewError(rpc.CodeTelegram, "Telegram returned an unexpected translation count")
	}
	result := rpc.TranslateResult{ChatID: toChatOf(peer).ID, ToLang: p.ToLang, Items: make([]rpc.TranslationItem, len(p.IDs))}
	for i, id := range p.IDs {
		result.Items[i] = rpc.TranslationItem{ID: id, Text: v.Result[i].Text}
	}
	return result, nil
}
