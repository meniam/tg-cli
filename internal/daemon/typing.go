package daemon

import (
	"context"
	"encoding/json"
	"tg-cli/internal/rpc"
)

func (d *daemon) chatTyping(ctx context.Context, raw json.RawMessage) (any, error) {
	var p rpc.TypingParams
	if err := decodeParams(raw, &p); err != nil {
		return nil, err
	}
	if err := rpc.ValidateTypingParams(p); err != nil {
		return nil, err
	}
	ref, id, err := d.writeReference(ctx, p.Chat, true)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		return rpc.TypingResult{ChatID: id, Action: p.Action, DryRun: true}, nil
	}
	result, err := d.opts.Telegram.Typing(ctx, ref, p)
	if err != nil {
		return nil, err
	}
	result.ChatID = id
	return result, nil
}
