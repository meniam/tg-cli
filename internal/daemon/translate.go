package daemon

import (
	"context"
	"encoding/json"
	"tg-cli/internal/chatref"
	"tg-cli/internal/rpc"
)

type translateTelegram interface {
	Translate(context.Context, chatref.Ref, rpc.TranslateParams) (rpc.TranslateResult, error)
}

func (d *daemon) messageTranslate(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.TranslateParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidateTranslate(&p); err != nil {
		return nil, err
	}
	_, chat, err := d.listChat(ctx, p.Chat)
	if err != nil {
		return nil, err
	}
	client, ok := d.telegram(ctx).(translateTelegram)
	if !ok {
		return nil, rpc.NewError(rpc.CodeInternal, "translation unavailable")
	}
	result, err := client.Translate(ctx, chatref.Ref{Kind: chatref.ID, ID: chat.ID}, p)
	if err != nil {
		return nil, err
	}
	if result.ChatID != chat.ID || !d.policy().Allowed(chat.ID) {
		return nil, rpc.ErrChatNotFound()
	}
	return result, nil
}
